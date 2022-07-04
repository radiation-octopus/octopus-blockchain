package block

import "math/big"

// 返回x或y中的较小值。
func BigMin(x, y *big.Int) *big.Int {
	if x.Cmp(y) > 0 {
		return y
	}
	return x
}
