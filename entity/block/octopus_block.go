package block

import (
	"encoding/binary"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus/utils"
	"io"
	"math/big"
	"reflect"
	"sync/atomic"
	"time"
)

var (
	EmptyRootHash  = entity.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")
	EmptyUncleHash = RlpHash([]*Header(nil))
)

type BlockNonce [8]byte

// EncodeNonce将给定整数转换为块nonce。
func EncodeNonce(i uint64) BlockNonce {
	var n BlockNonce
	binary.BigEndian.PutUint64(n[:], i)
	return n
}

// Uint64返回块nonce的整数值。
func (n BlockNonce) Uint64() uint64 {
	return binary.BigEndian.Uint64(n[:])
}

//区块头结构体
type Header struct {
	ParentHash  entity.Hash    `autoInjectCfg:"octopus.blockchain.binding.genesis.header.parentHash"` //父hash
	UncleHash   entity.Hash    `autoInjectCfg:"octopus.blockchain.binding.genesis.header.uncleHash"`  //叔hash
	Coinbase    entity.Address //工作者地址值
	Root        entity.Hash    `autoInjectCfg:"octopus.blockchain.binding.genesis.header.root"`        //根hash
	TxHash      entity.Hash    `autoInjectCfg:"octopus.blockchain.binding.genesis.header.txhash"`      //交易hash
	ReceiptHash entity.Hash    `autoInjectCfg:"octopus.blockchain.binding.genesis.header.receiptHash"` //收据hash
	//Bloom       Bloom
	Difficulty *big.Int `autoInjectCfg:"octopus.blockchain.binding.genesis.header.difficulty"` //难度值
	Number     *big.Int `autoInjectCfg:"octopus.blockchain.binding.genesis.header.number"`     //数量
	GasLimit   uint64   `autoInjectCfg:"octopus.blockchain.binding.genesis.header.gasLimit"`   //gas限制
	GasUsed    uint64   `autoInjectCfg:"octopus.blockchain.binding.genesis.header.gasUsed"`    //gas总和
	Time       uint64   `autoInjectCfg:"octopus.blockchain.binding.genesis.header.time"`       //时间戳
	Extra      []byte
	MixDigest  entity.Hash `autoInjectCfg:"octopus.blockchain.binding.genesis.header.mixDigest"` //mixhash
	Nonce      BlockNonce  `autoInjectCfg:"octopus.blockchain.binding.genesis.header.nonce"`     //唯一标识s

	//基本费用
	BaseFee *big.Int `autoInjectCfg:"octopus.blockchain.binding.genesis.header.baseFee"`
}

func (h *Header) Hash() entity.Hash {
	//哈希运算
	return RlpHash(h)
}

// 如果此标头/块没有收据，则EmptyReceipts返回true。
func (h *Header) EmptyReceipts() bool {
	return h.ReceiptHash == EmptyRootHash
}

// 如果没有额外的“body”来完成标头，则EmptyBody返回true，即：没有事务和未结项。
func (h *Header) EmptyBody() bool {
	return h.TxHash == EmptyRootHash && h.UncleHash == EmptyUncleHash
}

var headerSize = utils.StorageSize(reflect.TypeOf(Header{}).Size())

// 大小返回所有内部内容使用的近似内存。它用于近似和限制各种缓存的内存消耗。
func (h *Header) Size() utils.StorageSize {
	return headerSize + utils.StorageSize(len(h.Extra)+(h.Difficulty.BitLen()+h.Number.BitLen())/8)
}

// SanityCheck检查了一些基本的东西——这些检查远远超出了任何“正常”生产值应该具备的范围，
//主要用于防止无界字段塞满垃圾数据，从而增加处理开销
func (h *Header) SanityCheck() error {
	if h.Number != nil && !h.Number.IsUint64() {
		return fmt.Errorf("too large block number: bitlen %d", h.Number.BitLen())
	}
	if h.Difficulty != nil {
		if diffLen := h.Difficulty.BitLen(); diffLen > 80 {
			return fmt.Errorf("too large block difficulty: bitlen %d", diffLen)
		}
	}
	if eLen := len(h.Extra); eLen > 100*1024 {
		return fmt.Errorf("too large block extradata: size %d", eLen)
	}
	if h.BaseFee != nil {
		if bfLen := h.BaseFee.BitLen(); bfLen > 256 {
			return fmt.Errorf("too large base fee: bitlen %d", bfLen)
		}
	}
	return nil
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

	// 包eth使用这些字段来跟踪对等块中继。
	ReceivedAt   time.Time
	ReceivedFrom interface{}
}

// “外部”块编码。用于oct协议等。
type extblock struct {
	Header *Header
	Txs    []*Transaction
	Uncles []*Header
}

type Blocks []*Block

// 新块创建新块。复制输入数据，对标题和字段值的更改不会影响块。
//头中的TxHash、uncleshash、ReceiptHash和Bloom的值将被忽略，并设置为从给定的txs、uncles和receipts派生的值。
func NewBlock(header *Header, txs []*Transaction, receipts []*Receipt, hasher TrieHasher) *Block {
	b := &Block{header: CopyHeader(header)}
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

//SanityCheck可用于防止无界字段塞满垃圾数据，从而增加处理开销
func (b *Block) SanityCheck() error {
	return b.header.SanityCheck()
}

func (b Block) newGenesis() {

}

// 编码器RLP将b序列化为辐射章鱼RLP块格式。
func (b *Block) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, extblock{
		Header: b.header,
		Txs:    b.transactions,
		Uncles: b.uncles,
	})
}

//获取交易集
func (b *Block) Transactions() Transactions { return b.transactions }

func (b *Block) Number() *big.Int     { return new(big.Int).Set(b.header.Number) }
func (b *Block) GasLimit() uint64     { return b.header.GasLimit }
func (b *Block) GasUsed() uint64      { return b.header.GasUsed }
func (b *Block) Difficulty() *big.Int { return new(big.Int).Set(b.header.Difficulty) }
func (b *Block) Time() uint64         { return b.header.Time }

func (b *Block) NumberU64() uint64        { return b.header.Number.Uint64() }
func (b *Block) Nonce() uint64            { return binary.BigEndian.Uint64(b.header.Nonce[:]) }
func (b *Block) Coinbase() entity.Address { return b.header.Coinbase }
func (b *Block) Root() entity.Hash        { return b.header.Root }
func (b *Block) ParentHash() entity.Hash  { return b.header.ParentHash }
func (b *Block) TxHash() entity.Hash      { return b.header.TxHash }
func (b *Block) ReceiptHash() entity.Hash { return b.header.ReceiptHash }
func (b *Block) UncleHash() entity.Hash   { return b.header.UncleHash }

func (b *Block) BaseFee() *big.Int {
	if b.header.BaseFee == nil {
		return nil
	}
	return new(big.Int).Set(b.header.BaseFee)
}

// Size通过编码并返回块，或返回先前缓存的值，返回块的真实RLP编码存储大小。
func (b *Block) Size() utils.StorageSize {
	if size := b.size.Load(); size != nil {
		return size.(utils.StorageSize)
	}
	c := writeCounter(0)
	rlp.Encode(&c, b)
	b.size.Store(utils.StorageSize(c))
	return utils.StorageSize(c)
}

func (b *Block) Header() *Header { return CopyHeader(b.header) }

// Body returns the non-header content of the block.
func (b *Block) Body() *Body { return &Body{b.transactions, b.uncles} }

func (b *Block) Hash() entity.Hash {
	if hash := b.hash.Load(); hash != nil {
		return hash.(entity.Hash)
	}
	v := b.header.Hash()
	b.hash.Store(v)
	return v
}

func (b *Block) Uncles() []*Header { return b.uncles }

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

// WithSeal返回一个新块，其中包含来自b的数据，但标头替换为密封的数据块。
func (b *Block) WithSeal(header *Header) *Block {
	cpy := *header

	return &Block{
		header:       &cpy,
		transactions: b.transactions,
		uncles:       b.uncles,
	}
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

func CalcUncleHash(uncles []*Header) entity.Hash {
	if len(uncles) == 0 {
		return EmptyUncleHash
	}
	return rlpHash(uncles)
}

// NewBlockWithHeader使用给定的标头数据创建块。复制标头数据，对标头和字段值的更改不会影响块。
func NewBlockWithHeader(header *Header) *Block {
	return &Block{header: CopyHeader(header)}
}

// HeaderParentHashFromRLP返回RLP编码头的parentHash。
//如果“header”无效，则返回零哈希。
func HeaderParentHashFromRLP(header []byte) entity.Hash {
	// parentHash是第一个列表元素。
	listContent, _, err := rlp.SplitList(header)
	if err != nil {
		return entity.Hash{}
	}
	parentHash, _, err := rlp.SplitString(listContent)
	if err != nil {
		return entity.Hash{}
	}
	if len(parentHash) != 32 {
		return entity.Hash{}
	}
	return entity.BytesToHash(parentHash)
}
