package block

import (
	"encoding/binary"
	operationUtils "github.com/radiation-octopus/octopus-blockchain/operationUtils"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
	"sync/atomic"
)

var (
	EmptyRootHash  = operationUtils.BytesToHash(utils.Hex2Bytes("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"))
	EmptyUncleHash = []*Header(nil)
)

type BlockNonce [8]byte

//区块头结构体
type Header struct {
	ParentHash  operationUtils.Hash    `autoInjectCfg:"octopus.blockchain.binding.genesis.header.parentHash"` //父hash
	UncleHash   operationUtils.Hash    `autoInjectCfg:"octopus.blockchain.binding.genesis.header.uncleHash"`  //叔hash
	Coinbase    operationUtils.Address //工作者地址值
	Root        operationUtils.Hash    `autoInjectCfg:"octopus.blockchain.binding.genesis.header.root"`        //根hash
	TxHash      operationUtils.Hash    `autoInjectCfg:"octopus.blockchain.binding.genesis.header.txhash"`      //交易hash
	ReceiptHash operationUtils.Hash    `autoInjectCfg:"octopus.blockchain.binding.genesis.header.receiptHash"` //收据hash
	//Bloom       Bloom
	Difficulty *big.Int `autoInjectCfg:"octopus.blockchain.binding.genesis.header.difficulty"` //难度值
	Number     *big.Int `autoInjectCfg:"octopus.blockchain.binding.genesis.header.number"`     //数量
	GasLimit   uint64   `autoInjectCfg:"octopus.blockchain.binding.genesis.header.gasLimit"`   //gas限制
	GasUsed    uint64   `autoInjectCfg:"octopus.blockchain.binding.genesis.header.gasUsed"`    //gas总和
	Time       uint64   `autoInjectCfg:"octopus.blockchain.binding.genesis.header.time"`       //时间戳
	//Extra       []byte
	MixDigest operationUtils.Hash `autoInjectCfg:"octopus.blockchain.binding.genesis.header.mixDigest"` //mixhash
	Nonce     BlockNonce          `autoInjectCfg:"octopus.blockchain.binding.genesis.header.nonce"`     //唯一标识s

	//基本费用
	BaseFee *big.Int `autoInjectCfg:"octopus.blockchain.binding.genesis.header.baseFee"`
}

func (h *Header) Hash() operationUtils.Hash {
	//哈希运算
	//return rlpHash(h)
	return operationUtils.Hash{0}
}

//数据容器
type Body struct {
	Transactions []*Transaction
	Uncles       []*Header
}

type Block struct {
	header       *Header      //区块头信息
	uncles       []*Header    //叔块头信息
	transactions Transactions //交易信息
	// caches
	hash atomic.Value //缓存hash
	size atomic.Value //缓存大小
	td   *big.Int     //交易总难度
}

// 新块创建新块。复制输入数据，对标题和字段值的更改不会影响块。
//头中的TxHash、uncleshash、ReceiptHash和Bloom的值将被忽略，并设置为从给定的txs、uncles和receipts派生的值。
func NewBlock(header *Header, txs []*Transaction, receipts []*Receipt) *Block {
	b := &Block{header: CopyHeader(header), td: new(big.Int)}
	var hasher TrieHasher
	// TODO: panic if len(txs) != len(receipts)
	if len(txs) == 0 {
		b.header.TxHash = EmptyRootHash
	} else {
		b.header.TxHash = DeriveSha(Transactions(txs), hasher)
		b.transactions = make(Transactions, len(txs))
		copy(b.transactions, txs)
	}

	if len(receipts) == 0 {
		b.header.ReceiptHash = EmptyRootHash
	} else {
		b.header.ReceiptHash = DeriveSha(Receipts(receipts), hasher)
		//b.header.Bloom = CreateBloom(receipts)
	}

	//if len(uncles) == 0 {
	//	b.header.UncleHash = EmptyUncleHash
	//} else {
	//	b.header.UncleHash = CalcUncleHash(uncles)
	//	b.uncles = make([]*Header, len(uncles))
	//	for i := range uncles {
	//		b.uncles[i] = CopyHeader(uncles[i])
	//	}
	//}

	return b
}

func (b Block) newGenesis() {

}

//获取交易集
func (b *Block) Transactions() Transactions { return b.transactions }

func (b *Block) Number() *big.Int     { return new(big.Int).Set(b.header.Number) }
func (b *Block) GasLimit() uint64     { return b.header.GasLimit }
func (b *Block) GasUsed() uint64      { return b.header.GasUsed }
func (b *Block) Difficulty() *big.Int { return new(big.Int).Set(b.header.Difficulty) }
func (b *Block) Time() uint64         { return b.header.Time }

func (b *Block) NumberU64() uint64                { return b.header.Number.Uint64() }
func (b *Block) Nonce() uint64                    { return binary.BigEndian.Uint64(b.header.Nonce[:]) }
func (b *Block) Root() operationUtils.Hash        { return b.header.Root }
func (b *Block) ParentHash() operationUtils.Hash  { return b.header.ParentHash }
func (b *Block) TxHash() operationUtils.Hash      { return b.header.TxHash }
func (b *Block) ReceiptHash() operationUtils.Hash { return b.header.ReceiptHash }
func (b *Block) UncleHash() operationUtils.Hash   { return b.header.UncleHash }

func (b *Block) BaseFee() *big.Int {
	if b.header.BaseFee == nil {
		return nil
	}
	return new(big.Int).Set(b.header.BaseFee)
}

func (b *Block) Header() *Header { return CopyHeader(b.header) }

// Body returns the non-header content of the block.
func (b *Block) Body() *Body { return &Body{b.transactions, b.uncles} }

func (b *Block) Hash() operationUtils.Hash {
	if hash := b.hash.Load(); hash != nil {
		return hash.(operationUtils.Hash)
	}
	v := b.header.Hash()
	b.hash.Store(v)
	return v
}

// WithBody返回具有给定事务和叔叔内容的新块.
func (b *Block) WithBody(transactions []*Transaction, uncles []*Header) *Block {
	block := &Block{
		header:       CopyHeader(b.header),
		transactions: make([]*Transaction, len(transactions)),
		uncles:       make([]*Header, len(uncles)),
	}
	copy(block.transactions, transactions)
	for i := range uncles {
		block.uncles[i] = CopyHeader(uncles[i])
	}
	return block
}

func CopyHeader(h *Header) *Header {
	cpy := *h
	if cpy.Difficulty = new(big.Int); h.Difficulty != nil {
		cpy.Difficulty.Set(h.Difficulty)
	}
	if cpy.Number = new(big.Int); h.Number != nil {
		cpy.Number.Set(h.Number)
	}
	if h.BaseFee != nil {
		cpy.BaseFee = new(big.Int).Set(h.BaseFee)
	}
	//if len(h.Extra) > 0 {
	//	cpy.Extra = make([]byte, len(h.Extra))
	//	copy(cpy.Extra, h.Extra)
	//}
	return &cpy
}

// NewBlockWithHeader使用给定的标头数据创建块。复制标头数据，对标头和字段值的更改不会影响块。
func NewBlockWithHeader(header *Header) *Block {
	return &Block{header: CopyHeader(header)}
}
