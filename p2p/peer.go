package p2p

import (
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity/mclock"
	"github.com/radiation-octopus/octopus-blockchain/event"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/metrics"
	"github.com/radiation-octopus/octopus-blockchain/p2p/enode"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"io"
	"net"
	"sort"
	"sync"
	"time"
)

const (
	baseProtocolVersion    = 5
	baseProtocolLength     = uint64(16)
	baseProtocolMaxMsgSize = 2 * 1024

	snappyProtocolVersion = 5

	pingInterval = 15 * time.Second
)

const (
	// devp2p消息代码
	handshakeMsg = 0x00
	discMsg      = 0x01
	pingMsg      = 0x02
	pongMsg      = 0x03
)

const (
	// PeerEventTypeAdd是将对等添加到p2p时发出的事件类型。服务器
	PeerEventTypeAdd PeerEventType = "add"

	// PeerEventTypeDrop是对等点从p2p中删除时发出的事件类型。服务器
	PeerEventTypeDrop PeerEventType = "drop"

	// PeerEventTypeMsgSend是消息成功发送到对等方时发出的事件类型
	PeerEventTypeMsgSend PeerEventType = "msgsend"

	// PeerEventTypeMsgRecv是从对等方接收消息时发出的事件类型
	PeerEventTypeMsgRecv PeerEventType = "msgrecv"
)

var (
	ErrShuttingDown = errors.New("shutting down")
)

// 协议握手是协议握手的RLP结构。
type protoHandshake struct {
	Version    uint64
	Name       string
	Caps       []Cap
	ListenPort uint64
	ID         []byte // secp256k1公钥

	// 忽略其他字段（为了向前兼容）。
	Rest []rlp.RawValue `rlp:"tail"`
}

// 对等表示连接的远程节点。
type Peer struct {
	rw      *conn
	running map[string]*protoRW
	log     log.Logger
	created mclock.AbsTime

	wg       sync.WaitGroup
	protoErr chan error
	closed   chan struct{}
	disc     chan DiscReason

	// 事件接收消息发送/接收事件（如果设置）
	events   *event.Feed
	testPipe *MsgPipeRW // 用于测试
}

func newPeer(log log.Logger, conn *conn, protocols []Protocol) *Peer {
	protomap := matchProtocols(protocols, conn.caps, conn)
	p := &Peer{
		rw:       conn,
		running:  protomap,
		created:  mclock.Now(),
		disc:     make(chan DiscReason),
		protoErr: make(chan error, len(protomap)+1), // protocols + pingLoop
		closed:   make(chan struct{}),
		log:      log.New("id", conn.node.ID(), "conn", conn.flags),
	}
	return p
}

// Name 返回名称的缩写形式
func (p *Peer) Name() string {
	s := p.rw.name
	if len(s) > 20 {
		return s[:20] + "..."
	}
	return s
}

// 如果对等方是入站连接，则Inbound返回true
func (p *Peer) Inbound() bool {
	return p.rw.is(inboundConn)
}

// ID 返回节点的公钥。
func (p *Peer) ID() enode.ID {
	return p.rw.node.ID()
}

func (p *Peer) Log() log.Logger {
	return p.log
}

// 如果对等方使用特定协议的任何枚举版本进行活动连接，则RunningCap返回true，这意味着该节点和对等方都支持至少一个版本。
func (p *Peer) RunningCap(protocol string, versions []uint) bool {
	if proto, ok := p.running[protocol]; ok {
		for _, ver := range versions {
			if proto.Version == ver {
				return true
			}
		}
	}
	return false
}

//RemoteAddr返回网络连接的远程地址。
func (p *Peer) RemoteAddr() net.Addr {
	return p.rw.fd.RemoteAddr()
}

// LocalAddr返回网络连接的本地地址。
func (p *Peer) LocalAddr() net.Addr {
	return p.rw.fd.LocalAddr()
}

// Disconnect以给定的原因终止对等连接。它立即返回，不会等到连接关闭。
func (p *Peer) Disconnect(reason DiscReason) {
	if p.testPipe != nil {
		p.testPipe.Close()
	}

	select {
	case p.disc <- reason:
	case <-p.closed:
	}
}

func (p *Peer) run() (remoteRequested bool, err error) {
	var (
		writeStart = make(chan struct{}, 1)
		writeErr   = make(chan error, 1)
		readErr    = make(chan error, 1)
		reason     DiscReason // 发送给对等方
	)
	p.wg.Add(2)
	go p.readLoop(readErr)
	go p.pingLoop()

	// 启动所有协议处理程序。
	writeStart <- struct{}{}
	p.startProtocols(writeStart, writeErr)

	// 等待错误或断开连接。
loop:
	for {
		select {
		case err = <-writeErr:
			// A写入完成。如果没有错误，允许开始下一次写入。
			if err != nil {
				reason = DiscNetworkError
				break loop
			}
			writeStart <- struct{}{}
		case err = <-readErr:
			if r, ok := err.(DiscReason); ok {
				remoteRequested = true
				reason = r
			} else {
				reason = DiscNetworkError
			}
			break loop
		case err = <-p.protoErr:
			reason = discReasonForError(err)
			break loop
		case err = <-p.disc:
			reason = discReasonForError(err)
			break loop
		}
	}

	close(p.closed)
	p.rw.close(reason)
	p.wg.Wait()
	return remoteRequested, err
}

func (p *Peer) pingLoop() {
	ping := time.NewTimer(pingInterval)
	defer p.wg.Done()
	defer ping.Stop()
	for {
		select {
		case <-ping.C:
			if err := SendItems(p.rw, pingMsg); err != nil {
				p.protoErr <- err
				return
			}
			ping.Reset(pingInterval)
		case <-p.closed:
			return
		}
	}
}

func (p *Peer) readLoop(errc chan<- error) {
	defer p.wg.Done()
	for {
		msg, err := p.rw.ReadMsg()
		if err != nil {
			errc <- err
			return
		}
		msg.ReceivedAt = time.Now()
		if err = p.handle(msg); err != nil {
			errc <- err
			return
		}
	}
}

func (p *Peer) handle(msg Msg) error {
	switch {
	case msg.Code == pingMsg:
		msg.Discard()
		go SendItems(p.rw, pongMsg)
	case msg.Code == discMsg:
		// 这是最后一条消息。我们不需要放弃或检查错误，因为连接将在它之后关闭。
		var m struct{ R DiscReason }
		rlp.Decode(msg.Payload, &m)
		return m.R
	case msg.Code < baseProtocolLength:
		// 忽略其他基本协议消息
		return msg.Discard()
	default:
		// 这是一条子程序消息
		proto, err := p.getProto(msg.Code)
		if err != nil {
			return fmt.Errorf("msg code out of range: %v", msg.Code)
		}
		if metrics.Enabled {
			m := fmt.Sprintf("%s/%s/%d/%#02x", ingressMeterName, proto.Name, proto.Version, msg.Code-proto.offset)
			metrics.GetOrRegisterMeter(m, nil).Mark(int64(msg.meterSize))
			metrics.GetOrRegisterMeter(m+"/packets", nil).Mark(1)
		}
		select {
		case proto.in <- msg:
			return nil
		case <-p.closed:
			return io.EOF
		}
	}
	return nil
}

func (p *Peer) startProtocols(writeStart <-chan struct{}, writeErr chan<- error) {
	p.wg.Add(len(p.running))
	for _, proto := range p.running {
		proto := proto
		proto.closed = p.closed
		proto.wstart = writeStart
		proto.werr = writeErr
		var rw MsgReadWriter = proto
		if p.events != nil {
			rw = newMsgEventer(rw, p.events, p.ID(), proto.Name, p.Info().Network.RemoteAddress, p.Info().Network.LocalAddress)
		}
		p.log.Trace(fmt.Sprintf("Starting protocol %s/%d", proto.Name, proto.Version))
		go func() {
			defer p.wg.Done()
			err := proto.Run(p, rw)
			if err == nil {
				p.log.Trace(fmt.Sprintf("Protocol %s/%d returned", proto.Name, proto.Version))
				err = errProtocolReturned
			} else if !errors.Is(err, io.EOF) {
				p.log.Trace(fmt.Sprintf("Protocol %s/%d failed", proto.Name, proto.Version), "err", err)
			}
			p.protoErr <- err
		}()
	}
}

// getProto 找到负责处理给定消息代码的协议。
func (p *Peer) getProto(code uint64) (*protoRW, error) {
	for _, proto := range p.running {
		if code >= proto.offset && code < proto.offset+proto.Length {
			return proto, nil
		}
	}
	return nil, newPeerError(errInvalidMsgCode, "%d", code)
}

// Caps返回远程对等方的功能（支持的子程序）。
func (p *Peer) Caps() []Cap {
	return p.rw.caps
}

// Node返回对等节点的节点描述符。
func (p *Peer) Node() *enode.Node {
	return p.rw.node
}

// Fullname返回远程节点播发的节点名。
func (p *Peer) Fullname() string {
	return p.rw.name
}

// PeerInfo表示有关已连接对等方的已知信息的简短摘要。
//此处包含并初始化了与子协议无关的字段，并将协议细节委托给所有连接的子协议。
type PeerInfo struct {
	ENR     string   `json:"enr,omitempty"` // 以太坊节点记录
	Enode   string   `json:"enode"`         // 节点URL
	ID      string   `json:"id"`            // 唯一节点标识符
	Name    string   `json:"name"`          // 节点的名称，包括客户端类型、版本、操作系统、自定义数据
	Caps    []string `json:"caps"`          // 此对等方公布的协议
	Network struct {
		LocalAddress  string `json:"localAddress"`  // TCP数据连接的本地端点
		RemoteAddress string `json:"remoteAddress"` // TCP数据连接的远程端点
		Inbound       bool   `json:"inbound"`
		Trusted       bool   `json:"trusted"`
		Static        bool   `json:"static"`
	} `json:"network"`
	Protocols map[string]interface{} `json:"protocols"` // 子协议特定元数据字段
}

// Info收集并返回关于对等方的已知元数据集合。
func (p *Peer) Info() *PeerInfo {
	// 收集协议功能
	var caps []string
	for _, cap := range p.Caps() {
		caps = append(caps, cap.String())
	}
	// 组装通用对等元数据
	info := &PeerInfo{
		Enode:     p.Node().URLv4(),
		ID:        p.ID().String(),
		Name:      p.Fullname(),
		Caps:      caps,
		Protocols: make(map[string]interface{}),
	}
	if p.Node().Seq() > 0 {
		info.ENR = p.Node().String()
	}
	info.Network.LocalAddress = p.LocalAddr().String()
	info.Network.RemoteAddress = p.RemoteAddr().String()
	info.Network.Inbound = p.rw.is(inboundConn)
	info.Network.Trusted = p.rw.is(trustedConn)
	info.Network.Static = p.rw.is(staticDialedConn)

	// 收集所有正在运行的协议信息
	for _, proto := range p.running {
		protoInfo := interface{}("unknown")
		if query := proto.Protocol.PeerInfo; query != nil {
			if metadata := query(p.ID()); metadata != nil {
				protoInfo = metadata
			} else {
				protoInfo = "handshake"
			}
		}
		info.Protocols[proto.Name] = protoInfo
	}
	return info
}

// matchProtocols创建用于匹配命名子目录的结构。
func matchProtocols(protocols []Protocol, caps []Cap, rw MsgReadWriter) map[string]*protoRW {
	sort.Sort(capsByNameAndVersion(caps))
	offset := baseProtocolLength
	result := make(map[string]*protoRW)

outer:
	for _, cap := range caps {
		for _, proto := range protocols {
			if proto.Name == cap.Name && proto.Version == cap.Version {
				// 如果旧协议版本匹配，则将其还原
				if old := result[cap.Name]; old != nil {
					offset -= old.Length
				}
				// 分配新匹配项
				result[cap.Name] = &protoRW{Protocol: proto, offset: offset, in: make(chan Msg), w: rw}
				offset += proto.Length

				continue outer
			}
		}
	}
	return result
}

// PeerEventType 是p2p发出的对等事件的类型。服务器
type PeerEventType string

// PeerEvent 是对等点添加或删除时发出的事件。服务器或在对等连接上发送或接收消息时
type PeerEvent struct {
	Type          PeerEventType `json:"type"`
	Peer          enode.ID      `json:"peer"`
	Error         string        `json:"error,omitempty"`
	Protocol      string        `json:"protocol,omitempty"`
	MsgCode       *uint64       `json:"msg_code,omitempty"`
	MsgSize       *uint32       `json:"msg_size,omitempty"`
	LocalAddress  string        `json:"local,omitempty"`
	RemoteAddress string        `json:"remote,omitempty"`
}

type protoRW struct {
	Protocol
	in     chan Msg        // 接收已读消息
	closed <-chan struct{} // 对等计算机关闭时接收
	wstart <-chan struct{} // 开始写入时接收
	werr   chan<- error    // 对于写入结果
	offset uint64
	w      MsgWriter
}

func (rw *protoRW) ReadMsg() (Msg, error) {
	select {
	case msg := <-rw.in:
		msg.Code -= rw.offset
		return msg, nil
	case <-rw.closed:
		return Msg{}, io.EOF
	}
}

func (rw *protoRW) WriteMsg(msg Msg) (err error) {
	if msg.Code >= rw.Length {
		return newPeerError(errInvalidMsgCode, "not handled")
	}
	msg.meterCap = rw.cap()
	msg.meterCode = msg.Code

	msg.Code += rw.offset

	select {
	case <-rw.wstart:
		err = rw.w.WriteMsg(msg)
		// 将写状态报告回对等方。跑如果错误为非零，它将启动关机，否则将取消阻止下一次写入。
		//调用协议代码也应该因错误而退出，但我们不希望依赖于此。
		rw.werr <- err
	case <-rw.closed:
		err = ErrShuttingDown
	}
	return err
}

func countMatchingProtocols(protocols []Protocol, caps []Cap) int {
	n := 0
	for _, cap := range caps {
		for _, proto := range protocols {
			if proto.Name == cap.Name && proto.Version == cap.Version {
				n++
			}
		}
	}
	return n
}
