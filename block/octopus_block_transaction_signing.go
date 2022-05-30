package block

import (
	operationUtils "github.com/radiation-octopus/octopus-blockchain/operationUtils"
	"math/big"
)

type Signer interface {
	// 发件人返回交易的发件人地址。
	Sender(tx *Transaction) (operationUtils.Address, error)

	// 返回与给定签名相对应的原始R、S、V值。
	SignatureValues(tx *Transaction, sig []byte) (r, s, v *big.Int, err error)
	ChainID() *big.Int

	// 返回“签名哈希”，即由私钥签名的事务哈希。此哈希不能唯一标识事务。
	Hash(tx *Transaction) operationUtils.Hash

	// 如果给定的签名者与接收方相同，则Equal返回true。
	Equal(Signer) bool
}

//根据给定的链配置和块编号返回签名者。
func MakeSigner(blockNumber *big.Int) Signer {
	var signer Signer

	return signer
}

func Sender(signer Signer, tx *Transaction) (operationUtils.Address, error) {
	if sc := tx.from.Load(); sc != nil {
		//sigCache := sc.(sigCache)
		//// If the signer used to derive from in a previous
		//// call is not the same as used current, invalidate
		//// the cache.
		//if sigCache.signer.Equal(signer) {
		//	return sigCache.from, nil
		//}
	}

	addr, err := signer.Sender(tx)
	if err != nil {
		return operationUtils.Address{}, err
	}
	//tx.from.Store(sigCache{signer: signer, from: addr})
	return addr, nil
}
