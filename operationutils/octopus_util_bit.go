package operationutils

import (
	"encoding/hex"
	"math/big"
	"runtime"
	"unsafe"
)

var (
	ExpDiffPeriod = big.NewInt(100000)
	Big0          = big.NewInt(0)
	Big1          = big.NewInt(1)
	Big2          = big.NewInt(2)
	Big8          = big.NewInt(8)
	Big9          = big.NewInt(9)
	Big10         = big.NewInt(10)
	Big32         = big.NewInt(32)
	Big256        = big.NewInt(256)
	BigMinus99    = big.NewInt(-99)
)

const (
	wordSize = int(unsafe.Sizeof(uintptr(0)))

	supportsUnaligned = runtime.GOARCH == "386" || runtime.GOARCH == "amd64" || runtime.GOARCH == "ppc64" || runtime.GOARCH == "ppc64le" || runtime.GOARCH == "s390x"
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

// FromHex返回由十六进制字符串s表示的字节。s可以前缀为“0x”。
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

// RightPadBytes零焊盘向右切片，长度l。
func RightPadBytes(slice []byte, l int) []byte {
	if l <= len(slice) {
		return slice
	}

	padded := make([]byte, l)
	copy(padded, slice)

	return padded
}

// LeftPadBytes零焊盘向左切片，长度l。
func LeftPadBytes(slice []byte, l int) []byte {
	if l <= len(slice) {
		return slice
	}

	padded := make([]byte, l)
	copy(padded[l-len(slice):], slice)

	return padded
}

// 作为带有0x前缀的JSON字符串的大封送/解封送。零值封送为“0x0”。
//此时不支持负整数。尝试封送它们将返回错误。大于256bits的值将被Unmarshal拒绝，但将被封送而不会出错。
type Big big.Int

// Uint64封送/解封送为带有0x前缀的JSON字符串。零值封送为“0x0”。
type Uint64 uint64

//XORBytes对a和b中的字节进行xor运算。假定目标有足够的空间。返回xor'd的字节数。
func XORBytes(dst, a, b []byte) int {
	if supportsUnaligned {
		return fastXORBytes(dst, a, b)
	}
	return safeXORBytes(dst, a, b)
}

//fastXORBytes批量执行XOR。它只适用于支持未对齐读/写的体系结构。
func fastXORBytes(dst, a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	w := n / wordSize
	if w > 0 {
		dw := *(*[]uintptr)(unsafe.Pointer(&dst))
		aw := *(*[]uintptr)(unsafe.Pointer(&a))
		bw := *(*[]uintptr)(unsafe.Pointer(&b))
		for i := 0; i < w; i++ {
			dw[i] = aw[i] ^ bw[i]
		}
	}
	for i := n - n%wordSize; i < n; i++ {
		dst[i] = a[i] ^ b[i]
	}
	return n
}

// 安全xorbytes逐个xor。它适用于所有体系结构，无论是否支持未对齐的读/写。
func safeXORBytes(dst, a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		dst[i] = a[i] ^ b[i]
	}
	return n
}

// BigMax返回x或y中的较大值。
func BigMax(x, y *big.Int) *big.Int {
	if x.Cmp(y) < 0 {
		return y
	}
	return x
}
