package p2p

import (
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/event"
	"github.com/radiation-octopus/octopus-blockchain/p2p/enode"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"io"
	"sync/atomic"
	"time"
)

// Msg定义p2p消息的结构。
//请注意，由于有效负载读取器在发送过程中被消耗，因此消息只能发送一次。
//创建消息并发送任意次数都是不可能的。
//如果要重用编码结构，请将有效负载编码到字节数组中，并使用字节创建单独的消息。
//读卡器作为每次发送的有效负载。
type Msg struct {
	Code       uint64
	Size       uint32 // 原始有效负载的大小
	Payload    io.Reader
	ReceivedAt time.Time

	meterCap  Cap    // 出口计量的协议名称和版本
	meterCode uint64 // 出口计量协议内的消息
	meterSize uint32 // 用于入口计量的压缩消息大小
}

func (msg Msg) Time() time.Time {
	return msg.ReceivedAt
}

// 解码将消息的RLP内容解析为给定值，该值必须是指针。有关解码规则，请参阅包rlp。
func (msg Msg) Decode(val interface{}) error {
	s := rlp.NewStream(msg.Payload, uint64(msg.Size))
	if err := s.Decode(val); err != nil {
		return newPeerError(errInvalidMsg, "(code %x) (size %d) %v", msg.Code, msg.Size, err)
	}
	return nil
}

//Discard将剩余的有效载荷数据读入黑洞。
func (msg Msg) Discard() error {
	//_, err := io.Copy(io.Discard, msg.Payload)
	return nil
}

type MsgReader interface {
	ReadMsg() (Msg, error)
}

type MsgWriter interface {
	// WriteMsg发送消息。它将阻塞，直到消息的有效负载被另一端消耗。
	//请注意，消息只能发送一次，因为它们的有效负载读取器已耗尽。
	WriteMsg(Msg) error
}

//MsgReadWriter提供对编码消息的读写。
//实现应该确保可以从多个goroutine同时调用ReadMsg和WriteMsg。
type MsgReadWriter interface {
	MsgReader
	MsgWriter
}

// Send用给定的代码编写RLP编码的消息。数据应编码为RLP列表。
func Send(w MsgWriter, msgcode uint64, data interface{}) error {
	size, r, err := rlp.EncodeToReader(data)
	if err != nil {
		return err
	}
	return w.WriteMsg(Msg{Code: msgcode, Size: uint32(size), Payload: r})
}

func SendItems(w MsgWriter, msgcode uint64, elems ...interface{}) error {
	return Send(w, msgcode, elems)
}

// ExpectMsg从r读取消息，并验证其代码和编码的RLP内容是否与提供的值匹配。
//如果内容为零，则丢弃有效负载，并且不进行验证。
func ExpectMsg(r MsgReader, code uint64, content interface{}) error {
	msg, err := r.ReadMsg()
	if err != nil {
		return err
	}
	if msg.Code != code {
		return fmt.Errorf("message code mismatch: got %d, expected %d", msg.Code, code)
	}
	if content == nil {
		return msg.Discard()
	}
	contentEnc, err := rlp.EncodeToBytes(content)
	if err != nil {
		panic("content encode error: " + err.Error())
	}
	if int(msg.Size) != len(contentEnc) {
		return fmt.Errorf("message size mismatch: got %d, want %d", msg.Size, len(contentEnc))
	}
	//456actualContent, err := io.ReadAll(msg.Payload)
	if err != nil {
		return err
	}
	//if !bytes.Equal(actualContent, contentEnc) {
	//	return fmt.Errorf("message payload mismatch:\ngot:  %x\nwant: %x", actualContent, contentEnc)
	//}
	return nil
}

// MsgPipeRW是MsgReadWriter管道的端点。
type MsgPipeRW struct {
	w       chan<- Msg
	r       <-chan Msg
	closing chan struct{}
	closed  *int32
}

// Close取消阻止管道两端的任何挂起的ReadMsg和WriteMsg调用。
//他们将在关闭后返回。Close还会中断对消息负载的任何读取。
func (p *MsgPipeRW) Close() error {
	if atomic.AddInt32(p.closed, 1) != 1 {
		// 其他人已经关门了
		atomic.StoreInt32(p.closed, 1) // 避免溢出
		return nil
	}
	close(p.closing)
	return nil
}

//每当发送或接收消息时，msgEventer包装MsgReadWriter并发送事件
type msgEventer struct {
	MsgReadWriter

	feed          *event.Feed
	peerID        enode.ID
	Protocol      string
	localAddress  string
	remoteAddress string
}

//newMsgEventer返回一个msgEventer，该msgEventer向给定提要发送消息事件
func newMsgEventer(rw MsgReadWriter, feed *event.Feed, peerID enode.ID, proto, remote, local string) *msgEventer {
	return &msgEventer{
		MsgReadWriter: rw,
		feed:          feed,
		peerID:        peerID,
		Protocol:      proto,
		remoteAddress: remote,
		localAddress:  local,
	}
}
