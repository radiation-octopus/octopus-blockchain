package misc

import (
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity"
)

// VerifyGaslimit根据相对于主gas限值的增加/减少来验证集管gas限值。
func VerifyGaslimit(parentGasLimit, headerGasLimit uint64) error {
	// 验证gas限值是否保持在允许范围内
	diff := int64(parentGasLimit) - int64(headerGasLimit)
	if diff < 0 {
		diff *= -1
	}
	limit := parentGasLimit / entity.GasLimitBoundDivisor
	if uint64(diff) >= limit {
		return fmt.Errorf("invalid gas limit: have %d, want %d +-= %d", headerGasLimit, parentGasLimit, limit-1)
	}
	if headerGasLimit < entity.MinGasLimit {
		return errors.New("invalid gas limit below 5000")
	}
	return nil
}
