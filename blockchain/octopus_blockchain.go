package blockchain

import (
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum/common/prque"
	lru "github.com/hashicorp/golang-lru"
	"github.com/radiation-octopus/octopus-blockchain/blockchain/blockchainconfig"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	genesis2 "github.com/radiation-octopus/octopus-blockchain/entity/genesis"
	"github.com/radiation-octopus/octopus-blockchain/entity/rawdb"
	"github.com/radiation-octopus/octopus-blockchain/event"
	"github.com/radiation-octopus/octopus-blockchain/node"
	"github.com/radiation-octopus/octopus-blockchain/oct/octconfig"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus-blockchain/operationdb/tire"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus-blockchain/vm"
	"github.com/radiation-octopus/octopus/log"
	"math/big"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxFutureBlocks     = 256
	maxTimeFutureBlocks = 30
	TriesInMemory       = 128
)

// CacheConfig包含驻留在区块链中的trie缓存/精简的配置值。
type CacheConfig struct {
	TrieCleanLimit      int           // 用于在内存中缓存trie节点的内存余量（MB）
	TrieCleanJournal    string        // 用于保存干净缓存项的磁盘日志。
	TrieCleanRejournal  time.Duration // 定期将干净缓存转储到磁盘的时间间隔
	TrieCleanNoPrefetch bool          // 是否禁用后续块的启发式状态预取
	TrieDirtyLimit      int           // 开始将脏trie节点刷新到磁盘的内存限制（MB）
	TrieDirtyDisabled   bool          // 是否同时禁用trie写缓存和GC（存档节点）
	TrieTimeLimit       time.Duration // 将内存中的电流刷新到磁盘的时间限制
	SnapshotLimit       int           // 用于在内存中缓存快照项的内存余量（MB）
	Preimages           bool          // 是否将trie密钥的前映像存储到磁盘

	SnapshotWait bool // 等待启动时创建快照.
}

//blockchain结构体
type BlockChain struct {
	chainConfig *entity.ChainConfig // 链和网络配置
	cacheConfig *CacheConfig        // 精简的缓存配置

	db     typedb.Database //数据库
	triegc *prque.Prque    // 将块号映射到尝试gc的优先级队列
	gcproc time.Duration   // 累积trie转储的规范块处理

	TxLookupLimit uint64 //`autoInjectCfg:"octopus.blockchain.binding.genesis.header.txLookupLimit"` //一个区块容纳最大交易限制
	hc            *HeaderChain

	chainFeed     event.Feed
	chainHeadFeed event.Feed
	blockProcFeed event.Feed //区块过程注入事件
	scope         event.SubscriptionScope
	genesisBlock  *block2.Block

	//chainmu *syncx.ClosableMutex	//互斥锁，同步链写入操作使用
	currentBlock atomic.Value // 当前区块
	//currentFastBlock atomic.Value	//快速同步链的当前区块

	operationCache operationdb.DatabaseI // 要在导入之间重用的状态数据库（包含状态缓存）
	futureBlocks   *lru.Cache            //新区块缓存区
	wg             sync.WaitGroup        //同步等待属性
	quit           chan struct{}         //关闭属性
	running        int32                 //运行属性
	procInterrupt  int32                 //块处理中断信号

	engine    consensus.Engine //共识引擎
	validator Validator        //区块验证器
	processor Processor        //区块处理器
	forker    *ForkChoice      //最高难度值

	vmConfig vm.Config
}

//// IsLondon返回num是否等于或大于London fork块。
//func (c *entity.ChainConfig) IsLondon(num *big.Int) bool {
//	return isForked(c.LondonBlock, num)
//}

//isForked返回在块s上调度的fork是否在给定的头块上处于活动状态。
func isForked(s, head *big.Int) bool {
	if s == nil || head == nil {
		return false
	}
	return s.Cmp(head) <= 0
}

//迭代器
type insertIterator struct {
	chain Blocks //正在迭代链

	results <-chan error // 来自共识引擎验证结果接收器
	errors  []error      // 错误接收属性

	index     int       // 迭代器当前偏移量
	validator Validator // 验证器
}

func (bc *BlockChain) Engine() consensus.Engine { return bc.engine }

//获取下一个区块
func (it *insertIterator) next() (*block2.Block, error) {
	//如果我们到达链的末端，中止
	if it.index+1 >= len(it.chain) {
		it.index = len(it.chain)
		return nil, nil
	}
	// 推进迭代器并等待验证结果（如果尚未完成）
	it.index++
	if len(it.errors) <= it.index {
		it.errors = append(it.errors, <-it.results)
	}
	if it.errors[it.index] != nil {
		return it.chain[it.index], it.errors[it.index]
	}
	// 运行正文验证并返回,>>>>>>，it.validator.ValidateBody(it.chain[it.index])
	return it.chain[it.index], nil
}

//previous返回正在处理的上一个标头，或为零
func (it *insertIterator) previous() *block2.Header {
	if it.index < 1 {
		return nil
	}
	return it.chain[it.index-1].Header()
}

//链启动类，配置参数启动
func (bc *BlockChain) start(node *node.Node, config *octconfig.Config) {
	genesis := genesis2.MakeGenesis()
	//config := &octconfig.Config{
	//	Genesis:   genesis,
	//	NetworkId: genesis.Config.ChainID.Uint64(),
	//	//SyncMode:        downloader.FullSync,
	//	DatabaseCache:   256,
	//	DatabaseHandles: 256,
	//	TxPool:          blockchainconfig.DefaultTxPoolConfig,
	//	//GPO:             ethconfig.Defaults.GPO,
	//	//Octell:          ethconfig.Defaults.Octell,
	//	Miner: blockchainconfig.Config{
	//		Octerbase: entity.Address{1},
	//		GasCeil:   genesis.GasLimit * 11 / 10,
	//		GasPrice:  big.NewInt(1),
	//		Recommit:  time.Second,
	//	},
	//	RPCTxFeeCap: 1,
	//}
	config.Genesis = genesis
	config.NetworkId = genesis.Config.ChainID.Uint64()
	config.DatabaseCache = 256
	config.DatabaseHandles = 256
	config.TxPool = blockchainconfig.DefaultTxPoolConfig
	config.Miner.Octerbase = entity.Address{1}
	config.Miner.GasCeil = genesis.GasLimit * 11 / 10
	config.Miner.GasPrice = big.NewInt(1)
	config.Miner.Recommit = time.Second
	//组装以太坊对象
	chainDb, err := node.OpenDatabaseWithFreezer("chaindata", config.DatabaseCache, config.DatabaseHandles, config.DatabaseFreezer, "oct/db/chaindata/", false)
	bc.db = chainDb
	//构造创世区块
	//genesis := genesis2.DefaultGenesisBlock()
	chainConfig, _, _ := genesis2.SetupGenesisBlockWithOverride(bc.db, genesis, nil, nil)

	if err != nil {
		errors.New("chainDb start failed")
	}
	var cacheConfig = &CacheConfig{
		//TrieCleanLimit:      config.TrieCleanCache,
		//TrieCleanJournal:    stack.ResolvePath(config.TrieCleanCacheJournal),
		//TrieCleanRejournal:  config.TrieCleanCacheRejournal,
		//TrieCleanNoPrefetch: config.NoPrefetch,
		//TrieDirtyLimit:      config.TrieDirtyCache,
		//TrieDirtyDisabled:   config.NoPruning,
		//TrieTimeLimit:       config.TrieTimeout,
		//SnapshotLimit:       config.SnapshotCache,
		//Preimages:           config.Preimages,
	}
	//初始化区块链
	bc, erro := newBlockChain(bc, cacheConfig, chainConfig, nil, nil)
	if erro != nil {
		errors.New("blockchain start fail")
	}
}

//链终止
func (bc *BlockChain) close() {

}

func (bc *BlockChain) GetDB() typedb.Database {
	return bc.db
}

func (bc *BlockChain) getBlockChain() *BlockChain {
	return bc
}

// GetVMConfig返回块链VM配置。
func (bc *BlockChain) GetVMConfig() *vm.Config {
	return &bc.vmConfig
}

//构建区块链结构体
func newBlockChain(bc *BlockChain, cacheConfig *CacheConfig, chainConfig *entity.ChainConfig, engine consensus.Engine, shouldPreserve func(header *block2.Header) bool) (*BlockChain, error) {
	futureBlocks, _ := lru.New(maxFutureBlocks)
	bc.quit = make(chan struct{})
	bc.futureBlocks = futureBlocks
	bc.engine = engine
	bc.chainConfig = chainConfig
	bc.cacheConfig = cacheConfig
	bc.triegc = prque.New(nil)
	//bc = &BlockChain{
	//	db:   db,
	//	quit: make(chan struct{}),
	//	//chainmu:       syncx.NewClosableMutex(),
	//	//stateCache: state.NewDatabaseWithConfig(db, &trie.Config{
	//	//	Cache:     cacheConfig.TrieCleanLimit,
	//	//	Journal:   cacheConfig.TrieCleanJournal,
	//	//	Preimages: cacheConfig.Preimages,
	//	//}),
	//	futureBlocks: futureBlocks,
	//	engine:       engine,
	//}
	bc.forker = NewForkChoice(bc, shouldPreserve)
	bc.operationCache = operationdb.NewDatabaseWithConfig(bc.db, &tire.Config{
		//Cache:     cacheConfig.TrieCleanLimit,
		//Journal:   cacheConfig.TrieCleanJournal,
		//Preimages: cacheConfig.Preimages,
	})
	//构建区块验证器
	bc.validator = NewBlockValidator(bc, engine)
	//构建区块处理器
	bc.processor = NewBlockProcessor(bc, engine)

	var err error
	bc.hc, err = NewHeaderChain(bc.db, engine, bc.insertStopped)
	if err != nil {
		return nil, err
	}
	//获取创世区块
	bc.genesisBlock = bc.GetBlockByNumber(0)
	if bc.genesisBlock == nil {
		return nil, errors.New("创世区块未发现")
	}

	var nilBlock *block2.Block
	bc.currentBlock.Store(nilBlock)
	//bc.currentFastBlock.Store(nilBlock)
	//bc.currentFinalizedBlock.Store(nilBlock)
	if bc.empty() {
	}
	if err := bc.loadLastState(); err != nil {
		return nil, err
	}
	//确保区块有效可用一系列校验
	//head := bc.CurrentBlock()

	//开启未来区块处理
	bc.wg.Add(1)
	go bc.updateFutureBlocks()

	return bc, nil
}

//该创世区块在数据库对应是否有数据
func (bc *BlockChain) empty() bool {

	return true
}

//加载数据库最新链的状态
//同步区块数据
func (bc *BlockChain) loadLastState() error {
	//恢复最后一个已知的头块
	head := rawdb.ReadHeadBlockHash(bc.db)
	if head == (entity.Hash{}) {
		// 数据库损坏或为空，从头开始初始化
		log.Warn("Empty database, resetting chain")
		return bc.Reset()
	}
	// 确保整个头部模块可用
	currentBlock := bc.GetBlockByHash(head)
	if currentBlock == nil {
		// 数据库损坏或为空，从头开始初始化
		log.Warn("Head block missing, resetting chain", "hash", head)
		return bc.Reset()
	}
	// 加入当前区块
	bc.currentBlock.Store(currentBlock)
	//headBlockGauge.Update(int64(currentBlock.NumberU64()))

	// 还原最后一个已知的标头
	currentHeader := currentBlock.Header()
	if head := rawdb.ReadHeadHeaderHash(bc.db); head != (entity.Hash{}) {
		if header := bc.GetHeaderByHash(head); header != nil {
			currentHeader = header
		}
	}
	bc.hc.SetCurrentHeader(currentHeader)

	// 恢复最后一个已知的磁头快速块
	//bc.currentFastBlock.Store(currentBlock)
	//headFastBlockGauge.Update(int64(currentBlock.NumberU64()))

	//if head := rawdb.ReadHeadFastBlockHash(bc.db); head != (entity.Hash{}) {
	//	if block := bc.GetBlockByHash(head); block != nil {
	//		bc.currentFastBlock.Store(block)
	//		headFastBlockGauge.Update(int64(block.NumberU64()))
	//	}
	//}

	// 恢复最后一个已知的已完成块
	//if head := rawdb.ReadFinalizedBlockHash(bc.db); head != (entity.Hash{}) {
	//	if block := bc.GetBlockByHash(head); block != nil {
	//		bc.currentFinalizedBlock.Store(block)
	//		//headFinalizedBlockGauge.Update(int64(block.NumberU64()))
	//	}
	//}
	// 为用户发布状态日志
	//currentFastBlock := bc.CurrentFastBlock()
	//currentFinalizedBlock := bc.CurrentFinalizedBlock()

	headerTd := bc.GetTd(currentHeader.Hash(), currentHeader.Number.Uint64())
	blockTd := bc.GetTd(currentBlock.Hash(), currentBlock.NumberU64())
	//fastTd := bc.GetTd(currentFastBlock.Hash(), currentFastBlock.NumberU64())

	log.Info("Loaded most recent local header", "number", currentHeader.Number, "hash", currentHeader.Hash(), "td", headerTd, "age")
	log.Info("Loaded most recent local full block", "number", currentBlock.Number(), "hash", currentBlock.Hash(), "td", blockTd, "age")
	//log.Info("Loaded most recent local fast block", "number", currentFastBlock.Number(), "hash", currentFastBlock.Hash(), "td", fastTd, "age", common.PrettyAge(time.Unix(int64(currentFastBlock.Time()), 0)))

	//if currentFinalizedBlock != nil {
	//	finalTd := bc.GetTd(currentFinalizedBlock.Hash(), currentFinalizedBlock.NumberU64())
	//	log.Info("Loaded most recent local finalized block", "number", currentFinalizedBlock.Number(), "hash", currentFinalizedBlock.Hash(), "td", finalTd, "age", common.PrettyAge(time.Unix(int64(currentFinalizedBlock.Time()), 0)))
	//}
	//if pivot := rawdb.ReadLastPivotNumber(bc.db); pivot != nil {
	//	log.Info("Loaded last fast-sync pivot marker", "number", *pivot)
	//}
	return nil
}

//重置清除整个区块链，将其恢复到起源状态。
func (bc *BlockChain) Reset() error {
	return bc.ResetWithGenesisBlock(bc.genesisBlock)
}

// ResetWithGenesisBlock清除整个区块链，将其恢复到指定的genesis状态。
func (bc *BlockChain) ResetWithGenesisBlock(genesis *block2.Block) error {
	// 转储整个块链并清除缓存
	//if err := bc.SetHead(0); err != nil {
	//	return err
	//}
	//if !bc.chainmu.TryLock() {
	//	return errChainStopped
	//}
	//defer bc.chainmu.Unlock()

	// 准备genesis块并重新初始化链
	batch := bc.db.NewBatch()
	rawdb.WriteTd(batch, genesis.Hash(), genesis.NumberU64(), genesis.Difficulty())
	rawdb.WriteBlock(batch, genesis)
	if err := batch.Write(); err != nil {
		log.Info("Failed to write genesis block", "err", err)
	}
	bc.writeHeadBlock(genesis)

	// 上次更新所有内存链标记
	bc.genesisBlock = genesis
	bc.currentBlock.Store(bc.genesisBlock)
	//headBlockGauge.Update(int64(bc.genesisBlock.NumberU64()))
	bc.hc.SetGenesis(bc.genesisBlock.Header())
	bc.hc.SetCurrentHeader(bc.genesisBlock.Header())
	//bc.currentFastBlock.Store(bc.genesisBlock)
	//headFastBlockGauge.Update(int64(bc.genesisBlock.NumberU64()))
	return nil
}

//SetHead将本地链倒回到新的头部。根据节点是快速同步还是完全同步以及处于何种状态，该方法将尝试从磁盘中删除最少的数据，同时保持链的一致性。
//func (bc *BlockChain) SetHead(head uint64) error {
//	_, err := bc.setHeadBeyondRoot(head, entity.Hash{}, false)
//	return err
//}

//writeHeadBlock将新的头块注入当前块链。
//此方法假定块确实是一个真正的头部。
//如果head header和head fast sync块较旧或位于不同的侧链上，它还会将它们重置为相同的块。
//注意，此函数假设“mu”互斥锁被保持！
func (bc *BlockChain) writeHeadBlock(block *block2.Block) {
	// 将块添加到规范链号方案中，并标记为头部
	batch := bc.db.NewBatch()
	rawdb.WriteHeadHeaderHash(batch, block.Hash())
	//operationdb.WriteHeadFastBlockHash(batch, block.Hash())
	rawdb.WriteCanonicalHash(batch, block.Hash(), block.NumberU64())
	rawdb.WriteTxLookupEntriesByBlock(batch, block)
	rawdb.WriteHeadBlockHash(batch, block.Hash())

	// 将整个批刷新到磁盘中，如果失败，请退出节点
	//if err := batch.Write(); err != nil {
	//	log.Info("Failed to update chain indexes and markers", "err", err)
	//}
	// 在最后一步中更新所有内存链标记
	bc.hc.SetCurrentHeader(block.Header())

	//bc.currentFastBlock.Store(block)
	//headFastBlockGauge.Update(int64(block.NumberU64()))

	bc.currentBlock.Store(block)
	//headBlockGauge.Update(int64(block.NumberU64()))
}

//循环更新区块
func (bc *BlockChain) updateFutureBlocks() {
	futureTimer := time.NewTicker(5 * time.Second)
	defer futureTimer.Stop()
	defer bc.wg.Done()
	for {
		select {
		case <-futureTimer.C:
			bc.procFutureBlocks()
		case <-bc.quit:
			return
		}
	}
}

// 调用StopInsert后，insertStopped返回true。
func (bc *BlockChain) insertStopped() bool {
	return atomic.LoadInt32(&bc.procInterrupt) == 1
}

func (bc *BlockChain) procFutureBlocks() {
	blocks := make([]*block2.Block, 0, bc.futureBlocks.Len())
	for _, hash := range bc.futureBlocks.Keys() {
		if b, exist := bc.futureBlocks.Peek(hash); exist {
			blocks = append(blocks, b.(*block2.Block))
		}
	}
	if len(blocks) > 0 {
		for i := range blocks {
			log.Debug("新增区块：")
			bc.InsertChain(blocks[i : i+1])
		}
	}
}

type Blocks []*block2.Block

func (bc *BlockChain) InsertChain(chain Blocks) (int, error) {
	for i := 1; i < len(chain); i++ {
		block, prev := chain[i], chain[i-1]
		fmt.Println("校验区块父hash是否正确：", block, prev)
	}

	return bc.insertChain(chain, true, true)
}

func (bc *BlockChain) insertChain(chain Blocks, verifySeals, setHead bool) (int, error) {

	//表头验证器
	headers := make([]*block2.Header, len(chain))
	seals := make([]bool, len(chain))

	abort, results := bc.engine.VerifyHeaders(bc, headers, seals)
	defer close(abort)

	//操作块
	it := newInsertIterator(chain, results, bc.validator)
	block, _ := it.next()
	parent := it.previous()
	if parent == nil {
		parent = bc.GetHeader(block.ParentHash(), block.NumberU64()-1)
	}

	//数据库操作
	operationdb, err := operationdb.NewOperationDb(parent.Root, bc.operationCache)
	if err != nil {
		return it.index, err
	}
	receipts, logs, _, err := bc.processor.Process(block, operationdb, bc.vmConfig) //usedGas

	var status WriteStatus
	if !setHead {
		// 不要设置头部，只插入块
		err = bc.writeBlockWithState(block, receipts, logs, operationdb)
	} else {
		status, err = bc.writeBlockAndSetHead(block, receipts, logs, operationdb, false)
	}
	switch status {

	}
	// 这里还有街区吗？我们唯一关心的是未来
	if block != nil && errors.Is(err, consensus.ErrFutureBlock) {
		if err := bc.addFutureBlock(block); err != nil {
			return it.index, err
		}
		block, err = it.next()

		for ; block != nil && errors.Is(err, consensus.ErrUnknownAncestor); block, err = it.next() {
			if err := bc.addFutureBlock(block); err != nil {
				return it.index, err
			}
		}
	}

	return it.index, err
}

//创建一个迭代器
func newInsertIterator(chain Blocks, results <-chan error, validator Validator) *insertIterator {
	return &insertIterator{
		chain:     chain,
		results:   results,
		errors:    make([]error, 0, len(chain)),
		index:     -1,
		validator: validator,
	}
}

// WriteBlockAndSetHead将给定块和所有关联状态写入数据库，并将该块作为新链头应用。
func (bc *BlockChain) WriteBlockAndSetHead(block *block2.Block, receipts []*block2.Receipt, logs []*log.OctopusLog, state *operationdb.OperationDB, emitHeadEvent bool) (status WriteStatus, err error) {
	//if !bc.chainmu.TryLock() {
	//	return NonStatTy, errChainStopped
	//}
	//defer bc.chainmu.Unlock()

	return bc.writeBlockAndSetHead(block, receipts, logs, state, emitHeadEvent)
}

var lastWrite uint64

func (bc *BlockChain) writeBlockWithState(block *block2.Block, receipts []*block2.Receipt, logs []*log.OctopusLog, operation *operationdb.OperationDB) error {
	// 计算方块的总难度
	ptd := bc.GetTd(block.ParentHash(), block.NumberU64()-1)
	if ptd == nil {
		return errors.New("unknown ancestor")
	}
	// 确保插入期间没有不一致的状态泄漏
	externTd := new(big.Int).Add(block.Difficulty(), ptd)

	// 与规范状态无关，将块本身写入数据库。
	// 注：块的所有组件（td、哈希->数字映射、标题、正文、收据）都应该以原子方式写入。BlockBatch用于包含所有组件。
	blockBatch := bc.db.NewBatch()
	rawdb.WriteTd(blockBatch, block.Hash(), block.NumberU64(), externTd)
	rawdb.WriteBlock(blockBatch, block)
	rawdb.WriteReceipts(blockBatch, block.Hash(), block.NumberU64(), receipts)
	//rawdb.WritePreimages(blockBatch, operation.Preimages())
	if terr := blockBatch.Write(); terr != nil {
		log.Info("Failed to write block into disk", "terr", terr)
	}
	//将所有缓存状态更改提交到基础内存数据库中。
	root, terr := operation.Commit(bc.chainConfig.IsEIP158(block.Number()))
	if terr != nil {
		return terr
	}
	triedb := bc.operationCache.TrieDB()

	return triedb.Commit(root, false, nil)
	//如果正在运行存档节点，请始终刷新
	//if bc.cacheConfig.TrieDirtyDisabled {
	//	return triedb.Commit(root, false, nil)
	//} else {
	//	// 已满但未存档节点，请执行正确的垃圾收集
	//	triedb.Reference(root, entity.Hash{}) // 保持trie活动的元数据引用
	//	bc.triegc.Push(root, -int64(block.NumberU64()))
	//
	//	if current := block.NumberU64(); current > TriesInMemory {
	//		// 如果超出内存允许，则将成熟的单例节点刷新到磁盘
	//		var (
	//			nodes, imgs = triedb.Size()
	//			limit       = utils.StorageSize(bc.cacheConfig.TrieDirtyLimit) * 1024 * 1024
	//		)
	//		if nodes > limit || imgs > 4*1024*1024 {
	//			triedb.Cap(limit - typedb.IdealBatchSize)
	//		}
	//		// 找到我们需要提交的下一个状态
	//		chosen := current - TriesInMemory
	//
	//		// 如果超出了超时限制，则将整个trie刷新到磁盘
	//		// 如果缺少标头（后面是规范链），我们将重新定位低差异侧链。暂停提交，直到此操作完成。
	//		header := bc.GetHeaderByNumber(chosen)
	//		if header == nil {
	//			log.Warn("Reorg in progress, trie commit postponed", "number", chosen)
	//		} else {
	//			// 如果我们超出了限制，但还没有达到足够大的内存间隙，请警告用户系统正在变得不稳定。
	//			if chosen < lastWrite+TriesInMemory && bc.gcproc >= 2*bc.cacheConfig.TrieTimeLimit {
	//				log.Info("State in memory for too long, committing", "time", bc.gcproc, "allowance", bc.cacheConfig.TrieTimeLimit, "optimum", float64(chosen-lastWrite)/TriesInMemory)
	//			}
	//			// 刷新整个trie并重新启动计数器
	//			triedb.Commit(header.Root, true, nil)
	//			lastWrite = chosen
	//			bc.gcproc = 0
	//		}
	//		//if bc.gcproc > bc.cacheConfig.TrieTimeLimit {
	//		//	// 如果缺少标头（后面是规范链），我们将重新定位低差异侧链。暂停提交，直到此操作完成。
	//		//	header := bc.GetHeaderByNumber(chosen)
	//		//	if header == nil {
	//		//		log.Warn("Reorg in progress, trie commit postponed", "number", chosen)
	//		//	} else {
	//		//		// 如果我们超出了限制，但还没有达到足够大的内存间隙，请警告用户系统正在变得不稳定。
	//		//		if chosen < lastWrite+TriesInMemory && bc.gcproc >= 2*bc.cacheConfig.TrieTimeLimit {
	//		//			log.Info("State in memory for too long, committing", "time", bc.gcproc, "allowance", bc.cacheConfig.TrieTimeLimit, "optimum", float64(chosen-lastWrite)/TriesInMemory)
	//		//		}
	//		//		// 刷新整个trie并重新启动计数器
	//		//		triedb.Commit(header.Root, true, nil)
	//		//		lastWrite = chosen
	//		//		bc.gcproc = 0
	//		//	}
	//		//}
	//		// 垃圾收集低于我们要求的写保留率的任何内容
	//		for !bc.triegc.Empty() {
	//			root, number := bc.triegc.Pop()
	//			if uint64(-number) > chosen {
	//				bc.triegc.Push(root, number)
	//				break
	//			}
	//			triedb.Dereference(root.(entity.Hash))
	//		}
	//	}
	//}
	//return nil
}

// addFutureBlock检查块是否在允许的最大窗口内，以接受未来处理，如果块太超前且未添加，
//则返回错误。过渡后的待办事项，不应保留未来的障碍。
//因为它不再在Geth端进行检查。
func (bc *BlockChain) addFutureBlock(block *block2.Block) error {
	max := uint64(time.Now().Unix() + maxTimeFutureBlocks)
	if block.Time() > max {
		return fmt.Errorf("future block timestamp %v > allowed %v", block.Time(), max)
	}
	if block.Difficulty().Cmp(operationutils.Big0) == 0 {
		// 切勿将PoS块添加到未来队列中
		return nil
	}
	bc.futureBlocks.Add(block.Hash(), block)
	return nil
}

type WriteStatus byte

const (
	NonStatTy WriteStatus = iota
	CanonStatTy
	SideStatTy
)

// reorg获取两个块，一个旧链和一个新链，并将重构这些块，将它们插入到新的规范链中，积累潜在的缺失事务，并发布关于它们的事件。
//请注意，此处不会处理新的头块，呼叫者需要从外部处理。
func (bc *BlockChain) reorg(oldBlock, newBlock *block2.Block) error {
	var (
		newChain    Blocks
		oldChain    Blocks
		commonBlock *block2.Block

		deletedTxs []entity.Hash
		addedTxs   []entity.Hash

		//deletedLogs [][]*log.OctopusLog
		//rebirthLogs [][]*log.OctopusLog
	)
	// 将长链减少到与短链相同的数量
	if oldBlock.NumberU64() > newBlock.NumberU64() {
		// 旧链较长，将所有事务和日志收集为已删除的事务和日志
		for ; oldBlock != nil && oldBlock.NumberU64() != newBlock.NumberU64(); oldBlock = bc.GetBlock(oldBlock.ParentHash(), oldBlock.NumberU64()-1) {
			oldChain = append(oldChain, oldBlock)
			for _, tx := range oldBlock.Transactions() {
				deletedTxs = append(deletedTxs, tx.Hash())
			}

			// 收集已删除的日志以进行通知
			//logs := bc.collectLogs(oldBlock.Hash(), true)
			//if len(logs) > 0 {
			//	deletedLogs = append(deletedLogs, logs)
			//}
		}
	} else {
		// 新链更长，将所有块隐藏起来以备后续插入
		for ; newBlock != nil && newBlock.NumberU64() != oldBlock.NumberU64(); newBlock = bc.GetBlock(newBlock.ParentHash(), newBlock.NumberU64()-1) {
			newChain = append(newChain, newBlock)
		}
	}
	if oldBlock == nil {
		return fmt.Errorf("invalid old chain")
	}
	if newBlock == nil {
		return fmt.Errorf("invalid new chain")
	}
	// reorg的两边都是相同的数字，减少两边，直到找到共同的祖先
	for {
		// 如果找到了共同的祖先，就退出
		if oldBlock.Hash() == newBlock.Hash() {
			commonBlock = oldBlock
			break
		}
		// 移除旧块并隐藏新块
		oldChain = append(oldChain, oldBlock)
		for _, tx := range oldBlock.Transactions() {
			deletedTxs = append(deletedTxs, tx.Hash())
		}

		// 收集已删除的日志以进行通知
		//logs := bc.collectLogs(oldBlock.Hash(), true)
		//if len(logs) > 0 {
		//	deletedLogs = append(deletedLogs, logs)
		//}
		newChain = append(newChain, newBlock)

		// 用两条链条后退
		oldBlock = bc.GetBlock(oldBlock.ParentHash(), oldBlock.NumberU64()-1)
		if oldBlock == nil {
			return fmt.Errorf("invalid old chain")
		}
		newBlock = bc.GetBlock(newBlock.ParentHash(), newBlock.NumberU64()-1)
		if newBlock == nil {
			return fmt.Errorf("invalid new chain")
		}
	}

	// 确保用户看到较大的REORG
	if len(oldChain) > 0 && len(newChain) > 0 {
		logFn := log.Info
		msg := "Chain reorg detected"
		if len(oldChain) > 63 {
			msg = "Large chain reorg detected"
			logFn = log.Warn
		}
		logFn(msg, "number", commonBlock.Number(), "hash", commonBlock.Hash(),
			"drop", len(oldChain), "dropfrom", oldChain[0].Hash(), "add", len(newChain), "addfrom", newChain[0].Hash())
		//blockReorgAddMeter.Mark(int64(len(newChain)))
		//blockReorgDropMeter.Mark(int64(len(oldChain)))
		//blockReorgMeter.Mark(1)
	} else if len(newChain) > 0 {
		// 特殊情况发生在合并后阶段，当前头是新头的祖先，而这两个块不是连续的
		log.Info("Extend chain", "add", len(newChain), "number", newChain[0].Number(), "hash", newChain[0].Hash())
		//blockReorgAddMeter.Mark(int64(len(newChain)))
	} else {
		// len(newChain) == 0 && len(oldChain) > 0
		// 将正则链倒带到较低的点。
		log.Error("Impossible reorg, please file an issue", "oldnum", oldBlock.Number(), "oldhash", oldBlock.Hash(), "oldblocks", len(oldChain), "newnum", newBlock.Number(), "newhash", newBlock.Hash(), "newblocks", len(newChain))
	}
	// 插入新链（头块除外（逆序）），注意正确的增量顺序。
	for i := len(newChain) - 1; i >= 1; i-- {
		// 以规范的方式插入块，重新写入历史
		bc.writeHeadBlock(newChain[i])

		// 收集新添加的事务。
		for _, tx := range newChain[i].Transactions() {
			addedTxs = append(addedTxs, tx.Hash())
		}
	}

	// 立即删除无用的索引，其中包括非规范事务索引、规范链索引。
	indexesBatch := bc.db.NewBatch()
	for _, tx := range block2.HashDifference(deletedTxs, addedTxs) {
		rawdb.DeleteTxLookupEntry(indexesBatch, tx)
	}

	// 删除所有不属于新规范链的哈希标记。由于reorg函数不处理新链头，因此应删除所有大于或等于新链头的哈希标记。
	number := commonBlock.NumberU64()
	if len(newChain) > 1 {
		number = newChain[1].NumberU64()
	}
	for i := number + 1; ; i++ {
		hash := rawdb.ReadCanonicalHash(bc.db, i)
		if hash == (entity.Hash{}) {
			break
		}
		rawdb.DeleteCanonicalHash(indexesBatch, i)
	}
	if err := indexesBatch.Write(); err != nil {
		log.Info("Failed to delete useless indexes", "err", err)
	}

	// 收集日志
	//for i := len(newChain) - 1; i >= 1; i-- {
	//	// 收集因链重组而重新生成的日志
	//	logs := bc.collectLogs(newChain[i].Hash(), false)
	//	if len(logs) > 0 {
	//		rebirthLogs = append(rebirthLogs, logs)
	//	}
	//}
	//// 如果需要触发任何日志，请立即启动。
	////理论上，如果没有要触发的事件，我们可以避免创建这种goroutine，但实际上，只有在重新定位空块时才会发生这种情况，
	////而这只会发生在性能不成问题的空闲网络上。
	//if len(deletedLogs) > 0 {
	//	bc.rmLogsFeed.Send(RemovedLogsEvent{mergeLogs(deletedLogs, true)})
	//}
	//if len(rebirthLogs) > 0 {
	//	bc.logsFeed.Send(mergeLogs(rebirthLogs, false))
	//}
	//if len(oldChain) > 0 {
	//	for i := len(oldChain) - 1; i >= 0; i-- {
	//		bc.chainSideFeed.Send(ChainSideEvent{Block: oldChain[i]})
	//	}
	//}
	return nil
}

func (bc *BlockChain) writeBlockAndSetHead(block *block2.Block, receipts []*block2.Receipt, logs []*log.OctopusLog, state *operationdb.OperationDB, emitHeadEvent bool) (status WriteStatus, err error) {
	if err := bc.writeBlockWithState(block, receipts, logs, state); err != nil {
		return NonStatTy, err
	}
	currentBlock := bc.CurrentBlock()
	reorg, err := bc.forker.ReorgNeeded(currentBlock.Header(), block.Header())
	if err != nil {
		return NonStatTy, err
	}
	if reorg {
		// 如果父对象不是头块，则重新组织链
		if block.ParentHash() != currentBlock.Hash() {
			if err := bc.reorg(currentBlock, block); err != nil {
				return NonStatTy, err
			}
		}
		status = CanonStatTy
	} else {
		status = SideStatTy
	}
	// 设置新头。
	if status == CanonStatTy {
		bc.writeHeadBlock(block)
	}
	bc.futureBlocks.Remove(block.Hash())

	if status == CanonStatTy {
		bc.chainFeed.Send(event.ChainEvent{Block: block, Hash: block.Hash(), Logs: logs})
		//if len(logs) > 0 {
		//	bc.logsFeed.Send(logs)
		//}
		// 理论上，我们应该在注入规范块时触发ChainHeadEvent，但有时我们可以插入一批规范块。
		//避免触发过多的ChainHeadEvents，我们将触发累积的ChainHeadEvent并在此处禁用fire事件。
		if emitHeadEvent {
			bc.chainHeadFeed.Send(event.ChainHeadEvent{Block: block})
		}
	} else {
		//bc.chainSideFeed.Send(ChainSideEvent{Block: block})
	}
	return status, nil
}
