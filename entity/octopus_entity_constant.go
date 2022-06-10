package entity

import "github.com/radiation-octopus/octopus/utils"

//定义hash和地址长度byte
const (
	// hash长度
	HashLength = 32
	// 地址值长度
	AddressLength = 20
	//合同创建 gas
	TxGasContractCreation uint64 = 53000
	//交易 gas
	TxGas uint64 = 21000
	//交易数据非零gas限制
	TxDataNonZeroGasFrontier uint64 = 68
	//eip2028类型gas限制
	TxDataNonZeroGasEIP2028 uint64 = 16

	TxDataZeroGas uint64 = 4
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

// SetBytes sets the hash to the value of b.
// If b is larger than len(h), b will be cropped from the left.
func (h *Hash) SetBytes(b []byte) {
	if len(b) > len(h) {
		b = b[len(b)-HashLength:]
	}

	copy(h[HashLength-len(b):], b)
}
