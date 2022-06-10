package block

import (
	"crypto/ecdsa"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"math/big"
)

type Signer interface {
	// 发件人返回交易的发件人地址。
	Sender(tx *Transaction) (entity.Address, error)

	// 返回与给定签名相对应的原始R、S、V值。
	SignatureValues(tx *Transaction, sig []byte) (r, s, v *big.Int, err error)
	ChainID() *big.Int

	// 返回“签名哈希”，即由私钥签名的事务哈希。此哈希不能唯一标识事务。
	Hash(tx *Transaction) entity.Hash

	// 如果给定的签名者与接收方相同，则Equal返回true。
	Equal(Signer) bool
}

//根据给定的链配置和块编号返回签名者。
func MakeSigner(blockNumber *big.Int) Signer {
	var signer Signer

	return signer
}

func Sender(signer Signer, tx *Transaction) (entity.Address, error) {
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
		return entity.Address{}, err
	}
	//tx.from.Store(sigCache{signer: signer, from: addr})
	return addr, nil
}

// LatestSigner返回给定链配置可用的“最有权限”签名者。
//具体而言，当EIP-155重播保护和EIP-2930访问列表事务各自的分叉计划在链配置中的任何块号上发生时，这将支持EIP-155重播保护和EIP-2930访问列表事务。
//在当前块号未知的事务处理代码中使用此选项。如果当前块号可用，请改用MakeSigner。
func LatestSigner() Signer {
	//if config.ChainID != nil {
	//	if config.LondonBlock != nil {
	//		return NewLondonSigner(config.ChainID)
	//	}
	//	if config.BerlinBlock != nil {
	//		return NewEIP2930Signer(config.ChainID)
	//	}
	//	if config.EIP155Block != nil {
	//		return NewEIP155Signer(config.ChainID)
	//	}
	//}
	return HomesteadSigner{}
}

// HomesteadTransaction使用homestead规则实现TransactionInterface。
type HomesteadSigner struct{ FrontierSigner }

func (s HomesteadSigner) Hash(tx *Transaction) entity.Hash {
	return tx.Hash()
}

func (s HomesteadSigner) ChainID() *big.Int {
	return nil
}
func (s HomesteadSigner) Equal(s2 Signer) bool {
	_, ok := s2.(HomesteadSigner)
	return ok
}
func (hs HomesteadSigner) Sender(tx *Transaction) (entity.Address, error) {
	if tx.Type() != LegacyTxType {
		return entity.Address{}, ErrTxTypeNotSupported
	}
	v, r, s := tx.RawSignatureValues()
	return recoverPlain(hs.Hash(tx), r, s, v, true)
}

// SignatureValue返回签名值。此签名需要采用[R | | S | V]格式，其中V为0或1。
func (hs HomesteadSigner) SignatureValues(tx *Transaction, sig []byte) (r, s, v *big.Int, err error) {
	return hs.FrontierSigner.SignatureValues(tx, sig)
}

type FrontierSigner struct{}

func (s FrontierSigner) ChainID() *big.Int {
	return nil
}

func (s FrontierSigner) Equal(s2 Signer) bool {
	_, ok := s2.(FrontierSigner)
	return ok
}

func (fs FrontierSigner) Sender(tx *Transaction) (entity.Address, error) {
	if tx.Type() != LegacyTxType {
		return entity.Address{}, ErrTxTypeNotSupported
	}
	v, r, s := tx.RawSignatureValues()
	return recoverPlain(fs.Hash(tx), r, s, v, false)
}

// SignatureValue返回签名值。此签名需要采用[R | | S | V]格式，其中V为0或1。
func (fs FrontierSigner) SignatureValues(tx *Transaction, sig []byte) (r, s, v *big.Int, err error) {
	if tx.Type() != LegacyTxType {
		return nil, nil, nil, ErrTxTypeNotSupported
	}
	r, s, v = decodeSignature(sig)
	return r, s, v, nil
}

// 散列返回要由发送方签名的散列。它不能唯一标识事务。
func (fs FrontierSigner) Hash(tx *Transaction) entity.Hash {
	return crypto.RlpHash([]interface{}{
		tx.Nonce(),
		tx.GasPrice(),
		tx.Gas(),
		tx.To(),
		tx.Value(),
		tx.Data(),
	})
}

// SignTx使用给定的签名者和私钥对事务进行签名。
func SignTx(tx *Transaction, s Signer, prv *ecdsa.PrivateKey) (*Transaction, error) {
	h := s.Hash(tx)
	sig, err := crypto.SignECDSA(h[:], prv)
	if err != nil {
		return nil, err
	}
	return tx.WithSignature(s, sig)
}

func decodeSignature(sig []byte) (r, s, v *big.Int) {
	if len(sig) != crypto.SignatureLength {
		panic(fmt.Sprintf("wrong size for signature: got %d, want %d", len(sig), crypto.SignatureLength))
	}
	r = new(big.Int).SetBytes(sig[:32])
	s = new(big.Int).SetBytes(sig[32:64])
	v = new(big.Int).SetBytes([]byte{sig[64] + 27})
	return r, s, v
}

func recoverPlain(sighash entity.Hash, R, S, Vb *big.Int, homestead bool) (entity.Address, error) {
	if Vb.BitLen() > 8 {
		return entity.Address{}, ErrInvalidSig
	}
	V := byte(Vb.Uint64() - 27)
	if !crypto.ValidateSignatureValues(V, R, S, homestead) {
		return entity.Address{}, ErrInvalidSig
	}
	// 以未压缩格式对签名进行编码
	r, s := R.Bytes(), S.Bytes()
	sig := make([]byte, crypto.SignatureLength)
	copy(sig[32-len(r):32], r)
	copy(sig[64-len(s):64], s)
	sig[64] = V
	// 从签名中恢复公钥
	//pub, err := crypto.Ecrecover(sighash[:], sig)
	//if err != nil {
	//	return entity.Address{}, err
	//}
	//if len(pub) == 0 || pub[0] != 4 {
	//	return entity.Address{}, errors.New("invalid public key")
	//}
	var addr entity.Address
	//copy(addr[:], crypto.Keccak256(pub[1:])[12:])
	return addr, nil
}
