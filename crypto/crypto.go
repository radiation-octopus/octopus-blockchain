package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	operationUtils "github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
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

// Keccak512计算并返回输入数据的Keccak512哈希。
func Keccak512(data ...[]byte) []byte {
	d := sha3.NewLegacyKeccak512()
	for _, b := range data {
		d.Write(b)
	}
	return d.Sum(nil)
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

// CreateAddress在给定字节和nonce的情况下创建以太坊地址
func CreateAddress(b entity.Address, nonce uint64) entity.Address {
	data, _ := rlp.EncodeToBytes([]interface{}{b, nonce})
	return entity.BytesToAddress(Keccak256(data)[12:])
}

func zeroBytes(bytes []byte) {
	for i := range bytes {
		bytes[i] = 0
	}
}

// ToecdsanSafe盲目地将二进制blob转换为私钥。除非您确信输入有效，并且希望避免由于错误的原点编码（0个前缀被截断）而导致的错误，否则几乎不应该使用它。
func ToECDSAUnsafe(d []byte) *ecdsa.PrivateKey {
	priv, _ := toECDSA(d, false)
	return priv
}

//toECDSA使用给定的D值创建私钥。strict参数控制键的长度是应强制为曲线大小，还是还可以接受传统编码（0个前缀）。
func toECDSA(d []byte, strict bool) (*ecdsa.PrivateKey, error) {
	priv := new(ecdsa.PrivateKey)
	priv.PublicKey.Curve = S256()
	if strict && 8*len(d) != priv.Params().BitSize {
		return nil, fmt.Errorf("invalid length, need %d bits", priv.Params().BitSize)
	}
	priv.D = new(big.Int).SetBytes(d)

	// priv.D必须<N
	if priv.D.Cmp(secp256k1N) >= 0 {
		return nil, fmt.Errorf("invalid private key, >=N")
	}
	// priv.D不得为零或负。
	if priv.D.Sign() <= 0 {
		return nil, fmt.Errorf("invalid private key, zero or negative")
	}

	priv.PublicKey.X, priv.PublicKey.Y = priv.PublicKey.Curve.ScalarBaseMult(d)
	if priv.PublicKey.X == nil {
		return nil, errors.New("invalid private key")
	}
	return priv, nil
}

func PubkeyToAddress(p ecdsa.PublicKey) entity.Address {
	pubBytes := FromECDSAPub(&p)
	return entity.BytesToAddress(Keccak256(pubBytes[1:])[12:])
}

func FromECDSAPub(pub *ecdsa.PublicKey) []byte {
	if pub == nil || pub.X == nil || pub.Y == nil {
		return nil
	}
	return elliptic.Marshal(S256(), pub.X, pub.Y)
}

// CreateAddress在给定字节和nonce的情况下创建章鱼地址
//func CreateAddress(b entity.Address, nonce uint64) entity.Address {
//	data, _ := rlp.EncodeToBytes([]interface{}{b, nonce})
//	return entity.BytesToAddress(Keccak256()[12:])
//}
