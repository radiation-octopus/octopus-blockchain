package miner

import (
	"errors"
	"fmt"
	mapset "github.com/deckarep/golang-set"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	operationUtils "github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus-blockchain/terr"
	"github.com/radiation-octopus/octopus-blockchain/transition"
	"github.com/radiation-octopus/octopus/log"
	"math/big"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// ResultQueSize是侦听密封结果的通道的大小。
	resultQueueSize = 10

	// txChanSize是侦听NewTxsEvent的通道的大小。
	//该数字参考了tx池的大小。
	txChanSize = 4096

	// chainHeadChanSize是侦听ChainHeadEvent的通道的大小。
	chainHeadChanSize = 10

	//chainSideChanSize是侦听ChainSideEvent的通道的大小。
	chainSideChanSize = 10

	// resubmitAdjustChanSize是重新提交间隔调整通道的大小。
	resubmitAdjustChanSize = 10

	// sealingLogAtDepth是记录成功密封之前的确认数。
	sealingLogAtDepth = 7

	// minRecommitInterval是使用任何新到达的事务重新创建密封块的最小时间间隔。
	minRecommitInterval = 1 * time.Second

	// maxRecommitInterval是使用任何新到达的事务重新创建密封块的最大时间间隔。
	maxRecommitInterval = 15 * time.Second

	// 间隔调整比率是单个间隔调整对密封工作重新提交间隔的影响。
	intervalAdjustRatio = 0.1

	// intervalAdjustBias在新的重新提交间隔计算过程中应用，有利于增加上限或降低下限，以便可以达到上限。
	intervalAdjustBias = 200 * 1000.0 * 1000.0

	// staleThreshold是可接受的陈旧块的最大深度。
	staleThreshold = 7
)

const (
	commitInterruptNone int32 = iota
	commitInterruptNewHead
	commitInterruptResubmit
)

var (
	errBlockInterruptedByNewHead  = errors.New("The new owner arrives when building the module")
	errBlockInterruptedByRecommit = errors.New("Resubmit interrupt on building block")
)

type worker struct {
	config      *Config
	chainConfig *entity.ChainConfig
	engine      consensus.Engine
	oct         Backend
	chain       *blockchain.BlockChain

	// 订阅
	pendingLogsFeed blockchain.Feed

	// 订阅的事件
	//mux          *event.TypeMux
	txsCh        chan blockchain.NewTxsEvent
	txsSub       blockchain.Subscription
	chainHeadCh  chan blockchain.ChainHeadEvent
	chainHeadSub blockchain.Subscription
	//chainSideCh  chan core.ChainSideEvent
	//chainSideSub event.Subscription

	// 通道
	newWorkCh          chan *newWorkReq
	getWorkCh          chan *getWorkReq
	taskCh             chan *task
	resultCh           chan *block.Block
	startCh            chan struct{}
	exitCh             chan struct{}
	resubmitIntervalCh chan time.Duration
	resubmitAdjustCh   chan *intervalAdjust

	wg sync.WaitGroup

	current      *environment                 // 当前工作生命周期执行环境
	localUncles  map[entity.Hash]*block.Block // 本地分叉区块作为潜在叔块
	remoteUncles map[entity.Hash]*block.Block // 分叉区块中潜在的叔块
	//unconfirmed  *unconfirmedBlocks           	// 本地产生但尚未被确认的区块

	mu       sync.RWMutex //保护coinbase的锁
	coinbase entity.Address
	extra    []byte

	pendingMu    sync.RWMutex          //队列锁
	pendingTasks map[entity.Hash]*task //任务map

	//snapshotMu       sync.RWMutex // The lock used to protect the snapshots below
	//snapshotBlock    *block.Block
	//snapshotReceipts block.Receipts
	//snapshotState    *db.OperationDB

	// 原子状态计数器
	running int32 // 指示共识引擎是否正在运行
	newTxs  int32 // 交易计数

	// noempty是用于控制是否启用预密封空块功能的标志。默认值为false（默认情况下启用预密封）。但在某些特殊场景中，共识引擎会立即密封块，在这种情况下，此功能会将所有空块不间断地添加到规范链中，并且不会包含任何真正的事务。
	noempty uint32

	//外部功能
	isLocalB func(ader *block.Header) bool // 用于确定指定区块是否由本地工作者开采的函数。

	// 测试勾
	newTaskHook  func(*task)                        // 方法在接收到新的密封任务时调用。
	skipSealHook func(*task) bool                   //决定是否跳过密封的方法。
	fullTaskHook func()                             // 方法在推送完全密封任务之前调用。
	resubmitHook func(time.Duration, time.Duration) // 方法在更新重新提交间隔时调用。
}

func newWorker(config *Config, engine consensus.Engine, oct Backend, init bool) *worker {
	worker := &worker{
		config: config,
		engine: engine,
		oct:    oct,
		//mux:                mux,
		chain: oct.BlockChain(),
		//isLocalBlock:       isLocalBlock,
		localUncles:  make(map[entity.Hash]*block.Block),
		remoteUncles: make(map[entity.Hash]*block.Block),
		//unconfirmed:        newUnconfirmedBlocks(eth.BlockChain(), sealingLogAtDepth),
		pendingTasks: make(map[entity.Hash]*task),
		txsCh:        make(chan blockchain.NewTxsEvent, txChanSize),
		chainHeadCh:  make(chan blockchain.ChainHeadEvent, chainHeadChanSize),
		//chainSideCh:        make(chan core.ChainSideEvent, chainSideChanSize),
		newWorkCh:          make(chan *newWorkReq),
		getWorkCh:          make(chan *getWorkReq),
		taskCh:             make(chan *task),
		resultCh:           make(chan *block.Block, resultQueueSize),
		exitCh:             make(chan struct{}),
		startCh:            make(chan struct{}, 1),
		resubmitIntervalCh: make(chan time.Duration),
		resubmitAdjustCh:   make(chan *intervalAdjust, resubmitAdjustChanSize),
	}
	// 订阅发送池的NewTxsEvent
	worker.txsSub = oct.TxPool().SubscribeNewTxsEvent(worker.txsCh)
	//订阅区块链事件
	worker.chainHeadSub = oct.BlockChain().SubscribeChainHeadEvent(worker.chainHeadCh)
	//worker.chainSideSub = oct.BlockChain().SubscribeChainSideEvent(worker.chainSideCh)

	// 如果用户指定的重新提交间隔太短，请清理该间隔。
	recommit := worker.config.Recommit
	if recommit < minRecommitInterval {
		log.Warn("Sanitizing miner recommit interval", "provided", recommit, "updated", minRecommitInterval)
		recommit = minRecommitInterval
	}

	worker.wg.Add(4)
	go worker.mainLoop()            //监听其他三个通道
	go worker.newWorkLoop(recommit) //新建工作通道
	go worker.resultLoop()          //数据储存通道
	go worker.taskLoop()            //任务处理通道

	// 提交第一个工作以初始化挂起状态。
	if init {
		worker.startCh <- struct{}{}
	}
	return worker
}

// start将运行状态设置为1并触发新工作提交。
func (w *worker) start() {
	atomic.StoreInt32(&w.running, 1)
	w.startCh <- struct{}{}
}

// 停止将运行状态设置为0。
func (w *worker) stop() {
	atomic.StoreInt32(&w.running, 0)
}

// isRunning返回一个指示器，指示worker是否正在运行。
func (w *worker) isRunning() bool {
	return atomic.LoadInt32(&w.running) == 1
}

//close终止工作线程维护的所有后台线程。
//注意：工作进程不支持多次关闭。
func (w *worker) close() {
	atomic.StoreInt32(&w.running, 0)
	close(w.exitCh)
	w.wg.Wait()
}

//setEtherbase设置用于初始化块coinbase字段的etherbase。
func (w *worker) setEtherbase(addr entity.Address) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.coinbase = addr
}

// mainLoop负责根据接收到的事件生成和提交密封工作。支持两种模式：自动生成任务并提交，或根据给定参数返回任务，用于各种提议。
func (w *worker) mainLoop() {
	defer w.wg.Done()
	defer w.txsSub.Unsubscribe()
	defer w.chainHeadSub.Unsubscribe()
	//defer w.chainSideSub.Unsubscribe()
	defer func() {
		if w.current != nil {
			w.current.discard()
		}
	}()

	cleanTicker := time.NewTicker(time.Second * 10)
	defer cleanTicker.Stop()

	for {
		select {
		case req := <-w.newWorkCh:
			w.commitWork(req.interrupt, req.noempty, req.timestamp)
		case req := <-w.getWorkCh:
			block, err := w.generateWork(req.params)
			if err != nil {
				req.err = err
				req.result <- nil
			} else {
				req.result <- block
			}

		//case ev := <-w.chainSideCh:
		//	// Short circuit for duplicate side blocks
		//	if _, exist := w.localUncles[ev.Block.Hash()]; exist {
		//		continue
		//	}
		//	if _, exist := w.remoteUncles[ev.Block.Hash()]; exist {
		//		continue
		//	}
		//	// Add side block to possible uncle block set depending on the author.
		//	if w.isLocalBlock != nil && w.isLocalBlock(ev.Block.Header()) {
		//		w.localUncles[ev.Block.Hash()] = ev.Block
		//	} else {
		//		w.remoteUncles[ev.Block.Hash()] = ev.Block
		//	}
		//	// If our sealing block contains less than 2 uncle blocks,
		//	// add the new uncle block if valid and regenerate a new
		//	// sealing block for higher profit.
		//	if w.isRunning() && w.current != nil && len(w.current.uncles) < 2 {
		//		start := time.Now()
		//		if terr := w.commitUncle(w.current, ev.Block.Header()); terr == nil {
		//			w.commit(w.current.copy(), nil, true, start)
		//		}
		//	}

		case <-cleanTicker.C:
			chainHead := w.chain.CurrentBlock()
			for hash, uncle := range w.localUncles {
				if uncle.NumberU64()+staleThreshold <= chainHead.NumberU64() {
					delete(w.localUncles, hash)
				}
			}
			for hash, uncle := range w.remoteUncles {
				if uncle.NumberU64()+staleThreshold <= chainHead.NumberU64() {
					delete(w.remoteUncles, hash)
				}
			}

		case ev := <-w.txsCh:
			// 如果我们没有密封，则将事务应用到挂起状态注意：收到的所有事务可能与当前密封块中已包含的事务不连续。这些交易将自动消除。
			if !w.isRunning() && w.current != nil {
				// 如果块已满，则中止
				if gp := w.current.gasPool; gp != nil && gp.Gas() < entity.TxGas {
					continue
				}
				txs := make(map[entity.Address]block.Transactions)
				for _, tx := range ev.Txs {
					acc, _ := block.Sender(w.current.signer, tx)
					txs[acc] = append(txs[acc], tx)
				}
				txset := block.NewTransactionsByPriceAndNonce(w.current.signer, txs, w.current.header.BaseFee)
				//tcount := w.current.tcount
				w.commitTransactions(w.current, txset, nil)

				// 仅当有任何新事务添加到挂起的块时才更新快照
				//if tcount != w.current.tcount {
				//	w.updateSnapshot(w.current)
				//}
			} else {
				//特殊情况下，如果共识引擎为0周期团（开发模式），请在此提交密封工作，因为所有空提交都将被团拒绝。当然，提前封存（空提交）已禁用。
				if w.chainConfig.Engine == "" {
					w.commitWork(nil, true, time.Now().Unix())
				}
			}
			atomic.AddInt32(&w.newTxs, int32(len(ev.Txs)))

		// 系统停止
		case <-w.exitCh:
			return
		case <-w.txsSub.Err():
			return
		case <-w.chainHeadSub.Err():
			return
			//case <-w.chainSideSub.Err():
			//	return
		}
	}
}

// commitWork基于父块生成几个新的密封任务，并将其提交给密封器。
func (w *worker) commitWork(interrupt *int32, noempty bool, timestamp int64) {
	start := time.Now()

	// 如果工作进程正在运行或需要，请设置coinbase
	var coinbase entity.Address
	if w.isRunning() {
		if w.coinbase == (entity.Address{}) {
			log.Error("Refusing to mine without etherbase")
			return
		}
		coinbase = w.coinbase // 使用预设地址作为费用收件人
	}
	work, err := w.prepareWork(&generateParams{
		timestamp: uint64(timestamp),
		coinbase:  coinbase,
	})
	if err != nil {
		return
	}
	// 基于临时复制状态创建一个空块，以便在不等待块执行完成的情况下提前密封。
	if !noempty && atomic.LoadUint32(&w.noempty) == 0 {
		w.commit(work.copy(), nil, false, start)
	}

	//从txpool填充挂起的事务
	err = w.fillTransactions(interrupt, work)
	if errors.Is(err, errBlockInterruptedByNewHead) {
		work.discard()
		return
	}

	w.commit(work.copy(), w.fullTaskHook, true, start)

	// 用新的工作替换旧的工作，同时终止所有剩余的预取进程并启动新的预取进程。
	if w.current != nil {
		w.current.discard()
	}
	w.current = work
}

// generateWork根据给定的参数生成密封块。
func (w *worker) generateWork(params *generateParams) (*block.Block, error) {
	work, err := w.prepareWork(params)
	if err != nil {
		return nil, err
	}
	defer work.discard()

	w.fillTransactions(nil, work)

	return block.NewBlock(work.header, work.txs, work.receipts), nil
}

// prepareWork根据给定的参数构造密封任务，可以基于最后一个链头，也可以基于指定的父级。在此函数中，尚未填充挂起的事务，只返回空任务。
func (w *worker) prepareWork(genParams *generateParams) (*environment, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	//查找密封任务的父块
	parent := w.chain.CurrentBlock()
	if genParams.parentHash != (entity.Hash{}) {
		parent = w.chain.GetBlockByHash(genParams.parentHash)
	}
	if parent == nil {
		return nil, fmt.Errorf("missing parent")
	}
	// 健全性检查时间戳的正确性，如果允许变异，则将时间戳重述给父级+1。
	timestamp := genParams.timestamp
	if parent.Time() >= timestamp {
		if genParams.forceTime {
			return nil, fmt.Errorf("invalid timestamp, parent %d given %d", parent.Time(), timestamp)
		}
		timestamp = parent.Time() + 1
	}
	// 构造密封块标题，如果允许，设置额外字段
	num := parent.Number()
	header := &block.Header{
		ParentHash: parent.Hash(),
		Number:     num.Add(num, operationUtils.Big1),
		GasLimit:   blockchain.CalcGasLimit(parent.GasLimit(), w.config.GasCeil),
		Time:       timestamp,
		Coinbase:   genParams.coinbase,
	}
	//if !genParams.noExtra && len(w.extra) != 0 {
	//	header.Extra = w.extra
	//}
	//从信标链设置随机性字段（如果可用）。
	if genParams.random != (entity.Hash{}) {
		header.MixDigest = genParams.random
	}
	// 如果我们在EIP-1559链上，请设置baseFee和GasLimit
	//if w.chainConfig.IsLondon(header.Number) {
	//	header.BaseFee = misc.CalcBaseFee(w.chainConfig, parent.Header())
	//	if !w.chainConfig.IsLondon(parent.Number()) {
	//		parentGasLimit := parent.GasLimit() * entity.ElasticityMultiplier
	//		header.GasLimit = blockchain.CalcGasLimit(parentGasLimit, w.config.GasCeil)
	//	}
	//}
	// 使用默认或自定义共识引擎运行共识准备。
	if terr := w.engine.Prepare(w.chain, header); terr != nil {
		log.Error("Failed to prepare header for sealing", "terr", terr)
		return nil, terr
	}
	// 如果在奇怪的状态下开始工作，可能会发生这种情况。请注意genParams。coinbase可以与header不同。Coinbase-since-clique算法可以修改标头中的Coinbase字段。
	env, err := w.makeEnv(parent, header, genParams.coinbase)
	if err != nil {
		log.Error("无法创建密封配置", "terr", err)
		return nil, err
	}
	// 只有在允许的情况下，才能为密封工作积累叔叔。
	//if !genParams.noUncle {
	//	commitUncles := func(blocks map[entity.Hash]*block.Block) {
	//		for hash, uncle := range blocks {
	//			if len(env.uncles) == 2 {
	//				break
	//			}
	//			if terr := w.commitUncle(env, uncle.Header()); terr != nil {
	//				log.Trace("Possible uncle rejected", "hash", hash, "reason", terr)
	//			} else {
	//				log.Debug("Committing new uncle to block", "hash", hash)
	//			}
	//		}
	//	}
	//	// Prefer to locally generated uncle
	//	commitUncles(w.localUncles)
	//	commitUncles(w.remoteUncles)
	//}
	return env, nil
}

// makeEnv为密封块创建新环境。
func (w *worker) makeEnv(parent *block.Block, header *block.Header, coinbase entity.Address) (*environment, error) {
	// 检索要在顶部执行的父状态，并为工作者启动一个预取程序，以加快封块速度。
	operation, err := w.chain.StateAt(parent.Root())
	if err != nil {
		// 注意：由于可以在任意父块上创建密封块，但父块的状态可能已经被修剪，因此将来需要进行必要的状态恢复。
		// 可接受的最大reorg深度可由最终试块限制
		operation, err = w.oct.StateAtBlock(parent, 1024, nil, false, false)
		log.Warn("Recovered mining state", "root", parent.Root(), "terr", err)
	}
	if err != nil {
		return nil, err
	}
	//operation.StartPrefetcher("miner")

	// 注：传递的coinbase可能与header不同。
	env := &environment{
		signer:    block.MakeSigner(header.Number),
		operation: operation,
		coinbase:  coinbase,
		ancestors: mapset.NewSet(),
		family:    mapset.NewSet(),
		header:    header,
		uncles:    make(map[entity.Hash]*block.Header),
	}
	// 处理08时，祖先包含07（快速块）
	for _, ancestor := range w.chain.GetBlocksFromHash(parent.Hash(), 7) {
		//for _, uncle := range ancestor.Uncles() {
		//	env.family.Add(uncle.Hash())
		//}
		env.family.Add(ancestor.Hash())
		env.ancestors.Add(ancestor.Hash())
	}
	// 跟踪返回错误的事务，以便将其删除
	env.tcount = 0
	return env, nil
}

// commit运行任何事务后状态修改，组装最终块，并在一致性引擎运行时提交新工作。
//请注意，假设允许对传递的env进行突变，请先进行深度复制。
func (w *worker) commit(env *environment, interval func(), update bool, start time.Time) error {
	if w.isRunning() {
		if interval != nil {
			interval()
		}
		// 创建本地环境副本，避免与快照状态的数据竞争。
		env := env.copy()
		block, terr := w.engine.FinalizeAndAssemble(w.chain, env.header, env.operation, env.txs, env.unclelist(), env.receipts)
		if terr != nil {
			return terr
		}
		// 如果我们是后期合并，只需忽略
		if !w.isTTDReached(block.Header()) {
			select {
			case w.taskCh <- &task{receipts: env.receipts, state: env.operation, block: block, createdAt: time.Now()}:
				//w.unconfirmed.Shift(block.NumberU64() - 1)
				log.Info("Commit new sealing work", "number", block.Number(),
					"uncles", len(env.uncles), "txs", env.tcount,
					"gas", block.GasUsed(), "fees", totalFees(block, env.receipts),
					//"elapsed", common.PrettyDuration(time.Since(start))
				)

			case <-w.exitCh:
				log.Info("工作者已退出")
			}
		}
	}
	//if update {
	//	w.updateSnapshot(env)
	//}
	return nil
}

//如果给定块已达到合并转换的总终端难度，则ISTTDREATCH返回指示符。
func (w *worker) isTTDReached(header *block.Header) bool {
	td := w.chain.GetTd(header.ParentHash, header.Number.Uint64()-1)
	return td != nil
}

//fillTransactions从txpool检索挂起的事务，并将它们填充到给定的密封块中。将来可以使用插件定制事务选择和排序策略。
func (w *worker) fillTransactions(interrupt *int32, env *environment) error {
	// 将挂起的事务拆分为本地事务和远程事务
	//用所有可用的挂起事务填充块。
	pending := w.oct.TxPool().Pending(true)
	localTxs, remoteTxs := make(map[entity.Address]block.Transactions), pending
	for _, account := range w.oct.TxPool().Locals() {
		if txs := remoteTxs[account]; len(txs) > 0 {
			delete(remoteTxs, account)
			localTxs[account] = txs
		}
	}
	if len(localTxs) > 0 {
		txs := block.NewTransactionsByPriceAndNonce(env.signer, localTxs, env.header.BaseFee)
		if err := w.commitTransactions(env, txs, interrupt); err != nil {
			return err
		}
	}
	if len(remoteTxs) > 0 {
		txs := block.NewTransactionsByPriceAndNonce(env.signer, remoteTxs, env.header.BaseFee)
		if err := w.commitTransactions(env, txs, interrupt); err != nil {
			return err
		}
	}
	return nil
}

func (w *worker) commitTransactions(env *environment, txs *block.TransactionsByPriceAndNonce, interrupt *int32) error {
	gasLimit := env.header.GasLimit
	if env.gasPool == nil {
		env.gasPool = new(transition.GasPool).AddGas(gasLimit)
	}
	var coalescedLogs []*log.OctopusLog

	for {
		// 在以下三种情况下，我们将中断事务的执行。
		//（1） 新头块事件到达，中断信号为1
		//（2） 工人启动或重启，中断信号为1
		//（3） 工人用任何新到达的事务重新创建密封块，中断信号为2。
		//对于前两种情况，半成品将被丢弃。
		//对于第三种情况，半成品将提交给共识引擎。
		if interrupt != nil && atomic.LoadInt32(interrupt) != commitInterruptNone {
			// 由于提交过于频繁，通知重新提交循环以增加重新提交间隔。
			if atomic.LoadInt32(interrupt) == commitInterruptResubmit {
				ratio := float64(gasLimit-env.gasPool.Gas()) / float64(gasLimit)
				if ratio < 0.1 {
					ratio = 0.1
				}
				w.resubmitAdjustCh <- &intervalAdjust{
					ratio: ratio,
					inc:   true,
				}
				return errBlockInterruptedByRecommit
			}
			return errBlockInterruptedByNewHead
		}
		//如果我们没有足够的gas进行进一步的交易，那么我们就完蛋了
		if env.gasPool.Gas() < entity.TxGas {
			log.Error("Not enough gas for further transactions", "have", env.gasPool, "want", entity.TxGas)
			break
		}
		// 检索下一个事务，如果全部完成，则中止
		tx := txs.Peek()
		if tx == nil {
			break
		}
		//此处可以忽略错误。该错误已在事务接受期间被检查为事务池。
		// 无论当前hf如何，我们都使用eip155签名者。
		from, _ := block.Sender(env.signer, tx)
		//检查发送是否受重播保护。如果我们不在EIP155 hf阶段，开始忽略发送方，直到我们这样做。
		//if tx.Protected() && !w.chainConfig.IsEIP155(env.header.Number) {
		//	log.Error("Ignoring reply protected transaction", "hash", tx.Hash(), "eip155", w.chainConfig.EIP155Block)
		//	txs.Pop()
		//	continue
		//}
		// 开始执行事务
		env.operation.Prepare(tx.Hash(), env.tcount)

		logs, err := w.commitTransaction(env, tx)
		switch {
		case errors.Is(err, terr.ErrGasLimitReached):
			//弹出当前的gas交易，而不从帐户转入下一个交易
			log.Error("Gas limit exceeded for current block", "sender", from)
			txs.Pop()

		case errors.Is(err, terr.ErrNonceTooLow):
			// 事务池和miner、shift之间的新head通知数据竞争
			log.Error("Skipping transaction with low nonce", "sender", from, "nonce", tx.Nonce())
			txs.Shift()

		case errors.Is(err, terr.ErrNonceTooHigh):
			//事务池和miner之间的Reorg通知数据竞争，跳过帐户
			log.Error("Skipping account with hight nonce", "sender", from, "nonce", tx.Nonce())
			txs.Pop()

		case errors.Is(err, nil):
			// 一切正常，从同一个帐户收集日志并在下一个事务中转移
			coalescedLogs = append(coalescedLogs, logs...)
			env.tcount++
			txs.Shift()

		case errors.Is(err, terr.ErrTxTypeNotSupported):
			// 弹出不受支持的事务，而不从帐户转入下一个事务
			log.Error("Skipping unsupported transaction type", "sender", from, "type", tx.Type())
			txs.Pop()

		default:
			// 奇怪的terr，放弃事务并获得下一个事务（注意，nonce too high子句将阻止我们徒劳地执行）。
			log.Debug("Transaction failed, account skipped", "hash", tx.Hash(), "terr", err)
			txs.Shift()
		}
	}

	if !w.isRunning() && len(coalescedLogs) > 0 {
		// 密封时，我们不会推动吊坠。原因是，当我们密封时，工作者会每3秒钟重新生成一个密封块。为了避免推送重复的pendingLog，我们禁用了挂起的日志推送。

		// 制作一个副本，州缓存日志，当本地工作者开采区块时，通过填写区块哈希，这些日志从待定日志“升级”到已开采日志。
		//如果在处理PendingLogseEvent之前“升级”了日志，这可能会导致争用情况。
		cpy := make([]*log.OctopusLog, len(coalescedLogs))
		for i, l := range coalescedLogs {
			cpy[i] = new(log.OctopusLog)
			*cpy[i] = *l
		}
		w.pendingLogsFeed.Send(cpy)
	}
	// 如果当前间隔大于用户指定的间隔，则通知重新提交循环以缩短重新提交间隔。
	if interrupt != nil {
		w.resubmitAdjustCh <- &intervalAdjust{inc: false}
	}
	return nil
}

func (w *worker) commitTransaction(env *environment, tx *block.Transaction) ([]*log.OctopusLog, error) {
	//snap := env.operation.Snapshot()

	receipt, err := blockchain.ApplyTransaction(w.chain, &env.coinbase, env.gasPool, env.operation, env.header, tx, &env.header.GasUsed, *w.chain.GetVMConfig())
	if err != nil {
		//env.operation.RevertToSnapshot(snap)
		return nil, err
	}
	env.txs = append(env.txs, tx)
	env.receipts = append(env.receipts, receipt)

	return receipt.Logs, nil
}

// newWorkLoop是一个独立的goroutine，用于在收到事件后提交新的密封工作。
func (w *worker) newWorkLoop(recommit time.Duration) {
	defer w.wg.Done()
	var (
		interrupt   *int32
		minRecommit = recommit // 用户指定的最小重新提交间隔。
		timestamp   int64      // 每轮密封的时间戳。
	)

	timer := time.NewTimer(0)
	defer timer.Stop()
	<-timer.C // 放弃初始勾号

	// commit使用给定信号中止正在运行的事务执行，并重新提交一个新的信号。
	commit := func(noempty bool, s int32) {
		if interrupt != nil {
			atomic.StoreInt32(interrupt, s)
		}
		interrupt = new(int32)
		select {
		case w.newWorkCh <- &newWorkReq{interrupt: interrupt, noempty: noempty, timestamp: timestamp}:
		case <-w.exitCh:
			return
		}
		timer.Reset(recommit)
		atomic.StoreInt32(&w.newTxs, 0)
	}
	// clearPending清除过时的挂起任务。
	clearPending := func(number uint64) {
		w.pendingMu.Lock()
		for h, t := range w.pendingTasks {
			if t.block.NumberU64()+staleThreshold <= number {
				delete(w.pendingTasks, h)
			}
		}
		w.pendingMu.Unlock()
	}

	for {
		select {
		case <-w.startCh:
			clearPending(w.chain.CurrentBlock().NumberU64())
			timestamp = time.Now().Unix()
			commit(false, commitInterruptNewHead)

		case head := <-w.chainHeadCh:
			clearPending(head.Block.NumberU64())
			timestamp = time.Now().Unix()
			commit(false, commitInterruptNewHead)

		//case <-timer.C:
		//	// If sealing is running resubmit a new work cycle periodically to pull in
		//	// higher priced transactions. Disable this overhead for pending blocks.
		//	if w.isRunning() && (w.chainConfig.Clique == nil || w.chainConfig.Clique.Period > 0) {
		//		// Short circuit if no new transaction arrives.
		//		if atomic.LoadInt32(&w.newTxs) == 0 {
		//			timer.Reset(recommit)
		//			continue
		//		}
		//		commit(true, commitInterruptResubmit)
		//	}

		case interval := <-w.resubmitIntervalCh:
			// Adjust resubmit interval explicitly by user.
			if interval < minRecommitInterval {
				log.Warn("Sanitizing miner recommit interval", "provided", interval, "updated", minRecommitInterval)
				interval = minRecommitInterval
			}
			log.Info("Miner recommit interval update", "from", minRecommit, "to", interval)
			minRecommit, recommit = interval, interval

			if w.resubmitHook != nil {
				w.resubmitHook(minRecommit, recommit)
			}

		case adjust := <-w.resubmitAdjustCh:
			// Adjust resubmit interval by feedback.
			if adjust.inc {
				before := recommit
				target := float64(recommit.Nanoseconds()) / adjust.ratio
				recommit = recalcRecommit(minRecommit, recommit, target, true)
				log.Info("Increase miner recommit interval", "from", before, "to", recommit)
			} else {
				before := recommit
				recommit = recalcRecommit(minRecommit, recommit, float64(minRecommit.Nanoseconds()), false)
				log.Info("Decrease miner recommit interval", "from", before, "to", recommit)
			}

			if w.resubmitHook != nil {
				w.resubmitHook(minRecommit, recommit)
			}

		case <-w.exitCh:
			return
		}
	}
}

//taskLoop是一个独立的goroutine，用于从生成器获取密封任务并将其推送到一致性引擎。
func (w *worker) taskLoop() {
	defer w.wg.Done()
	var (
		stopCh chan struct{}
		prev   entity.Hash
	)

	// interrupt aborts the in-flight sealing task.
	interrupt := func() {
		if stopCh != nil {
			close(stopCh)
			stopCh = nil
		}
	}
	for {
		select {
		case task := <-w.taskCh:
			if w.newTaskHook != nil {
				w.newTaskHook(task)
			}
			// 由于重新提交，拒绝重复的密封工作。
			sealHash := w.engine.SealHash(task.block.Header())
			if sealHash == prev {
				continue
			}
			// 中断之前的密封操作
			interrupt()
			stopCh, prev = make(chan struct{}), sealHash

			if w.skipSealHook != nil && w.skipSealHook(task) {
				continue
			}
			w.pendingMu.Lock()
			w.pendingTasks[sealHash] = task
			w.pendingMu.Unlock()

			if err := w.engine.Seal(w.chain, task.block, w.resultCh, stopCh); err != nil {
				log.Warn("Block sealing failed", "terr", err)
				w.pendingMu.Lock()
				delete(w.pendingTasks, sealHash)
				w.pendingMu.Unlock()
			}
		case <-w.exitCh:
			interrupt()
			return
		}
	}
}

// resultLoop是一个独立的goroutine，用于处理密封结果提交和将相关数据刷新到数据库。
func (w *worker) resultLoop() {
	defer w.wg.Done()
	for {
		select {
		case b := <-w.resultCh:
			// 接收空结果时短路。
			if b == nil {
				continue
			}
			// 收到因重新提交而导致的重复结果时短路。
			if w.chain.HasBlock(b.Hash(), b.NumberU64()) {
				continue
			}
			var (
				sealhash = w.engine.SealHash(b.Header())
				hash     = b.Hash()
			)
			w.pendingMu.RLock()
			task, exist := w.pendingTasks[sealhash]
			w.pendingMu.RUnlock()
			if !exist {
				log.Error("找到块，但没有相关的挂起任务", "number", b.Number(), "sealhash", sealhash, "hash", hash)
				continue
			}
			//不同的块可以共享相同的sealhash，在此进行深度复制以防止写-写冲突。
			var (
				receipts = make([]*block.Receipt, len(task.receipts))
				logs     []*log.OctopusLog
			)
			for i, taskReceipt := range task.receipts {
				receipt := new(block.Receipt)
				receipts[i] = receipt
				*receipt = *taskReceipt

				// 添加块位置字段
				receipt.BlockHash = hash
				receipt.BlockNumber = b.Number()
				receipt.TransactionIndex = uint(i)

				// 更新所有日志中的块哈希，因为它现在可用，而不是在创建单个事务的收据/日志时可用。
				receipt.Logs = make([]*log.OctopusLog, len(taskReceipt.Logs))
				for i, taskLog := range taskReceipt.Logs {
					log := new(log.OctopusLog)
					receipt.Logs[i] = log
					*log = *taskLog
					//log.BlockHash = hash
				}
				logs = append(logs, receipt.Logs...)
			}
			// 将块和状态提交到数据库。
			_, err := w.chain.WriteBlockAndSetHead(b, receipts, logs, task.state, true)
			if err != nil {
				log.Error("写入区块链失败", "terr", err)
				continue
			}
			//log.Info("已成功密封新块", "number", b.Number(), "sealhash", sealhash, "hash", hash,
			//	"elapsed", common.PrettyDuration(time.Since(task.createdAt)))

			//找到块，但没有相关的挂起选项卡广播块并宣布链插入事件SK
			//w.mux.Post(core.NewMinedBlockEvent{Block: b})
			//
			//// 将块插入到挂起的块集中，以便resultLoop进行确认
			//w.unconfirmed.Insert(b.NumberU64(), b.Hash())

		case <-w.exitCh:
			return
		}
	}
}

/**
newWorkReq表示使用相关中断通知程序提交新密封工作的请求。
*/
type newWorkReq struct {
	interrupt *int32
	noempty   bool
	timestamp int64
}

/**
getWorkReq表示使用提供的参数获取新密封工作的请求。
*/
type getWorkReq struct {
	params *generateParams
	err    error
	result chan *block.Block
}

/*
generateParams包装用于生成密封任务的各种设置。
*/
type generateParams struct {
	timestamp  uint64         // 密封任务的timstamp
	forceTime  bool           // 标记给定的时间戳是否不可变
	parentHash entity.Hash    // 父块哈希，空表示最新链头
	coinbase   entity.Address // 包含交易的费用接收人地址
	random     entity.Hash    // 信标链生成的随机性，合并前为空
	noUncle    bool           // 标记是否允许包含父块
	noExtra    bool           // 标记是否允许额外字段分配
}

/*
任务包含共识引擎密封和结果提交的所有信息
*/
type task struct {
	receipts  []*block.Receipt
	state     *operationdb.OperationDB
	block     *block.Block
	createdAt time.Time
}

/*
intervalAdjust表示重新提交间隔调整
*/
type intervalAdjust struct {
	ratio float64
	inc   bool
}

/*
环境是工作人员的当前环境，保存密封块生成的所有信息.
*/
type environment struct {
	signer block.Signer //签名者

	operation *operationdb.OperationDB // 在此处应用状态更改
	ancestors mapset.Set               //祖先集（用于检查叔叔父有效性）
	family    mapset.Set               // family集合（用于检查叔叔是否无效）
	tcount    int                      // 循环中的tx计数
	gasPool   *transition.GasPool      //用于包装交易的可用gas
	coinbase  entity.Address

	header   *block.Header
	txs      []*block.Transaction
	receipts []*block.Receipt
	uncles   map[entity.Hash]*block.Header
}

//discard终止后台预取程序go例程。应始终为所有创建的环境实例调用它，否则可能会发生go例程泄漏。
func (env *environment) discard() {
	if env.operation == nil {
		return
	}
	env.operation.StopPrefetcher()
}

// unclelist以列表格式返回包含的uncles。
func (env *environment) unclelist() []*block.Header {
	var uncles []*block.Header
	for _, uncle := range env.uncles {
		uncles = append(uncles, uncle)
	}
	return uncles
}

// 复制创建环境的深度副本。
func (env *environment) copy() *environment {
	cpy := &environment{
		signer:    env.signer,
		operation: env.operation.Copy(),
		ancestors: env.ancestors.Clone(),
		family:    env.family.Clone(),
		tcount:    env.tcount,
		coinbase:  env.coinbase,
		header:    block.CopyHeader(env.header),
		receipts:  copyReceipts(env.receipts),
	}
	if env.gasPool != nil {
		gasPool := *env.gasPool
		cpy.gasPool = &gasPool
	}
	// The content of txs and uncles are immutable, unnecessary
	// to do the expensive deep copy for them.
	cpy.txs = make([]*block.Transaction, len(env.txs))
	copy(cpy.txs, env.txs)
	cpy.uncles = make(map[entity.Hash]*block.Header)
	for hash, uncle := range env.uncles {
		cpy.uncles[hash] = uncle
	}
	return cpy
}

// CopyReceives生成给定收据的深度副本。
func copyReceipts(receipts []*block.Receipt) []*block.Receipt {
	result := make([]*block.Receipt, len(receipts))
	for i, l := range receipts {
		cpy := *l
		result[i] = &cpy
	}
	return result
}

// totalFees computes total consumed miner fees in ETH. Block transactions and receipts have to have the same order.
func totalFees(block *block.Block, receipts []*block.Receipt) *big.Float {
	feesWei := new(big.Int)
	for i, tx := range block.Transactions() {
		minerFee, _ := tx.EffectiveGasTip(block.BaseFee())
		feesWei.Add(feesWei, new(big.Int).Mul(new(big.Int).SetUint64(receipts[i].GasUsed), minerFee))
	}
	return new(big.Float).Quo(new(big.Float).SetInt(feesWei), new(big.Float).SetInt(big.NewInt(entity.Octcao)))
}

// recalcRecommit recalculates the resubmitting interval upon feedback.
func recalcRecommit(minRecommit, prev time.Duration, target float64, inc bool) time.Duration {
	var (
		prevF = float64(prev.Nanoseconds())
		next  float64
	)
	if inc {
		next = prevF*(1-intervalAdjustRatio) + intervalAdjustRatio*(target+intervalAdjustBias)
		max := float64(maxRecommitInterval.Nanoseconds())
		if next > max {
			next = max
		}
	} else {
		next = prevF*(1-intervalAdjustRatio) + intervalAdjustRatio*(target-intervalAdjustBias)
		min := float64(minRecommit.Nanoseconds())
		if next < min {
			next = min
		}
	}
	return time.Duration(int64(next))
}
