package p2p

import (
	"bytes"
	"crypto/ecdsa"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity/bitutil"
	"github.com/radiation-octopus/octopus-blockchain/metrics"
	"github.com/radiation-octopus/octopus-blockchain/p2p/rlpx"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus/utils"
	"io"
	"net"
	"sync"
	"time"
)

const (
	//双向加密握手和协议握手的总超时。
	handshakeTimeout = 5 * time.Second

	// 这是发送断开原因的超时。
	//这比通常的超时要短，因为如果已知连接存在问题，我们不想等待。
	discWriteTimeout = 1 * time.Second
)

// rlpxTransport是实际（非测试）连接使用的传输。它使用锁和读/写截止日期包装RLPx连接。
type rlpxTransport struct {
	rmu, wmu sync.Mutex
	wbuf     bytes.Buffer
	conn     *rlpx.Conn
}

func (t *rlpxTransport) doEncHandshake(prv *ecdsa.PrivateKey) (*ecdsa.PublicKey, error) {
	t.conn.SetDeadline(time.Now().Add(handshakeTimeout))
	return t.conn.Handshake(prv)
}

func (t *rlpxTransport) doProtoHandshake(our *protoHandshake) (their *protoHandshake, err error) {
	// 同时写入握手，我们更喜欢返回握手读取错误。
	//如果远程端有正当理由提前断开我们的连接，我们应该将其作为错误返回，以便可以在其他地方跟踪它。
	werr := make(chan error, 1)
	go func() { werr <- Send(t, handshakeMsg, our) }()
	if their, err = readProtocolHandshake(t); err != nil {
		<-werr // 确保写入也终止
		return nil, err
	}
	if err := <-werr; err != nil {
		return nil, fmt.Errorf("write error: %v", err)
	}
	// 如果协议版本支持Snappy编码，请立即升级
	t.conn.SetSnappy(their.Version >= snappyProtocolVersion)

	return their, nil
}

func (t *rlpxTransport) ReadMsg() (Msg, error) {
	t.rmu.Lock()
	defer t.rmu.Unlock()

	var msg Msg
	t.conn.SetReadDeadline(time.Now().Add(frameReadTimeout))
	code, data, wireSize, err := t.conn.Read()
	if err == nil {
		// 协议消息被异步调度到子程序处理程序，但包rlpx可以在下一次调用Read时重用返回的“数据”缓冲区。
		//复制消息数据以避免出现问题。
		data = utils.CopyBytes(data)
		msg = Msg{
			ReceivedAt: time.Now(),
			Code:       code,
			Size:       uint32(len(data)),
			meterSize:  uint32(wireSize),
			Payload:    bytes.NewReader(data),
		}
	}
	return msg, err
}

func (t *rlpxTransport) WriteMsg(msg Msg) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()

	// 将消息数据复制到写入缓冲区。
	t.wbuf.Reset()
	if _, err := io.CopyN(&t.wbuf, msg.Payload, int64(msg.Size)); err != nil {
		return err
	}

	// 写下信息。
	t.conn.SetWriteDeadline(time.Now().Add(frameWriteTimeout))
	size, err := t.conn.Write(msg.Code, t.wbuf.Bytes())
	if err != nil {
		return err
	}

	// Set metrics.
	msg.meterSize = size
	if metrics.Enabled && msg.meterCap.Name != "" { // 不测量非子程序消息
		//m := fmt.Sprintf("%s/%s/%d/%#02x", egressMeterName, msg.meterCap.Name, msg.meterCap.Version, msg.meterCode)
		//metrics.GetOrRegisterMeter(m, nil).Mark(int64(msg.meterSize))
		//metrics.GetOrRegisterMeter(m+"/packets", nil).Mark(1)
	}
	return nil
}

func (t *rlpxTransport) close(err error) {
	t.wmu.Lock()
	defer t.wmu.Unlock()

	// 如果可能的话，告诉远端我们为什么要断开连接。只有在底层连接支持设置超时时，我们才会麻烦地这样做。
	if t.conn != nil {
		if r, ok := err.(DiscReason); ok && r != DiscNetworkError {
			deadline := time.Now().Add(discWriteTimeout)
			if err := t.conn.SetWriteDeadline(deadline); err == nil {
				// 连接支持写入截止时间。
				t.wbuf.Reset()
				rlp.Encode(&t.wbuf, []DiscReason{r})
				t.conn.Write(discMsg, t.wbuf.Bytes())
			}
		}
	}
	t.conn.Close()
}

func newRLPX(conn net.Conn, dialDest *ecdsa.PublicKey) transport {
	return &rlpxTransport{conn: rlpx.NewConn(conn, dialDest)}
}

func readProtocolHandshake(rw MsgReader) (*protoHandshake, error) {
	msg, err := rw.ReadMsg()
	if err != nil {
		return nil, err
	}
	if msg.Size > baseProtocolMaxMsgSize {
		return nil, fmt.Errorf("message too big")
	}
	if msg.Code == discMsg {
		// 根据规范，在协议握手有效之前断开连接，如果握手后检查失败，我们将自行发送。
		//然而，我们不能直接返回原因，因为它在其他方面也得到了回应。用一根绳子把它包起来。
		var reason [1]DiscReason
		rlp.Decode(msg.Payload, &reason)
		return nil, reason[0]
	}
	if msg.Code != handshakeMsg {
		return nil, fmt.Errorf("expected handshake, got %x", msg.Code)
	}
	var hs protoHandshake
	if err := msg.Decode(&hs); err != nil {
		return nil, err
	}
	if len(hs.ID) != 64 || !bitutil.TestBytes(hs.ID) {
		return nil, DiscInvalidIdentity
	}
	return &hs, nil
}
