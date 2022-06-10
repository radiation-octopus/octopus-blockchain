package blockchain

import (
	"container/heap"
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus-blockchain/terr"
	"github.com/radiation-octopus/octopus-blockchain/transition"
	"github.com/radiation-octopus/octopus/log"
	"github.com/radiation-octopus/octopus/utils"
	"math"
	"math/big"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

//区块链提供区块链的状态和当前气体限制，以便在tx池和事件订阅者中进行一些预检查。
type blockChainop interface {
	CurrentBlock() *block.Block
	GetBlock(hash entity.Hash, number uint64) *block.Block
	StateAt(root entity.Hash) (*operationdb.OperationDB, error)
	SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) Subscription
}

const (
	//chainHeadChanSize是侦听ChainHeadEvent的通道的大小。
	chainHeadChanSize = 10

	//txSlotSize用于根据单个事务的大小计算其占用的数据槽数。插槽用作DoS保护，确保验证新事务保持不变（实际上是O（maxslots），其中当前最大插槽为4个）。
	txSlotSize = 32 * 1024

	// txMaxSize是单个事务可以具有的最大大小。这一领域有着非同寻常的后果：更大的事务更难传播，成本也更高；更大的事务还需要更多的资源来验证它们是否适合池。
	txMaxSize = 4 * txSlotSize // 128KB

	// urgentRatio:floatingRatio是两个队列的容量比
	urgentRatio   = 4
	floatingRatio = 1
)

var (
	evictionInterval    = time.Minute     //检查可收回事务的时间间隔
	statsReportInterval = 8 * time.Second // 报告事务池统计信息的时间间隔
)

var (
	// 如果事务已包含在池中，则返回ErrAlreadyKnown。
	ErrAlreadyKnown = errors.New("already known")

	// 如果事务包含无效签名，则返回ErrInvalidSender。
	ErrInvalidSender = errors.New("invalid sender")

	// 如果交易的天然气价格低于为交易池配置的最低价格，则返回ErrUnderpriced。
	ErrUnderpriced = errors.New("transaction underpriced")

	// 如果事务池已满且无法访问其他远程事务，则返回ErrTxPoolOverflow。
	ErrTxPoolOverflow = errors.New("txpool is full")

	// 如果试图用不同的交易替换交易而没有所需的涨价，则返回errReplaceUnpriced。
	ErrReplaceUnderpriced = errors.New("replacement transaction underpriced")

	// 如果事务的请求气体限制超过当前块的最大允许量，则返回ErrGasLimit。
	ErrGasLimit = errors.New("exceeds block gas limit")

	// ErrNegativeValue是一个健全错误，用于确保任何人都无法使用负值指定事务。
	ErrNegativeValue = errors.New("negative value")

	// 如果事务的输入数据大于用户可能使用的某个有意义的限制，则返回ErrOversizedData。这不是导致事务无效的一致错误，而是DOS保护。
	ErrOversizedData = errors.New("oversized data")
)

var (
//reorgDurationTimer = metrics.NewRegisteredTimer("txpool/reorgtime", nil)
)

/*
SubscriptionScope提供了一种功能，可以一次取消订阅多个订阅。
对于处理多个订阅的代码，可以使用一个作用域通过单个调用方便地取消所有订阅。该示例演示了在大型程序中的典型用法。
零值已准备好使用。
*/
type SubscriptionScope struct {
	mu     sync.Mutex
	subs   map[*scopeSub]struct{}
	closed bool
}

//Close calls取消对所有跟踪订阅的订阅，并阻止进一步添加到跟踪集。关闭后跟踪的调用返回nil。
func (sc *SubscriptionScope) Close() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.closed {
		return
	}
	sc.closed = true
	for s := range sc.subs {
		s.s.Unsubscribe()
	}
	sc.subs = nil
}

type scopeSub struct {
	sc *SubscriptionScope
	s  Subscription
}

func (s *scopeSub) Err() <-chan error {
	return s.s.Err()
}

func (s *scopeSub) Unsubscribe() {
	s.s.Unsubscribe()
	s.sc.mu.Lock()
	defer s.sc.mu.Unlock()
	delete(s.sc.subs, s)
}

// Track开始跟踪订阅。如果作用域已关闭，Track将返回nil。返回的订阅是包装。取消订阅包装将其从范围中删除。
func (sc *SubscriptionScope) Track(s Subscription) Subscription {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.closed {
		return nil
	}
	if sc.subs == nil {
		sc.subs = make(map[*scopeSub]struct{})
	}
	ss := &scopeSub{sc, s}
	sc.subs[ss] = struct{}{}
	return ss
}

/**
交易池定义
*/
type TxPool struct {
	config TxPoolConfig
	//chainconfig *params.ChainConfig
	chain    blockChainop
	gasPrice *big.Int
	txFeed   Feed
	scope    SubscriptionScope
	signer   block.Signer
	mu       sync.RWMutex

	istanbul bool // Fork指示我们是否处于伊斯坦布尔阶段。
	eip2718  bool // Fork指示器是否使用EIP-2718类型的事务。
	eip1559  bool // Fork指示器是否使用EIP-1559类型的事务。

	currentState  *operationdb.OperationDB // 区块链头部的当前状态
	pendingNonces *txNoncer                // 挂起状态跟踪虚拟nonce
	currentMaxGas uint64                   // 交易上限的当前gas限值

	locals *accountSet // 要免除逐出规则的本地事务集
	//journal *txJournal  // 要备份到磁盘的本地事务日志

	pending map[entity.Address]*txList   // 所有当前可处理的事务
	queue   map[entity.Address]*txList   // 排队但不可处理的事务
	beats   map[entity.Address]time.Time // 每个已知帐户的最后心跳
	all     *txLookup                    // 允许查找的所有事务
	priced  *txPricedList                // 按价格排序的所有交易记录

	chainHeadCh     chan ChainHeadEvent
	chainHeadSub    Subscription
	reqResetCh      chan *txpoolResetRequest
	reqPromoteCh    chan *accountSet
	queueTxEventCh  chan *block.Transaction
	reorgDoneCh     chan chan struct{}
	reorgShutdownCh chan struct{}  // 请求关闭scheduleReorgLoop
	wg              sync.WaitGroup // 轨道运行，scheduleReorgLoop
	InitDoneCh      chan struct{}  // 池初始化后关闭（用于测试）

	changesSinceReorg int // 一个计数器，显示在reorg之间我们执行了多少次下降。
}

type TxPoolConfig struct {
	Locals    []entity.Address // 默认情况下应视为本地的地址
	NoLocals  bool             // 是否应禁用本地事务处理
	Journal   string           // 节点重新启动后的本地事务日志
	Rejournal time.Duration    // 重新生成本地事务日志的时间间隔

	PriceLimit uint64 // 强制执行的最低gas价格，以便进入事务池
	PriceBump  uint64 // 替换现有交易的最低涨价百分比（nonce）

	AccountSlots uint64 // 每个帐户保证的可执行事务插槽数
	GlobalSlots  uint64 // 所有帐户的最大可执行事务插槽数
	AccountQueue uint64 // 每个帐户允许的最大不可执行事务槽数
	GlobalQueue  uint64 // 所有帐户的最大不可执行事务插槽数

	Lifetime time.Duration // 非可执行事务排队的最长时间
}

var DefaultTxPoolConfig = TxPoolConfig{
	Journal:   "transactions.rlp",
	Rejournal: time.Hour,

	PriceLimit: 1,
	PriceBump:  10,

	AccountSlots: 16,
	GlobalSlots:  4096 + 1024, // urgent + floating queue capacity with 4:1 ratio
	AccountQueue: 64,
	GlobalQueue:  1024,

	Lifetime: 3 * time.Hour,
}

// sanitize检查提供的用户配置，并更改任何不合理或不可行的内容。
func (config *TxPoolConfig) sanitize() TxPoolConfig {
	conf := *config
	if conf.Rejournal < time.Second {
		log.Warn("Sanitizing invalid txpool journal time", "provided", conf.Rejournal, "updated", time.Second)
		conf.Rejournal = time.Second
	}
	if conf.PriceLimit < 1 {
		log.Warn("Sanitizing invalid txpool price limit", "provided", conf.PriceLimit, "updated", DefaultTxPoolConfig.PriceLimit)
		conf.PriceLimit = DefaultTxPoolConfig.PriceLimit
	}
	if conf.PriceBump < 1 {
		log.Warn("Sanitizing invalid txpool price bump", "provided", conf.PriceBump, "updated", DefaultTxPoolConfig.PriceBump)
		conf.PriceBump = DefaultTxPoolConfig.PriceBump
	}
	if conf.AccountSlots < 1 {
		log.Warn("Sanitizing invalid txpool account slots", "provided", conf.AccountSlots, "updated", DefaultTxPoolConfig.AccountSlots)
		conf.AccountSlots = DefaultTxPoolConfig.AccountSlots
	}
	if conf.GlobalSlots < 1 {
		log.Warn("Sanitizing invalid txpool global slots", "provided", conf.GlobalSlots, "updated", DefaultTxPoolConfig.GlobalSlots)
		conf.GlobalSlots = DefaultTxPoolConfig.GlobalSlots
	}
	if conf.AccountQueue < 1 {
		log.Warn("Sanitizing invalid txpool account queue", "provided", conf.AccountQueue, "updated", DefaultTxPoolConfig.AccountQueue)
		conf.AccountQueue = DefaultTxPoolConfig.AccountQueue
	}
	if conf.GlobalQueue < 1 {
		log.Warn("Sanitizing invalid txpool global queue", "provided", conf.GlobalQueue, "updated", DefaultTxPoolConfig.GlobalQueue)
		conf.GlobalQueue = DefaultTxPoolConfig.GlobalQueue
	}
	if conf.Lifetime < 1 {
		log.Warn("Sanitizing invalid txpool lifetime", "provided", conf.Lifetime, "updated", DefaultTxPoolConfig.Lifetime)
		conf.Lifetime = DefaultTxPoolConfig.Lifetime
	}
	return conf
}

// NewTxPool创建一个新的事务池来收集、排序和过滤网络中的入站事务。
func NewTxPool(config TxPoolConfig, chain blockChainop) *TxPool {
	// 清理输入，确保未设定易受影响的gas价格
	config = (&config).sanitize()

	// 使用事务池的初始设置创建事务池
	pool := &TxPool{
		config: config,
		//chainconfig:     chainconfig,
		chain:           chain,
		signer:          block.LatestSigner(),
		pending:         make(map[entity.Address]*txList),
		queue:           make(map[entity.Address]*txList),
		beats:           make(map[entity.Address]time.Time),
		all:             newTxLookup(),
		chainHeadCh:     make(chan ChainHeadEvent, chainHeadChanSize),
		reqResetCh:      make(chan *txpoolResetRequest),
		reqPromoteCh:    make(chan *accountSet),
		queueTxEventCh:  make(chan *block.Transaction),
		reorgDoneCh:     make(chan chan struct{}),
		reorgShutdownCh: make(chan struct{}),
		InitDoneCh:      make(chan struct{}),
		gasPrice:        new(big.Int).SetUint64(config.PriceLimit),
	}
	pool.locals = newAccountSet(pool.signer)
	for _, addr := range config.Locals {
		log.Info("Setting new local account", "address", addr)
		pool.locals.add(addr)
	}
	pool.priced = newTxPricedList(pool.all)
	pool.reset(nil, chain.CurrentBlock().Header())

	// 尽早启动reorg循环，以便它可以处理日志加载期间生成的请求。
	pool.wg.Add(1)
	go pool.scheduleReorgLoop()

	// 如果启用了本地事务和日志记录，则从磁盘加载
	//if !config.NoLocals && config.Journal != "" {
	//	pool.journal = newTxJournal(config.Journal)
	//
	//	if terr := pool.journal.load(pool.AddLocals); terr != nil {
	//		log.Warn("Failed to load transaction journal", "terr", terr)
	//	}
	//	if terr := pool.journal.rotate(pool.local()); terr != nil {
	//		log.Warn("Failed to rotate transaction journal", "terr", terr)
	//	}
	//}

	// 从区块链订阅事件并启动主事件循环。
	pool.chainHeadSub = pool.chain.SubscribeChainHeadEvent(pool.chainHeadCh)
	pool.wg.Add(1)
	go pool.loop()

	return pool
}

// newTxLookup returns a new txLookup structure.
func newTxLookup() *txLookup {
	return &txLookup{
		locals:  make(map[entity.Hash]*block.Transaction),
		remotes: make(map[entity.Hash]*block.Transaction),
	}
}

func (pool *TxPool) Pending(enforceTips bool) map[entity.Address]block.Transactions {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	pending := make(map[entity.Address]block.Transactions)
	for addr, list := range pool.pending {
		txs := list.Flatten()

		//如果工作者要求执行小费，现在就封顶名单
		//if enforceTips && !pool.locals.contains(addr) {
		//	for i, tx := range txs {
		//		if tx.EffectiveGasTipIntCmp(pool.gasPrice, pool.priced.urgent.baseFee) < 0 {
		//			txs = txs[:i]
		//			break
		//		}
		//	}
		//}
		if len(txs) > 0 {
			pending[addr] = txs
		}
	}
	return pending
}

//Locals检索池当前认为是本地的帐户。
func (pool *TxPool) Locals() []entity.Address {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	return pool.locals.flatten()
}

// SubscribeNewTxsEvent注册NewTxsEvent的订阅，并开始向给定通道发送事件。
func (pool *TxPool) SubscribeNewTxsEvent(ch chan<- NewTxsEvent) Subscription {
	return pool.scope.Track(pool.txFeed.Subscribe(ch))
}

// scheduleReorgLoop计划reset和PromoteExecutable的运行。上面的代码不应该直接调用这些方法，而是使用requestReset和requestPromoteExecutables请求正在运行的方法。
func (pool *TxPool) scheduleReorgLoop() {
	defer pool.wg.Done()

	var (
		curDone       chan struct{} // runReorg处于活动状态时非nil
		nextDone      = make(chan struct{})
		launchNextRun bool
		reset         *txpoolResetRequest
		dirtyAccounts *accountSet
		queuedEvents  = make(map[entity.Address]*txSortedMap)
	)
	for {
		// 如果需要，启动下一个后台reorg
		if curDone == nil && launchNextRun {
			// 运行后台reorg和公告
			go pool.runReorg(nextDone, reset, dirtyAccounts, queuedEvents)

			// 为下一轮reorg做好准备
			curDone, nextDone = nextDone, make(chan struct{})
			launchNextRun = false

			reset, dirtyAccounts = nil, nil
			queuedEvents = make(map[entity.Address]*txSortedMap)
		}

		select {
		case req := <-pool.reqResetCh:
			// 重置请求：如果请求已挂起，则更新标头。
			if reset == nil {
				reset = req
			} else {
				reset.newHead = req.newHead
			}
			launchNextRun = true
			pool.reorgDoneCh <- nextDone

		case req := <-pool.reqPromoteCh:
			// 升级请求：如果请求已挂起，则更新地址集。
			if dirtyAccounts == nil {
				dirtyAccounts = req
			} else {
				//dirtyAccounts.merge(req)
			}
			launchNextRun = true
			pool.reorgDoneCh <- nextDone

		case tx := <-pool.queueTxEventCh:
			// 将事件排队，但不要安排reorg。如果调用方希望发送事件，则由调用方稍后请求。
			addr, _ := block.Sender(pool.signer, tx)
			if _, ok := queuedEvents[addr]; !ok {
				queuedEvents[addr] = newTxSortedMap()
			}
			queuedEvents[addr].Put(tx)

		case <-curDone:
			curDone = nil

		case <-pool.reorgShutdownCh:
			// 等待当前运行完成。
			if curDone != nil {
				<-curDone
			}
			close(nextDone)
			return
		}
	}
}

//runReorg代表scheduleReorgLoop运行reset和promoteExecutables。
func (pool *TxPool) runReorg(done chan struct{}, reset *txpoolResetRequest, dirtyAccounts *accountSet, events map[entity.Address]*txSortedMap) {
	defer func(t0 time.Time) {
		//reorgDurationTimer.Update(time.Since(t0))
	}(time.Now())
	defer close(done)

	var promoteAddrs []entity.Address
	if dirtyAccounts != nil && reset == nil {
		// 只有脏账户需要升级，除非我们正在重置。
		//对于重置，将提升tx队列中的所有地址，并且可以避免展平操作。
		promoteAddrs = dirtyAccounts.flatten()
	}
	pool.mu.Lock()
	if reset != nil {
		// 从旧磁头重置为新磁头，重新安排任何重新调整的交易
		pool.reset(reset.oldHead, reset.newHead)

		// 已重置Nonces，丢弃任何过时的事件`
		for addr := range events {
			events[addr].Forward(pool.pendingNonces.get(addr))
			if events[addr].Len() == 0 {
				delete(events, addr)
			}
		}
		// 重置所有地址需要升级
		promoteAddrs = make([]entity.Address, 0, len(pool.queue))
		for addr := range pool.queue {
			promoteAddrs = append(promoteAddrs, addr)
		}
	}
	// 检查发送新交易的每个帐户的挂起交易
	promoted := pool.promoteExecutables(promoteAddrs)

	// 如果出现新的块，请验证挂起的事务池。这将删除已包含在区块中或因其他交易（例如，较高的gas价格）而无效的任何交易。
	if reset != nil {
		pool.demoteUnexecutables()
		//if reset.newHead != nil && pool.chainconfig.IsLondon(new(big.Int).Add(reset.newHead.Number, big.NewInt(1))) {
		//	pendingBaseFee := misc.CalcBaseFee(pool.chainconfig, reset.newHead)
		//	pool.priced.SetBaseFee(pendingBaseFee)
		//}
		// 将所有帐户更新为最新的已知挂起状态
		nonces := make(map[entity.Address]uint64, len(pool.pending))
		for addr, list := range pool.pending {
			highestPending := list.LastElement()
			nonces[addr] = highestPending.Nonce() + 1
		}
		pool.pendingNonces.setAll(nonces)
	}
	// 确保池,队列和池。挂起的大小保持在配置的限制内。
	pool.truncatePending()
	pool.truncateQueue()

	//dropBetweenReorgHistogram.Update(int64(pool.changesSinceReorg))
	pool.changesSinceReorg = 0 //重置更改计数器
	pool.mu.Unlock()

	// 通知子系统新添加的事务
	for _, tx := range promoted {
		addr, _ := block.Sender(pool.signer, tx)
		if _, ok := events[addr]; !ok {
			events[addr] = newTxSortedMap()
		}
		events[addr].Put(tx)
	}
	if len(events) > 0 {
		var txs []*block.Transaction
		for _, set := range events {
			txs = append(txs, set.Flatten()...)
		}
		pool.txFeed.Send(NewTxsEvent{txs})
	}
}

// 重置检索区块链的当前状态，并确保交易池的内容相对于链状态有效。
func (pool *TxPool) reset(oldHead, newHead *block.Header) {
	// 如果要重新调整旧状态，请重新注入所有丢弃的事务
	var reinject block.Transactions

	if oldHead != nil && oldHead.Hash() != newHead.ParentHash {
		//如果reorg太深，请避免这样做（将在快速同步期间发生）
		oldNum := oldHead.Number.Uint64()
		newNum := newHead.Number.Uint64()

		if depth := uint64(math.Abs(float64(oldNum) - float64(newNum))); depth > 64 {
			log.Debug("Skipping deep transaction reorg", "depth", depth)
		} else {
			// Reorg看起来很浅，足以将所有事务都拉入内存
			var discarded, included block.Transactions
			var (
				rem = pool.chain.GetBlock(oldHead.Hash(), oldHead.Number.Uint64())
				add = pool.chain.GetBlock(newHead.Hash(), newHead.Number.Uint64())
			)
			if rem == nil {
				// 如果执行定位头，我们只需从链中丢弃旧的定位头，就会发生这种情况。如果是这样的话，我们就没有丢失的交易了，也没有什么可补充的了
				if newNum >= oldNum {
					// 如果我们重新调整到一个相同或更高的数字，那么这不是一个设定值的情况
					log.Warn("Transaction pool reset with missing oldhead",
						"old", oldHead.Hash(), "oldnum", oldNum, "new", newHead.Hash(), "newnum", newNum)
					return
				}
				// 如果reorg的结果是一个较低的数字，则表明原因是设定头
				log.Debug("Skipping transaction reset caused by setHead",
					"old", oldHead.Hash(), "oldnum", oldNum, "new", newHead.Hash(), "newnum", newNum)
				// 我们仍然需要更新当前状态s.th。用户可以读取丢失的事务
			} else {
				for rem.NumberU64() > add.NumberU64() {
					discarded = append(discarded, rem.Transactions()...)
					if rem = pool.chain.GetBlock(rem.ParentHash(), rem.NumberU64()-1); rem == nil {
						log.Error("Unrooted old chain seen by tx pool", "block", oldHead.Number, "hash", oldHead.Hash())
						return
					}
				}
				for add.NumberU64() > rem.NumberU64() {
					included = append(included, add.Transactions()...)
					if add = pool.chain.GetBlock(add.ParentHash(), add.NumberU64()-1); add == nil {
						log.Error("Unrooted new chain seen by tx pool", "block", newHead.Number, "hash", newHead.Hash())
						return
					}
				}
				for rem.Hash() != add.Hash() {
					discarded = append(discarded, rem.Transactions()...)
					if rem = pool.chain.GetBlock(rem.ParentHash(), rem.NumberU64()-1); rem == nil {
						log.Error("Unrooted old chain seen by tx pool", "block", oldHead.Number, "hash", oldHead.Hash())
						return
					}
					included = append(included, add.Transactions()...)
					if add = pool.chain.GetBlock(add.ParentHash(), add.NumberU64()-1); add == nil {
						log.Error("Unrooted new chain seen by tx pool", "block", newHead.Number, "hash", newHead.Hash())
						return
					}
				}
				reinject = block.TxDifference(discarded, included)
			}
		}
	}
	//将内部状态初始化为当前磁头
	if newHead == nil {
		newHead = pool.chain.CurrentBlock().Header() // 测试期间的特殊情况
	}
	statedb, err := pool.chain.StateAt(newHead.Root)
	if err != nil {
		log.Error("Failed to reset txpool state", "terr", err)
		return
	}
	pool.currentState = statedb
	pool.pendingNonces = newTxNoncer(statedb)
	pool.currentMaxGas = newHead.GasLimit

	// 注入因reorgs而丢弃的任何事务
	log.Debug("Reinjecting stale transactions", "count", len(reinject))
	senderCacher.recover(pool.signer, reinject)
	pool.addTxsLocked(reinject, false)

	//按下一个挂起的块编号更新所有叉指示器。
	//next := new(big.Int).Add(newHead.Number, big.NewInt(1))
	//pool.istanbul = pool.chainconfig.IsIstanbul(next)
	//pool.eip2718 = pool.chainconfig.IsBerlin(next)
	//pool.eip1559 = pool.chainconfig.IsLondon(next)
}

// add验证事务并将其插入到不可执行队列中，以便稍后挂起升级和执行。
//如果事务是已挂起或排队的事务的替换，则如果其价格较高，则会覆盖以前的事务。
//如果新添加的交易标记为本地，则其发送帐户将添加到许可列表中，以防止任何关联交易因定价限制而从池中退出。
func (pool *TxPool) add(tx *block.Transaction, local bool) (replaced bool, err error) {
	// 如果事务已知，则放弃它
	hash := tx.Hash()
	if pool.all.Get(hash) != nil {
		log.Info("Discarding already known transaction", "hash", hash)
		//knownTxMeter.Mark(1)
		return false, ErrAlreadyKnown
	}
	// 制作本地标志。如果它来自本地源或来自网络，但发送方之前标记为本地，则将其视为本地事务。
	isLocal := local || pool.locals.containsTx(tx)

	//如果事务未通过基本验证，请放弃它
	if err := pool.validateTx(tx, isLocal); err != nil {
		log.Info("Discarding invalid transaction", "hash", hash, "terr", err)
		//invalidTxMeter.Mark(1)
		return false, err
	}
	// 如果交易池已满，则放弃定价过低的交易
	if uint64(pool.all.Slots()+numSlots(tx)) > pool.config.GlobalSlots+pool.config.GlobalQueue {
		// 如果新交易定价过低，请不要接受
		if !isLocal && pool.priced.Underpriced(tx) {
			log.Info("Discarding underpriced transaction", "hash", hash, "gasTipCap", tx.GasTipCap(), "gasFeeCap", tx.GasFeeCap())
			//underpricedTxMeter.Mark(1)
			return false, ErrUnderpriced
		}
		//我们即将替换一笔交易。reorg对删除什么以及如何删除进行了更彻底的分析，但它是异步运行的。我们不想在reorg运行之间进行太多替换，因此我们将替换数量限制为插槽的25%
		if pool.changesSinceReorg > int(pool.config.GlobalSlots/4) {
			//throttleTxMeter.Mark(1)
			return false, ErrTxPoolOverflow
		}

		// 新的交易比我们最糟糕的交易要好，给它腾出空间吧。如果是本地事务，则强制放弃所有可用事务。
		//否则，如果我们不能为新的留出足够的空间，请中止操作。
		drop, success := pool.priced.Discard(pool.all.Slots()-int(pool.config.GlobalSlots+pool.config.GlobalQueue)+numSlots(tx), isLocal)

		// 特殊情况下，我们仍然无法为新的tx腾出空间。
		if !isLocal && !success {
			log.Info("Discarding overflown transaction", "hash", hash)
			//overflowedTxMeter.Mark(1)
			return false, ErrTxPoolOverflow
		}
		// 增加reorg后的拒绝计数
		pool.changesSinceReorg += len(drop)
		// 取消定价过低的远程交易。
		for _, tx := range drop {
			log.Info("Discarding freshly underpriced transaction", "hash", tx.Hash(), "gasTipCap", tx.GasTipCap(), "gasFeeCap", tx.GasFeeCap())
			//underpricedTxMeter.Mark(1)
			pool.removeTx(tx.Hash(), false)
		}
	}
	// 尝试替换挂起池中的现有事务
	from, _ := block.Sender(pool.signer, tx) // 已验证
	if list := pool.pending[from]; list != nil && list.Overlaps(tx) {
		// 暂挂，检查是否满足所需的涨价要求
		inserted, old := list.Add(tx, pool.config.PriceBump)
		if !inserted {
			//pendingDiscardMeter.Mark(1)
			return false, ErrReplaceUnderpriced
		}
		// 新交易更好，替换旧交易
		if old != nil {
			pool.all.Remove(old.Hash())
			pool.priced.Removed(1)
			//pendingReplaceMeter.Mark(1)
		}
		pool.all.Add(tx, isLocal)
		pool.priced.Put(tx, isLocal)
		pool.journalTx(from, tx)
		pool.queueTxEvent(tx)
		log.Info("Pooled new executable transaction", "hash", hash, "from", from, "to", tx.To())

		// 成功升级，心跳加速
		pool.beats[from] = time.Now()
		return old != nil, nil
	}
	// 新事务未替换挂起的事务，请推入队列
	replaced, err = pool.enqueueTx(hash, tx, isLocal, true)
	if err != nil {
		return false, err
	}
	// 标记本地地址和日志本地事务
	if local && !pool.locals.contains(from) {
		log.Info("Setting new local account", "address", from)
		pool.locals.add(from)
		pool.priced.Removed(pool.all.RemoteToLocals(pool.locals)) // 如果第一次标记为本地，则迁移远程设备。
	}
	if isLocal {
		//localGauge.Inc(1)
	}
	pool.journalTx(from, tx)

	log.Info("Pooled new future transaction", "hash", hash, "from", from, "to", tx.To())
	return replaced, nil
}

// promoteExecutables将可处理的事务从未来队列移动到挂起的事务集。在此过程中，将删除所有无效事务（低nonce、低余额）。
func (pool *TxPool) promoteExecutables(accounts []entity.Address) []*block.Transaction {
	// 跟踪提升的事务以立即广播它们
	var promoted []*block.Transaction

	// 迭代所有帐户并升级任何可执行事务
	for _, addr := range accounts {
		list := pool.queue[addr]
		if list == nil {
			continue // 以防有人用不存在的帐户call
		}
		// 删除所有被认为太旧的事务（低nonce）
		forwards := list.Forward(pool.currentState.GetNonce(addr))
		for _, tx := range forwards {
			hash := tx.Hash()
			pool.all.Remove(hash)
		}
		log.Info("Removed old queued transactions", "count", len(forwards))
		// 放弃所有成本过高的交易（余额不足或gas不足）
		drops, _ := list.Filter(pool.currentState.GetBalance(addr), pool.currentMaxGas)
		for _, tx := range drops {
			hash := tx.Hash()
			pool.all.Remove(hash)
		}
		log.Info("Removed unpayable queued transactions", "count", len(drops))
		//queuedNofundsMeter.Mark(int64(len(drops)))

		//收集所有可执行事务并升级它们
		readies := list.Ready(pool.pendingNonces.get(addr))
		for _, tx := range readies {
			hash := tx.Hash()
			if pool.promoteTx(addr, hash, tx) {
				promoted = append(promoted, tx)
			}
		}
		log.Info("Promoted queued transactions", "count", len(promoted))
		//queuedGauge.Dec(int64(len(readies)))

		// 删除超过允许限制的所有事务
		var caps block.Transactions
		if !pool.locals.contains(addr) {
			caps = list.Cap(int(pool.config.AccountQueue))
			for _, tx := range caps {
				hash := tx.Hash()
				pool.all.Remove(hash)
				log.Info("Removed cap-exceeding queued transaction", "hash", hash)
			}
			//queuedRateLimitMeter.Mark(int64(len(caps)))
		}
		// 将所有丢弃的项目标记为已删除
		pool.priced.Removed(len(forwards) + len(drops) + len(caps))
		//queuedGauge.Dec(int64(len(forwards) + len(drops) + len(caps)))
		if pool.locals.contains(addr) {
			//localGauge.Dec(int64(len(forwards) + len(drops) + len(caps)))
		}
		// 如果整个队列条目变为空，请将其删除。
		if list.Empty() {
			delete(pool.queue, addr)
			delete(pool.beats, addr)
		}
	}
	return promoted
}

// promoteTx将一个事务添加到待处理（可处理）事务列表中，并返回是否插入了该事务或旧事务更好。
//注意，此方法假定池锁定已保持！
func (pool *TxPool) promoteTx(addr entity.Address, hash entity.Hash, tx *block.Transaction) bool {
	// 尝试将事务插入挂起队列
	if pool.pending[addr] == nil {
		pool.pending[addr] = newTxList(true)
	}
	list := pool.pending[addr]

	inserted, old := list.Add(tx, pool.config.PriceBump)
	if !inserted {
		// 旧事务更好，放弃此
		pool.all.Remove(hash)
		pool.priced.Removed(1)
		//pendingDiscardMeter.Mark(1)
		return false
	}
	// 否则，放弃以前的任何交易并标记此交易
	if old != nil {
		pool.all.Remove(old.Hash())
		pool.priced.Removed(1)
		//pendingReplaceMeter.Mark(1)
	} else {
		// 未替换任何内容，请碰撞挂起的计数器
		//pendingGauge.Inc(1)
	}
	// 设置潜在的新挂起的nonce，并通知任何子系统新的tx
	pool.pendingNonces.set(addr, tx.Nonce()+1)

	//成功升级，心跳加速
	pool.beats[addr] = time.Now()
	return true
}

// DemoteUnexecuctables从池的可执行/挂起队列中删除无效的和已处理的事务，并且任何无法执行的后续事务都会移回未来队列。
//注意：在定价列表中，事务没有标记为已删除，因为重新堆总是由SetBaseFee显式触发，触发重新堆是不必要的，也是浪费的。此函数
func (pool *TxPool) demoteUnexecutables() {
	// 迭代所有帐户并降级任何不可执行的事务
	for addr, list := range pool.pending {
		nonce := pool.currentState.GetNonce(addr)

		// 删除所有被认为太旧的事务（低nonce）
		olds := list.Forward(nonce)
		for _, tx := range olds {
			hash := tx.Hash()
			pool.all.Remove(hash)
			log.Info("Removed old pending transaction", "hash", hash)
		}
		// 放弃所有成本过高的交易（余额不足或gas不足），并将所有伤残者排回队列等待稍后处理
		drops, invalids := list.Filter(pool.currentState.GetBalance(addr), pool.currentMaxGas)
		for _, tx := range drops {
			hash := tx.Hash()
			log.Info("Removed unpayable pending transaction", "hash", hash)
			pool.all.Remove(hash)
		}
		//pendingNofundsMeter.Mark(int64(len(drops)))

		for _, tx := range invalids {
			hash := tx.Hash()
			log.Info("Demoting pending transaction", "hash", hash)

			//内部随机播放不应触及查找集。
			pool.enqueueTx(hash, tx, false, false)
		}
		//pendingGauge.Dec(int64(len(olds) + len(drops) + len(invalids)))
		if pool.locals.contains(addr) {
			//localGauge.Dec(int64(len(olds) + len(drops) + len(invalids)))
		}
		// 如果前面有缺口，提醒（永远不要发生）并推迟所有交易
		if list.Len() > 0 && list.txs.Get(nonce) == nil {
			gapped := list.Cap(0)
			for _, tx := range gapped {
				hash := tx.Hash()
				log.Error("Demoting invalidated transaction", "hash", hash)

				// 内部随机播放不应触及查找集。
				pool.enqueueTx(hash, tx, false, false)
			}
			//pendingGauge.Dec(int64(len(gapped)))
			// 这可能发生在reorg中，因此将其记录到计量
			//blockReorgInvalidatedTx.Mark(int64(len(gapped)))
		}
		// 如果整个挂起条目变为空，请将其删除。
		if list.Empty() {
			delete(pool.pending, addr)
		}
	}
}

//enqueueTx将新事务插入不可执行事务队列。
//注意，此方法假定池锁定已保持！
func (pool *TxPool) enqueueTx(hash entity.Hash, tx *block.Transaction, local bool, addAll bool) (bool, error) {
	// 尝试将事务插入未来队列
	from, _ := block.Sender(pool.signer, tx) // 已验证
	if pool.queue[from] == nil {
		pool.queue[from] = newTxList(false)
	}
	inserted, old := pool.queue[from].Add(tx, pool.config.PriceBump)
	if !inserted {
		// 旧事务更好，放弃此
		//queuedDiscardMeter.Mark(1)
		return false, errors.New("replacement transaction underpriced")
	}
	//放弃以前的任何交易并标记此交易
	if old != nil {
		pool.all.Remove(old.Hash())
		pool.priced.Removed(1)
		//queuedReplaceMeter.Mark(1)
	} else {
		// 未替换任何内容，请碰撞排队的计数器
		//queuedGauge.Inc(1)
	}
	// 如果事务不在查找集中，但应该在那里，请显示错误日志。
	if pool.all.Get(hash) == nil && !addAll {
		log.Error("Missing transaction in lookup set, please report the issue", "hash", hash)
	}
	if addAll {
		pool.all.Add(tx, local)
		pool.priced.Put(tx, local)
	}
	// 如果我们从来没有记录过心跳，现在就做。
	if _, exist := pool.beats[from]; !exist {
		pool.beats[from] = time.Now()
	}
	return old != nil, nil
}

// 如果池高于挂起限制，truncatePending将从挂起队列中删除事务。该算法尝试将具有许多挂起事务的所有帐户的事务计数减少大约相等的数目。
func (pool *TxPool) truncatePending() {
	pending := uint64(0)
	for _, list := range pool.pending {
		pending += uint64(list.Len())
	}
	if pending <= pool.config.GlobalSlots {
		return
	}

	//pendingBeforeCap := pending
	// 收集垃圾邮件订单，首先惩罚大型交易对手
	spammers := utils.NewPrque(nil)
	for addr, list := range pool.pending {
		// 仅从高滚轴收回事务
		if !pool.locals.contains(addr) && uint64(list.Len()) > pool.config.AccountSlots {
			spammers.Push(addr, int64(list.Len()))
		}
	}
	// 逐步删除违规者的交易
	offenders := []entity.Address{}
	for pending > pool.config.GlobalSlots && !spammers.Empty() {
		// 如果不是本地地址，则检索下一个罪犯
		offender, _ := spammers.Pop()
		offenders = append(offenders, offender.(entity.Address))

		// 平衡余额，直到所有余额都相同或低于阈值
		if len(offenders) > 1 {
			// 计算所有当前违规者的均衡阈值
			threshold := pool.pending[offender.(entity.Address)].Len()

			// 反复减少所有违规者，直到达到限制或阈值以下
			for pending > pool.config.GlobalSlots && pool.pending[offenders[len(offenders)-2]].Len() > threshold {
				for i := 0; i < len(offenders)-1; i++ {
					list := pool.pending[offenders[i]]

					caps := list.Cap(list.Len() - 1)
					for _, tx := range caps {
						// 也从全局池中删除事务
						hash := tx.Hash()
						pool.all.Remove(hash)

						// 将帐户nonce更新为已删除的事务
						pool.pendingNonces.setIfLower(offenders[i], tx.Nonce())
						log.Info("Removed fairness-exceeding pending transaction", "hash", hash)
					}
					pool.priced.Removed(len(caps))
					//pendingGauge.Dec(int64(len(caps)))
					if pool.locals.contains(offenders[i]) {
						//localGauge.Dec(int64(len(caps)))
					}
					pending--
				}
			}
		}
	}

	// 如果仍高于阈值，则减少到极限或最小容差
	if pending > pool.config.GlobalSlots && len(offenders) > 0 {
		for pending > pool.config.GlobalSlots && uint64(pool.pending[offenders[len(offenders)-1]].Len()) > pool.config.AccountSlots {
			for _, addr := range offenders {
				list := pool.pending[addr]

				caps := list.Cap(list.Len() - 1)
				for _, tx := range caps {
					// 也从全局池中删除事务
					hash := tx.Hash()
					pool.all.Remove(hash)

					// 将帐户nonce更新为已删除的事务
					pool.pendingNonces.setIfLower(addr, tx.Nonce())
					log.Info("Removed fairness-exceeding pending transaction", "hash", hash)
				}
				pool.priced.Removed(len(caps))
				//pendingGauge.Dec(int64(len(caps)))
				if pool.locals.contains(addr) {
					//localGauge.Dec(int64(len(caps)))
				}
				pending--
			}
		}
	}
	//pendingRateLimitMeter.Mark(int64(pendingBeforeCap - pending))
}

// 如果池高于全局队列限制，truncateQueue会删除队列中的旧事务。
func (pool *TxPool) truncateQueue() {
	queued := uint64(0)
	for _, list := range pool.queue {
		queued += uint64(list.Len())
	}
	if queued <= pool.config.GlobalQueue {
		return
	}

	// 按心跳对具有排队事务的所有帐户进行排序
	addresses := make(addressesByHeartbeat, 0, len(pool.queue))
	for addr := range pool.queue {
		if !pool.locals.contains(addr) { // 不要丢弃本地人
			addresses = append(addresses, addressByHeartbeat{addr, pool.beats[addr]})
		}
	}
	sort.Sort(sort.Reverse(addresses))

	// 删除事务，直到总数低于限制或只保留本地事务
	for drop := queued - pool.config.GlobalQueue; drop > 0 && len(addresses) > 0; {
		addr := addresses[len(addresses)-1]
		list := pool.queue[addr.address]

		addresses = addresses[:len(addresses)-1]

		// 如果事务小于溢出，则删除所有事务
		if size := uint64(list.Len()); size <= drop {
			for _, tx := range list.Flatten() {
				pool.removeTx(tx.Hash(), true)
			}
			drop -= size
			//queuedRateLimitMeter.Mark(int64(size))
			continue
		}
		// Otherwise drop only last few transactions
		txs := list.Flatten()
		for i := len(txs) - 1; i >= 0 && drop > 0; i-- {
			pool.removeTx(txs[i].Hash(), true)
			drop--
			//queuedRateLimitMeter.Mark(1)
		}
	}
}

// removeTx从队列中删除单个事务，将所有后续事务移回未来队列。
func (pool *TxPool) removeTx(hash entity.Hash, outofbound bool) {
	// 获取要删除的事务
	tx := pool.all.Get(hash)
	if tx == nil {
		return
	}
	addr, _ := block.Sender(pool.signer, tx) // 插入期间已验证

	// 将其从已知事务列表中删除
	pool.all.Remove(hash)
	if outofbound {
		pool.priced.Removed(1)
	}
	if pool.locals.contains(addr) {
		//localGauge.Dec(1)
	}
	// 从挂起列表中删除事务并重置帐户nonce
	if pending := pool.pending[addr]; pending != nil {
		if removed, invalids := pending.Remove(tx); removed {
			// 如果没有剩余的挂起事务，请删除该列表
			if pending.Empty() {
				delete(pool.pending, addr)
			}
			// 推迟任何无效交易
			for _, tx := range invalids {
				// 内部随机播放不应触及查找集。
				pool.enqueueTx(tx.Hash(), tx, false, false)
			}
			// 如果需要，请更新帐户nonce
			pool.pendingNonces.setIfLower(addr, tx.Nonce())
			// 减少挂起计数器
			//pendingGauge.Dec(int64(1 + len(invalids)))
			return
		}
	}
	// 事务在未来队列中
	if future := pool.queue[addr]; future != nil {
		if removed, _ := future.Remove(tx); removed {
			// 减少排队计数器
			//queuedGauge.Dec(1)
		}
		if future.Empty() {
			delete(pool.queue, addr)
			delete(pool.beats, addr)
		}
	}
}

// AddRemote将单个事务排队到池中（如果该事务有效）。
//这是一个围绕AddRemotes的方便包装器。不推荐使用：使用AddRemotes
func (pool *TxPool) AddRemote(tx *block.Transaction) error {
	errs := pool.AddRemotes([]*block.Transaction{tx})
	return errs[0]
}

// AddRemotes将一批有效的事务排入池中。
//如果发件人不在本地跟踪的发件人中，则将应用完整的定价约束。
//此方法用于从p2p网络添加事务，不等待池重组和内部事件传播。
func (pool *TxPool) AddRemotes(txs []*block.Transaction) []error {
	return pool.addTxs(txs, false, false)
}

// AddLocals将一批有效的事务排入池中，将发件人标记为本地发件人，确保他们绕过本地定价限制。
//此方法用于从RPC API添加事务，并执行同步池重组和事件传播。
func (pool *TxPool) AddLocals(txs []*block.Transaction) []error {
	return pool.addTxs(txs, !pool.config.NoLocals, true)
}

// AddLocal将单个本地事务排入池（如果有效）。这是一个围绕AddLocals的方便包装器。
func (pool *TxPool) AddLocal(tx *block.Transaction) error {
	errs := pool.AddLocals([]*block.Transaction{tx})
	return errs[0]
}

// addTxs尝试将一批有效的事务排队。
func (pool *TxPool) addTxs(txs []*block.Transaction, local, sync bool) []error {
	// 在不获取池锁或恢复签名的情况下筛选出已知的池
	var (
		errs = make([]error, len(txs))
		news = make([]*block.Transaction, 0, len(txs))
	)
	for i, tx := range txs {
		// 如果已知事务，请预先设置错误槽
		if pool.all.Get(tx.Hash()) != nil {
			errs[i] = ErrAlreadyKnown
			//knownTxMeter.Mark(1)
			continue
		}
		// 尽快排除具有无效签名的事务，并在获取锁之前在事务中缓存发件人
		_, err := block.Sender(pool.signer, tx)
		if err != nil {
			errs[i] = ErrInvalidSender
			//invalidTxMeter.Mark(1)
			continue
		}
		// 累积所有未知事务以进行更深入的处理
		news = append(news, tx)
	}
	if len(news) == 0 {
		return errs
	}

	// 处理所有新事务并将所有错误合并到原始切片中
	pool.mu.Lock()
	newErrs, dirtyAddrs := pool.addTxsLocked(news, local)
	pool.mu.Unlock()

	var nilSlot = 0
	for _, err := range newErrs {
		for errs[nilSlot] != nil {
			nilSlot++
		}
		errs[nilSlot] = err
		nilSlot++
	}
	// 如果需要，重新标记池内构件并返回
	done := pool.requestPromoteExecutables(dirtyAddrs)
	if sync {
		<-done
	}
	return errs
}

//addTxsLocked尝试将一批有效的事务排队。必须持有事务池锁。
func (pool *TxPool) addTxsLocked(txs []*block.Transaction, local bool) ([]error, *accountSet) {
	dirty := newAccountSet(pool.signer)
	errs := make([]error, len(txs))
	for i, tx := range txs {
		replaced, err := pool.add(tx, local)
		errs[i] = err
		if err == nil && !replaced {
			dirty.addTx(tx)
		}
	}
	//validTxMeter.Mark(int64(len(dirty.accounts)))
	return errs, dirty
}

// validateTx根据一致性规则检查事务是否有效，并遵守本地节点的一些启发式限制（价格和大小）。
func (pool *TxPool) validateTx(tx *block.Transaction, local bool) error {
	//在EIP-2718/2930激活之前，仅接受旧事务。
	if !pool.eip2718 && tx.Type() != block.LegacyTxType {
		return terr.ErrTxTypeNotSupported
	}
	//拒绝动态费用交易，直到EIP-1559激活。
	if !pool.eip1559 && tx.Type() == block.DynamicFeeTxType {
		return terr.ErrTxTypeNotSupported
	}
	// 拒绝超过定义大小的事务以防止DOS攻击
	if uint64(tx.Size()) > txMaxSize {
		return ErrOversizedData
	}
	// 交易不能为负。使用RLP解码事务可能永远不会发生这种情况，但如果使用RPC创建事务，则可能会发生这种情况。
	if tx.Value().Sign() < 0 {
		return ErrNegativeValue
	}
	// 确保交易不超过当前区块限制gas。
	if pool.currentMaxGas < tx.Gas() {
		return ErrGasLimit
	}
	// 超大数字的健全性检查
	if tx.GasFeeCap().BitLen() > 256 {
		return terr.ErrFeeCapVeryHigh
	}
	if tx.GasTipCap().BitLen() > 256 {
		return terr.ErrTipVeryHigh
	}
	// 确保gasFeeCap大于或等于gasticap。
	if tx.GasFeeCapIntCmp(tx.GasTipCap()) < 0 {
		return terr.ErrTipAboveFeeCap
	}
	// 确保交易签名正确。
	from, err := block.Sender(pool.signer, tx)
	if err != nil {
		return ErrInvalidSender
	}
	// 以我们自己的最低可接受gas价格或tip放弃非本地交易
	if !local && tx.GasTipCapIntCmp(pool.gasPrice) < 0 {
		return ErrUnderpriced
	}
	// 确保事务符合nonce排序
	if pool.currentState.GetNonce(from) > tx.Nonce() {
		return terr.ErrNonceTooLow
	}
	//交易人应有足够的资金支付成本     成本==V+GP*GL
	if pool.currentState.GetBalance(from).Cmp(tx.Cost()) < 0 {
		return terr.ErrInsufficientFunds
	}
	// 确保交易的gas比基本的tx费用多。
	intrGas, err := transition.IntrinsicGas(tx.Data())
	if err != nil {
		return err
	}
	if tx.Gas() < intrGas {
		return terr.ErrIntrinsicGas
	}
	return nil
}

// journalTx将指定的事务添加到本地磁盘日志中，如果它被视为是从本地帐户发送的。
func (pool *TxPool) journalTx(from entity.Address, tx *block.Transaction) {
	// 仅当日志已启用且事务为本地事务时
	//if pool.journal == nil || !pool.locals.contains(from) {
	//	return
	//}
	//if err := pool.journal.insert(tx); err != nil {
	//	log.Warn("Failed to journal local transaction", "err", err)
	//}
}

// queueTxEvent将要在下一次reorg运行中发送的事务事件排入队列。
func (pool *TxPool) queueTxEvent(tx *block.Transaction) {
	select {
	case pool.queueTxEventCh <- tx:
	case <-pool.reorgShutdownCh:
	}
}

// 循环是交易池的主要事件循环，等待外部区块链事件以及各种报告和交易逐出事件并对其作出反应。
func (pool *TxPool) loop() {
	defer pool.wg.Done()

	var (
		prevPending, prevQueued, prevStales int
		// 启动统计报告和事务逐出计时器
		report  = time.NewTicker(statsReportInterval)
		evict   = time.NewTicker(evictionInterval)
		journal = time.NewTicker(pool.config.Rejournal)
		// 跟踪事务REORG的以前标头
		head = pool.chain.CurrentBlock()
	)
	defer report.Stop()
	defer evict.Stop()
	defer journal.Stop()

	// 通知测试初始化阶段已完成
	close(pool.InitDoneCh)
	for {
		fmt.Println("循环处理chainhead")
		select {
		// 处理ChainHeadEvent
		case ev := <-pool.chainHeadCh:
			if ev.Block != nil {
				pool.requestReset(head.Header(), ev.Block.Header())
				head = ev.Block
			}

		// 系统停止
		case <-pool.chainHeadSub.Err():
			close(pool.reorgShutdownCh)
			return

		// 处理统计数据报告记号
		case <-report.C:
			pool.mu.RLock()
			pending, queued := pool.stats()
			pool.mu.RUnlock()
			stales := int(atomic.LoadInt64(&pool.priced.stales))

			if pending != prevPending || queued != prevQueued || stales != prevStales {
				log.Debug("Transaction pool status report", "executable", pending, "queued", queued, "stales", stales)
				prevPending, prevQueued, prevStales = pending, queued, stales
			}

		// 处理非活动帐户事务逐出
		case <-evict.C:
			pool.mu.Lock()
			for addr := range pool.queue {
				// 从逐出机制跳过本地事务
				if pool.locals.contains(addr) {
					continue
				}
				// 应移除年龄足够大的非本地人
				if time.Since(pool.beats[addr]) > pool.config.Lifetime {
					list := pool.queue[addr].Flatten()
					for _, tx := range list {
						pool.removeTx(tx.Hash(), true)
					}
					//queuedEvictionMeter.Mark(int64(len(list)))
				}
			}
			pool.mu.Unlock()

			// 处理本地事务日志循环
			//case <-journal.C:
			//	if pool.journal != nil {
			//		pool.mu.Lock()
			//		if err := pool.journal.rotate(pool.local()); err != nil {
			//			log.Warn("Failed to rotate local tx journal", "err", err)
			//		}
			//		pool.mu.Unlock()
			//	}
		}
	}
}

//requestReset请求将池重置为新的头块。重置发生时，返回的通道关闭。
func (pool *TxPool) requestReset(oldHead *block.Header, newHead *block.Header) chan struct{} {
	select {
	case pool.reqResetCh <- &txpoolResetRequest{oldHead, newHead}:
		return <-pool.reorgDoneCh
	case <-pool.reorgShutdownCh:
		return pool.reorgShutdownCh
	}
}

//stats检索当前池的统计信息，即挂起的事务数和排队（不可执行）的事务数。
func (pool *TxPool) stats() (int, int) {
	pending := 0
	for _, list := range pool.pending {
		pending += list.Len()
	}
	queued := 0
	for _, list := range pool.queue {
		queued += list.Len()
	}
	return pending, queued
}

// equestPromoteExecutables请求对给定地址进行事务提升检查。升级检查完成后，返回的通道将关闭。
func (pool *TxPool) requestPromoteExecutables(set *accountSet) chan struct{} {
	select {
	case pool.reqPromoteCh <- set:
		return <-pool.reorgDoneCh
	case <-pool.reorgShutdownCh:
		return pool.reorgShutdownCh
	}
}

// Stop终止事务池。
func (pool *TxPool) Stop() {
	// 取消订阅从txpool注册的所有订阅
	pool.scope.Close()

	// 取消订阅从区块链注册的订阅
	pool.chainHeadSub.Unsubscribe()
	pool.wg.Wait()

	//if pool.journal != nil {
	//	pool.journal.close()
	//}
	log.Info("Transaction pool stopped")
}

// addressByHeartbeat是一个带有最后一个活动时间戳的帐户地址。
type addressByHeartbeat struct {
	address   entity.Address
	heartbeat time.Time
}

type txpoolResetRequest struct {
	oldHead, newHead *block.Header
}

type addressesByHeartbeat []addressByHeartbeat

func (a addressesByHeartbeat) Len() int           { return len(a) }
func (a addressesByHeartbeat) Less(i, j int) bool { return a[i].heartbeat.Before(a[j].heartbeat) }
func (a addressesByHeartbeat) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }

//accountSet只是一组用于检查是否存在的地址，以及能够从事务中派生地址的签名者。
type accountSet struct {
	accounts map[entity.Address]struct{}
	signer   block.Signer
	cache    *[]entity.Address
}

// 展平返回此集合中的地址列表，并将其缓存以供以后重用。不应更改返回的切片！
func (as *accountSet) flatten() []entity.Address {
	if as.cache == nil {
		accounts := make([]entity.Address, 0, len(as.accounts))
		for account := range as.accounts {
			accounts = append(accounts, account)
		}
		as.cache = &accounts
	}
	return *as.cache
}

//包含检查给定地址是否包含在集合中。
func (as *accountSet) contains(addr entity.Address) bool {
	_, exist := as.accounts[addr]
	return exist
}

//containsTx检查给定tx的发送方是否在集合内。如果无法派生发件人，此方法将返回false。
func (as *accountSet) containsTx(tx *block.Transaction) bool {
	if addr, err := block.Sender(as.signer, tx); err == nil {
		return as.contains(addr)
	}
	return false
}

// add在要跟踪的集合中插入新地址。
func (as *accountSet) add(addr entity.Address) {
	as.accounts[addr] = struct{}{}
	as.cache = nil
}

//addTx将tx的发送方添加到集合中。
func (as *accountSet) addTx(tx *block.Transaction) {
	if addr, err := block.Sender(as.signer, tx); err == nil {
		as.add(addr)
	}
}

// newAccountSet为发件人派生创建一个新的地址集，该地址集具有关联的签名者。
func newAccountSet(signer block.Signer, addrs ...entity.Address) *accountSet {
	as := &accountSet{
		accounts: make(map[entity.Address]struct{}),
		signer:   signer,
	}
	for _, addr := range addrs {
		as.add(addr)
	}
	return as
}

/*
txNoncer是一个小型虚拟状态数据库，用于管理池中帐户的可执行nonce，如果帐户未知，则返回到从真实状态数据库读取。
*/
type txNoncer struct {
	fallback *operationdb.OperationDB
	nonces   map[entity.Address]uint64
	lock     sync.Mutex
}

// newTxNoncer创建一个新的虚拟状态数据库来跟踪池nonce。
func newTxNoncer(statedb *operationdb.OperationDB) *txNoncer {
	return &txNoncer{
		fallback: statedb.Copy(),
		nonces:   make(map[entity.Address]uint64),
	}
}

// get返回帐户的当前nonce，如果帐户未知，则返回到真实状态数据库。
func (txn *txNoncer) get(addr entity.Address) uint64 {
	// 我们使用互斥体进行get操作，即使对于读访问，底层状态也会改变db。
	txn.lock.Lock()
	defer txn.lock.Unlock()

	if _, ok := txn.nonces[addr]; !ok {
		txn.nonces[addr] = txn.fallback.GetNonce(addr)
	}
	return txn.nonces[addr]
}

// set在虚拟操作数据库中插入一个新的虚拟nonce，以便在池请求时返回，而不是访问真实状态数据库。
func (txn *txNoncer) set(addr entity.Address, nonce uint64) {
	txn.lock.Lock()
	defer txn.lock.Unlock()

	txn.nonces[addr] = nonce
}

// setAll将所有帐户的nonce设置为给定映射。
func (txn *txNoncer) setAll(all map[entity.Address]uint64) {
	txn.lock.Lock()
	defer txn.lock.Unlock()

	txn.nonces = all
}

//如果新的虚拟nonce较低，setIfLower会将新的虚拟nonce更新到虚拟操作数据库中。
func (txn *txNoncer) setIfLower(addr entity.Address, nonce uint64) {
	txn.lock.Lock()
	defer txn.lock.Unlock()

	if _, ok := txn.nonces[addr]; !ok {
		txn.nonces[addr] = txn.fallback.GetNonce(addr)
	}
	if txn.nonces[addr] <= nonce {
		return
	}
	txn.nonces[addr] = nonce
}

/*
txList是属于某个帐户的交易的“列表”，按帐户时值排序。同一类型可用于存储可执行/挂起队列的连续事务；并用于存储不可执行/未来队列的有间隙事务，行为变化很小
*/
type txList struct {
	strict bool         // nonce是否严格连续
	txs    *txSortedMap // 事务的堆索引排序哈希映射

	costcap *big.Int // 成本最高交易记录的价格（仅当超过余额时重置）
	gascap  uint64   // 最高支出交易的gas限额（仅当超过区块限额时重置）
}

// newTxList创建一个新的事务列表，用于维护nonce可索引、快速、有间隙、可排序的事务列表。
func newTxList(strict bool) *txList {
	return &txList{
		strict:  strict,
		txs:     newTxSortedMap(),
		costcap: new(big.Int),
	}
}

// 扁平化基于松散排序的内部表示创建事务的nonce排序切片。如果在对内容进行任何修改之前再次请求排序，则会缓存排序结果。
func (l *txList) Flatten() block.Transactions {
	return l.txs.Flatten()
}

//Forward从列表中删除nonce低于所提供阈值的所有事务。每个删除的事务都会返回以进行任何删除后维护。
func (l *txList) Forward(threshold uint64) block.Transactions {
	return l.txs.Forward(threshold)
}

// 筛选器从列表中删除成本或气体限额高于所提供阈值的所有交易记录。每个删除的事务都会返回以进行任何删除后维护。还将返回严格模式无效的事务。
//该方法使用缓存的costcap和gascap快速确定计算所有成本是否有意义，或者余额是否涵盖所有成本。如果阈值低于costgas上限，则在删除新失效的交易后，上限将重置为新的高点。
func (l *txList) Filter(costLimit *big.Int, gasLimit uint64) (block.Transactions, block.Transactions) {
	// 如果所有事务都低于阈值，则短路
	if l.costcap.Cmp(costLimit) <= 0 && l.gascap <= gasLimit {
		return nil, nil
	}
	l.costcap = new(big.Int).Set(costLimit) // 将上限降低到阈值
	l.gascap = gasLimit

	// 过滤掉账户资金上方的所有交易
	removed := l.txs.Filter(func(tx *block.Transaction) bool {
		return tx.Gas() > gasLimit || tx.Cost().Cmp(costLimit) > 0
	})

	if len(removed) == 0 {
		return nil, nil
	}
	var invalids block.Transactions
	// 如果列表是严格的，请过滤高于最低nonce的任何内容
	if l.strict {
		lowest := uint64(math.MaxUint64)
		for _, tx := range removed {
			if nonce := tx.Nonce(); lowest > nonce {
				lowest = nonce
			}
		}
		invalids = l.txs.filter(func(tx *block.Transaction) bool { return tx.Nonce() > lowest })
	}
	l.txs.reheap()
	return removed, invalids
}

// Ready检索从提供的nonce开始的、可供处理的事务的顺序递增列表。返回的事务将从列表中删除。
//注意，还将返回nonce低于start的所有事务，以防止进入无效状态。这不是应该发生的事情，但自我纠正比失败要好！
func (l *txList) Ready(start uint64) block.Transactions {
	return l.txs.Ready(start)
}

//Add尝试在列表中插入一个新事务，返回该事务是否已被接受，如果是，则返回它所替换的任何以前的事务。
//如果新交易被列入清单，清单的成本和天然气阈值也可能会更新。
func (l *txList) Add(tx *block.Transaction, priceBump uint64) (bool, *block.Transaction) {
	// 如果有旧的更好的事务，请中止
	old := l.txs.Get(tx.Nonce())
	if old != nil {
		if old.GasFeeCapCmp(tx) >= 0 || old.GasTipCapCmp(tx) >= 0 {
			return false, nil
		}
		// thresholdFeeCap = oldFC  * (100 + priceBump) / 100
		a := big.NewInt(100 + int64(priceBump))
		aFeeCap := new(big.Int).Mul(a, old.GasFeeCap())
		aTip := a.Mul(a, old.GasTipCap())

		// thresholdTip    = oldTip * (100 + priceBump) / 100
		b := big.NewInt(100)
		thresholdFeeCap := aFeeCap.Div(aFeeCap, b)
		thresholdTip := aTip.Div(aTip, b)

		// 我们必须确保新的费用上限和tip都高于旧的费用上限和tip，并检查百分比阈值，以确保这对于低（Wei级）gas价格替代是准确的。
		if tx.GasFeeCapIntCmp(thresholdFeeCap) < 0 || tx.GasTipCapIntCmp(thresholdTip) < 0 {
			return false, nil
		}
	}
	// 否则，用当前事务覆盖旧事务
	l.txs.Put(tx)
	if cost := tx.Cost(); l.costcap.Cmp(cost) < 0 {
		l.costcap = cost
	}
	if gas := tx.Gas(); l.gascap < gas {
		l.gascap = gas
	}
	return true, old
}

// Cap对项目数量进行了严格限制，返回所有超过该限制的交易。
func (l *txList) Cap(threshold int) block.Transactions {
	return l.txs.Cap(threshold)
}

//Empty返回事务列表是否为空。
func (l *txList) Empty() bool {
	return l.Len() == 0
}

//Len返回事务列表的长度。
func (l *txList) Len() int {
	return l.txs.Len()
}

// LastElement返回平坦列表的最后一个元素，因此，具有最高nonce的事务
func (l *txList) LastElement() *block.Transaction {
	return l.txs.LastElement()
}

// Remove从维护的列表中删除事务，返回是否找到该事务，并返回由于删除而无效的任何事务（仅限严格模式）。
func (l *txList) Remove(tx *block.Transaction) (bool, block.Transactions) {
	// Remove the transaction from the set
	nonce := tx.Nonce()
	if removed := l.txs.Remove(nonce); !removed {
		return false, nil
	}
	// In strict mode, filter out non-executable transactions
	if l.strict {
		return true, l.txs.Filter(func(tx *block.Transaction) bool { return tx.Nonce() > nonce })
	}
	return true, nil
}

//重叠返回指定的事务是否与列表中已包含的事务具有相同的nonce。
func (l *txList) Overlaps(tx *block.Transaction) bool {
	return l.txs.Get(tx.Nonce()) != nil
}

// newTxPricedList创建一个新的按价格排序的事务堆。
func newTxPricedList(all *txLookup) *txPricedList {
	return &txPricedList{
		all: all,
	}
}

/*
txSortedMap是一个nonce->事务哈希映射，具有基于堆的索引，允许以nonce递增的方式迭代内容。
*/
type txSortedMap struct {
	items map[uint64]*block.Transaction // 存储事务数据的哈希映射
	index *nonceHeap                    // 所有存储事务的nonce堆（非严格模式）
	cache block.Transactions            // 已排序事务的缓存
}

//newTxSortedMap创建一个新的nonce排序事务映射。
func newTxSortedMap() *txSortedMap {
	return &txSortedMap{
		items: make(map[uint64]*block.Transaction),
		index: new(nonceHeap),
	}
}

// 扁平化基于松散排序的内部表示创建事务的nonce排序切片。如果在对内容进行任何修改之前再次请求排序，则会缓存排序结果。
func (m *txSortedMap) Flatten() block.Transactions {
	// 复制缓存以防止意外修改
	cache := m.flatten()
	txs := make(block.Transactions, len(cache))
	copy(txs, cache)
	return txs
}

// Len返回事务映射的长度。
func (m *txSortedMap) Len() int {
	return len(m.items)
}

// Get检索与给定nonce关联的当前事务。
func (m *txSortedMap) Get(nonce uint64) *block.Transaction {
	return m.items[nonce]
}

// Put在映射中插入新事务，同时更新映射的nonce索引。如果已存在具有相同nonce的事务，则会覆盖该事务。
func (m *txSortedMap) Put(tx *block.Transaction) {
	nonce := tx.Nonce()
	if m.items[nonce] == nil {
		heap.Push(m.index, nonce)
	}
	m.items[nonce], m.cache = tx, nil
}

func (m *txSortedMap) flatten() block.Transactions {
	// If the sorting was not cached yet, create and cache it
	if m.cache == nil {
		m.cache = make(block.Transactions, 0, len(m.items))
		for _, tx := range m.items {
			m.cache = append(m.cache, tx)
		}
		sort.Sort(block.TxByNonce(m.cache))
	}
	return m.cache
}

// Forward从映射中删除nonce低于所提供阈值的所有事务。每个删除的事务都会返回以进行任何删除后维护。
func (m *txSortedMap) Forward(threshold uint64) block.Transactions {
	var removed block.Transactions

	// 弹出堆项目，直到达到阈值
	for m.index.Len() > 0 && (*m.index)[0] < threshold {
		nonce := heap.Pop(m.index).(uint64)
		removed = append(removed, m.items[nonce])
		delete(m.items, nonce)
	}
	// If we had a cached order, shift the front
	if m.cache != nil {
		m.cache = m.cache[len(removed):]
	}
	return removed
}

// 筛选器迭代事务列表，并删除指定函数计算结果为true的所有事务。
//与“Filter”相反，Filter在操作完成后重新初始化堆。
//如果您想连续进行几次过滤，那么最好先进行一次。过滤器（func1）后跟。过滤器（func2）或reheap（）
func (m *txSortedMap) Filter(filter func(*block.Transaction) bool) block.Transactions {
	removed := m.filter(filter)
	// 如果事务被删除，堆和缓存将被破坏
	if len(removed) > 0 {
		m.reheap()
	}
	return removed
}

//筛选器与筛选器相同，但**不**重新生成堆。只有在紧接着调用Filter或reheap（）时，才应使用此方法
func (m *txSortedMap) filter(filter func(*block.Transaction) bool) block.Transactions {
	var removed block.Transactions

	// Collect all the transactions to filter out
	for nonce, tx := range m.items {
		if filter(tx) {
			removed = append(removed, tx)
			delete(m.items, nonce)
		}
	}
	if len(removed) > 0 {
		m.cache = nil
	}
	return removed
}

func (m *txSortedMap) reheap() {
	*m.index = make([]uint64, 0, len(m.items))
	for nonce := range m.items {
		*m.index = append(*m.index, nonce)
	}
	heap.Init(m.index)
	m.cache = nil
}

//Ready检索从提供的nonce开始的、可供处理的事务的顺序递增列表。返回的事务将从列表中删除。
//注意，还将返回nonce低于start的所有事务，以防止进入无效状态。这不是应该发生的事情，但自我纠正比失败要好！
func (m *txSortedMap) Ready(start uint64) block.Transactions {
	// Short circuit if no transactions are available
	if m.index.Len() == 0 || (*m.index)[0] > start {
		return nil
	}
	// Otherwise start accumulating incremental transactions
	var ready block.Transactions
	for next := (*m.index)[0]; m.index.Len() > 0 && (*m.index)[0] == next; next++ {
		ready = append(ready, m.items[next])
		delete(m.items, next)
		heap.Pop(m.index)
	}
	m.cache = nil

	return ready
}

//Cap对项目数量进行了严格限制，返回所有超过该限制的交易。
func (m *txSortedMap) Cap(threshold int) block.Transactions {
	// 如果项目数量低于限制，则短路
	if len(m.items) <= threshold {
		return nil
	}
	// 否则，收集和删除最高的nonced事务
	var drops block.Transactions

	sort.Sort(*m.index)
	for size := len(m.items); size > threshold; size-- {
		drops = append(drops, m.items[(*m.index)[size-1]])
		delete(m.items, (*m.index)[size-1])
	}
	*m.index = (*m.index)[:threshold]
	heap.Init(m.index)

	// 如果我们有缓存，请向后移动
	if m.cache != nil {
		m.cache = m.cache[:len(m.cache)-len(drops)]
	}
	return drops
}

// LastElement返回平坦列表的最后一个元素，因此，具有最高nonce的事务
func (m *txSortedMap) LastElement() *block.Transaction {
	cache := m.flatten()
	return cache[len(cache)-1]
}

//Remove从维护的映射中删除事务，返回是否找到该事务。
func (m *txSortedMap) Remove(nonce uint64) bool {
	// 如果不存在交易，则短路
	_, ok := m.items[nonce]
	if !ok {
		return false
	}
	// 否则，请删除事务并修复堆索引
	for i := 0; i < m.index.Len(); i++ {
		if (*m.index)[i] == nonce {
			heap.Remove(m.index, i)
			break
		}
	}
	delete(m.items, nonce)
	m.cache = nil

	return true
}

//nonceHeap是一个堆。64位无符号整数的接口实现，用于从可能存在间隙的未来队列中检索已排序的事务。
type nonceHeap []uint64

func (h nonceHeap) Len() int           { return len(h) }
func (h nonceHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h nonceHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *nonceHeap) Push(x interface{}) {
	*h = append(*h, x.(uint64))
}

func (h *nonceHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

/*
txLookup循环跟踪事务
*/
type txLookup struct {
	slots   int
	lock    sync.RWMutex
	locals  map[entity.Hash]*block.Transaction
	remotes map[entity.Hash]*block.Transaction
}

//删除从查找中删除事务。
func (t *txLookup) Remove(hash entity.Hash) {
	t.lock.Lock()
	defer t.lock.Unlock()

	tx, ok := t.locals[hash]
	if !ok {
		tx, ok = t.remotes[hash]
	}
	if !ok {
		log.Error("No transaction found to be deleted", "hash", hash)
		return
	}
	t.slots -= numSlots(tx)
	//slotsGauge.Update(int64(t.slots))

	delete(t.locals, hash)
	delete(t.remotes, hash)
}

// RemoteCount返回查找中的当前远程事务数。
func (t *txLookup) RemoteCount() int {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return len(t.remotes)
}

// Range对映射中存在的每个键和值调用f。传递的回调应该返回是否需要继续迭代的指示符。调用方需要指定要迭代的集合（或两者）。
func (t *txLookup) Range(f func(hash entity.Hash, tx *block.Transaction, local bool) bool, local bool, remote bool) {
	t.lock.RLock()
	defer t.lock.RUnlock()

	if local {
		for key, value := range t.locals {
			if !f(key, value, true) {
				return
			}
		}
	}
	if remote {
		for key, value := range t.remotes {
			if !f(key, value, false) {
				return
			}
		}
	}
}

// 添加将事务添加到查找中。
func (t *txLookup) Add(tx *block.Transaction, local bool) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.slots += numSlots(tx)
	//slotsGauge.Update(int64(t.slots))

	if local {
		t.locals[tx.Hash()] = tx
	} else {
		t.remotes[tx.Hash()] = tx
	}
}

// Get returns a transaction if it exists in the lookup, or nil if not found.
func (t *txLookup) Get(hash entity.Hash) *block.Transaction {
	t.lock.RLock()
	defer t.lock.RUnlock()

	if tx := t.locals[hash]; tx != nil {
		return tx
	}
	return t.remotes[hash]
}

// Slots返回查找中使用的当前插槽数。
func (t *txLookup) Slots() int {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return t.slots
}

//GetRemote如果在查找中存在事务，则返回事务；如果未找到事务，则返回nil。
func (t *txLookup) GetRemote(hash entity.Hash) *block.Transaction {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return t.remotes[hash]
}

// RemoteToLocals将属于给定局部变量集的事务迁移到局部变量集。假设使用的局部变量集是线程安全的。
func (t *txLookup) RemoteToLocals(locals *accountSet) int {
	t.lock.Lock()
	defer t.lock.Unlock()

	var migrated int
	for hash, tx := range t.remotes {
		if locals.containsTx(tx) {
			t.locals[hash] = tx
			delete(t.remotes, hash)
			migrated += 1
		}
	}
	return migrated
}

// numSlots计算单个事务所需的插槽数。
func numSlots(tx *block.Transaction) int {
	return int((tx.Size() + txSlotSize - 1) / txSlotSize)
}

/*
txPricedList是一个价格排序堆，允许以价格递增的方式对事务池内容进行操作。它构建在txpool中的所有事务上，但只对远程部分感兴趣。这意味着只有远程事务才会被考虑用于跟踪、排序、逐出等。
两个堆用于排序：紧急堆（基于下一个块中的有效tip）和浮动堆（基于gasFeeCap）。总是选择较大的堆进行逐出。从紧急堆中逐出的事务首先降级到浮动堆中。
在某些情况下（在拥塞期间，当块已满时），紧急堆可以提供更好的包含候选，而在其他情况下（在baseFee峰值的顶部），浮动堆更好。当基本费用减少时，它们的行为类似。
*/
type txPricedList struct {
	// 到（重新堆触发器）的过期价格点数。此字段是以原子方式访问的，必须是第一个字段，以确保它与原子正确对齐 。AddInt64.
	stales int64

	all              *txLookup  // 指向所有事务映射的指针
	urgent, floating priceHeap  // 所有存储的**远程**交易的成堆价格
	reheapMu         sync.Mutex // 互斥体声明只有一个例程正在重新连接列表
}

// Removed通知prices事务列表旧事务已从池中删除。该列表只会保留一个过时对象的计数器，如果有足够大比例的事务过时，则会更新堆。
func (l *txPricedList) Removed(count int) {
	// 碰撞陈旧计数器，但如果仍然过低（<25%），则退出
	stales := atomic.AddInt64(&l.stales, int64(count))
	if int(stales) <= (len(l.urgent.list)+len(l.floating.list))/4 {
		return
	}
	// 看来我们已经达到了一个关键的过时交易数量，reheap
	l.Reheap()
}

//Reheap基于当前远程事务集强制重建堆。
func (l *txPricedList) Reheap() {
	l.reheapMu.Lock()
	defer l.reheapMu.Unlock()
	//start := time.Now()
	atomic.StoreInt64(&l.stales, 0)
	l.urgent.list = make([]*block.Transaction, 0, l.all.RemoteCount())
	l.all.Range(func(hash entity.Hash, tx *block.Transaction, local bool) bool {
		l.urgent.list = append(l.urgent.list, tx)
		return true
	}, false, true) // 仅迭代远程
	heap.Init(&l.urgent)

	// 只有通过将最差的一半事务移动到浮动堆中，才能迭代remotesbalance以消除这两个堆注意：
	//Discard在第一次逐出之前也会这样做，但Reheap可以更有效地做到这一点。此外，如果浮动队列是空的，那么第一次定价过低的情况将不太理想。
	floatingCount := len(l.urgent.list) * floatingRatio / (urgentRatio + floatingRatio)
	l.floating.list = make([]*block.Transaction, floatingCount)
	for i := 0; i < floatingCount; i++ {
		l.floating.list[i] = heap.Pop(&l.urgent).(*block.Transaction)
	}
	heap.Init(&l.floating)
	//reheapTimer.Update(time.Since(start))
}

// Put在堆中插入一个新事务。
func (l *txPricedList) Put(tx *block.Transaction, local bool) {
	if local {
		return
	}
	// 首先将每个新事务插入紧急堆；丢弃将平衡堆
	heap.Push(&l.urgent, tx)
}

// 低价检查交易是否比当前跟踪的最低价（远程）交易便宜（或与之一样便宜）。
func (l *txPricedList) Underpriced(tx *block.Transaction) bool {
	// 注意：对于两个队列，定价过低被定义为比所有非空队列中最差的项目（如果有）更差。如果两个队列都是空的，那么任何东西都不会被低估。
	return (l.underpricedFor(&l.urgent, tx) || len(l.urgent.list) == 0) &&
		(l.underpricedFor(&l.floating, tx) || len(l.floating.list) == 0) &&
		(len(l.urgent.list) != 0 || len(l.floating.list) != 0)
}

//UnpricedFor检查事务是否比给定堆中价格最低的（远程）事务便宜（或与之一样便宜）。
func (l *txPricedList) underpricedFor(h *priceHeap, tx *block.Transaction) bool {
	// 如果在堆开始处找到过时的价格点，则丢弃该价格点
	for len(h.list) > 0 {
		head := h.list[0]
		if l.all.GetRemote(head.Hash()) == nil { // 已删除或迁移
			atomic.AddInt64(&l.stales, -1)
			heap.Pop(h)
			continue
		}
		break
	}
	// 检查交易是否定价过低
	if len(h.list) == 0 {
		return false // 根本没有远程事务。
	}
	// 如果远程交易比
	// 本地跟踪的最便宜的，拒绝它。
	return h.cmp(h.list[0], tx) >= 0
}

//iscard查找许多定价过低的交易，将其从定价列表中删除，并返回这些交易，以便从整个池中进一步删除。
//注：本地交易不会被视为逐出。
func (l *txPricedList) Discard(slots int, force bool) (block.Transactions, bool) {
	drop := make(block.Transactions, 0, slots) // 要放弃的远程低价交易
	for slots > 0 {
		if len(l.urgent.list)*floatingRatio > len(l.floating.list)*urgentRatio || floatingRatio == 0 {
			// 如果在清理过程中发现过时事务，则丢弃该事务
			tx := heap.Pop(&l.urgent).(*block.Transaction)
			if l.all.GetRemote(tx.Hash()) == nil { // 已删除或迁移
				atomic.AddInt64(&l.stales, -1)
				continue
			}
			// 找到非陈旧事务，移动到浮动堆
			heap.Push(&l.floating, tx)
		} else {
			if len(l.floating.list) == 0 {
				// 如果两个堆都为空，则停止
				break
			}
			// 如果在清理过程中发现过时事务，则丢弃该事务
			tx := heap.Pop(&l.floating).(*block.Transaction)
			if l.all.GetRemote(tx.Hash()) == nil { // 已删除或迁移
				atomic.AddInt64(&l.stales, -1)
				continue
			}
			// 找到非过期事务，放弃它
			drop = append(drop, tx)
			slots -= numSlots(tx)
		}
	}
	//如果我们仍然不能为新交易腾出足够的空间
	if slots > 0 && !force {
		for _, tx := range drop {
			heap.Push(&l.urgent, tx)
		}
		return nil, false
	}
	return drop, true
}

type priceHeap struct {
	baseFee *big.Int //更改baseFee后，应始终对堆进行重新排序
	list    []*block.Transaction
}

func (h *priceHeap) Push(x interface{}) {
	tx := x.(*block.Transaction)
	h.list = append(h.list, tx)
}

func (h *priceHeap) Pop() interface{} {
	old := h.list
	n := len(old)
	x := old[n-1]
	old[n-1] = nil
	h.list = old[0 : n-1]
	return x
}

func (h *priceHeap) Len() int      { return len(h.list) }
func (h *priceHeap) Swap(i, j int) { h.list[i], h.list[j] = h.list[j], h.list[i] }

func (h *priceHeap) Less(i, j int) bool {
	switch h.cmp(h.list[i], h.list[j]) {
	case -1:
		return true
	case 1:
		return false
	default:
		return h.list[i].Nonce() > h.list[j].Nonce()
	}
}

func (h *priceHeap) cmp(a, b *block.Transaction) int {
	if h.baseFee != nil {
		// Compare effective tips if baseFee is specified
		if c := a.EffectiveGasTipCmp(b, h.baseFee); c != 0 {
			return c
		}
	}
	// 如果未指定基本费用或有效小费相等，则比较费用上限
	if c := a.GasFeeCapCmp(b); c != 0 {
		return c
	}
	// 如果有效小费和费用上限相等，则比较小费
	return a.GasTipCapCmp(b)
}

// senderCacher是一个并发事务发送方恢复程序和缓存程序。
var senderCacher = newTxSenderCacher(runtime.NumCPU())

// txSenderCacherRequest是一种请求，用于恢复具有特定签名方案的事务发送方，并将其缓存到事务本身中。
//inc字段定义每次恢复后要跳过的事务数，用于将相同的底层输入数组提供给不同的线程，但确保它们快速处理早期事务。
type txSenderCacherRequest struct {
	signer block.Signer
	txs    []*block.Transaction
	inc    int
}

// txSenderCacher是一种助手结构，用于在后台线程上从数字签名中并发地ecrecover事务发送者。
type txSenderCacher struct {
	threads int
	tasks   chan *txSenderCacherRequest
}

// newTxSenderCacher创建一个新的事务发送方后台缓存，并在GOMAXPROCS允许的范围内启动尽可能多的处理Goroutine。
func newTxSenderCacher(threads int) *txSenderCacher {
	cacher := &txSenderCacher{
		tasks:   make(chan *txSenderCacherRequest, threads),
		threads: threads,
	}
	for i := 0; i < threads; i++ {
		go cacher.cache()
	}
	return cacher
}

//缓存是一个无限循环，它从各种形式的数据结构中缓存事务发送者。
func (cacher *txSenderCacher) cache() {
	for task := range cacher.tasks {
		for i := 0; i < len(task.txs); i += task.inc {
			block.Sender(task.signer, task.txs[i])
		}
	}
}

//恢复从一批事务中恢复发件人，并将其缓存回相同的数据结构中。没有进行验证，也没有对无效签名作出任何反应。这取决于以后调用代码。
func (cacher *txSenderCacher) recover(signer block.Signer, txs []*block.Transaction) {
	// 如果没有要恢复的内容，请中止
	if len(txs) == 0 {
		return
	}
	// 确保我们拥有有意义的任务大小并安排恢复
	tasks := cacher.threads
	if len(txs) < tasks*4 {
		tasks = (len(txs) + 3) / 4
	}
	for i := 0; i < tasks; i++ {
		cacher.tasks <- &txSenderCacherRequest{
			signer: signer,
			txs:    txs[i:],
			inc:    tasks,
		}
	}
}

//recoverFromBlocks从一批块中恢复发送方，并将其缓存回相同的数据结构中。没有进行验证，也没有对无效签名作出任何反应。这取决于以后调用代码。
func (cacher *txSenderCacher) recoverFromBlocks(signer block.Signer, blocks []*block.Block) {
	count := 0
	for _, block := range blocks {
		count += len(block.Transactions())
	}
	txs := make([]*block.Transaction, 0, count)
	for _, block := range blocks {
		txs = append(txs, block.Transactions()...)
	}
	cacher.recover(signer, txs)
}
