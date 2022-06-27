package blockchain

import (
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"math/big"
)

func (bc *BlockChain) getBlockByNumber(number uint64) *block.Block {
	hash := operationdb.ReadCanonicalHash(number)
	if hash == (entity.Hash{}) {
		return nil
	}
	return bc.GetBlock(hash, number)
}

func (bc *BlockChain) CurrentHeader() *block.Header {
	return bc.hc.CurrentHeader()
}

func (bc *BlockChain) GetHeader(hash entity.Hash, number uint64) *block.Header {
	return bc.hc.GetHeader(hash, number)
}

func (bc *BlockChain) GetHeaderByHash(hash entity.Hash) *block.Header {
	return bc.hc.GetHeaderByHash(hash)
}

func (bc *BlockChain) GetHeaderByNumber(number uint64) *block.Header {
	return bc.hc.GetHeaderByNumber(number)
}

func (bc *BlockChain) GetTd(hash entity.Hash, number uint64) *big.Int {
	return bc.hc.GetTd(hash, number)
}

// GetBlock通过哈希和数字从数据库中检索块，如果找到，则将其缓存。
func (bc *BlockChain) GetBlock(hash entity.Hash, number uint64) *block.Block {
	// Short circuit if the block's already in the cache, retrieve otherwise
	//if block, ok := bc.blockCache.Get(hash); ok {
	//	return block.(*types.Block)
	//}
	block := operationdb.ReadBlock(bc.db, hash, number)
	if block == nil {
		return nil
	}
	// 缓存找到的块以备下次使用并返回
	//bc.blockCache.Add(block.Hash(), block)
	return block
}

// HasBlock检查数据库中是否完全存在块。
func (bc *BlockChain) HasBlock(hash entity.Hash, number uint64) bool {
	//if bc.blockCache.Contains(hash) {
	//	return true
	//}
	return operationdb.HasBody(bc.db, hash, number)
}

// GetBlockByHash通过哈希从数据库中检索块，如果找到，则将其缓存。
func (bc *BlockChain) GetBlockByHash(hash entity.Hash) *block.Block {
	//number := bc.hc.GetBlockNumber(hash)
	//if number == nil {
	//	return nil
	//}
	var number uint64
	return bc.GetBlock(hash, number)
}

// Subscribe ChainHeadEvent注册ChainHeadEvent的订阅。
func (bc *BlockChain) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) Subscription {
	return bc.scope.Track(bc.chainHeadFeed.Subscribe(ch))
}

func (bc *BlockChain) CurrentBlock() *block.Block {
	return bc.currentBlock.Load().(*block.Block)
}

//StateAt基于特定时间点返回新的可变状态。
func (bc *BlockChain) StateAt(root entity.Hash) (*operationdb.OperationDB, error) {
	return operationdb.NewOperationDb(root, bc.operationCache)
}

// GetBlocksFromHash返回与哈希对应的块，最多返回n-1个祖先。
func (bc *BlockChain) GetBlocksFromHash(hash entity.Hash, n int) (blocks []*block.Block) {
	number := bc.hc.GetBlockNumber(hash)
	var nu uint64
	if number == nil {
		return nil
	}
	for i := 0; i < n; i++ {
		block := bc.GetBlock(hash, nu)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		hash = block.ParentHash()
		//*number--
	}
	return
}

// GetBlockByNumber按编号从数据库中检索块，如果找到，则缓存它（与其哈希关联）。
func (bc *BlockChain) GetBlockByNumber(number uint64) *block.Block {
	hash := operationdb.ReadCanonicalHash(number)
	if hash == (entity.Hash{}) {
		return nil
	}
	return bc.GetBlock(hash, number)
}

//Processor返回当前处理器。
func (bc *BlockChain) Processor() Processor {
	return bc.processor
}

// Config检索链的fork配置。
func (bc *BlockChain) Config() *entity.ChainConfig { return bc.chainConfig }
