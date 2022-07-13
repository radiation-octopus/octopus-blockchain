package p2p

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/entity/mclock"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/p2p/enode"
	"github.com/radiation-octopus/octopus-blockchain/p2p/netutil"
	mrand "math/rand"
	"net"
	"sync"
	"time"
)

const (
	// 这是重拨某个节点之间等待的时间量。该限制略高于inboundThrottleTime，以防止小型专用网络中的拨号失败。
	dialHistoryExpiration = inboundThrottleTime + 5*time.Second

	// 配置“寻找对等点”消息。
	dialStatsLogInterval = 10 * time.Second // 最多打印一次
	dialStatsPeerLimit   = 3                // 但如果超过这么多的同龄人

	// 端点分辨率受到有界回退的限制。
	initialResolveDelay = 60 * time.Second
	maxResolveDelay     = time.Hour
)

// checkDial errors:
var (
	errSelf             = errors.New("is self")
	errAlreadyDialing   = errors.New("already dialing")
	errAlreadyConnected = errors.New("already connected")
	errRecentlyDialed   = errors.New("recently dialed")
	errNetRestrict      = errors.New("not contained in netrestrict list")
	errNoPort           = errors.New("node does not provide TCP port")
)

// NodeDialer用于连接网络中的节点，通常使用底层网络。
//拨号器，但也使用网络。管道测试。
type NodeDialer interface {
	Dial(context.Context, *enode.Node) (net.Conn, error)
}

type nodeResolver interface {
	Resolve(*enode.Node) *enode.Node
}

type dialSetupFunc func(net.Conn, connFlag, *enode.Node) error

type dialConfig struct {
	self           enode.ID         // 我们自己的ID
	maxDialPeers   int              // 最大拨号对等数
	maxActiveDials int              // 活动拨号的最大数量
	netRestrict    *netutil.Netlist // IP netrestrict列表，如果为零则禁用
	resolver       nodeResolver
	dialer         NodeDialer
	log            log.Logger
	clock          mclock.Clock
	rand           *mrand.Rand
}

func (cfg dialConfig) withDefaults() dialConfig {
	if cfg.maxActiveDials == 0 {
		cfg.maxActiveDials = defaultMaxPendingPeers
	}
	if cfg.log == nil {
		cfg.log = log.Root()
	}
	if cfg.clock == nil {
		cfg.clock = mclock.System{}
	}
	if cfg.rand == nil {
		seedb := make([]byte, 8)
		crand.Read(seedb)
		seed := int64(binary.BigEndian.Uint64(seedb))
		cfg.rand = mrand.New(mrand.NewSource(seed))
	}
	return cfg
}

// tcpDialer使用真实的TCP连接实现NodeDialer。
type tcpDialer struct {
	d *net.Dialer
}

func (t tcpDialer) Dial(ctx context.Context, dest *enode.Node) (net.Conn, error) {
	return t.d.DialContext(ctx, "tcp", nodeAddr(dest).String())
}

func nodeAddr(n *enode.Node) net.Addr {
	return &net.TCPAddr{IP: n.IP(), Port: n.TCP()}
}

// 拨号程序创建出站连接并将其提交到服务器。可以创建两种类型的对等连接：
//-静态拨号是预配置的连接。 拨号程序尝试始终保持这些节点的连接。
//-动态拨号是根据节点发现结果创建的。拨号程序从其输入迭代器中连续读取候选节点，并尝试创建到通过迭代器到达的节点的对等连接。
type dialScheduler struct {
	dialConfig
	setupFunc   dialSetupFunc
	wg          sync.WaitGroup
	cancel      context.CancelFunc
	ctx         context.Context
	nodesIn     chan *enode.Node
	doneCh      chan *dialTask
	addStaticCh chan *enode.Node
	remStaticCh chan *enode.Node
	addPeerCh   chan *conn
	remPeerCh   chan *conn

	// 下面的所有内容都属于循环，只能由循环goroutine上的代码访问。
	dialing   map[enode.ID]*dialTask // 活动任务
	peers     map[enode.ID]struct{}  // 所有连接的对等点
	dialPeers int                    // 当前已拨号对等点的数量

	// 静态映射跟踪所有静态拨号任务。
	//可用静态拨号任务的子集（即那些通过checkDial的任务）保存在staticPool中。
	//调度程序更喜欢从池中启动随机静态任务，而不是从迭代器中启动动态拨号。
	static     map[enode.ID]*dialTask
	staticPool []*dialTask

	// 拨号历史记录保留最近拨打的节点。历史记录的成员没有拨号。
	history          expHeap
	historyTimer     mclock.Timer
	historyTimerTime mclock.AbsTime

	// 对于logStats
	lastStatsLog     mclock.AbsTime
	doneSinceLastLog int
}

func newDialScheduler(config dialConfig, it enode.Iterator, setupFunc dialSetupFunc) *dialScheduler {
	d := &dialScheduler{
		dialConfig:  config.withDefaults(),
		setupFunc:   setupFunc,
		dialing:     make(map[enode.ID]*dialTask),
		static:      make(map[enode.ID]*dialTask),
		peers:       make(map[enode.ID]struct{}),
		doneCh:      make(chan *dialTask),
		nodesIn:     make(chan *enode.Node),
		addStaticCh: make(chan *enode.Node),
		remStaticCh: make(chan *enode.Node),
		addPeerCh:   make(chan *conn),
		remPeerCh:   make(chan *conn),
	}
	d.lastStatsLog = d.clock.Now()
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.wg.Add(2)
	go d.readNodes(it)
	go d.loop(it)
	return d
}

// peerAdded更新对等集。
func (d *dialScheduler) peerAdded(c *conn) {
	select {
	case d.addPeerCh <- c:
	case <-d.ctx.Done():
	}
}

//对等移动更新对等集。
func (d *dialScheduler) peerRemoved(c *conn) {
	select {
	case d.remPeerCh <- c:
	case <-d.ctx.Done():
	}
}

// removeStatic删除静态拨号候选。
func (d *dialScheduler) removeStatic(n *enode.Node) {
	select {
	case d.remStaticCh <- n:
	case <-d.ctx.Done():
	}
}

//readNodes在其自己的goroutine中运行，并将节点从输入迭代器传递到nodesIn通道。
func (d *dialScheduler) readNodes(it enode.Iterator) {
	defer d.wg.Done()

	for it.Next() {
		select {
		case d.nodesIn <- it.Node():
		case <-d.ctx.Done():
		}
	}
}

// 停止关闭拨号程序，取消所有当前拨号任务。
func (d *dialScheduler) stop() {
	d.cancel()
	d.wg.Wait()
}

// addStatic添加静态拨号候选。
func (d *dialScheduler) addStatic(n *enode.Node) {
	select {
	case d.addStaticCh <- n:
	case <-d.ctx.Done():
	}
}

// 环路是拨号器的主环路。
func (d *dialScheduler) loop(it enode.Iterator) {
	var (
		nodesCh    chan *enode.Node
		historyExp = make(chan struct{}, 1)
	)

loop:
	for {
		// 如果插槽可用，则启动新的拨号盘。
		slots := d.freeDialSlots()
		slots -= d.startStaticDials(slots)
		if slots > 0 {
			nodesCh = d.nodesIn
		} else {
			nodesCh = nil
		}
		d.rearmHistoryTimer(historyExp)
		d.logStats()

		select {
		case node := <-nodesCh:
			if err := d.checkDial(node); err != nil {
				d.log.Trace("Discarding dial candidate", "id", node.ID(), "ip", node.IP(), "reason", err)
			} else {
				d.startDial(newDialTask(node, dynDialedConn))
			}

		case task := <-d.doneCh:
			id := task.dest.ID()
			delete(d.dialing, id)
			d.updateStaticPool(id)
			d.doneSinceLastLog++

		case c := <-d.addPeerCh:
			if c.is(dynDialedConn) || c.is(staticDialedConn) {
				d.dialPeers++
			}
			id := c.node.ID()
			d.peers[id] = struct{}{}
			// 从静态池中删除，因为节点现在已连接。
			task := d.static[id]
			if task != nil && task.staticPoolIndex >= 0 {
				d.removeFromStaticPool(task.staticPoolIndex)
			}
			// TODO: cancel dials to connected peers

		case c := <-d.remPeerCh:
			if c.is(dynDialedConn) || c.is(staticDialedConn) {
				d.dialPeers--
			}
			delete(d.peers, c.node.ID())
			d.updateStaticPool(c.node.ID())

		case node := <-d.addStaticCh:
			id := node.ID()
			_, exists := d.static[id]
			d.log.Trace("Adding static node", "id", id, "ip", node.IP(), "added", !exists)
			if exists {
				continue loop
			}
			task := newDialTask(node, staticDialedConn)
			d.static[id] = task
			if d.checkDial(node) == nil {
				d.addToStaticPool(task)
			}

		case node := <-d.remStaticCh:
			id := node.ID()
			task := d.static[id]
			d.log.Trace("Removing static node", "id", id, "ok", task != nil)
			if task != nil {
				delete(d.static, id)
				if task.staticPoolIndex >= 0 {
					d.removeFromStaticPool(task.staticPoolIndex)
				}
			}

		case <-historyExp:
			d.expireHistory()

		case <-d.ctx.Done():
			it.Close()
			break loop
		}
	}

	d.stopHistoryTimer(historyExp)
	for range d.dialing {
		<-d.doneCh
	}
	d.wg.Done()
}

//freeDialSlots返回空闲拨号插槽的数量。
//当对等方在其任务仍在运行的情况下连接时，结果可能是负面的。
func (d *dialScheduler) freeDialSlots() int {
	slots := (d.maxDialPeers - d.dialPeers) * 2
	if slots > d.maxActiveDials {
		slots = d.maxActiveDials
	}
	free := slots - len(d.dialing)
	return free
}

//startStaticDials启动n个静态拨号任务。
func (d *dialScheduler) startStaticDials(n int) (started int) {
	for started = 0; started < n && len(d.staticPool) > 0; started++ {
		idx := d.rand.Intn(len(d.staticPool))
		task := d.staticPool[idx]
		d.startDial(task)
		d.removeFromStaticPool(idx)
	}
	return started
}

// removeFromStaticPool从staticPool中删除idx处的任务。
//它通过将池的当前最后一个元素移动到idx，然后将池缩短一。
func (d *dialScheduler) removeFromStaticPool(idx int) {
	task := d.staticPool[idx]
	end := len(d.staticPool) - 1
	d.staticPool[idx] = d.staticPool[end]
	d.staticPool[idx].staticPoolIndex = idx
	d.staticPool[end] = nil
	d.staticPool = d.staticPool[:end]
	task.staticPoolIndex = -1
}

// startDial在单独的goroutine中运行给定的拨号任务。
func (d *dialScheduler) startDial(task *dialTask) {
	d.log.Trace("Starting p2p dial", "id", task.dest.ID(), "ip", task.dest.IP(), "flag", task.flags)
	hkey := string(task.dest.ID().Bytes())
	d.history.add(hkey, d.clock.Now().Add(dialHistoryExpiration))
	d.dialing[task.dest.ID()] = task
	go func() {
		task.run(d)
		d.doneCh <- task
	}()
}

//rearmHistoryTimer将d.historyTimer配置为在d.history中的下一项过期时激发。
func (d *dialScheduler) rearmHistoryTimer(ch chan struct{}) {
	if len(d.history) == 0 || d.historyTimerTime == d.history.nextExpiry() {
		return
	}
	d.stopHistoryTimer(ch)
	d.historyTimerTime = d.history.nextExpiry()
	timeout := time.Duration(d.historyTimerTime - d.clock.Now())
	d.historyTimer = d.clock.AfterFunc(timeout, func() { ch <- struct{}{} })
}

// stopHistoryTimer停止计时器并耗尽其发送的通道。
func (d *dialScheduler) stopHistoryTimer(ch chan struct{}) {
	if d.historyTimer != nil && !d.historyTimer.Stop() {
		<-ch
	}
}

// logStats将拨号程序统计信息打印到日志中。
//当连接了足够多的对等方时，消息将被抑制，因为用户只应在其客户端启动或重新联机时看到消息。
func (d *dialScheduler) logStats() {
	now := d.clock.Now()
	if d.lastStatsLog.Add(dialStatsLogInterval) > now {
		return
	}
	if d.dialPeers < dialStatsPeerLimit && d.dialPeers < d.maxDialPeers {
		d.log.Info("Looking for peers", "peercount", len(d.peers), "tried", d.doneSinceLastLog, "static", len(d.static))
	}
	d.doneSinceLastLog = 0
	d.lastStatsLog = now
}

// 如果不应拨打节点n，则checkDial返回错误。
func (d *dialScheduler) checkDial(n *enode.Node) error {
	if n.ID() == d.self {
		return errSelf
	}
	if n.IP() != nil && n.TCP() == 0 {
		//如果通过发现发现非TCP节点，则会触发此检查。
		//如果没有IP，则该节点是静态节点，实际端点将在稍后的dialTask中解析。
		return errNoPort
	}
	if _, ok := d.dialing[n.ID()]; ok {
		return errAlreadyDialing
	}
	if _, ok := d.peers[n.ID()]; ok {
		return errAlreadyConnected
	}
	if d.netRestrict != nil && !d.netRestrict.Contains(n.IP()) {
		return errNetRestrict
	}
	if d.history.contains(string(n.ID().Bytes())) {
		return errRecentlyDialed
	}
	return nil
}

// updateStaticPool尝试将给定的静态拨号移回静态池。
func (d *dialScheduler) updateStaticPool(id enode.ID) {
	task, ok := d.static[id]
	if ok && task.staticPoolIndex < 0 && d.checkDial(task.dest) == nil {
		d.addToStaticPool(task)
	}
}

func (d *dialScheduler) addToStaticPool(task *dialTask) {
	if task.staticPoolIndex >= 0 {
		panic("attempt to add task to staticPool twice")
	}
	d.staticPool = append(d.staticPool, task)
	task.staticPoolIndex = len(d.staticPool) - 1
}

// expireHistory从d.history中删除过期项目。
func (d *dialScheduler) expireHistory() {
	d.historyTimer.Stop()
	d.historyTimer = nil
	d.historyTimerTime = 0
	d.history.expire(d.clock.Now(), func(hkey string) {
		var id enode.ID
		copy(id[:], hkey)
		d.updateStaticPool(id)
	})
}

// 为所拨打的每个节点生成的拨号任务。
type dialTask struct {
	staticPoolIndex int
	flags           connFlag
	// 这些字段是任务的专用字段，在任务运行时，dialScheduler不应访问这些字段。
	dest         *enode.Node
	lastResolved mclock.AbsTime
	resolveDelay time.Duration
}

func newDialTask(dest *enode.Node, flags connFlag) *dialTask {
	return &dialTask{dest: dest, flags: flags, staticPoolIndex: -1}
}

func (t *dialTask) run(d *dialScheduler) {
	if t.needResolve() && !t.resolve(d) {
		return
	}

	err := t.dial(d, t.dest)
	if err != nil {
		// 对于静态节点，如果拨号失败，请再次解决。
		if _, ok := err.(*dialError); ok && t.flags&staticDialedConn != 0 {
			if t.resolve(d) {
				t.dial(d, t.dest)
			}
		}
	}
}

func (t *dialTask) needResolve() bool {
	return t.flags&staticDialedConn != 0 && t.dest.IP() == nil
}

// 解决使用发现查找目标的当前端点的尝试。通过回退限制解析操作，以避免发现网络中充斥着对不存在的节点的无用查询。
//当找到节点时，退避延迟重置。
func (t *dialTask) resolve(d *dialScheduler) bool {
	if d.resolver == nil {
		return false
	}
	if t.resolveDelay == 0 {
		t.resolveDelay = initialResolveDelay
	}
	if t.lastResolved > 0 && time.Duration(d.clock.Now()-t.lastResolved) < t.resolveDelay {
		return false
	}
	resolved := d.resolver.Resolve(t.dest)
	t.lastResolved = d.clock.Now()
	if resolved == nil {
		t.resolveDelay *= 2
		if t.resolveDelay > maxResolveDelay {
			t.resolveDelay = maxResolveDelay
		}
		d.log.Debug("Resolving node failed", "id", t.dest.ID(), "newdelay", t.resolveDelay)
		return false
	}
	// The node was found.
	t.resolveDelay = initialResolveDelay
	t.dest = resolved
	d.log.Debug("Resolved node", "id", t.dest.ID(), "addr", &net.TCPAddr{IP: t.dest.IP(), Port: t.dest.TCP()})
	return true
}

//拨号执行实际的连接尝试。
func (t *dialTask) dial(d *dialScheduler, dest *enode.Node) error {
	fd, err := d.dialer.Dial(d.ctx, t.dest)
	if err != nil {
		d.log.Trace("Dial error", "id", t.dest.ID(), "addr", nodeAddr(t.dest), "conn", t.flags, "err", cleanupDialErr(err))
		return &dialError{err}
	}
	mfd := newMeteredConn(fd, false, &net.TCPAddr{IP: dest.IP(), Port: dest.TCP()})
	return d.setupFunc(mfd, t.flags, dest)
}

type dialError struct {
	error
}

func cleanupDialErr(err error) error {
	if netErr, ok := err.(*net.OpError); ok && netErr.Op == "dial" {
		return netErr.Err
	}
	return err
}
