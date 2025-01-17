package accounts

import (
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/event"
	"reflect"
	"sort"
	"sync"
)

// managerSubBufferSize确定manager将在其通道中缓冲多少传入钱包事件。
const managerSubBufferSize = 50

// 被移除以支持Clef。
type Config struct {
	InsecureUnlockAllowed bool // 是否允许在不安全的环境下解锁帐户
}

// Manager是一个总体客户经理，可以与各种后端进行通信以签署交易。
type Manager struct {
	config      *Config                    // 全球客户经理配置
	backends    map[reflect.Type][]Backend // 当前注册的后端索引
	updaters    []event.Subscription       // 所有后端的钱包更新订阅
	updates     chan WalletEvent           // 后端钱包更改订阅接收器
	newBackends chan newBackendEvent       // 要由经理跟踪的传入后端
	wallets     []Wallet                   // 缓存所有注册后端的所有钱包

	feed event.Feed // 钱包馈送通知到达/离开

	quit chan chan error
	term chan struct{} // 更新循环终止时通道关闭
	lock sync.RWMutex
}

//更新是钱包事件循环，用于侦听来自后端的通知并更新钱包缓存。
func (am *Manager) update() {
	// 管理器终止时关闭所有订阅
	defer func() {
		am.lock.Lock()
		for _, sub := range am.updaters {
			sub.Unsubscribe()
		}
		am.updaters = nil
		am.lock.Unlock()
	}()

	// 循环直到终止
	for {
		select {
		case event := <-am.updates:
			// 钱包事件到达，更新本地缓存
			am.lock.Lock()
			switch event.Kind {
			case WalletArrived:
				am.wallets = merge(am.wallets, event.Wallet)
			case WalletDropped:
				am.wallets = drop(am.wallets, event.Wallet)
			}
			am.lock.Unlock()

			// 通知事件的所有侦听器
			am.feed.Send(event)
		case event := <-am.newBackends:
			am.lock.Lock()
			// 更新缓存
			backend := event.backend
			am.wallets = merge(am.wallets, backend.Wallets()...)
			am.updaters = append(am.updaters, backend.Subscribe(am.updates))
			kind := reflect.TypeOf(backend)
			am.backends[kind] = append(am.backends[kind], backend)
			am.lock.Unlock()
			close(event.processed)
		case errc := <-am.quit:
			// 经理终止，返回
			errc <- nil
			// 信号事件发射器循环未接收值以防止其卡住。
			close(am.term)
			return
		}
	}
}

//AddBackend开始跟踪钱包更新的其他后端。cmd/geth假设此func返回后，后端已经集成。
func (am *Manager) AddBackend(backend Backend) {
	done := make(chan struct{})
	am.newBackends <- newBackendEvent{backend, done}
	<-done
}

// Subscribe创建异步订阅，以便在经理检测到钱包从其任何后端到达或离开时接收通知。
func (am *Manager) Subscribe(sink chan<- WalletEvent) event.Subscription {
	return am.feed.Subscribe(sink)
}

// 后端从帐户管理器检索具有给定类型的后端。
func (am *Manager) Backends(kind reflect.Type) []Backend {
	am.lock.RLock()
	defer am.lock.RUnlock()
	fmt.Println(am.backends[kind])
	return am.backends[kind]
}

// Wallet检索与特定URL关联的钱包。
func (am *Manager) Wallet(url string) (Wallet, error) {
	am.lock.RLock()
	defer am.lock.RUnlock()

	parsed, err := parseURL(url)
	if err != nil {
		return nil, err
	}
	for _, wallet := range am.walletsNoLock() {
		if wallet.URL() == parsed {
			return wallet, nil
		}
	}
	return nil, ErrUnknownWallet
}

// Accounts返回acentitymanager中所有钱包的所有帐户地址
func (am *Manager) Accounts() []entity.Address {
	am.lock.RLock()
	defer am.lock.RUnlock()

	addresses := make([]entity.Address, 0) // 如果为空，则返回[]，而不是nil
	for _, wallet := range am.wallets {
		for _, account := range wallet.Accounts() {
			addresses = append(addresses, account.Address)
		}
	}
	return addresses
}

// Wallets返回在此帐户管理器下注册的所有签名者帐户。
func (am *Manager) Wallets() []Wallet {
	am.lock.RLock()
	defer am.lock.RUnlock()

	return am.walletsNoLock()
}

// 查找尝试查找与特定帐户对应的钱包。由于帐户可以动态添加到钱包中或从钱包中删除，因此此方法在钱包数量上具有线性运行时。
func (am *Manager) Find(account Account) (Wallet, error) {
	am.lock.RLock()
	defer am.lock.RUnlock()

	for _, wallet := range am.wallets {
		if wallet.Contains(account) {
			return wallet, nil
		}
	}
	return nil, ErrUnknownAccount
}

// walletsNoLock返回所有注册的钱包。呼叫者必须持有am。锁
func (am *Manager) walletsNoLock() []Wallet {
	cpy := make([]Wallet, len(am.wallets))
	copy(cpy, am.wallets)
	return cpy
}

// 关闭终止客户经理的内部通知过程。
func (am *Manager) Close() error {
	errc := make(chan error)
	am.quit <- errc
	return <-errc
}

// Config返回帐户管理器的配置。
func (am *Manager) Config() *Config {
	return am.config
}

//newBackendEvent让经理知道它应该跟踪给定的后端以进行钱包更新。
type newBackendEvent struct {
	backend   Backend
	processed chan struct{} //通知事件发射器后端已集成
}

// NewManager创建一个通用帐户管理器，通过各种支持的后端签署交易。
func NewManager(config *Config, backends ...Backend) *Manager {
	// 从后端检索钱包的初始列表并按URL排序
	var wallets []Wallet
	for _, backend := range backends {
		wallets = merge(wallets, backend.Wallets()...)
	}
	//从所有后端订阅钱包通知
	updates := make(chan WalletEvent, managerSubBufferSize)

	subs := make([]event.Subscription, len(backends))
	for i, backend := range backends {
		subs[i] = backend.Subscribe(updates)
	}
	// 组装客户经理并返回
	am := &Manager{
		config:      config,
		backends:    make(map[reflect.Type][]Backend),
		updaters:    subs,
		updates:     updates,
		newBackends: make(chan newBackendEvent),
		wallets:     wallets,
		quit:        make(chan chan error),
		term:        make(chan struct{}),
	}
	for _, backend := range backends {
		kind := reflect.TypeOf(backend)
		am.backends[kind] = append(am.backends[kind], backend)
	}
	go am.update()

	return am
}

//merge是一种类似于append for Wallet的排序方法，通过在正确的位置插入新的钱包，可以保持原始列表的顺序。
//假设原始切片已按URL排序。
func merge(slice []Wallet, wallets ...Wallet) []Wallet {
	for _, wallet := range wallets {
		n := sort.Search(len(slice), func(i int) bool { return slice[i].URL().Cmp(wallet.URL()) >= 0 })
		if n == len(slice) {
			slice = append(slice, wallet)
			continue
		}
		slice = append(slice[:n], append([]Wallet{wallet}, slice[n:]...)...)
	}
	return slice
}

//drop是merge的couterpart，它从已排序的缓存中查找钱包并删除指定的钱包。
func drop(slice []Wallet, wallets ...Wallet) []Wallet {
	for _, wallet := range wallets {
		n := sort.Search(len(slice), func(i int) bool { return slice[i].URL().Cmp(wallet.URL()) >= 0 })
		if n == len(slice) {
			// 钱包未找到，可能在启动过程中发生
			continue
		}
		slice = append(slice[:n], slice[n+1:]...)
	}
	return slice
}
