package operationutils

import "math/big"

// EncodeBig将bigint编码为前缀为0x的十六进制字符串。
func EncodeBig(bigint *big.Int) string {
	if sign := bigint.Sign(); sign == 0 {
		return "0x0"
	} else if sign > 0 {
		return "0x" + bigint.Text(16)
	} else {
		return "-0x" + bigint.Text(16)[1:]
	}
}
