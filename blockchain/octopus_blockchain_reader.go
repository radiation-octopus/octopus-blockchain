package blockchain

import (
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/entity/rawdb"
	"github.com/radiation-octopus/octopus-blockchain/event"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"math/big"
)

func (bc *BlockChain) CurrentHeader() *block2.Header {
	return bc.hc.CurrentHeader()
}

// CurrentFinalizedBlock检索规范链的当前最终块。区块从区块链的内部缓存中检索。
func (bc *BlockChain) CurrentFinalizedBlock() *block2.Block {
	//return bc.currentFinalizedBlock.Load().(*block2.Block)
	return nil
}

func (bc *BlockChain) GetHeader(hash entity.Hash, number uint64) *block2.Header {
	return bc.hc.GetHeader(hash, number)
}

func (bc *BlockChain) GetHeaderByHash(hash entity.Hash) *block2.Header {
	return bc.hc.GetHeaderByHash(hash)
}

func (bc *BlockChain) GetHeaderByNumber(number uint64) *block2.Header {
	return bc.hc.GetHeaderByNumber(number)
}

// GetCanonicalHash返回给定块号的规范哈希
func (bc *BlockChain) GetCanonicalHash(number uint64) entity.Hash {
	return bc.hc.GetCanonicalHash(number)
}

func (bc *BlockChain) GetTd(hash entity.Hash, number uint64) *big.Int {
	return bc.hc.GetTd(hash, number)
}

// GetBlock通过哈希和数字从数据库中检索块，如果找到，则将其缓存。
func (bc *BlockChain) GetBlock(hash entity.Hash, number uint64) *block2.Block {
	// 如果块已在缓存中，则短路，否则检索
	//if block, ok := bc.blockCache.Get(hash); ok {
	//	return block.(*block2.Block)
	//}
	block := rawdb.ReadBlock(bc.db, hash, number)
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
	return rawdb.HasBody(bc.db, hash, number)
}

// GetHeadersFrom以rlp形式返回从给定数字向后的连续标头段。
func (bc *BlockChain) GetHeadersFrom(number, count uint64) []rlp.RawValue {
	return bc.hc.GetHeadersFrom(number, count)
}

//获取给定块的第n个祖先。
//它假设给定的块或其近祖先是规范的。maxnoncanonic指向一个向下的计数器，该计数器限制在我们到达正则链之前要单独检查的块的数量。
//注意：祖先==0返回同一块，1返回其父块，依此类推。
func (bc *BlockChain) GetAncestor(hash entity.Hash, number, ancestor uint64, maxNonCanonical *uint64) (entity.Hash, uint64) {
	return bc.hc.GetAncestor(hash, number, ancestor, maxNonCanonical)
}

// GetBlockByHash通过哈希从数据库中检索块，如果找到，则将其缓存。
func (bc *BlockChain) GetBlockByHash(hash entity.Hash) *block2.Block {
	//number := bc.hc.GetBlockNumber(hash)
	//if number == nil {
	//	return nil
	//}
	var number uint64
	return bc.GetBlock(hash, number)
}

// 如果新的txlookup限制与旧的不匹配，则SetTxlookup限制负责将txlookup限制更新为存储在db中的原始限制。
func (bc *BlockChain) SetTxLookupLimit(limit uint64) {
	bc.TxLookupLimit = limit
}

// StateCache returns the caching database underpinning the blockchain instance.
func (bc *BlockChain) StateCache() operationdb.DatabaseI {
	return bc.stateCache
}

// TrioNode从临时内存缓存或持久存储中检索与trie节点相关的数据块。
func (bc *BlockChain) TrieNode(hash entity.Hash) ([]byte, error) {
	return bc.stateCache.TrieDB().Node(hash)
}

// ContractCodeWithPrefix从临时内存缓存或持久存储中检索与协定哈希相关联的数据块。
//如果内存缓存中不存在代码，请使用新的代码方案检查存储。
func (bc *BlockChain) ContractCodeWithPrefix(hash entity.Hash) ([]byte, error) {
	type codeReader interface {
		ContractCodeWithPrefix(addrHash, codeHash entity.Hash) ([]byte, error)
	}
	return bc.stateCache.(codeReader).ContractCodeWithPrefix(entity.Hash{}, hash)
}

// GetBodyRLP通过哈希从数据库中检索RLP编码的块体，如果找到，则将其缓存。
func (bc *BlockChain) GetBodyRLP(hash entity.Hash) rlp.RawValue {
	// 如果尸体已经在缓存中，则短路，否则检索
	if cached, ok := bc.bodyRLPCache.Get(hash); ok {
		return cached.(rlp.RawValue)
	}
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadBodyRLP(bc.db, hash, *number)
	if len(body) == 0 {
		return nil
	}
	// 缓存找到的尸体以备下次使用并返回
	bc.bodyRLPCache.Add(hash, body)
	return body
}

// Subscribe ChainHeadEvent注册ChainHeadEvent的订阅。
func (bc *BlockChain) SubscribeChainHeadEvent(ch chan<- event.ChainHeadEvent) event.Subscription {
	return bc.scope.Track(bc.chainHeadFeed.Subscribe(ch))
}

func (bc *BlockChain) CurrentBlock() *block2.Block {
	return bc.currentBlock.Load().(*block2.Block)
}

//StateAt基于特定时间点返回新的可变状态。
func (bc *BlockChain) StateAt(root entity.Hash) (*operationdb.OperationDB, error) {
	return operationdb.NewOperationDb(root, bc.operationCache)
}

// GetBlocksFromHash返回与哈希对应的块，最多返回n-1个祖先。
func (bc *BlockChain) GetBlocksFromHash(hash entity.Hash, n int) (blocks []*block2.Block) {
	number := bc.hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	for i := 0; i < n; i++ {
		block := bc.GetBlock(hash, *number)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		hash = block.ParentHash()
		*number--
	}
	return
}

// GetBlockByNumber按编号从数据库中检索块，如果找到，则缓存它（与其哈希关联）。
func (bc *BlockChain) GetBlockByNumber(number uint64) *block2.Block {
	hash := rawdb.ReadCanonicalHash(bc.db, number)
	if hash == (entity.Hash{}) {
		return nil
	}
	return bc.GetBlock(hash, number)
}

//Processor返回当前处理器。
func (bc *BlockChain) Processor() Processor {
	return bc.processor
}

// SubscribeLogsEvent注册[]*类型的订阅。日志
func (bc *BlockChain) SubscribeLogsEvent(ch chan<- []*block2.Log) event.Subscription {
	return bc.scope.Track(bc.logsFeed.Subscribe(ch))
}

// SubscribeRemovedLogsEvent注册RemovedLogsEvent的订阅。
func (bc *BlockChain) SubscribeRemovedLogsEvent(ch chan<- event.RemovedLogsEvent) event.Subscription {
	return bc.scope.Track(bc.rmLogsFeed.Subscribe(ch))
}

// SubscribeChainEvent注册ChainEvent的订阅。
func (bc *BlockChain) SubscribeChainEvent(ch chan<- event.ChainEvent) event.Subscription {
	return bc.scope.Track(bc.chainFeed.Subscribe(ch))
}

// Config检索链的fork配置。
func (bc *BlockChain) Config() *entity.ChainConfig { return bc.chainConfig }
