package vm

import "github.com/holiman/uint256"

// Gas costs
const (
	GasQuickStep   uint64 = 2
	GasFastestStep uint64 = 3
	GasFastStep    uint64 = 5
	GasMidStep     uint64 = 8
	GasSlowStep    uint64 = 10
	GasExtStep     uint64 = 20
)

// callGas返回通话的实际气体成本。
//在宅地价格变化期间，天然气成本发生了变化。
//作为EIP 150（TangerineWhistle）的一部分，返回的气体为气基*63/64。
func callGas(isEip150 bool, availableGas, base uint64, callCost *uint256.Int) (uint64, error) {
	if isEip150 {
		availableGas = availableGas - base
		gas := availableGas - availableGas/64
		// 如果位长度超过64位，我们知道EIP150新计算的“gas”小于请求量。
		//因此，我们返回新气体，而不是返回错误。
		if !callCost.IsUint64() || gas < callCost.Uint64() {
			return gas, nil
		}
	}
	if !callCost.IsUint64() {
		return 0, ErrGasUintOverflow
	}

	return callCost.Uint64(), nil
}
