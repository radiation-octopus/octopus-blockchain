package block

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"math/big"
)

var ErrInvalidChainId = errors.New("invalid chain id for signer")

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

// sigCache用于缓存派生发件人，并包含用于派生发件人的签名者。
type sigCache struct {
	signer Signer
	from   entity.Address
}

//根据给定的链配置和块编号返回签名者。
func MakeSigner(blockNumber *big.Int) Signer {
	var signer Signer
	//switch {
	//case config.IsLondon(blockNumber):
	//	signer = NewLondonSigner(config.ChainID)
	//case config.IsBerlin(blockNumber):
	//	signer = NewEIP2930Signer(config.ChainID)
	//case config.IsEIP155(blockNumber):
	//	signer = NewEIP155Signer(config.ChainID)
	//case config.IsHomestead(blockNumber):
	//	signer = HomesteadSigner{}
	//default:
	//	signer = FrontierSigner{}
	//}
	signer = NewLondonSigner(big.NewInt(666))
	return signer
}

func Sender(signer Signer, tx *Transaction) (entity.Address, error) {
	if sc := tx.from.Load(); sc != nil {
		sigCache := sc.(sigCache)
		// 如果以前调用中用于派生的签名者与当前使用的签名者不同，请使缓存无效。
		if sigCache.signer.Equal(signer) {
			return sigCache.from, nil
		}
	}

	addr, err := signer.Sender(tx)
	if err != nil {
		return entity.Address{}, err
	}
	tx.from.Store(sigCache{signer: signer, from: addr})
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
	return NewLondonSigner(big.NewInt(666))
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
	return RlpHash([]interface{}{
		tx.Nonce(),
		tx.GasPrice(),
		tx.Gas(),
		tx.To(),
		tx.Value(),
		tx.Data(),
	})
}

//EIP155Signer使用EIP-155规则实现签名者。这接受受重播保护的交易以及未受保护的宅地交易。
type EIP155Signer struct {
	chainId, chainIdMul *big.Int
}

func NewEIP155Signer(chainId *big.Int) EIP155Signer {
	if chainId == nil {
		chainId = new(big.Int)
	}
	return EIP155Signer{
		chainId:    chainId,
		chainIdMul: new(big.Int).Mul(chainId, big.NewInt(2)),
	}
}

// SignatureValue返回签名值。此签名需要采用[R | | S | V]格式，其中V为0或1。
func (s EIP155Signer) SignatureValues(tx *Transaction, sig []byte) (R, S, V *big.Int, err error) {
	if tx.Type() != LegacyTxType {
		return nil, nil, nil, ErrTxTypeNotSupported
	}
	R, S, V = decodeSignature(sig)
	if s.chainId.Sign() != 0 {
		V = big.NewInt(int64(sig[64] + 35))
		V.Add(V, s.chainIdMul)
	}
	return R, S, V, nil
}

type eip2930Signer struct{ EIP155Signer }

var big8 = big.NewInt(8)

func (s eip2930Signer) Sender(tx *Transaction) (entity.Address, error) {
	V, R, S := tx.RawSignatureValues()
	switch tx.Type() {
	case LegacyTxType:
		if !tx.Protected() {
			return HomesteadSigner{}.Sender(tx)
		}
		V = new(big.Int).Sub(V, s.chainIdMul)
		V.Sub(V, big8)
	case AccessListTxType:
		//AL TX定义为使用0和1作为其恢复id，添加27相当于未受保护的宅地签名。
		V = new(big.Int).Add(V, big.NewInt(27))
	default:
		return entity.Address{}, ErrTxTypeNotSupported
	}
	if tx.ChainId().Cmp(s.chainId) != 0 {
		return entity.Address{}, ErrInvalidChainId
	}
	return recoverPlain(s.Hash(tx), R, S, V, true)
}

// Hash returns the hash to be signed by the sender.
// It does not uniquely identify the transaction.
func (s eip2930Signer) Hash(tx *Transaction) entity.Hash {
	switch tx.Type() {
	case LegacyTxType:
		return RlpHash([]interface{}{
			tx.Nonce(),
			tx.GasPrice(),
			tx.Gas(),
			tx.To(),
			tx.Value(),
			tx.Data(),
			s.chainId, uint(0), uint(0),
		})
	case AccessListTxType:
		return PrefixedRlpHash(
			tx.Type(),
			[]interface{}{
				s.chainId,
				tx.Nonce(),
				tx.GasPrice(),
				tx.Gas(),
				tx.To(),
				tx.Value(),
				tx.Data(),
				//tx.AccessList(),
			})
	default:
		// 这种情况不应该发生，但如果有人通过RPC发送错误的json结构，
		//可能更谨慎的做法是返回一个空哈希，而不是通过恐慌性恐慌杀死节点（“不支持的事务类型：%d”，tx.typ）
		return entity.Hash{}
	}
}

func (s eip2930Signer) SignatureValues(tx *Transaction, sig []byte) (R, S, V *big.Int, err error) {
	switch txdata := tx.inner.(type) {
	case *LegacyTx:
		fmt.Println(txdata)
		return s.EIP155Signer.SignatureValues(tx, sig)
	//case *AccessListTx:
	//	//检查tx的链ID是否与签名者匹配。这里我们也接受ID零，因为它表示在tx中没有指定链ID。
	//	if txdata.ChainID.Sign() != 0 && txdata.ChainID.Cmp(s.chainId) != 0 {
	//		return nil, nil, nil, ErrInvalidChainId
	//	}
	//	R, S, _ = decodeSignature(sig)
	//	V = big.NewInt(int64(sig[64]))
	default:
		return nil, nil, nil, ErrTxTypeNotSupported
	}
	return R, S, V, nil
}

type londonSigner struct{ eip2930Signer }

func (s londonSigner) Sender(tx *Transaction) (entity.Address, error) {
	if tx.Type() != DynamicFeeTxType {
		return s.eip2930Signer.Sender(tx)
	}
	V, R, S := tx.RawSignatureValues()
	// DynamicFee TX定义为使用0和1作为其恢复id，添加27相当于未受保护的宅地签名。
	V = new(big.Int).Add(V, big.NewInt(27))
	if tx.ChainId().Cmp(s.chainId) != 0 {
		return entity.Address{}, ErrInvalidChainId
	}
	return recoverPlain(s.Hash(tx), R, S, V, true)
}

func (l londonSigner) SignatureValues(tx *Transaction, sig []byte) (R, S, V *big.Int, err error) {
	txdata, ok := tx.inner.(*DynamicFeeTx)
	if !ok {
		return l.eip2930Signer.SignatureValues(tx, sig)
	}
	// 检查tx的链ID是否与签名者匹配。这里我们也接受ID零，因为它表示在tx中没有指定链ID。
	if txdata.ChainID.Sign() != 0 && txdata.ChainID.Cmp(l.chainId) != 0 {
		return nil, nil, nil, ErrInvalidChainId
	}
	R, S, _ = decodeSignature(sig)
	V = big.NewInt(int64(sig[64]))
	return R, S, V, nil
}

func (l londonSigner) ChainID() *big.Int {
	return l.chainId
}

// 哈希返回要由发送方签名的哈希。它不能唯一标识事务。
func (s londonSigner) Hash(tx *Transaction) entity.Hash {
	if tx.Type() != DynamicFeeTxType {
		return s.eip2930Signer.Hash(tx)
	}
	return PrefixedRlpHash(
		tx.Type(),
		[]interface{}{
			s.chainId,
			tx.Nonce(),
			tx.GasTipCap(),
			tx.GasFeeCap(),
			tx.Gas(),
			tx.To(),
			tx.Value(),
			tx.Data(),
			//tx.AccessList(),
		})
}

func (l londonSigner) Equal(signer Signer) bool {
	x, ok := signer.(londonSigner)
	return ok && x.chainId.Cmp(l.chainId) == 0
}

//NewLondonSigner,返回接受
//-EIP-1559动态费用交易
//-EIP-2930访问列表事务，
//-EIP-155重播受保护的事务，以及
//-遗留宅地交易。
func NewLondonSigner(chainId *big.Int) Signer {
	return londonSigner{eip2930Signer{NewEIP155Signer(chainId)}}
}

//LatestSignerForChainID返回可用的“最允许的”签名者。具体而言，如果chainID为非nil，这将支持EIP-155重播保护和所有实现的EIP-2718事务类型。
//在当前块号和fork配置未知的事务处理代码中使用此选项。
//如果您有一个ChainConfig，请改用LatestSigner。如果您有一个ChainConfig并知道当前的块号，请改用MakeSigner。
func LatestSignerForChainID(chainID *big.Int) Signer {
	if chainID == nil {
		return HomesteadSigner{}
	}
	return NewLondonSigner(chainID)
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
	a := Vb.BitLen()
	if a > 8 {
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
	pub, err := crypto.Ecrecover(sighash[:], sig)
	if err != nil {
		return entity.Address{}, err
	}
	if len(pub) == 0 || pub[0] != 4 {
		return entity.Address{}, errors.New("invalid public key")
	}
	var addr entity.Address
	copy(addr[:], crypto.Keccak256(pub[1:])[12:])
	return addr, nil
}

// TransactionArgs表示构造新事务或消息调用的参数。
type TransactionArgs struct {
	From                 *entity.Address        `json:"from"`
	To                   *entity.Address        `json:"to"`
	Gas                  *operationutils.Uint64 `json:"gas"`
	GasPrice             *operationutils.Big    `json:"gasPrice"`
	MaxFeePerGas         *operationutils.Big    `json:"maxFeePerGas"`
	MaxPriorityFeePerGas *operationutils.Big    `json:"maxPriorityFeePerGas"`
	Value                *operationutils.Big    `json:"value"`
	Nonce                *operationutils.Uint64 `json:"nonce"`

	// 出于向后兼容性的原因，我们接受“数据”和“输入”。“input”是一个较新的名称，客户端应首选它。
	Data  *operationutils.Bytes `json:"data"`
	Input *operationutils.Bytes `json:"input"`

	// 由AccessListTxType事务引入。AccessList*类型。AccessList `json：“AccessList，省略empty”`
	ChainID *operationutils.Big `json:"chainId,omitempty"`
}

//从检索事务发送方地址。
func (args *TransactionArgs) FromAddr() entity.Address {
	if args.From == nil {
		return entity.Address{}
	}
	return *args.From
}

//data检索事务调用数据。首选输入字段。
func (args *TransactionArgs) data() []byte {
	if args.Input != nil {
		return *args.Input
	}
	if args.Data != nil {
		return *args.Data
	}
	return nil
}

// toTransaction将参数转换为事务。这假定已调用setDefaults。
func (args *TransactionArgs) ToTransaction() *Transaction {
	var data TxData
	switch {
	case args.MaxFeePerGas != nil:
		//al := AccessList{}
		//if args.AccessList != nil {
		//	al = *args.AccessList
		//}
		data = &DynamicFeeTx{
			To:        args.To,
			ChainID:   (*big.Int)(args.ChainID),
			Nonce:     uint64(*args.Nonce),
			Gas:       uint64(*args.Gas),
			GasFeeCap: (*big.Int)(args.MaxFeePerGas),
			GasTipCap: (*big.Int)(args.MaxPriorityFeePerGas),
			Value:     (*big.Int)(args.Value),
			Data:      args.data(),
			//AccessList: al,
		}
	//case args.AccessList != nil:
	//	data = &types.AccessListTx{
	//		To:         args.To,
	//		ChainID:    (*big.Int)(args.ChainID),
	//		Nonce:      uint64(*args.Nonce),
	//		Gas:        uint64(*args.Gas),
	//		GasPrice:   (*big.Int)(args.GasPrice),
	//		Value:      (*big.Int)(args.Value),
	//		Data:       args.data(),
	//		AccessList: *args.AccessList,
	//	}
	default:
		data = &LegacyTx{
			To:       args.To,
			Nonce:    uint64(*args.Nonce),
			Gas:      uint64(*args.Gas),
			GasPrice: (*big.Int)(args.GasPrice),
			Value:    (*big.Int)(args.Value),
			Data:     args.data(),
		}
	}
	return NewTx(data)
}
