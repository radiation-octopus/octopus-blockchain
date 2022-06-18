package consensus

import (
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"math/big"
)

//该接口定义了验证期间访问本地本地区块两所需的一小部分方法
type ChainHeaderReader interface {
	// Config retrieves the blockchain's chain configuration.
	//Config() *params.ChainConfig

	// 从本地链检索当前头
	CurrentHeader() *block.Header

	// 通过hash和数字从数据库检索块头
	GetHeader(hash entity.Hash, number uint64) *block.Header

	// 按编号从数据库检索块头
	GetHeaderByNumber(number uint64) *block.Header

	// 通过其hash从数据库中检索块头
	GetHeaderByHash(hash entity.Hash) *block.Header

	// 通过hash和数字从数据库中检索总难度
	GetTd(hash entity.Hash, number uint64) *big.Int
}

//共识引擎接口
type Engine interface {
	Author(header *block.Header) (entity.Address, error)
	//表头验证器，该方法返回退出通道以终止操作，验证顺序为切片排序
	VerifyHeaders(chain ChainHeaderReader, headers []*block.Header, seals []bool) (chan<- struct{}, <-chan error)

	// FinalizeAndAssemble运行任何交易后状态修改（例如区块奖励）并组装最终区块。
	//注意：可能会更新区块标题和状态数据库，以反映最终确定时发生的任何共识规则（例如区块奖励）。
	FinalizeAndAssemble(chain ChainHeaderReader, header *block.Header, state *operationdb.OperationDB, txs []*block.Transaction,
		uncles []*block.Header, receipts []*block.Receipt) (*block.Block, error)

	//Seal为给定的输入块生成新的密封请求，并将结果推送到给定的通道中。
	//注意，该方法立即返回，并将异步发送结果。根据一致性算法，还可能返回多个结果。
	Seal(chain ChainHeaderReader, block *block.Block, results chan<- *block.Block, stop <-chan struct{}) error

	// SealHash返回块在被密封之前的哈希值。
	SealHash(header *block.Header) entity.Hash
}

// FinalizeAndAssemble 实现 consensus.Engine
//累积区块和叔块奖励，设定最终状态并组装积木区块。
//func (ethash *Ethash) FinalizeAndAssemble(chain ChainHeaderReader, header *block.Header, state *db.OperationDB, txs []*block.Transaction, uncles []*block.Header, receipts []*block.Receipt) (*block.Block, terr) {
//	// Finalize block
//	//ethash.Finalize(chain, header, state, txs, uncles)
//
//	// 收割台已完成，组装成块并返回
//	return block.NewBlock(header, txs, uncles, receipts), nil
//}
