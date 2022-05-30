package blockchain

import (
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/operationUtils"
)

type BlockValidator struct {
	//config *params.chainConfig 	//链配置
	bc     *BlockChain      //标准连
	engine consensus.Engine //共识引擎
}

//
func NewBlockValidator(blockchain *BlockChain, engine consensus.Engine) *BlockValidator {
	validator := &BlockValidator{
		bc:     blockchain,
		engine: engine,
	}
	return validator
}

//定义验证器处理接口
type Validator interface {
	//验证给定块内容
	validateBody(block *block.Block) error
}

//区块验证具体实现
func (v *BlockValidator) validateBody(block *block.Block) error {

	return nil
}

//CalcGasLimit计算父块之后的下一个块的gas极限。其目的是保持基线gas接近所提供的目标，如果基线gas较低，则向目标方向增加。
func CalcGasLimit(parentGasLimit, desiredLimit uint64) uint64 {
	delta := parentGasLimit/operationUtils.GasLimitBoundDivisor - 1
	limit := parentGasLimit
	if desiredLimit < operationUtils.MinGasLimit {
		desiredLimit = operationUtils.MinGasLimit
	}
	// 如果我们超出了允许的gas范围，我们会努力向他们靠近
	if limit < desiredLimit {
		limit = parentGasLimit + delta
		if limit > desiredLimit {
			limit = desiredLimit
		}
		return limit
	}
	if limit > desiredLimit {
		limit = parentGasLimit - delta
		if limit < desiredLimit {
			limit = desiredLimit
		}
	}
	return limit
}
