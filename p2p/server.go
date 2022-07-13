package p2p

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/entity/mclock"
	"github.com/radiation-octopus/octopus-blockchain/event"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/p2p/discover"
	"github.com/radiation-octopus/octopus-blockchain/p2p/enode"
	"github.com/radiation-octopus/octopus-blockchain/p2p/enr"
	"github.com/radiation-octopus/octopus-blockchain/p2p/nat"
	"github.com/radiation-octopus/octopus-blockchain/p2p/netutil"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultDialTimeout = 15 * time.Second

	// 这是discovery混音器的公平旋钮。
	//在寻找同龄人时，我们会等待这么长时间，等待一个候选人来源，然后再继续尝试其他来源。
	discmixTimeout = 5 * time.Second

	// 连接默认值。
	defaultMaxPendingPeers = 50
	defaultDialRatio       = 3

	// 这一时间限制了每个源IP的入站连接尝试。
	inboundThrottleTime = 30 * time.Second

	// 阅读完整消息所允许的最长时间。这实际上是连接可以空闲的时间量。
	frameReadTimeout = 30 * time.Second

	// 写入完整消息所允许的最长时间。
	frameWriteTimeout = 20 * time.Second
)

var errServerStopped = errors.New("server stopped")

const (
	dynDialedConn connFlag = 1 << iota
	staticDialedConn
	inboundConn
	trustedConn
)

// Config保存服务器选项。
type Config struct {
	// 该字段必须设置为有效的secp256k1私钥。
	PrivateKey *ecdsa.PrivateKey `toml:"-"`

	// MaxPeers是可以连接的最大对等数。它必须大于零。
	MaxPeers int

	// MaxPendingPeers是在握手阶段可以挂起的最大对等点数，对于入站和出站连接分别计算。零默认为预设值。
	MaxPendingPeers int `toml:",omitempty"`

	// DialRatio控制入站连接与已拨连接的比率。
	//示例：2的拨号率允许拨打1/2的连接。将DialRatio设置为零默认为3。
	DialRatio int `toml:",omitempty"`

	// 节点发现可用于禁用对等发现机制。禁用对协议调试（手动拓扑）很有用。
	NoDiscovery bool

	// DiscoveryV5指定是否应启动基于新主题发现的V5发现协议。
	DiscoveryV5 bool `toml:",omitempty"`

	// 名称设置此服务器的节点名称。使用common。MakeName以创建符合现有约定的名称。
	Name string `toml:"-"`

	// 引导节点用于建立与网络其余部分的连接。
	BootstrapNodes []*enode.Node

	// BootstrapNodesV5用于使用V5发现协议建立与网络其余部分的连接。
	BootstrapNodesV5 []*enode.Node `toml:",omitempty"`

	// 静态节点用作预先配置的连接，在断开连接时始终保持并重新连接。
	StaticNodes []*enode.Node

	// 受信任节点用作预先配置的连接，始终允许连接，甚至超过对等限制。
	TrustedNodes []*enode.Node

	// 连接可以限制在某些IP网络上。如果此选项设置为非nil值，则只考虑与列表中包含的一个IP网络匹配的主机。
	NetRestrict *netutil.Netlist `toml:",omitempty"`

	// NodeDatabase是指向数据库的路径，其中包含以前在网络中看到的活动节点。
	NodeDatabase string `toml:",omitempty"`

	// 协议应包含服务器支持的协议。为每个对等点启动匹配协议。
	Protocols []Protocol `toml:"-" json:"-"`

	// 如果ListenAddr设置为非nil地址，服务器将侦听传入的连接。
	//如果端口为零，操作系统将选择一个端口。当服务器启动时，ListenAddr字段将用实际地址更新。
	ListenAddr string

	// 如果设置为非零值，则使用给定的NAT端口映射器使侦听端口可用于Internet。
	NAT nat.Interface `toml:",omitempty"`

	// 如果拨号器设置为非零值，则给定的拨号器用于拨号出站对等连接。
	Dialer NodeDialer `toml:"-"`

	// 如果NoDial为true，服务器将不会拨打任何对等方。
	NoDial bool `toml:",omitempty"`

	// 如果设置了EnableMsgEvents，那么每当向对等方发送消息或从对等方接收消息时，服务器将发出对等事件
	EnableMsgEvents bool

	// Logger是一个用于p2p的自定义记录器。服务器
	Logger log.Logger `toml:",omitempty"`

	clock mclock.Clock
}

//服务器管理所有对等连接。
type Server struct {
	// 服务器运行时，不能修改配置字段。
	Config

	// 测试用挂钩。这些是有用的，因为我们可以抑制整个协议堆栈。
	newTransport func(net.Conn, *ecdsa.PublicKey) transport
	newPeerHook  func(*Peer)
	listenFunc   func(network, addr string) (net.Listener, error)

	lock    sync.Mutex // 保护qidong
	running bool

	listener     net.Listener
	ourHandshake *protoHandshake
	loopWG       sync.WaitGroup // 运行，列表循环
	peerFeed     event.Feed
	log          log.Logger

	nodedb    *enode.DB
	localnode *enode.LocalNode
	ntab      *discover.UDPv4
	DiscV5    *discover.UDPv5
	discmix   *enode.FairMix
	dialsched *dialScheduler

	// 进入运行循环的通道。
	quit                    chan struct{}
	addtrusted              chan *enode.Node
	removetrusted           chan *enode.Node
	peerOp                  chan peerOpFunc
	peerOpDone              chan struct{}
	delpeer                 chan peerDrop
	checkpointPostHandshake chan *conn
	checkpointAddPeer       chan *conn

	// 运行循环和listenLoop的状态。
	inboundHistory expHeap
}

// Start开始运行服务器。停止后无法重新使用服务器。
func (srv *Server) Start() (err error) {
	srv.lock.Lock()
	defer srv.lock.Unlock()
	if srv.running {
		return errors.New("server already running")
	}
	srv.running = true
	srv.log = srv.Config.Logger
	if srv.log == nil {
		srv.log = log.Root()
	}
	if srv.clock == nil {
		srv.clock = mclock.System{}
	}
	if srv.NoDial && srv.ListenAddr == "" {
		srv.log.Warn("P2P server will be useless, neither dialing nor listening")
	}

	// static fields
	if srv.PrivateKey == nil {
		return errors.New("Server.PrivateKey must be set to a non-nil key")
	}
	if srv.newTransport == nil {
		srv.newTransport = newRLPX
	}
	if srv.listenFunc == nil {
		srv.listenFunc = net.Listen
	}
	srv.quit = make(chan struct{})
	srv.delpeer = make(chan peerDrop)
	srv.checkpointPostHandshake = make(chan *conn)
	srv.checkpointAddPeer = make(chan *conn)
	srv.addtrusted = make(chan *enode.Node)
	srv.removetrusted = make(chan *enode.Node)
	srv.peerOp = make(chan peerOpFunc)
	srv.peerOpDone = make(chan struct{})

	if err := srv.setupLocalNode(); err != nil {
		return err
	}
	if srv.ListenAddr != "" {
		if err := srv.setupListening(); err != nil {
			return err
		}
	}
	if err := srv.setupDiscovery(); err != nil {
		return err
	}
	srv.setupDialScheduler()

	srv.loopWG.Add(1)
	go srv.run()
	return nil
}

// AddPeer将给定节点添加到静态节点集中。当对等集中有空间时，服务器将连接到节点。
//如果连接因任何原因失败，服务器将尝试重新连接对等方。
func (srv *Server) AddPeer(node *enode.Node) {
	srv.dialsched.addStatic(node)
}

// RemovePeer从静态节点集中删除节点。如果给定节点当前作为对等节点连接，它也会断开与该节点的连接。此方法阻止，直到退出所有协议并删除对等方。
//不要在协议实现中使用RemovePeer，而是在对等机上调用Disconnect。
func (srv *Server) RemovePeer(node *enode.Node) {
	var (
		ch  chan *PeerEvent
		sub event.Subscription
	)
	// 断开主环路上的对等连接。
	srv.doPeerOp(func(peers map[enode.ID]*Peer) {
		srv.dialsched.removeStatic(node)
		if peer := peers[node.ID()]; peer != nil {
			ch = make(chan *PeerEvent, 1)
			sub = srv.peerFeed.Subscribe(ch)
			peer.Disconnect(DiscRequested)
		}
	})
	// 等待对等连接结束。
	if ch != nil {
		defer sub.Unsubscribe()
		for ev := range ch {
			if ev.Peer == node.ID() && ev.Type == PeerEventTypeDrop {
				return
			}
		}
	}
}

// AddTrustedPeer将给定节点添加到保留的受信任列表中，该列表允许节点始终连接，即使插槽已满。
func (srv *Server) AddTrustedPeer(node *enode.Node) {
	select {
	case srv.addtrusted <- node:
	case <-srv.quit:
	}
}

// RemoveTrustedPeer从受信任对等集中删除给定节点。
func (srv *Server) RemoveTrustedPeer(node *enode.Node) {
	select {
	case srv.removetrusted <- node:
	case <-srv.quit:
	}
}

// SubscribeEvents将给定通道订阅给对等事件
func (srv *Server) SubscribeEvents(ch chan *PeerEvent) event.Subscription {
	return srv.peerFeed.Subscribe(ch)
}

// doPeerOp在主循环上运行fn。
func (srv *Server) doPeerOp(fn peerOpFunc) {
	select {
	case srv.peerOp <- fn:
		<-srv.peerOpDone
	case <-srv.quit:
	}
}

func (srv *Server) setupLocalNode() error {
	// 创建devp2p握手。
	pubkey := crypto.FromECDSAPub(&srv.PrivateKey.PublicKey)
	srv.ourHandshake = &protoHandshake{Version: baseProtocolVersion, Name: srv.Name, ID: pubkey[1:]}
	for _, p := range srv.Protocols {
		srv.ourHandshake.Caps = append(srv.ourHandshake.Caps, p.cap())
	}
	sort.Sort(capsByNameAndVersion(srv.ourHandshake.Caps))

	// 创建本地节点。
	db, err := enode.OpenDB(srv.Config.NodeDatabase)
	if err != nil {
		return err
	}
	srv.nodedb = db
	srv.localnode = enode.NewLocalNode(db, srv.PrivateKey)
	srv.localnode.SetFallbackIP(net.IP{127, 0, 0, 1})
	for _, p := range srv.Protocols {
		for _, e := range p.Attributes {
			srv.localnode.Set(e)
		}
	}
	switch srv.NAT.(type) {
	case nil:
		// 没有NAT接口，什么都不做。
	case nat.ExtIP:
		// ExtIP不阻塞，请立即设置IP。
		ip, _ := srv.NAT.ExternalIP()
		srv.localnode.SetStaticIP(ip)
	default:
		// 询问路由器有关IP的信息。这需要一段时间并阻止启动，请在后台执行。
		srv.loopWG.Add(1)
		go func() {
			defer srv.loopWG.Done()
			if ip, err := srv.NAT.ExternalIP(); err == nil {
				srv.localnode.SetStaticIP(ip)
			}
		}()
	}
	return nil
}

func (srv *Server) setupListening() error {
	// 启动侦听器。
	listener, err := srv.listenFunc("tcp", srv.ListenAddr)
	if err != nil {
		return err
	}
	srv.listener = listener
	srv.ListenAddr = listener.Addr().String()

	// 如果配置了NAT，则更新本地节点记录并映射TCP侦听端口。
	if tcp, ok := listener.Addr().(*net.TCPAddr); ok {
		srv.localnode.Set(enr.TCP(tcp.Port))
		if !tcp.IP.IsLoopback() && srv.NAT != nil {
			srv.loopWG.Add(1)
			go func() {
				nat.Map(srv.NAT, srv.quit, "tcp", tcp.Port, tcp.Port, "ethereum p2p")
				srv.loopWG.Done()
			}()
		}
	}

	srv.loopWG.Add(1)
	go srv.listenLoop()
	return nil
}

//listenLoop在其自己的goroutine中运行，并接受入站连接。
func (srv *Server) listenLoop() {
	srv.log.Debug("TCP listener up", "addr", srv.listener.Addr())

	// 插槽通道限制接受新连接。
	tokens := defaultMaxPendingPeers
	if srv.MaxPendingPeers > 0 {
		tokens = srv.MaxPendingPeers
	}
	slots := make(chan struct{}, tokens)
	for i := 0; i < tokens; i++ {
		slots <- struct{}{}
	}

	// 等待插槽在退出时返回。这确保了所有连接goroutine在listenLoop返回之前都已关闭。
	defer srv.loopWG.Done()
	defer func() {
		for i := 0; i < cap(slots); i++ {
			<-slots
		}
	}()

	for {
		// 在接受之前等待空闲时间。
		<-slots

		var (
			fd      net.Conn
			err     error
			lastLog time.Time
		)
		for {
			fd, err = srv.listener.Accept()
			if netutil.IsTemporaryError(err) {
				if time.Since(lastLog) > 1*time.Second {
					srv.log.Debug("Temporary read error", "err", err)
					lastLog = time.Now()
				}
				time.Sleep(time.Millisecond * 200)
				continue
			} else if err != nil {
				srv.log.Debug("Read error", "err", err)
				slots <- struct{}{}
				return
			}
			break
		}

		remoteIP := netutil.AddrIP(fd.RemoteAddr())
		if err := srv.checkInboundConn(remoteIP); err != nil {
			srv.log.Debug("Rejected inbound connection", "addr", fd.RemoteAddr(), "err", err)
			fd.Close()
			slots <- struct{}{}
			continue
		}
		if remoteIP != nil {
			var addr *net.TCPAddr
			if tcp, ok := fd.RemoteAddr().(*net.TCPAddr); ok {
				addr = tcp
			}
			fd = newMeteredConn(fd, true, addr)
			srv.log.Trace("Accepted connection", "addr", fd.RemoteAddr())
		}
		go func() {
			srv.SetupConn(fd, inboundConn, nil)
			slots <- struct{}{}
		}()
	}
}

//SetupConn运行握手并尝试将连接添加为对等连接。
//当连接被添加为对等连接或握手失败时，它返回。
func (srv *Server) SetupConn(fd net.Conn, flags connFlag, dialDest *enode.Node) error {
	c := &conn{fd: fd, flags: flags, cont: make(chan error)}
	if dialDest == nil {
		c.transport = srv.newTransport(fd, nil)
	} else {
		c.transport = srv.newTransport(fd, dialDest.Pubkey())
	}

	err := srv.setupConn(c, flags, dialDest)
	if err != nil {
		c.close(err)
	}
	return err
}

func (srv *Server) setupConn(c *conn, flags connFlag, dialDest *enode.Node) error {
	// 防止剩余的等待连接进入握手。
	srv.lock.Lock()
	running := srv.running
	srv.lock.Unlock()
	if !running {
		return errServerStopped
	}

	// 如果拨号，请找出远程公钥。
	if dialDest != nil {
		dialPubkey := new(ecdsa.PublicKey)
		if err := dialDest.Load((*enode.Secp256k1)(dialPubkey)); err != nil {
			err = errors.New("dial destination doesn't have a secp256k1 public key")
			srv.log.Trace("Setting up connection failed", "addr", c.fd.RemoteAddr(), "conn", c.flags, "err", err)
			return err
		}
	}

	// 运行RLPx握手。
	remotePubkey, err := c.doEncHandshake(srv.PrivateKey)
	if err != nil {
		srv.log.Trace("Failed RLPx handshake", "addr", c.fd.RemoteAddr(), "conn", c.flags, "err", err)
		return err
	}
	if dialDest != nil {
		c.node = dialDest
	} else {
		c.node = nodeFromConn(remotePubkey, c.fd)
	}
	clog := srv.log.New("id", c.node.ID(), "addr", c.fd.RemoteAddr(), "conn", c.flags)
	err = srv.checkpoint(c, srv.checkpointPostHandshake)
	if err != nil {
		clog.Trace("Rejected peer", "err", err)
		return err
	}

	// 运行功能协商握手。
	phs, err := c.doProtoHandshake(srv.ourHandshake)

	if err != nil {
		clog.Trace("Failed p2p handshake", "err", err)
		return err
	}
	if id := c.node.ID(); !bytes.Equal(crypto.Keccak256(phs.ID), id[:]) {
		clog.Trace("Wrong devp2p handshake identity", "phsid", hex.EncodeToString(phs.ID))
		return DiscUnexpectedIdentity
	}
	c.caps, c.name = phs.Caps, phs.Name
	err = srv.checkpoint(c, srv.checkpointAddPeer)
	if err != nil {
		clog.Trace("Rejected peer", "err", err)
		return err
	}

	return nil
}

//检查点将conn发送到run，conn执行该阶段的握手后检查（握手后，addpeer）。
func (srv *Server) checkpoint(c *conn, stage chan<- *conn) error {
	select {
	case stage <- c:
	case <-srv.quit:
		return errServerStopped
	}
	return <-c.cont
}

func (srv *Server) checkInboundConn(remoteIP net.IP) error {
	if remoteIP == nil {
		return nil
	}
	//拒绝与NetRestrict不匹配的连接。
	if srv.NetRestrict != nil && !srv.NetRestrict.Contains(remoteIP) {
		return fmt.Errorf("not in netrestrict list")
	}
	// 拒绝尝试过多的互联网同行。
	now := srv.clock.Now()
	srv.inboundHistory.expire(now, nil)
	if !netutil.IsLAN(remoteIP) && srv.inboundHistory.contains(remoteIP.String()) {
		return fmt.Errorf("too many attempts")
	}
	srv.inboundHistory.add(remoteIP.String(), now.Add(inboundThrottleTime))
	return nil
}

func (srv *Server) setupDiscovery() error {
	srv.discmix = enode.NewFairMix(discmixTimeout)

	// 添加特定于协议的发现源。
	added := make(map[string]bool)
	for _, proto := range srv.Protocols {
		if proto.DialCandidates != nil && !added[proto.Name] {
			srv.discmix.AddSource(proto.DialCandidates)
			added[proto.Name] = true
		}
	}

	// 如果禁用DHT，则不要侦听UDP端点。
	if srv.NoDiscovery && !srv.DiscoveryV5 {
		return nil
	}

	addr, err := net.ResolveUDPAddr("udp", srv.ListenAddr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	realaddr := conn.LocalAddr().(*net.UDPAddr)
	srv.log.Debug("UDP listener up", "addr", realaddr)
	if srv.NAT != nil {
		if !realaddr.IP.IsLoopback() {
			srv.loopWG.Add(1)
			go func() {
				nat.Map(srv.NAT, srv.quit, "udp", realaddr.Port, realaddr.Port, "ethereum discovery")
				srv.loopWG.Done()
			}()
		}
	}
	srv.localnode.SetFallbackUDP(realaddr.Port)

	// Discovery V4
	var unhandled chan discover.ReadPacket
	var sconn *sharedUDPConn
	if !srv.NoDiscovery {
		if srv.DiscoveryV5 {
			unhandled = make(chan discover.ReadPacket, 100)
			sconn = &sharedUDPConn{conn, unhandled}
		}
		cfg := discover.Config{
			PrivateKey:  srv.PrivateKey,
			NetRestrict: srv.NetRestrict,
			Bootnodes:   srv.BootstrapNodes,
			Unhandled:   unhandled,
			Log:         srv.log,
		}
		ntab, err := discover.ListenV4(conn, srv.localnode, cfg)
		if err != nil {
			return err
		}
		srv.ntab = ntab
		srv.discmix.AddSource(ntab.RandomNodes())
	}

	// Discovery V5
	if srv.DiscoveryV5 {
		cfg := discover.Config{
			PrivateKey:  srv.PrivateKey,
			NetRestrict: srv.NetRestrict,
			Bootnodes:   srv.BootstrapNodesV5,
			Log:         srv.log,
		}
		var err error
		if sconn != nil {
			srv.DiscV5, err = discover.ListenV5(sconn, srv.localnode, cfg)
		} else {
			srv.DiscV5, err = discover.ListenV5(conn, srv.localnode, cfg)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (srv *Server) setupDialScheduler() {
	config := dialConfig{
		self:           srv.localnode.ID(),
		maxDialPeers:   srv.maxDialedConns(),
		maxActiveDials: srv.MaxPendingPeers,
		log:            srv.Logger,
		netRestrict:    srv.NetRestrict,
		dialer:         srv.Dialer,
		clock:          srv.clock,
	}
	if srv.ntab != nil {
		config.resolver = srv.ntab
	}
	if config.dialer == nil {
		config.dialer = tcpDialer{&net.Dialer{Timeout: defaultDialTimeout}}
	}
	srv.dialsched = newDialScheduler(config, srv.discmix, srv.SetupConn)
	for _, n := range srv.StaticNodes {
		srv.dialsched.addStatic(n)
	}
}

func (srv *Server) maxDialedConns() (limit int) {
	if srv.NoDial || srv.MaxPeers == 0 {
		return 0
	}
	if srv.DialRatio == 0 {
		limit = srv.MaxPeers / defaultDialRatio
	} else {
		limit = srv.MaxPeers / srv.DialRatio
	}
	if limit == 0 {
		limit = 1
	}
	return limit
}

// run是服务器的主循环。
func (srv *Server) run() {
	srv.log.Info("Started P2P networking", "self", srv.localnode.Node().URLv4())
	defer srv.loopWG.Done()
	defer srv.nodedb.Close()
	defer srv.discmix.Close()
	defer srv.dialsched.stop()

	var (
		peers        = make(map[enode.ID]*Peer)
		inboundCount = 0
		trusted      = make(map[enode.ID]bool, len(srv.TrustedNodes))
	)
	// 将受信任节点放入映射以加快检查速度。
	//可信对等点在启动时加载或通过AddTrustedPeer RPC添加。
	for _, n := range srv.TrustedNodes {
		trusted[n.ID()] = true
	}

running:
	for {
		select {
		case <-srv.quit:
			// 服务器已停止。运行清理逻辑。
			break running

		case n := <-srv.addtrusted:
			//AddTrustedPeer使用此通道将节点添加到受信任节点集。
			srv.log.Trace("Adding trusted node", "node", n)
			trusted[n.ID()] = true
			if p, ok := peers[n.ID()]; ok {
				p.rw.set(trustedConn, true)
			}

		case n := <-srv.removetrusted:
			// RemoveTrustedPeer使用此通道从受信任节点集中删除节点。
			srv.log.Trace("Removing trusted node", "node", n)
			delete(trusted, n.ID())
			if p, ok := peers[n.ID()]; ok {
				p.rw.set(trustedConn, false)
			}

		case op := <-srv.peerOp:
			// 该通道由对等方和对等方使用。
			op(peers)
			srv.peerOpDone <- struct{}{}

		case c := <-srv.checkpointPostHandshake:
			// 连接已通过加密握手，因此远程身份是已知的（但尚未验证）。
			if trusted[c.node.ID()] {
				// 确保在检查MaxPeers之前设置了trusted标志。
				c.flags |= trustedConn
			}
			c.cont <- srv.postHandshakeChecks(peers, inboundCount, c)

		case c := <-srv.checkpointAddPeer:
			// 此时，连接已通过协议握手。它的能力是已知的，远程身份是经过验证的。
			err := srv.addPeerChecks(peers, inboundCount, c)
			if err == nil {
				// 握手完毕，并通过了所有检查。
				p := srv.launchPeer(c)
				peers[c.node.ID()] = p
				srv.log.Debug("Adding p2p peer", "peercount", len(peers), "id", p.ID(), "conn", c.flags, "addr", p.RemoteAddr(), "name", p.Name())
				srv.dialsched.peerAdded(c)
				if p.Inbound() {
					inboundCount++
				}
			}
			c.cont <- err

		case pd := <-srv.delpeer:
			// 对等端断开连接。
			d := entity.PrettyDuration(mclock.Now() - pd.created)
			delete(peers, pd.ID())
			srv.log.Debug("Removing p2p peer", "peercount", len(peers), "id", pd.ID(), "duration", d, "req", pd.requested, "err", pd.err)
			srv.dialsched.peerRemoved(pd.rw)
			if pd.Inbound() {
				inboundCount--
			}
		}
	}

	srv.log.Trace("P2P networking is spinning down")

	// Terminate discovery. If there is a running lookup it will terminate soon.
	if srv.ntab != nil {
		srv.ntab.Close()
	}
	if srv.DiscV5 != nil {
		srv.DiscV5.Close()
	}
	// Disconnect all peers.
	for _, p := range peers {
		p.Disconnect(DiscQuitting)
	}
	// Wait for peers to shut down. Pending connections and tasks are
	// not handled here and will terminate soon-ish because srv.quit
	// is closed.
	for len(peers) > 0 {
		p := <-srv.delpeer
		p.log.Trace("<-delpeer (spindown)")
		delete(peers, p.ID())
	}
}

//Stop终止服务器和所有活动对等连接。它会一直阻塞，直到所有活动连接都已关闭。
func (srv *Server) Stop() {
	srv.lock.Lock()
	if !srv.running {
		srv.lock.Unlock()
		return
	}
	srv.running = false
	if srv.listener != nil {
		// 这将取消阻止侦听器接受
		srv.listener.Close()
	}
	close(srv.quit)
	srv.lock.Unlock()
	srv.loopWG.Wait()
}

func (srv *Server) postHandshakeChecks(peers map[enode.ID]*Peer, inboundCount int, c *conn) error {
	switch {
	case !c.is(trustedConn) && len(peers) >= srv.MaxPeers:
		return DiscTooManyPeers
	case !c.is(trustedConn) && c.is(inboundConn) && inboundCount >= srv.maxInboundConns():
		return DiscTooManyPeers
	case peers[c.node.ID()] != nil:
		return DiscAlreadyConnected
	case c.node.ID() == srv.localnode.ID():
		return DiscSelf
	default:
		return nil
	}
}

func (srv *Server) maxInboundConns() int {
	return srv.MaxPeers - srv.maxDialedConns()
}

func (srv *Server) addPeerChecks(peers map[enode.ID]*Peer, inboundCount int, c *conn) error {
	//丢弃没有匹配协议的连接。
	if len(srv.Protocols) > 0 && countMatchingProtocols(srv.Protocols, c.caps) == 0 {
		return DiscUselessPeer
	}
	// 重复握手后检查，因为对等集可能在执行这些检查后发生了更改。
	return srv.postHandshakeChecks(peers, inboundCount, c)
}

func (srv *Server) launchPeer(c *conn) *Peer {
	p := newPeer(srv.log, c, srv.Protocols)
	if srv.EnableMsgEvents {
		// 如果消息事件已启用，则将对等馈送传递给对等方。
		p.events = &srv.peerFeed
	}
	go srv.runPeer(p)
	return p
}

// runPeer在其自己的goroutine中为每个对等运行。
func (srv *Server) runPeer(p *Peer) {
	if srv.newPeerHook != nil {
		srv.newPeerHook(p)
	}
	srv.peerFeed.Send(&PeerEvent{
		Type:          PeerEventTypeAdd,
		Peer:          p.ID(),
		RemoteAddress: p.RemoteAddr().String(),
		LocalAddress:  p.LocalAddr().String(),
	})

	// 运行每个对等主循环。
	remoteRequested, err := p.run()

	// 在主循环上宣布断开连接以更新对等集。主循环等待在srv上发送现有对等点。
	//delpeer，因此此发送不应在srv上选择。退出
	srv.delpeer <- peerDrop{p, err, remoteRequested}

	// 将对等点广播到外部订阅者。这需要在发送到delpeer之后进行，
	//以便订阅者对对等集具有一致的视图（即，Server.Peers（）在接收到事件时不包括对等。
	srv.peerFeed.Send(&PeerEvent{
		Type:          PeerEventTypeDrop,
		Peer:          p.ID(),
		Error:         err.Error(),
		RemoteAddress: p.RemoteAddr().String(),
		LocalAddress:  p.LocalAddr().String(),
	})
}

//对等点返回所有连接的对等点。
func (srv *Server) Peers() []*Peer {
	var ps []*Peer
	srv.doPeerOp(func(peers map[enode.ID]*Peer) {
		for _, p := range peers {
			ps = append(ps, p)
		}
	})
	return ps
}

// PeerCount返回连接的对等点的数量。
func (srv *Server) PeerCount() int {
	var count int
	srv.doPeerOp(func(ps map[enode.ID]*Peer) {
		count = len(ps)
	})
	return count
}

// PeerInfo返回描述连接对等点的元数据对象数组。
func (srv *Server) PeersInfo() []*PeerInfo {
	// 收集所有通用和子协议特定信息
	infos := make([]*PeerInfo, 0, srv.PeerCount())
	for _, peer := range srv.Peers() {
		if peer != nil {
			infos = append(infos, peer.Info())
		}
	}
	// 按节点标识符按字母顺序对结果数组排序
	for i := 0; i < len(infos); i++ {
		for j := i + 1; j < len(infos); j++ {
			if infos[i].ID > infos[j].ID {
				infos[i], infos[j] = infos[j], infos[i]
			}
		}
	}
	return infos
}

func nodeFromConn(pubkey *ecdsa.PublicKey, conn net.Conn) *enode.Node {
	var ip net.IP
	var port int
	if tcp, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		ip = tcp.IP
		port = tcp.Port
	}
	return enode.NewV4(pubkey, ip, port, port)
}

// SharedDudpconn实现共享连接。
//Write将消息发送到底层连接，而read返回主侦听器发现无法处理并发送到未处理通道的消息。
type sharedUDPConn struct {
	*net.UDPConn
	unhandled chan discover.ReadPacket
}

type peerOpFunc func(map[enode.ID]*Peer)

type peerDrop struct {
	*Peer
	err       error
	requested bool // 如果对等方发出信号，则为true
}

type connFlag int32

type transport interface {
	// 两次握手。
	doEncHandshake(prv *ecdsa.PrivateKey) (*ecdsa.PublicKey, error)
	doProtoHandshake(our *protoHandshake) (*protoHandshake, error)
	// MsgReadWriter只能在加密握手完成后使用。
	//代码使用conn.id来跟踪这一点，方法是在加密握手后将其设置为非零值。
	MsgReadWriter
	// 传输必须提供Close，因为我们在一些测试中使用MsgPipe。
	//关闭实际的网络连接在这些测试中没有任何作用，因为MsgPipe没有使用它。
	close(err error)
}

//conn使用在两次握手期间收集的信息包装网络连接。
type conn struct {
	fd net.Conn
	transport
	node  *enode.Node
	flags connFlag
	cont  chan error // 运行循环使用cont向SetupConn发送错误信号。
	caps  []Cap      // 协议握手后有效
	name  string     // 协议握手后有效
}

func (c *conn) is(f connFlag) bool {
	flags := connFlag(atomic.LoadInt32((*int32)(&c.flags)))
	return flags&f != 0
}

func (c *conn) set(f connFlag, val bool) {
	for {
		oldFlags := connFlag(atomic.LoadInt32((*int32)(&c.flags)))
		flags := oldFlags
		if val {
			flags |= f
		} else {
			flags &= ^f
		}
		if atomic.CompareAndSwapInt32((*int32)(&c.flags), int32(oldFlags), int32(flags)) {
			return
		}
	}
}

// NodeInfo表示关于主机的已知信息的简短摘要。
type NodeInfo struct {
	ID    string `json:"id"`    // 唯一节点标识符（也是加密密钥）
	Name  string `json:"name"`  // 节点的名称，包括客户端类型、版本、操作系统、自定义数据
	Enode string `json:"enode"` // 用于从远程对等添加此对等的Enode URL
	ONR   string `json:"enr"`   // 辐射章鱼节点记录
	IP    string `json:"ip"`    // 节点的IP地址
	Ports struct {
		Discovery int `json:"discovery"` // 发现协议的UDP侦听端口
		Listener  int `json:"listener"`  // RLPx的TCP侦听端口
	} `json:"ports"`
	ListenAddr string                 `json:"listenAddr"`
	Protocols  map[string]interface{} `json:"protocols"`
}

//NodeInfo收集并返回关于主机的已知元数据集合。
func (srv *Server) NodeInfo() *NodeInfo {
	// 收集和组装通用节点信息
	node := srv.Self()
	info := &NodeInfo{
		Name:       srv.Name,
		Enode:      node.URLv4(),
		ID:         node.ID().String(),
		IP:         node.IP().String(),
		ListenAddr: srv.ListenAddr,
		Protocols:  make(map[string]interface{}),
	}
	info.Ports.Discovery = node.UDP()
	info.Ports.Listener = node.TCP()
	info.ONR = node.String()

	// 收集所有正在运行的协议信息（每个协议类型仅一次）
	for _, proto := range srv.Protocols {
		if _, ok := info.Protocols[proto.Name]; !ok {
			nodeInfo := interface{}("unknown")
			if query := proto.NodeInfo; query != nil {
				nodeInfo = proto.NodeInfo()
			}
			info.Protocols[proto.Name] = nodeInfo
		}
	}
	return info
}

// Self返回本地节点的端点信息。
func (srv *Server) Self() *enode.Node {
	srv.lock.Lock()
	ln := srv.localnode
	srv.lock.Unlock()

	if ln == nil {
		return enode.NewV4(&srv.PrivateKey.PublicKey, net.ParseIP("0.0.0.0"), 0, 0)
	}
	return ln.Node()
}
