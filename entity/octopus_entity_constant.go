package entity

import (
	"encoding/hex"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus/utils"
	"golang.org/x/crypto/sha3"
	"math/big"
)

//定义hash和地址长度byte
const (
	// hash长度
	HashLength = 32
	// 地址值长度
	AddressLength = 20
)

//定义hash字节类型
type Hash [HashLength]byte

type HashStruct struct {
	Hash Hash
}

func (hs HashStruct) getHash() Hash {
	return hs.Hash
}

//定义地址字节类型
type Address [AddressLength]byte

// Bytes获取基础地址的字符串表示形式。
func (a Address) Bytes() []byte { return a[:] }

func (a *Address) SetBytes(b []byte) {
	if len(b) > len(a) {
		b = b[len(b)-AddressLength:]
	}
	copy(a[AddressLength-len(b):], b)
}

// BigToAddress返回字节值为b的地址。如果b大于len（h），则从左侧裁剪b。
func BigToAddress(b *big.Int) Address { return BytesToAddress(b.Bytes()) }

//HexToAddress返回字节值为s的地址。如果s大于len（h），则s将从左侧裁剪。
func HexToAddress(s string) Address { return BytesToAddress(operationutils.FromHex(s)) }

// Bytes获取基础哈希的字节表示形式。
func (h Hash) Bytes() []byte { return h[:] }

// String 实现 fmt.Stringer.
func (a Address) String() string {
	return a.Hex()
}

//Hex返回地址的符合EIP55的十六进制字符串表示形式。
func (a Address) Hex() string {
	return string(a.checksumHex())
}

func (a *Address) checksumHex() []byte {
	buf := a.hex()

	// 计算校验和
	sha := sha3.NewLegacyKeccak256()
	sha.Write(buf[2:])
	hash := sha.Sum(nil)
	for i := 2; i < len(buf); i++ {
		hashByte := hash[(i-2)/2]
		if i%2 == 0 {
			hashByte = hashByte >> 4
		} else {
			hashByte &= 0xf
		}
		if buf[i] > '9' && hashByte > 7 {
			buf[i] -= 32
		}
	}
	return buf[:]
}

func (a Address) hex() []byte {
	var buf [len(a)*2 + 2]byte
	copy(buf[:2], "0x")
	hex.Encode(buf[2:], a[:])
	return buf[:]
}

type Bytes []byte

// 十六进制将哈希转换为十六进制字符串。
func (h Hash) Hex() string { return utils.Encode(h[:]) }

//BytesToAddress返回值为b的地址。
func BytesToAddress(b []byte) Address {
	var a Address
	a.SetBytes(b)
	return a
}

func BytesToHash(b []byte) Hash {
	var hash Hash
	hash.SetBytes(b)
	return hash
}

//SetBytes将哈希值设置为b。如果b大于len（h），则b将从左侧裁剪。
func (h *Hash) SetBytes(b []byte) {
	if len(b) > len(h) {
		b = b[len(b)-HashLength:]
	}

	copy(h[HashLength-len(b):], b)
}
