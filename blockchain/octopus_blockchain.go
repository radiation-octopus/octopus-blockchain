package blockchain

import (
	"errors"
	"fmt"
	lru "github.com/hashicorp/golang-lru"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/operationDB"
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
	db            operationDB.Database //数据库
	txLookupLimit uint64               `autoInjectCfg:"octopus.block.binding.txLookupLimit"` //一个区块容纳最大交易限制
	hc            *HeaderChain
	chainHeadFeed Feed
	blockProcFeed Feed //区块过程注入事件
	scope         SubscriptionScope
	genesisBlock  *block.Block

	//chainmu *syncx.ClosableMutex	//互斥锁，同步链写入操作使用
	currentBlock atomic.Value // 当前区块
	//currentFastBlock atomic.Value	//快速同步链的当前区块

	operationCache operationDB.Database
	stateCache     operationDB.Database // 要在导入之间重用的状态数据库（包含状态缓存）
	futureBlocks   *lru.Cache           //新区块缓存区
	wg             sync.WaitGroup       //同步等待属性
	quit           chan struct{}        //关闭属性
	running        int32                //运行属性
	procInterrupt  int32                //块处理中断信号

	engine    consensus.Engine //共识引擎
	validator Validator        //区块验证器
	processor Processor        //区块处理器
	forker    *ForkChoice      //最高难度值

	vmConfig vm.Config
}

type ChainConfig struct {
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
	//初始化区块链
	newBlockChain(operationDB.Database{}, nil, nil)
}

//链终止
func (bc *BlockChain) close() {

}

// GetVMConfig返回块链VM配置。
func (bc *BlockChain) GetVMConfig() *vm.Config {
	return &bc.vmConfig
}

//构建区块链结构体
func newBlockChain(db operationDB.Database, engine consensus.Engine, shouldPreserve func(header *block.Header) bool) (*BlockChain, error) {
	futureBlocks, _ := lru.New(maxFutureBlocks)
	bc := &BlockChain{
		db:   db,
		quit: make(chan struct{}),
		//chainmu:       syncx.NewClosableMutex(),
		futureBlocks: futureBlocks,
		engine:       engine,
	}
	//bc.forker = NewForkChoice(bc, shouldPreserve)
	//构建区块验证器
	bc.validator = NewBlockValidator(bc, engine)
	//构建区块处理器
	bc.processor = NewBlockProcessor(bc, engine)
	//获取创世区块
	bc.genesisBlock = bc.getBlockByNumber(0)
	//if bc.genesisBlock == nil {
	//	return nil, errors.New("创世区块未发现")
	//}
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

	return nil
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
	statedb, err := operationDB.New(parent.Root, bc.operationCache)
	if err != nil {
		return it.index, err
	}
	receipts, logs, _, err := bc.processor.Process(block, statedb, bc.vmConfig) //usedGas

	var status WriteStatus
	if !setHead {
		// 不要设置头部，只插入块
		err = bc.writeBlockWithState(block, receipts, logs, statedb)
	} else {
		status, err = bc.writeBlockAndSetHead(block, receipts, logs, statedb, false)
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
func (bc *BlockChain) WriteBlockAndSetHead(block *block.Block, receipts []*block.Receipt, logs []*log.OctopusLog, state *operationDB.OperationDB, emitHeadEvent bool) (status WriteStatus, err error) {
	//if !bc.chainmu.TryLock() {
	//	return NonStatTy, errChainStopped
	//}
	//defer bc.chainmu.Unlock()

	return bc.writeBlockAndSetHead(block, receipts, logs, state, emitHeadEvent)
}

func (bc *BlockChain) writeBlockWithState(block *block.Block, receipts []*block.Receipt, logs []*log.OctopusLog, operation *operationDB.OperationDB) error {
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
	operationDB.WriteTd(bc.db, block.Hash(), block.NumberU64(), externTd)
	operationDB.WriteBlock(block)
	operationDB.WriteReceipts(block.Hash(), receipts)
	//rawdb.WritePreimages(blockBatch, state.Preimages())
	//if err := blockBatch.Write(); err != nil {
	//	log.Crit("Failed to write block into disk", "err", err)
	//}
	// 将所有缓存状态更改提交到基础内存数据库中。
	//root, err := state.Commit(bc.chainConfig.IsEIP158(block.Number()))
	//if err != nil {
	//	return err
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

func (bc *BlockChain) writeBlockAndSetHead(block *block.Block, receipts []*block.Receipt, logs []*log.OctopusLog, state *operationDB.OperationDB, emitHeadEvent bool) (status WriteStatus, err error) {
	if err := bc.writeBlockWithState(block, receipts, logs, state); err != nil {
		return 0, err
	}
	return 1, nil
}
