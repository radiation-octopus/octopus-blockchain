package misc

import (
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"math/big"
)

// VerifyEip1559Header验证EIP-1559中更改的一些标头属性，
//-gas极限检查
//-基本费用检查
func VerifyEip1559Header(config *entity.ChainConfig, parent, header *block2.Header) error {
	// 验证气体限值是否保持在允许范围内
	parentGasLimit := parent.GasLimit
	if !config.IsLondon(parent.Number) {
		parentGasLimit = parent.GasLimit * entity.ElasticityMultiplier
	}
	if err := VerifyGaslimit(parentGasLimit, header.GasLimit); err != nil {
		return err
	}
	// 验证标头的格式是否正确
	if header.BaseFee == nil {
		return fmt.Errorf("header is missing baseFee")
	}
	// 根据父标题验证baseFee是否正确。
	expectedBaseFee := CalcBaseFee(config, parent)
	if header.BaseFee.Cmp(expectedBaseFee) != 0 {
		return fmt.Errorf("invalid baseFee: have %s, want %s, parentBaseFee %s, parentGasUsed %d",
			header.BaseFee, expectedBaseFee, parent.BaseFee, parent.GasUsed)
	}
	return nil
}

//CalcBaseFee计算标头的基本费用。
func CalcBaseFee(config *entity.ChainConfig, parent *block2.Header) *big.Int {
	// 如果当前块是第一个EIP-1559块，请返回InitialBaseFee。
	if !config.IsLondon(parent.Number) {
		return new(big.Int).SetUint64(entity.InitialBaseFee)
	}

	parentGasTarget := parent.GasLimit / entity.ElasticityMultiplier
	// 如果使用的父项GASUED与目标项相同，则基本费用保持不变。
	if parent.GasUsed == parentGasTarget {
		return new(big.Int).Set(parent.BaseFee)
	}

	var (
		num   = new(big.Int)
		denom = new(big.Int)
	)

	if parent.GasUsed > parentGasTarget {
		// 如果母区块使用的gas超过其目标，则基准费用应增加。最大值（1，parentBaseFee*GausedDelta/parentGasTarget/baseFeeChangeDenominator）
		num.SetUint64(parent.GasUsed - parentGasTarget)
		num.Mul(num, parent.BaseFee)
		num.Div(num, denom.SetUint64(parentGasTarget))
		num.Div(num, denom.SetUint64(entity.BaseFeeChangeDenominator))
		baseFeeDelta := operationutils.BigMax(num, operationutils.Big1)

		return num.Add(parent.BaseFee, baseFeeDelta)
	} else {
		// 否则，如果母区块使用的gas少于其目标，则基准费用应降低。最大值（0，parentBaseFee*GausedDelta/parentGasTarget/baseFeeChangeDenominator）
		num.SetUint64(parentGasTarget - parent.GasUsed)
		num.Mul(num, parent.BaseFee)
		num.Div(num, denom.SetUint64(parentGasTarget))
		num.Div(num, denom.SetUint64(entity.BaseFeeChangeDenominator))
		baseFee := num.Sub(parent.BaseFee, num)

		return operationutils.BigMax(baseFee, operationutils.Big0)
	}
}
