package blockchain

import (
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/operationDB"
	"github.com/radiation-octopus/octopus-blockchain/operationUtils"
	"math/big"
)

func (bc *BlockChain) getBlockByNumber(number uint64) *block.Block {
	//把创世区块注入
	//director.Register(new(Block))

	return nil
}

func (bc *BlockChain) CurrentHeader() *block.Header {
	return bc.hc.CurrentHeader()
}

func (bc *BlockChain) GetHeader(hash operationUtils.Hash, number uint64) *block.Header {
	return bc.hc.GetHeader(hash, number)
}

func (bc *BlockChain) GetHeaderByHash(hash operationUtils.Hash) *block.Header {
	return bc.hc.GetHeaderByHash(hash)
}

func (bc *BlockChain) GetHeaderByNumber(number uint64) *block.Header {
	return bc.hc.GetHeaderByNumber(number)
}

func (bc *BlockChain) GetTd(hash operationUtils.Hash, number uint64) *big.Int {
	return bc.hc.GetTd(hash, number)
}

// GetBlock通过哈希和数字从数据库中检索块，如果找到，则将其缓存。
func (bc *BlockChain) GetBlock(hash operationUtils.Hash) *block.Block {
	// Short circuit if the block's already in the cache, retrieve otherwise
	//if block, ok := bc.blockCache.Get(hash); ok {
	//	return block.(*types.Block)
	//}
	var number uint64
	block := operationDB.ReadBlock(bc.db, hash, number)
	if block == nil {
		return nil
	}
	// Cache the found block for next time and return
	//bc.blockCache.Add(block.Hash(), block)
	return block
}

// HasBlock检查数据库中是否完全存在块。
func (bc *BlockChain) HasBlock(hash operationUtils.Hash, number uint64) bool {
	//if bc.blockCache.Contains(hash) {
	//	return true
	//}
	return operationDB.HasBody(bc.db, hash, number)
}

// GetBlockByHash通过哈希从数据库中检索块，如果找到，则将其缓存。
func (bc *BlockChain) GetBlockByHash(hash operationUtils.Hash) *block.Block {
	//number := bc.hc.GetBlockNumber(hash)
	//if number == nil {
	//	return nil
	//}
	return bc.GetBlock(hash)
}

// Subscribe ChainHeadEvent注册ChainHeadEvent的订阅。
func (bc *BlockChain) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) Subscription {
	return bc.scope.Track(bc.chainHeadFeed.Subscribe(ch))
}

func (bc *BlockChain) CurrentBlock() *block.Block {
	return bc.currentBlock.Load().(*block.Block)
}

//StateAt基于特定时间点返回新的可变状态。
func (bc *BlockChain) StateAt(root operationUtils.Hash) (*operationDB.OperationDB, error) {
	return operationDB.New(root, bc.stateCache)
}

// GetBlocksFromHash返回与哈希对应的块，最多返回n-1个祖先。
func (bc *BlockChain) GetBlocksFromHash(hash operationUtils.Hash, n int) (blocks []*block.Block) {
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	for i := 0; i < n; i++ {
		block := bc.GetBlock(hash)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		hash = block.ParentHash()
		//*number--
	}
	return
}
