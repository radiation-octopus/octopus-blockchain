package operationutils

import "math/big"

var (
	Big0 = big.NewInt(0)
	Big1 = big.NewInt(1)
)

const (
	// big中的位数。单词
	wordBits = 32 << (uint64(^big.Word(0)) >> 63)
	// 大文件中的字节数。单词
	wordBytes = wordBits / 8
)

// PaddedBigBytes将大整数编码为大端字节片。切片的长度至少为n个字节。
func PaddedBigBytes(bigint *big.Int, n int) []byte {
	if bigint.BitLen()/8 >= n {
		return bigint.Bytes()
	}
	ret := make([]byte, n)
	ReadBits(bigint, ret)
	return ret
}

//ReadBits将bigint的绝对值编码为big-endian字节。呼叫者必须确保buf有足够的空间。如果buf太短，结果将不完整。
func ReadBits(bigint *big.Int, buf []byte) {
	i := len(buf)
	for _, d := range bigint.Bits() {
		for j := 0; j < wordBytes && i > 0; j++ {
			i--
			buf[i] = byte(d)
			d >>= 8
		}
	}
}
