package blockchain

import (
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus-blockchain/transition"
	"github.com/radiation-octopus/octopus-blockchain/vm"
	"github.com/radiation-octopus/octopus/log"
	"math/big"
)

//处理器结构体
type BlockProcessor struct {
	//config *entity.ChainConfig // 链配置
	bc     *BlockChain      // 标准链
	engine consensus.Engine // 共识引擎
}

//构建处理器
func NewBlockProcessor(bc *BlockChain, engine consensus.Engine) *BlockProcessor {
	bp := &BlockProcessor{
		bc:     bc,
		engine: engine,
	}
	return bp
}

//处理器接口
type Processor interface {
	//处理改变区块状态，将区块加入主链
	Process(block *block.Block, operationdb *operationdb.OperationDB, cfg vm.Config) (block.Receipts, []*log.OctopusLog, uint64, error)
}

func (p *BlockProcessor) Process(b *block.Block, operationdb *operationdb.OperationDB, cfg vm.Config) (block.Receipts, []*log.OctopusLog, uint64, error) {
	var (
		receipts    block.Receipts
		usedGas     = new(uint64)
		header      = b.Header()
		blockHash   = b.Hash()
		blockNumber = b.Number()
		allLogs     []*log.OctopusLog
		gp          = new(transition.GasPool).AddGas(b.GasLimit())
	)
	blockContext := vm.NewOVMBlockContext(header, p.bc, nil)
	//初始化虚拟机
	vmonv := vm.NewOVM(blockContext, vm.TxContext{}, operationdb, cfg)
	for i, tx := range b.Transactions() {
		msg, err := tx.AsMessage(block.MakeSigner(header.Number), header.BaseFee)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		receipt, err := applyTransaction(msg, gp, operationdb, blockNumber, blockHash, tx, usedGas, vmonv)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		receipts = append(receipts, receipt)
		allLogs = append(allLogs, receipt.Logs...)
	}

	return receipts, allLogs, *usedGas, nil
}

//处理事务
func applyTransaction(msg block.Message, gp *transition.GasPool, operationdb *operationdb.OperationDB, blockNumber *big.Int, blockHash entity.Hash, tx *block.Transaction, usedGas *uint64, ovm *vm.OVM) (*block.Receipt, error) {

	// 创建要在EVM环境中使用的新配置。
	txContext := vm.NewEVMTxContext(msg)
	ovm.Reset(txContext, operationdb)

	//将事务应用于当前状态（包含在env中）。
	result, err := transition.ApplyMessage(ovm, msg, gp)
	if err != nil {
		return nil, err
	}

	*usedGas += result.UsedGas
	// 为交易创建新收据，存储tx使用的中间根和gas。
	receipt := &block.Receipt{}
	receipt.TxHash = tx.Hash()
	receipt.GasUsed = result.UsedGas
	receipt.BlockHash = blockHash
	receipt.BlockNumber = blockNumber
	return receipt, err
}

// ApplyTransaction尝试将事务应用于给定的状态数据库，并使用其环境的输入参数。如果交易失败，则返回交易收据、使用的天然气和terr，表明阻塞无效。
func ApplyTransaction(bc vm.ChainContext, author *entity.Address, gp *transition.GasPool, statedb *operationdb.OperationDB, header *block.Header, tx *block.Transaction, usedGas *uint64, cfg vm.Config) (*block.Receipt, error) {
	msg, err := tx.AsMessage(block.MakeSigner(header.Number), header.BaseFee)
	if err != nil {
		return nil, err
	}
	// 创建要在EVM环境中使用的新配置
	blockContext := vm.NewOVMBlockContext(header, bc, author)
	vmenv := vm.NewOVM(blockContext, vm.TxContext{}, statedb, cfg)
	return applyTransaction(msg, gp, statedb, header.Number, header.Hash(), tx, usedGas, vmenv)
}
