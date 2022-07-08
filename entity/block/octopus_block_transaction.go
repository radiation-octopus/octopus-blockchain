package block

import (
	"bytes"
	"container/heap"
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
	"sync/atomic"
	"time"
)

var (
	ErrInvalidSig           = errors.New("invalid transaction v, r, s values")
	ErrUnexpectedProtection = errors.New("transaction type does not supported EIP-155 protected signatures")
	ErrInvalidTxType        = errors.New("transaction type not valid in this context")
	ErrTxTypeNotSupported   = errors.New("transaction type not supported")
	ErrGasFeeCapTooLow      = errors.New("fee cap less than base fee")
	errShortTypedTx         = errors.New("typed transaction too short")
)

const (
	LegacyTxType = iota
	AccessListTxType
	DynamicFeeTxType
)

type Transaction struct {
	inner TxData    // 交易共识内容
	time  time.Time // 交易在本地的出现的时间戳

	// caches
	hash atomic.Value
	size atomic.Value
	from atomic.Value
}

type TxData interface {
	txType() byte // 返回类型ID
	copy() TxData // 复制，初始化所以字段

	chainID() *big.Int
	//accessList() AccessList
	data() []byte
	gas() uint64
	gasPrice() *big.Int
	gasTipCap() *big.Int
	gasFeeCap() *big.Int
	value() *big.Int
	nonce() uint64
	to() *entity.Address

	rawSignatureValues() (v, r, s *big.Int)
	setSignatureValues(chainID, v, r, s *big.Int)
}

// ChainId返回事务的EIP155链ID。返回值将始终为非nil。对于不受重播保护的旧事务，返回值为零。
func (tx *Transaction) ChainId() *big.Int {
	return tx.inner.chainID()
}

func (tx *Transaction) Data() []byte { return tx.inner.data() }

// AccessList返回事务的访问列表。
//func (tx *Transaction) AccessList() AccessList { return tx.inner.accessList() }

// 返回交易的gas限制
func (tx *Transaction) Gas() uint64 { return tx.inner.gas() }

// 返回交易的gas价格
func (tx *Transaction) GasPrice() *big.Int { return new(big.Int).Set(tx.inner.gasPrice()) }

// 返回交易的gas价格上限
func (tx *Transaction) GasTipCap() *big.Int { return new(big.Int).Set(tx.inner.gasTipCap()) }

// 返回交易中每个gas的费用上限
func (tx *Transaction) GasFeeCap() *big.Int { return new(big.Int).Set(tx.inner.gasFeeCap()) }

// 返回交易的金额
func (tx *Transaction) Value() *big.Int { return new(big.Int).Set(tx.inner.value()) }

// 返回交易的发送方账户nonce
func (tx *Transaction) Nonce() uint64 { return tx.inner.nonce() }

// 返回交易的收件人地址
func (tx *Transaction) To() *entity.Address {
	return copyAddressPtr(tx.inner.to())
}

// 返回交易hash
func (tx *Transaction) Hash() entity.Hash {
	if hash := tx.hash.Load(); hash != nil {
		return hash.(entity.Hash)
	}
	var h entity.Hash
	if tx.Type() == LegacyTxType {
		h = RlpHash(tx.inner)
	} else {
		h = PrefixedRlpHash(tx.Type(), tx.inner)
	}
	tx.hash.Store(h)
	return h
}

//encodeTyped将类型化事务的规范编码写入w。
func (tx *Transaction) encodeTyped(w *bytes.Buffer) error {
	w.WriteByte(tx.Type())
	return rlp.Encode(w, tx.inner)
}

// EffectiveGasTip返回给定基本费用的有工作者gasticpap。注意：如果有效gasTipCap为负值，此方法将同时返回实际负值error和ErrGasFeeCapTooLow
func (tx *Transaction) EffectiveGasTip(baseFee *big.Int) (*big.Int, error) {
	if baseFee == nil {
		return tx.GasTipCap(), nil
	}
	var err error
	gasFeeCap := tx.GasFeeCap()
	if gasFeeCap.Cmp(baseFee) == -1 {
		err = ErrGasFeeCapTooLow
	}
	return BigMin(tx.GasTipCap(), gasFeeCap.Sub(gasFeeCap, baseFee)), err
}

// 类型返回事务类型。
func (tx *Transaction) Type() uint8 {
	return tx.inner.txType()
}

// Size通过编码并返回事务的真实RLP编码存储大小，或返回以前缓存的值。
func (tx *Transaction) Size() utils.StorageSize {
	if size := tx.size.Load(); size != nil {
		return size.(utils.StorageSize)
	}
	c := writeCounter(0)
	//rlp.Encode(&c, &tx.inner)
	tx.size.Store(utils.StorageSize(c))
	return utils.StorageSize(c)
}

//成本返回gas*gasPrice+价值。
func (tx *Transaction) Cost() *big.Int {
	total := new(big.Int).Mul(tx.GasPrice(), new(big.Int).SetUint64(tx.Gas()))
	total.Add(total, tx.Value())
	return total
}

// RawSignatureValue返回事务的V、R、S签名值。调用者不应修改返回值。
func (tx *Transaction) RawSignatureValues() (v, r, s *big.Int) {
	return tx.inner.rawSignatureValues()
}

type writeCounter utils.StorageSize

func (c *writeCounter) Write(b []byte) (int, error) {
	*c += writeCounter(len(b))
	return len(b), nil
}

// GasFeeCapCmp比较了两项交易的费用上限。
func (tx *Transaction) GasFeeCapCmp(other *Transaction) int {
	return tx.inner.gasFeeCap().Cmp(other.inner.gasFeeCap())
}

// gasticpapcmp比较两个事务的gasticpap。
func (tx *Transaction) GasTipCapCmp(other *Transaction) int {
	return tx.inner.gasTipCap().Cmp(other.inner.gasTipCap())
}

// gasticpapintcmp将事务的gasticpap与给定的gasticpap进行比较。
func (tx *Transaction) GasTipCapIntCmp(other *big.Int) int {
	return tx.inner.gasTipCap().Cmp(other)
}

// GasFeeCapIntCmp将交易的费用上限与给定的费用上限进行比较。
func (tx *Transaction) GasFeeCapIntCmp(other *big.Int) int {
	return tx.inner.gasFeeCap().Cmp(other)
}

//EffectiveGasTipValue与EffectiveGasTip相同，但如果有效gasTipCap为负值，则不会返回错误
func (tx *Transaction) EffectiveGasTipValue(baseFee *big.Int) *big.Int {
	effectiveTip, _ := tx.EffectiveGasTip(baseFee)
	return effectiveTip
}

//effectivegasticpmp比较假定给定基本费用的两个事务的有效gasticpap。
func (tx *Transaction) EffectiveGasTipCmp(other *Transaction, baseFee *big.Int) int {
	if baseFee == nil {
		return tx.GasTipCapCmp(other)
	}
	return tx.EffectiveGasTipValue(baseFee).Cmp(other.EffectiveGasTipValue(baseFee))
}

//作为核心信息返回
func (tx *Transaction) AsMessage(s Signer, baseFee *big.Int) (Message, error) {
	msg := Message{
		nonce:     tx.Nonce(),
		gasLimit:  tx.Gas(),
		gasPrice:  new(big.Int).Set(tx.GasPrice()),
		gasFeeCap: new(big.Int).Set(tx.GasFeeCap()),
		gasTipCap: new(big.Int).Set(tx.GasTipCap()),
		to:        tx.To(),
		amount:    tx.Value(),
		data:      tx.Data(),
		isFake:    false,
	}
	//如果提供了baseFee，请将gasPrice设置为effectiveGasPrice。
	//if baseFee != nil {
	//	msg.gasPrice = BigMin(msg.gasPrice.Add(msg.gasTipCap, baseFee), msg.gasFeeCap)
	//}
	var err error
	msg.from, err = Sender(s, tx)
	return msg, err
}

// Protected表示事务是否受重播保护。
func (tx *Transaction) Protected() bool {
	switch tx := tx.inner.(type) {
	case *LegacyTx:
		return tx.V != nil && isProtectedV(tx.V)
	default:
		return true
	}
}

type Transactions []*Transaction

func (s Transactions) Len() int { return len(s) }

// EncodeIndex将第i个事务编码为w。请注意，这不会检查错误，因为我们假设*事务将只包含通过解码或通过此包中的公共API构造的有效TX。
func (s Transactions) EncodeIndex(i int, w *bytes.Buffer) {
	tx := s[i]
	tx.encodeTyped(w)
}

//setDecoded设置解码后的内部事务和大小。
func (tx *Transaction) setDecoded(inner TxData, size int) {
	tx.inner = inner
	tx.time = time.Now()
	if size > 0 {
		tx.size.Store(utils.StorageSize(size))
	}
}

// WithSignature返回具有给定签名的新事务。此签名需要采用[R | | S | V]格式，其中V为0或1。
func (tx *Transaction) WithSignature(signer Signer, sig []byte) (*Transaction, error) {
	r, s, v, err := signer.SignatureValues(tx, sig)
	a := v.BitLen()
	fmt.Println(a)
	if err != nil {
		return nil, err
	}
	cpy := tx.inner.copy()
	cpy.setSignatureValues(signer.ChainID(), v, r, s)
	return &Transaction{inner: cpy, time: tx.time}, nil
}

// TxDifference返回一个新的集合，即a和b之间的差值。
func TxDifference(a, b Transactions) Transactions {
	keep := make(Transactions, 0, len(a))

	remove := make(map[entity.Hash]struct{})
	for _, tx := range b {
		remove[tx.Hash()] = struct{}{}
	}

	for _, tx := range a {
		if _, ok := remove[tx.Hash()]; !ok {
			keep = append(keep, tx)
		}
	}

	return keep
}

// HashDifference返回一个新集，即a和b之间的差。
func HashDifference(a, b []entity.Hash) []entity.Hash {
	keep := make([]entity.Hash, 0, len(a))

	remove := make(map[entity.Hash]struct{})
	for _, hash := range b {
		remove[hash] = struct{}{}
	}

	for _, hash := range a {
		if _, ok := remove[hash]; !ok {
			keep = append(keep, hash)
		}
	}

	return keep
}

//交易消息结构体
type Message struct {
	to        *entity.Address
	from      entity.Address
	nonce     uint64
	amount    *big.Int
	gasLimit  uint64
	gasPrice  *big.Int
	gasFeeCap *big.Int
	gasTipCap *big.Int
	data      []byte
	//accessList AccessList
	isFake bool
}

// txbynone实现了sort接口，允许按nonce对事务列表进行排序。这通常只在对单个帐户中的交易进行排序时有用，否则nonce比较没有多大意义。
type TxByNonce Transactions

func (s TxByNonce) Len() int           { return len(s) }
func (s TxByNonce) Less(i, j int) bool { return s[i].Nonce() < s[j].Nonce() }
func (s TxByNonce) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func NewMessage(from entity.Address, to *entity.Address, nonce uint64, amount *big.Int, gasLimit uint64, gasPrice, gasFeeCap, gasTipCap *big.Int, data []byte, isFake bool) Message {
	return Message{
		from:      from,
		to:        to,
		nonce:     nonce,
		amount:    amount,
		gasLimit:  gasLimit,
		gasPrice:  gasPrice,
		gasFeeCap: gasFeeCap,
		gasTipCap: gasTipCap,
		data:      data,
		isFake:    isFake,
	}
}

func (m Message) From() entity.Address { return m.from }
func (m Message) To() *entity.Address  { return m.to }
func (m Message) GasPrice() *big.Int   { return m.gasPrice }
func (m Message) GasFeeCap() *big.Int  { return m.gasFeeCap }
func (m Message) GasTipCap() *big.Int  { return m.gasTipCap }
func (m Message) Value() *big.Int      { return m.amount }
func (m Message) Gas() uint64          { return m.gasLimit }
func (m Message) Nonce() uint64        { return m.nonce }
func (m Message) Data() []byte         { return m.data }
func (m Message) IsFake() bool         { return m.isFake }

func copyAddressPtr(a *entity.Address) *entity.Address {
	if a == nil {
		return nil
	}
	cpy := *a
	return &cpy
}

/*
TransactionsByPriceAndNonce表示一组事务，这些事务可以按利润最大化的排序顺序返回事务，同时支持删除不可执行帐户的整批事务。
*/
type TransactionsByPriceAndNonce struct {
	txs     map[entity.Address]Transactions // 按帐户即时排序的交易列表
	heads   TxByPriceAndTime                // 每个唯一账户的下一笔交易（价格堆）
	signer  Signer                          // 事务集的签名者
	baseFee *big.Int                        // 当前基本费用
}

// NewTransactionsByPriceAndOnce创建一个交易集，该交易集可以以暂时兑现的方式检索按价格排序的交易。
//注意，输入映射被重新拥有，因此调用方在将其提供给构造函数后不应再与if进行交互。
func NewTransactionsByPriceAndNonce(signer Signer, txs map[entity.Address]Transactions, baseFee *big.Int) *TransactionsByPriceAndNonce {
	// 使用head事务初始化基于价格和接收时间的堆
	heads := make(TxByPriceAndTime, 0, len(txs))
	for from, accTxs := range txs {
		acc, _ := Sender(signer, accTxs[0])
		wrapped, err := NewTxWithMinerFee(accTxs[0], baseFee)
		// 如果发件人与中的不匹配或包装失败，请删除事务。
		if acc != from || err != nil {
			delete(txs, from)
			continue
		}
		heads = append(heads, wrapped)
		txs[from] = accTxs[1:]
	}
	heap.Init(&heads)

	// 组装并返回事务集
	return &TransactionsByPriceAndNonce{
		txs:     txs,
		heads:   heads,
		signer:  signer,
		baseFee: baseFee,
	}
}

// Peek按价格返回下一笔交易。
func (t *TransactionsByPriceAndNonce) Peek() *Transaction {
	if len(t.heads) == 0 {
		return nil
	}
	return t.heads[0].tx
}

//Shift将当前的最佳头部替换为同一帐户中的下一个头部。
func (t *TransactionsByPriceAndNonce) Shift() {
	acc, _ := Sender(t.signer, t.heads[0].tx)
	if txs, ok := t.txs[acc]; ok && len(txs) > 0 {
		if wrapped, err := NewTxWithMinerFee(txs[0], t.baseFee); err == nil {
			t.heads[0], t.txs[acc] = wrapped, txs[1:]
			heap.Fix(&t.heads, 0)
			return
		}
	}
	heap.Pop(&t.heads)
}

// Pop删除最佳交易，*而不是*将其替换为同一帐户中的下一个交易。
//当交易无法执行时，应使用此选项，因此应从同一帐户中丢弃所有后续交易。
func (t *TransactionsByPriceAndNonce) Pop() {
	heap.Pop(&t.heads)
}

/*
TxWithMinerFee用其天然气价格或有效的miner Gasticap来包装交易
*/
type TxWithMinerFee struct {
	tx       *Transaction
	minerFee *big.Int
}

// NewTxWithMinerFee创建一个打包的事务，如果提供了基本费用，则计算有效的miner Gasticap。如果有效miner gasTipCap为负值，则返回错误。
func NewTxWithMinerFee(tx *Transaction, baseFee *big.Int) (*TxWithMinerFee, error) {
	minerFee, err := tx.EffectiveGasTip(baseFee)
	if err != nil {
		return nil, err
	}
	return &TxWithMinerFee{
		tx:       tx,
		minerFee: minerFee,
	}, nil
}

/*
TxByPriceAndTime实现了sort和heap接口，这使得它对于一次性排序以及单独添加和删除元素非常有用。
*/
type TxByPriceAndTime []*TxWithMinerFee

func (s TxByPriceAndTime) Len() int { return len(s) }
func (s TxByPriceAndTime) Less(i, j int) bool {
	// 如果价格相等，则使用首次看到交易的时间进行确定性排序
	cmp := s[i].minerFee.Cmp(s[j].minerFee)
	if cmp == 0 {
		return s[i].tx.time.Before(s[j].tx.time)
	}
	return cmp > 0
}
func (s TxByPriceAndTime) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func (s *TxByPriceAndTime) Push(x interface{}) {
	*s = append(*s, x.(*TxWithMinerFee))
}

func (s *TxByPriceAndTime) Pop() interface{} {
	old := *s
	n := len(old)
	x := old[n-1]
	*s = old[0 : n-1]
	return x
}

//LegacyTx是常规以太坊事务的事务数据。
type LegacyTx struct {
	Nonce    uint64          // 发件人帐户的nonce
	GasPrice *big.Int        // gas价格
	Gas      uint64          // gas限制
	To       *entity.Address `rlp:"nil"` // nil表示合同创建
	Value    *big.Int        // wei金额
	Data     []byte          // 合同调用输入数据
	V, R, S  *big.Int        // 签名值
}

//copy创建事务数据的深度副本并初始化所有字段。
func (tx *LegacyTx) copy() TxData {
	cpy := &LegacyTx{
		Nonce: tx.Nonce,
		To:    copyAddressPtr(tx.To),
		Data:  utils.CopyBytes(tx.Data),
		Gas:   tx.Gas,
		// 下面对其进行了初始化。
		Value:    new(big.Int),
		GasPrice: new(big.Int),
		V:        new(big.Int),
		R:        new(big.Int),
		S:        new(big.Int),
	}
	if tx.Value != nil {
		cpy.Value.Set(tx.Value)
	}
	if tx.GasPrice != nil {
		cpy.GasPrice.Set(tx.GasPrice)
	}
	if tx.V != nil {
		cpy.V.Set(tx.V)
	}
	if tx.R != nil {
		cpy.R.Set(tx.R)
	}
	if tx.S != nil {
		cpy.S.Set(tx.S)
	}
	return cpy
}

// innerTx的访问器。
func (tx *LegacyTx) txType() byte      { return LegacyTxType }
func (tx *LegacyTx) chainID() *big.Int { return deriveChainId(tx.V) }

//func (tx *LegacyTx) accessList() AccessList { return nil }
func (tx *LegacyTx) data() []byte        { return tx.Data }
func (tx *LegacyTx) gas() uint64         { return tx.Gas }
func (tx *LegacyTx) gasPrice() *big.Int  { return tx.GasPrice }
func (tx *LegacyTx) gasTipCap() *big.Int { return tx.GasPrice }
func (tx *LegacyTx) gasFeeCap() *big.Int { return tx.GasPrice }
func (tx *LegacyTx) value() *big.Int     { return tx.Value }
func (tx *LegacyTx) nonce() uint64       { return tx.Nonce }
func (tx *LegacyTx) to() *entity.Address { return tx.To }

func (tx *LegacyTx) rawSignatureValues() (v, r, s *big.Int) {
	return tx.V, tx.R, tx.S
}

func (tx *LegacyTx) setSignatureValues(chainID, v, r, s *big.Int) {
	tx.V, tx.R, tx.S = v, r, s
}

// NewTransaction创建未签名的旧事务。不推荐使用：改用NewTx。
func NewTransaction(nonce uint64, to entity.Address, amount *big.Int, gasLimit uint64, gasPrice *big.Int, data []byte) *Transaction {
	return NewTx(&LegacyTx{
		Nonce:    nonce,
		To:       &to,
		Value:    amount,
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Data:     data,
	})
}

// NewTx创建一个新事务。
func NewTx(inner TxData) *Transaction {
	tx := new(Transaction)
	tx.setDecoded(inner.copy(), 0)
	return tx
}

//deriveChainId从给定的v参数派生链id
func deriveChainId(v *big.Int) *big.Int {
	if v.BitLen() <= 64 {
		v := v.Uint64()
		if v == 27 || v == 28 {
			return new(big.Int)
		}
		return new(big.Int).SetUint64((v - 35) / 2)
	}
	v = new(big.Int).Sub(v, big.NewInt(35))
	return v.Div(v, big.NewInt(2))
}

func isProtectedV(V *big.Int) bool {
	if V.BitLen() <= 8 {
		v := V.Uint64()
		return v != 27 && v != 28 && v != 1 && v != 0
	}
	//任何不是27或28的都被视为受保护
	return true
}

type DynamicFeeTx struct {
	ChainID   *big.Int
	Nonce     uint64
	GasTipCap *big.Int // a.k.a. maxPriorityFeePerGas
	GasFeeCap *big.Int // a.k.a. maxFeePerGas
	Gas       uint64
	To        *entity.Address `rlp:"nil"` // nil表示合同创建
	Value     *big.Int
	Data      []byte
	//AccessList AccessList

	// 签名值
	V *big.Int `json:"v" gencodec:"required"`
	R *big.Int `json:"r" gencodec:"required"`
	S *big.Int `json:"s" gencodec:"required"`
}

// copy创建事务数据的深度副本并初始化所有字段。
func (tx *DynamicFeeTx) copy() TxData {
	cpy := &DynamicFeeTx{
		Nonce: tx.Nonce,
		To:    copyAddressPtr(tx.To),
		Data:  utils.CopyBytes(tx.Data),
		Gas:   tx.Gas,
		// 这些内容复制如下。
		//AccessList: make(AccessList, len(tx.AccessList)),
		Value:     new(big.Int),
		ChainID:   new(big.Int),
		GasTipCap: new(big.Int),
		GasFeeCap: new(big.Int),
		V:         new(big.Int),
		R:         new(big.Int),
		S:         new(big.Int),
	}
	//copy(cpy.AccessList, tx.AccessList)
	if tx.Value != nil {
		cpy.Value.Set(tx.Value)
	}
	if tx.ChainID != nil {
		cpy.ChainID.Set(tx.ChainID)
	}
	if tx.GasTipCap != nil {
		cpy.GasTipCap.Set(tx.GasTipCap)
	}
	if tx.GasFeeCap != nil {
		cpy.GasFeeCap.Set(tx.GasFeeCap)
	}
	if tx.V != nil {
		cpy.V.Set(tx.V)
	}
	if tx.R != nil {
		cpy.R.Set(tx.R)
	}
	if tx.S != nil {
		cpy.S.Set(tx.S)
	}
	return cpy
}

// innerTx的访问器。
func (tx *DynamicFeeTx) txType() byte      { return DynamicFeeTxType }
func (tx *DynamicFeeTx) chainID() *big.Int { return tx.ChainID }

//func (tx *DynamicFeeTx) accessList() AccessList { return tx.AccessList }
func (tx *DynamicFeeTx) data() []byte        { return tx.Data }
func (tx *DynamicFeeTx) gas() uint64         { return tx.Gas }
func (tx *DynamicFeeTx) gasFeeCap() *big.Int { return tx.GasFeeCap }
func (tx *DynamicFeeTx) gasTipCap() *big.Int { return tx.GasTipCap }
func (tx *DynamicFeeTx) gasPrice() *big.Int  { return tx.GasFeeCap }
func (tx *DynamicFeeTx) value() *big.Int     { return tx.Value }
func (tx *DynamicFeeTx) nonce() uint64       { return tx.Nonce }
func (tx *DynamicFeeTx) to() *entity.Address { return tx.To }

func (tx *DynamicFeeTx) rawSignatureValues() (v, r, s *big.Int) {
	return tx.V, tx.R, tx.S
}

func (tx *DynamicFeeTx) setSignatureValues(chainID, v, r, s *big.Int) {
	tx.ChainID, tx.V, tx.R, tx.S = chainID, v, r, s
}
