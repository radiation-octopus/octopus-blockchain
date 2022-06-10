package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/crypto/secp256k1"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
)

//Ecrecover返回创建给定签名的未压缩公钥。
func Ecrecover(hash, sig []byte) ([]byte, error) {
	return nil, nil
}

// Sign计算ECDSA签名。
//此函数容易受到选定的明文攻击，这些攻击可能会泄漏有关用于签名的私钥的信息。
//呼叫者必须意识到对手无法选择给定摘要。常见的解决方案是在计算签名之前对任何输入进行散列。
//生成的签名采用[R | | S | V]格式，其中V为0或1。
func SignECDSA(digestHash []byte, prv *ecdsa.PrivateKey) (sig []byte, err error) {
	if len(digestHash) != DigestLength {
		return nil, fmt.Errorf("hash is required to be exactly %d bytes (%d)", DigestLength, len(digestHash))
	}
	seckey := operationutils.PaddedBigBytes(prv.D, prv.Params().BitSize/8)
	defer zeroBytes(seckey)
	return secp256k1.Sign(digestHash, seckey)
}

//S256返回secp256k1曲线的实例。
func S256() elliptic.Curve {
	return secp256k1.S256()
}
