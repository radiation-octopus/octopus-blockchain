package entity

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity/hexutil"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus/utils"
	"golang.org/x/crypto/sha3"
	"math/big"
	"reflect"
	"strings"
)

//定义hash和地址长度byte
const (
	// hash长度
	HashLength = 32
	// 地址值长度
	AddressLength = 20
)

var (
	hashT    = reflect.TypeOf(Hash{})
	addressT = reflect.TypeOf(Address{})
)

//定义hash字节类型
type Hash [HashLength]byte

// UnmarshalText以十六进制语法解析哈希。
func (h *Hash) UnmarshalText(input []byte) error {
	return hexutil.UnmarshalFixedText("Hash", input, h[:])
}

// String实现了stringer接口，在完全登录到文件时，记录器也会使用它。
func (h Hash) String() string {
	return h.Hex()
}

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

// BigToHash将b的字节表示形式设置为哈希。如果b大于len（h），b将从左侧裁剪。
func BigToHash(b *big.Int) Hash { return BytesToHash(b.Bytes()) }

// BigToAddress返回字节值为b的地址。如果b大于len（h），则从左侧裁剪b。
func BigToAddress(b *big.Int) Address { return BytesToAddress(b.Bytes()) }

//HexToAddress返回字节值为s的地址。如果s大于len（h），则s将从左侧裁剪。
func HexToAddress(s string) Address { return BytesToAddress(operationutils.FromHex(s)) }

// HexToHash将s的字节表示形式设置为哈希。如果b大于len（h），b将从左侧裁剪。
func HexToHash(s string) Hash { return BytesToHash(operationutils.FromHex(s)) }

// Bytes获取基础哈希的字节表示形式。
func (h Hash) Bytes() []byte { return h[:] }

// 大将哈希转换为大整数。
func (h Hash) Big() *big.Int { return new(big.Int).SetBytes(h[:]) }

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

//IsHexAddress验证字符串是否可以表示有效的十六进制编码以太坊地址。
func IsHexAddress(s string) bool {
	if has0xPrefix(s) {
		s = s[2:]
	}
	return len(s) == 2*AddressLength && isHex(s)
}

// MixedcaseAddress保留原始字符串，该字符串可能正确校验和，也可能不正确校验和
type MixedcaseAddress struct {
	addr     Address
	original string
}

//NewMixedcaseAddress构造函数（主要用于测试）
func NewMixedcaseAddress(addr Address) MixedcaseAddress {
	return MixedcaseAddress{addr: addr, original: addr.Hex()}
}

// NewMixedcaseAddressFromString is mainly meant for unit-testing
func NewMixedcaseAddressFromString(hexaddr string) (*MixedcaseAddress, error) {
	if !IsHexAddress(hexaddr) {
		return nil, errors.New("invalid address")
	}
	a := FromHex(hexaddr)
	return &MixedcaseAddress{addr: BytesToAddress(a), original: hexaddr}, nil
}

// MarshalJSON解析MixedcaseAddress
func (ma *MixedcaseAddress) UnmarshalJSON(input []byte) error {
	if err := hexutil.UnmarshalFixedJSON(addressT, input, ma.addr[:]); err != nil {
		return err
	}
	return json.Unmarshal(input, &ma.original)
}

// MarshalJSON marshals the original value
func (ma *MixedcaseAddress) MarshalJSON() ([]byte, error) {
	if strings.HasPrefix(ma.original, "0x") || strings.HasPrefix(ma.original, "0X") {
		return json.Marshal(fmt.Sprintf("0x%s", ma.original[2:]))
	}
	return json.Marshal(fmt.Sprintf("0x%s", ma.original))
}

// Address returns the address
func (ma *MixedcaseAddress) Address() Address {
	return ma.addr
}

// String implements fmt.Stringer
func (ma *MixedcaseAddress) String() string {
	if ma.ValidChecksum() {
		return fmt.Sprintf("%s [chksum ok]", ma.original)
	}
	return fmt.Sprintf("%s [chksum INVALID]", ma.original)
}

// ValidChecksum returns true if the address has valid checksum
func (ma *MixedcaseAddress) ValidChecksum() bool {
	return ma.original == ma.addr.Hex()
}

// Original returns the mixed-case input string
func (ma *MixedcaseAddress) Original() string {
	return ma.original
}

// UnprefixedAddress allows marshaling an Address without 0x prefix.
type UnprefixedAddress Address
