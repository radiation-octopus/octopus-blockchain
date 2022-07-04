package consensus

import (
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"math/big"
)

var (
	// 当验证块需要未知的祖先时，将返回ErrUnknownAncestor。
	ErrUnknownAncestor = errors.New("unknown ancestor")
	// 根据当前节点，当块的时间戳在未来时，将返回ErrFutureBlock。
	ErrFutureBlock = errors.New("block in the future")
	// 如果块的编号不等于其父块的编号加1，则返回ErrInvalidNumber。
	ErrInvalidNumber = errors.New("invalid block number")
)

//该接口定义了验证期间访问本地本地区块两所需的一小部分方法
type ChainHeaderReader interface {
	// Config检索区块链的链配置。
	Config() *entity.ChainConfig

	// 从本地链检索当前头
	CurrentHeader() *block2.Header

	// 通过hash和数字从数据库检索块头
	GetHeader(hash entity.Hash, number uint64) *block2.Header

	// 按编号从数据库检索块头
	GetHeaderByNumber(number uint64) *block2.Header

	// 通过其hash从数据库中检索块头
	GetHeaderByHash(hash entity.Hash) *block2.Header

	// 通过hash和数字从数据库中检索总难度
	GetTd(hash entity.Hash, number uint64) *big.Int
}

//共识引擎接口
type Engine interface {
	Author(header *block2.Header) (entity.Address, error)
	//表头验证器，该方法返回退出通道以终止操作，验证顺序为切片排序
	VerifyHeaders(chain ChainHeaderReader, headers []*block2.Header, seals []bool) (chan<- struct{}, <-chan error)

	// Prepare根据特定引擎的规则初始化块标头的一致性字段。更改以内联方式执行。
	Prepare(chain ChainHeaderReader, header *block2.Header) error

	// FinalizeAndAssemble运行任何交易后状态修改（例如区块奖励）并组装最终区块。
	//注意：可能会更新区块标题和状态数据库，以反映最终确定时发生的任何共识规则（例如区块奖励）。
	FinalizeAndAssemble(chain ChainHeaderReader, header *block2.Header, state *operationdb.OperationDB, txs []*block2.Transaction,
		uncles []*block2.Header, receipts []*block2.Receipt) (*block2.Block, error)

	//Seal为给定的输入块生成新的密封请求，并将结果推送到给定的通道中。
	//注意，该方法立即返回，并将异步发送结果。根据一致性算法，还可能返回多个结果。
	Seal(chain ChainHeaderReader, block *block2.Block, results chan<- *block2.Block, stop <-chan struct{}) error

	// SealHash返回块在被密封之前的哈希值。
	SealHash(header *block2.Header) entity.Hash

	// CalcDifficulty是难度调整算法。它返回新块应该具有的难度。
	CalcDifficulty(chain ChainHeaderReader, time uint64, parent *block2.Header) *big.Int
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
