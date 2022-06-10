package node

import (
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"sync"
)

// Manager是一个总体客户经理，可以与各种后端进行通信以签署交易。
type Manager struct {
	config *Config // 全球客户经理配置
	//backends    map[reflect.Type][]Backend // 当前注册的后端索引
	//updaters    []event.Subscription       // 所有后端的钱包更新订阅
	//updates     chan WalletEvent           // 后端钱包更改订阅接收器
	//newBackends chan newBackendEvent       // 要由经理跟踪的传入后端
	//wallets     []Wallet                   // 缓存所有注册后端的所有钱包

	feed blockchain.Feed // 钱包馈送通知到达/离开

	quit chan chan error
	term chan struct{} // 更新循环终止时通道关闭
	lock sync.RWMutex
}

// NewManager创建一个通用帐户管理器，通过各种支持的后端签署交易。
func NewManager(config *Config, backends ...Backend) *Manager {
	// 从后端检索钱包的初始列表并按URL排序
	//var wallets []Wallet
	//for _, backend := range backends {
	//	wallets = merge(wallets, backend.Wallets()...)
	//}
	// 从所有后端订阅钱包通知
	//updates := make(chan WalletEvent, managerSubBufferSize)

	//subs := make([]event.Subscription, len(backends))
	//for i, backend := range backends {
	//	subs[i] = backend.Subscribe(updates)
	//}
	// 组装客户经理并返回
	am := &Manager{
		config: config,
		//backends:    make(map[reflect.Type][]Backend),
		//updaters:    subs,
		//updates:     updates,
		//newBackends: make(chan newBackendEvent),
		//wallets:     wallets,
		quit: make(chan chan error),
		term: make(chan struct{}),
	}
	//for _, backend := range backends {
	//	kind := reflect.TypeOf(backend)
	//	am.backends[kind] = append(am.backends[kind], backend)
	//}
	//go am.update()

	return am
}
