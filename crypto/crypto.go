package crypto

import (
	"crypto/ecdsa"
	"crypto/rand"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	operationUtils "github.com/radiation-octopus/octopus-blockchain/operationutils"
	"golang.org/x/crypto/sha3"
	"hash"
	"math/big"
)

//SignatureLength表示携带具有恢复id的签名所需的字节长度。
const SignatureLength = 64 + 1 // 64字节ECDSA签名+1字节恢复id

// DigestLength设置签名摘要的精确长度
const DigestLength = 32

var (
	secp256k1N, _  = new(big.Int).SetString("fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141", 16)
	secp256k1halfN = new(big.Int).Div(secp256k1N, big.NewInt(2))
)

//KeccakState包裹sha3。状态除了通常的散列方法外，它还支持读取以从散列状态获取可变数量的数据。Read比Sum快，因为它不复制内部状态，但也修改内部状态。
type KeccakState interface {
	hash.Hash
	Read([]byte) (int, error)
}

// NewKeccakState创建新的KeccakState
func NewKeccakState() KeccakState {
	return sha3.NewLegacyKeccak256().(KeccakState)
}

// Keccak256计算并返回输入数据的Keccak256哈希。
func Keccak256(data ...[]byte) []byte {
	b := make([]byte, 32)
	d := NewKeccakState()
	for _, b := range data {
		d.Write(b)
	}
	d.Read(b)
	return b
}

// Keccak256哈希计算并返回输入数据的Keccak256哈希，将其转换为内部哈希数据结构。
func Keccak256Hash(data ...[]byte) (h entity.Hash) {
	d := NewKeccakState()
	for _, b := range data {
		d.Write(b)
	}
	d.Read(h[:])
	return h
}

// GenerateKey生成新的私钥。
func GenerateKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(S256(), rand.Reader)
}

// ValidateSignatureValues验证签名值对于给定的链规则是否有效。假设v值为0或1。
func ValidateSignatureValues(v byte, r, s *big.Int, homestead bool) bool {
	if r.Cmp(operationUtils.Big1) < 0 || s.Cmp(operationUtils.Big1) < 0 {
		return false
	}
	// 拒绝s值的上限（ECDSA延展性），请参阅secp256k1/libsecp256k1/include/secp256k1中的讨论。h类
	if homestead && s.Cmp(secp256k1halfN) > 0 {
		return false
	}
	// 边疆：允许s处于全N范围
	return r.Cmp(secp256k1N) < 0 && s.Cmp(secp256k1N) < 0 && (v == 0 || v == 1)
}

func zeroBytes(bytes []byte) {
	for i := range bytes {
		bytes[i] = 0
	}
}
