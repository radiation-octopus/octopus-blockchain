package node

import "github.com/radiation-octopus/octopus-blockchain/blockchain"

type Wallet struct {
}

//后端是一个“钱包提供商”，其中可能包含一批他们可以签署交易的账户，并可根据请求签署交易。
type Backend interface {
	// Wallet检索后端当前知道的钱包列表。
	//默认情况下，不会打开返回的钱包。
	//对于软件高清钱包，这意味着没有解密任何基本种子，而对于硬件钱包，则没有建立任何实际连接。
	//生成的钱包列表将根据后端分配的内部URL按字母顺序排序。
	//由于钱包（尤其是硬件）可能来去不定，因此在后续检索过程中，同一个钱包可能会出现在列表中的不同位置。
	Wallets() []Wallet

	// Subscribe创建异步订阅，以便在后端检测到钱包到达或离开时接收通知。
	Subscribe(sink chan<- WalletEvent) blockchain.Subscription
}

//WalletEventType表示钱包订阅子系统可以触发的不同事件类型。
type WalletEventType int

//WalletEvent是在检测到钱包到达或离开时由帐户后端触发的事件。
type WalletEvent struct {
	Wallet Wallet          // 钱包实例到达或离开
	Kind   WalletEventType // 系统中发生的事件类型
}
