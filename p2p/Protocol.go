package p2p

import (
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/p2p/enode"
	"github.com/radiation-octopus/octopus-blockchain/p2p/enr"
)

//协议表示一个P2P子协议实现。
type Protocol struct {
	// 名称应包含正式协议名称，通常为三个字母的单词。
	Name string

	// 版本应包含协议的版本号。
	Version uint

	// 长度应包含协议使用的消息代码数。
	Length uint64

	// 当与对等方协商协议时，在新的goroutine中调用Run。
	//它应该读写来自rw的消息。必须完全消耗每条消息的有效负载。
	//当Start返回时，对等连接关闭。它应该返回遇到的任何协议级错误（例如输入/输出错误）。
	Run func(peer *Peer, rw MsgReadWriter) error

	// NodeInfo是一种可选的辅助方法，用于检索有关主机节点的协议特定元数据。
	NodeInfo func() interface{}

	//PeerInfo是一种可选的辅助方法，用于检索网络中特定对等方的协议特定元数据。
	//如果设置了信息检索函数，但返回nil，则假设协议握手仍在运行。
	PeerInfo func(id enode.ID) interface{}

	// DialCandidates（若非nil）是一种告诉服务器应该拨打的协议特定节点的方法。
	//服务器不断地从迭代器中读取节点，并尝试创建到这些节点的连接。
	DialCandidates enode.Iterator

	// 属性包含节点记录的协议特定信息。
	Attributes []enr.Entry
}

func (p Protocol) cap() Cap {
	return Cap{p.Name, p.Version}
}

// Cap是对等能力的结构。
type Cap struct {
	Name    string
	Version uint
}

func (cap Cap) String() string {
	return fmt.Sprintf("%s/%d", cap.Name, cap.Version)
}

type capsByNameAndVersion []Cap

func (cs capsByNameAndVersion) Len() int      { return len(cs) }
func (cs capsByNameAndVersion) Swap(i, j int) { cs[i], cs[j] = cs[j], cs[i] }
func (cs capsByNameAndVersion) Less(i, j int) bool {
	return cs[i].Name < cs[j].Name || (cs[i].Name == cs[j].Name && cs[i].Version < cs[j].Version)
}
