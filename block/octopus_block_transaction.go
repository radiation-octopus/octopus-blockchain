package block

import (
	"bytes"
	"container/heap"
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
	"sync/atomic"
	"time"
)

var (
	ErrGasFeeCapTooLow = errors.New("fee cap less than base fee")
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

	//rawSignatureValues() (v, r, s *big.Int)
	//setSignatureValues(chainID, v, r, s *big.Int)
}

func (tx *Transaction) Data() []byte { return tx.inner.data() }

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
	//if tx.Type() == LegacyTxType {
	//	h = rlpHash(tx.inner)
	//} else {
	//	h = prefixedRlpHash(tx.Type(), tx.inner)
	//}
	h = entity.Hash{0}
	tx.hash.Store(h)
	return h
}

//encodeTyped将类型化事务的规范编码写入w。
func (tx *Transaction) encodeTyped(w *bytes.Buffer) error {
	//w.WriteByte(tx.Type())
	//return rlp.Encode(w, tx.inner)
	return nil
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

type Transactions []*Transaction

func (s Transactions) Len() int { return len(s) }

// EncodeIndex将第i个事务编码为w。请注意，这不会检查错误，因为我们假设*事务将只包含通过解码或通过此包中的公共API构造的有效TX。
func (s Transactions) EncodeIndex(i int, w *bytes.Buffer) {
	tx := s[i]
	tx.encodeTyped(w)
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

// NewTransactionsByPriceAndNonce creates a transaction set that can retrieve
// price sorted transactions in a nonce-honouring way.
//
// Note, the input map is reowned so the caller should not interact any more with
// if after providing it to the constructor.
func NewTransactionsByPriceAndNonce(signer Signer, txs map[entity.Address]Transactions, baseFee *big.Int) *TransactionsByPriceAndNonce {
	// Initialize a price and received time based heap with the head transactions
	heads := make(TxByPriceAndTime, 0, len(txs))
	for from, accTxs := range txs {
		acc, _ := Sender(signer, accTxs[0])
		wrapped, err := NewTxWithMinerFee(accTxs[0], baseFee)
		// Remove transaction if sender doesn't match from, or if wrapping fails.
		if acc != from || err != nil {
			delete(txs, from)
			continue
		}
		heads = append(heads, wrapped)
		txs[from] = accTxs[1:]
	}
	heap.Init(&heads)

	// Assemble and return the transaction set
	return &TransactionsByPriceAndNonce{
		txs:     txs,
		heads:   heads,
		signer:  signer,
		baseFee: baseFee,
	}
}

// Peek returns the next transaction by price.
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
