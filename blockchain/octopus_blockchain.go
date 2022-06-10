package blockchain

import (
	"errors"
	"fmt"
	lru "github.com/hashicorp/golang-lru"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/memorydb"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus-blockchain/vm"
	"github.com/radiation-octopus/octopus/log"
	"math/big"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxFutureBlocks = 256
)

//blockchain结构体
type BlockChain struct {
	db operationdb.Database //数据库

	TxLookupLimit uint64 `autoInjectCfg:octopus.blockchain.binding.genesis.header.txLookupLimit"` //一个区块容纳最大交易限制
	hc            *HeaderChain
	chainHeadFeed Feed
	blockProcFeed Feed //区块过程注入事件
	scope         SubscriptionScope
	genesisBlock  *block.Block

	//chainmu *syncx.ClosableMutex	//互斥锁，同步链写入操作使用
	currentBlock atomic.Value // 当前区块
	//currentFastBlock atomic.Value	//快速同步链的当前区块

	operationCache operationdb.DatabaseI
	stateCache     operationdb.DatabaseI // 要在导入之间重用的状态数据库（包含状态缓存）
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

type ChainConfig struct {
	ChainID *big.Int `json:"chainId"` // chainId标识当前链并用于重播保护
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
func (it *insertIterator) next() (*block.Block, error) {
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
func (it *insertIterator) previous() *block.Header {
	if it.index < 1 {
		return nil
	}
	return it.chain[it.index-1].Header()
}

//链启动类，配置参数启动
func (bc *BlockChain) start() {
	//构造创世区块
	genesis := DefaultGenesisBlock()
	SetupGenesisBlockWithOverride(bc.db, genesis, nil, nil)
	//打开内存数据库
	chainDb, err := OpenDatabaseWithFreezer()
	if err != nil {
		errors.New("chainDb start failed")
	}
	//初始化区块链
	bc, erro := newBlockChain(bc, chainDb, nil, nil)
	if erro != nil {
		errors.New("blockchain start fail")
	}
}

//链终止
func (bc *BlockChain) close() {

}

func (bc *BlockChain) getBlockChain() *BlockChain {
	return bc
}

// GetVMConfig返回块链VM配置。
func (bc *BlockChain) GetVMConfig() *vm.Config {
	return &bc.vmConfig
}

//构建区块链结构体
func newBlockChain(bc *BlockChain, db operationdb.Database, engine consensus.Engine, shouldPreserve func(header *block.Header) bool) (*BlockChain, error) {
	futureBlocks, _ := lru.New(maxFutureBlocks)
	bc.db = db
	bc.quit = make(chan struct{})
	bc.futureBlocks = futureBlocks
	bc.engine = engine
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
	//bc.forker = NewForkChoice(bc, shouldPreserve)
	bc.stateCache = operationdb.NewDatabaseWithConfig(db, &operationdb.Config{
		//Cache:     cacheConfig.TrieCleanLimit,
		//Journal:   cacheConfig.TrieCleanJournal,
		//Preimages: cacheConfig.Preimages,
	})
	//构建区块验证器
	bc.validator = NewBlockValidator(bc, engine)
	//构建区块处理器
	bc.processor = NewBlockProcessor(bc, engine)

	var err error
	bc.hc, err = NewHeaderChain(engine, bc.insertStopped)
	if err != nil {
		return nil, err
	}
	//获取创世区块
	bc.genesisBlock = bc.getBlockByNumber(0)
	if bc.genesisBlock == nil {
		return nil, errors.New("创世区块未发现")
	}

	var nilBlock *block.Block
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
	head := operationdb.ReadHeadBlockHash()
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

	// Restore the last known head header
	currentHeader := currentBlock.Header()
	if head := operationdb.ReadHeadHeaderHash(bc.db); head != (entity.Hash{}) {
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
func (bc *BlockChain) ResetWithGenesisBlock(genesis *block.Block) error {
	// 转储整个块链并清除缓存
	//if err := bc.SetHead(0); err != nil {
	//	return err
	//}
	//if !bc.chainmu.TryLock() {
	//	return errChainStopped
	//}
	//defer bc.chainmu.Unlock()

	// 准备genesis块并重新初始化链
	//batch := bc.db.NewBatch()
	operationdb.WriteTd(genesis.Hash(), genesis.NumberU64(), genesis.Difficulty())
	operationdb.WriteBlock(genesis)
	//if err := batch.Write(); err != nil {
	//	log.Info("Failed to write genesis block", "err", err)
	//}
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
func (bc *BlockChain) writeHeadBlock(block *block.Block) {
	// 将块添加到规范链号方案中，并标记为头部
	//batch := bc.db.NewBatch()
	operationdb.WriteHeadHeaderHash(block.Hash())
	//operationdb.WriteHeadFastBlockHash(batch, block.Hash())
	operationdb.WriteCanonicalHash(block.Hash(), block.NumberU64())
	operationdb.WriteTxLookupEntriesByBlock(block)
	operationdb.WriteHeadBlockHash(block.Hash())

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
	log.Info("新增区块：")
	blocks := make([]*block.Block, 0, bc.futureBlocks.Len())
	for _, hash := range bc.futureBlocks.Keys() {
		if b, exist := bc.futureBlocks.Peek(hash); exist {
			blocks = append(blocks, b.(*block.Block))
		}
	}
	if len(blocks) > 0 {
		for i := range blocks {
			bc.InsertChain(blocks[i : i+1])
		}
	}
}

type Blocks []*block.Block

func (bc *BlockChain) InsertChain(chain Blocks) (int, error) {
	for i := 1; i < len(chain); i++ {
		block, prev := chain[i], chain[i-1]
		fmt.Println("校验区块父hash是否正确：", block, prev)
	}

	return bc.insertChain(chain, true, true)
}

func (bc *BlockChain) insertChain(chain Blocks, verifySeals, setHead bool) (int, error) {

	//表头验证器
	headers := make([]*block.Header, len(chain))
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

// WriteBlockAndSetHead writes the given block and all associated state to the database,
// and applies the block as the new chain head.
func (bc *BlockChain) WriteBlockAndSetHead(block *block.Block, receipts []*block.Receipt, logs []*log.OctopusLog, state *operationdb.OperationDB, emitHeadEvent bool) (status WriteStatus, err error) {
	//if !bc.chainmu.TryLock() {
	//	return NonStatTy, errChainStopped
	//}
	//defer bc.chainmu.Unlock()

	return bc.writeBlockAndSetHead(block, receipts, logs, state, emitHeadEvent)
}

func (bc *BlockChain) writeBlockWithState(block *block.Block, receipts []*block.Receipt, logs []*log.OctopusLog, operation *operationdb.OperationDB) error {
	// Calculate the total difficulty of the block
	ptd := bc.GetTd(block.ParentHash(), block.NumberU64()-1)
	if ptd == nil {
		return errors.New("unknown ancestor")
	}
	// Make sure no inconsistent state is leaked during insertion
	externTd := new(big.Int).Add(block.Difficulty(), ptd)

	// Irrelevant of the canonical status, write the block itself to the database.
	//
	// Note all the components of block(td, hash->number map, header, body, receipts)
	// should be written atomically. BlockBatch is used for containing all components.
	//blockBatch := bc.db.NewBatch()
	operationdb.WriteTd(block.Hash(), block.NumberU64(), externTd)
	operationdb.WriteBlock(block)
	operationdb.WriteReceipts(block.Hash(), receipts)
	//rawdb.WritePreimages(blockBatch, state.Preimages())
	//if terr := blockBatch.Write(); terr != nil {
	//	log.Crit("Failed to write block into disk", "terr", terr)
	//}
	// 将所有缓存状态更改提交到基础内存数据库中。
	//root, terr := state.Commit(bc.chainConfig.IsEIP158(block.Number()))
	//if terr != nil {
	//	return terr
	//}
	//triedb := bc.stateCache.TrieDB()

	// If we're running an archive node, always flush
	//if bc.cacheConfig.TrieDirtyDisabled {
	//	return triedb.Commit(root, false, nil)
	//} else {
	//	// Full but not archive node, do proper garbage collection
	//	triedb.Reference(root, common.Hash{}) // metadata reference to keep trie alive
	//	bc.triegc.Push(root, -int64(block.NumberU64()))
	//
	//	if current := block.NumberU64(); current > TriesInMemory {
	//		// If we exceeded our memory allowance, flush matured singleton nodes to disk
	//		var (
	//			nodes, imgs = triedb.Size()
	//			limit       = common.StorageSize(bc.cacheConfig.TrieDirtyLimit) * 1024 * 1024
	//		)
	//		if nodes > limit || imgs > 4*1024*1024 {
	//			triedb.Cap(limit - ethdb.IdealBatchSize)
	//		}
	//		// Find the next state trie we need to commit
	//		chosen := current - TriesInMemory
	//
	//		// If we exceeded out time allowance, flush an entire trie to disk
	//		if bc.gcproc > bc.cacheConfig.TrieTimeLimit {
	//			// If the header is missing (canonical chain behind), we're reorging a low
	//			// diff sidechain. Suspend committing until this operation is completed.
	//			header := bc.GetHeaderByNumber(chosen)
	//			if header == nil {
	//				log.Warn("Reorg in progress, trie commit postponed", "number", chosen)
	//			} else {
	//				// If we're exceeding limits but haven't reached a large enough memory gap,
	//				// warn the user that the system is becoming unstable.
	//				if chosen < lastWrite+TriesInMemory && bc.gcproc >= 2*bc.cacheConfig.TrieTimeLimit {
	//					log.Info("State in memory for too long, committing", "time", bc.gcproc, "allowance", bc.cacheConfig.TrieTimeLimit, "optimum", float64(chosen-lastWrite)/TriesInMemory)
	//				}
	//				// Flush an entire trie and restart the counters
	//				triedb.Commit(header.Root, true, nil)
	//				lastWrite = chosen
	//				bc.gcproc = 0
	//			}
	//		}
	//		// Garbage collect anything below our required write retention
	//		for !bc.triegc.Empty() {
	//			root, number := bc.triegc.Pop()
	//			if uint64(-number) > chosen {
	//				bc.triegc.Push(root, number)
	//				break
	//			}
	//			triedb.Dereference(root.(common.Hash))
	//		}
	//	}
	//}
	return nil
}

type WriteStatus byte

func (bc *BlockChain) writeBlockAndSetHead(block *block.Block, receipts []*block.Receipt, logs []*log.OctopusLog, state *operationdb.OperationDB, emitHeadEvent bool) (status WriteStatus, err error) {
	if err := bc.writeBlockWithState(block, receipts, logs, state); err != nil {
		return 0, err
	}
	return 1, nil
}

// OpenDatabaseWithFreezer从节点的数据目录中打开一个具有给定名称的现有数据库（如果找不到以前的名称，则创建一个），
//并向其附加一个链冻结器，该链冻结器将已有的的链数据从数据库移动到不可变的仅附加文件。
//如果节点是临时节点，则返回内存数据库。
func OpenDatabaseWithFreezer() (operationdb.Database, error) {
	var db operationdb.Database
	var err error
	db = memorydb.NewMemoryDatabase()
	//n.lock.Lock()
	//defer n.lock.Unlock()
	//if n.state == closedState {
	//	return nil, ErrNodeStopped
	//}
	//
	//var db operationdb.Database
	//var err error
	//if n.config.DataDir == "" {
	//	db = operationdb.NewMemoryDatabase()
	//} else {
	//root := n.ResolvePath(name)
	//switch {
	//case freezer == "":
	//	freezer = filepath.Join(root, "ancient")
	//case !filepath.IsAbs(freezer):
	//	freezer = n.ResolvePath(freezer)
	//}
	//db, err = rawdb.NewLevelDBDatabaseWithFreezer(root, cache, handles, freezer, namespace, readonly)
	//}
	//
	//if err == nil {
	//	db = n.wrapDatabase(db)
	//}
	return db, err
}
