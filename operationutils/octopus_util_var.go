package operationutils

import (
	"encoding/hex"
	"math/big"
)

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

// 字节封送/解封为带有0x前缀的JSON字符串。空切片封送为“0x”。
type Bytes []byte

// FromHex returns the bytes represented by the hexadecimal string s.
// s may be prefixed with "0x".
func FromHex(s string) []byte {
	if has0xPrefix(s) {
		s = s[2:]
	}
	if len(s)%2 == 1 {
		s = "0" + s
	}
	return Hex2Bytes(s)
}

// has0xPrefix验证以“0x”或“0x”开头的str。
func has0xPrefix(str string) bool {
	return len(str) >= 2 && str[0] == '0' && (str[1] == 'x' || str[1] == 'X')
}

// Hex2Bytes返回由十六进制字符串str表示的字节。
func Hex2Bytes(str string) []byte {
	h, _ := hex.DecodeString(str)
	return h
}

// 作为带有0x前缀的JSON字符串的大封送/解封送。零值封送为“0x0”。
//此时不支持负整数。尝试封送它们将返回错误。大于256bits的值将被Unmarshal拒绝，但将被封送而不会出错。
type Big big.Int

// Uint64封送/解封送为带有0x前缀的JSON字符串。零值封送为“0x0”。
type Uint64 uint64
