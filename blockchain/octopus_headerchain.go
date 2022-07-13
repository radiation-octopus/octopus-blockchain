package blockchain

import (
	crand "crypto/rand"
	"errors"
	lru "github.com/hashicorp/golang-lru"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/entity/rawdb"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"math"
	"math/big"
	mrand "math/rand"
	"sync/atomic"
)

const (
	headerCacheLimit = 512
	tdCacheLimit     = 1024
	numberCacheLimit = 2048
)

type HeaderChain struct {
	//config        *entity.ChainConfig
	chainDb       typedb.Database
	genesisHeader *block2.Header //创世区块的当前头部

	currentHeader     atomic.Value //头链的当前头部
	currentHeaderHash entity.Hash  //头链的当前头的hash

	headerCache *lru.Cache // 缓存最近的块头
	tdCache     *lru.Cache // 缓存最近的块总困难数
	numberCache *lru.Cache // 缓存最近的块号

	procInterrupt func() bool

	rand   *mrand.Rand
	engine consensus.Engine
}

func (hc *HeaderChain) CurrentHeader() *block2.Header {
	return hc.currentHeader.Load().(*block2.Header)
}

//GetHeader通过哈希和数字从数据库中检索块头，如果找到，则将其缓存。
func (hc *HeaderChain) GetHeader(hash entity.Hash, number uint64) *block2.Header {
	//先查看缓存通道是否存在
	if header, ok := hc.headerCache.Get(hash); ok {
		return header.(*block2.Header)
	}
	//检索数据库查询
	header := rawdb.ReadHeader(hc.chainDb, hash, number)
	if header == nil {
		return nil
	}
	//缓存找到的标头以供下次使用并返回
	hc.headerCache.Add(hash, header)
	return header
}

//GetHeaderByNumber按编号从数据库中检索块头，如果找到，则缓存它（与其哈希关联）。
func (hc *HeaderChain) GetHeaderByNumber(number uint64) *block2.Header {
	hash := rawdb.ReadCanonicalHash(hc.chainDb, number)
	if hash == (entity.Hash{}) {
		return nil
	}
	return hc.GetHeader(hash, number)
}
func (hc *HeaderChain) GetHeaderByHash(hash entity.Hash) *block2.Header {
	number := hc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return hc.GetHeader(hash, *number)
}

// GetHeadersFrom以rlp形式返回从给定数字向后的连续标头段。
//如果“number”高于最高的本地标头，此方法将返回一个best effort响应，其中包含我们确实拥有的标头。
func (hc *HeaderChain) GetHeadersFrom(number, count uint64) []rlp.RawValue {
	// 如果请求是针对未来的标头，我们仍然返回能够提供服务的标头部分
	if current := hc.CurrentHeader().Number.Uint64(); current < number {
		if count > number-current {
			count -= number - current
			number = current
		} else {
			return nil
		}
	}
	var headers []rlp.RawValue
	// 如果缓存中已经有一些标头，请在转到db之前使用它。
	hash := rawdb.ReadCanonicalHash(hc.chainDb, number)
	if hash == (entity.Hash{}) {
		return nil
	}
	for count > 0 {
		header, ok := hc.headerCache.Get(hash)
		if !ok {
			break
		}
		h := header.(*block2.Header)
		rlpData, _ := rlp.EncodeToBytes(h)
		headers = append(headers, rlpData)
		hash = h.ParentHash
		count--
		number--
	}
	// Read remaining from db
	if count > 0 {
		headers = append(headers, rawdb.ReadHeaderRange(hc.chainDb, number, count)...)
	}
	return headers
}

// 获取给定块的第n个祖先。
//它假设给定的块或其近祖先是规范的。maxnoncanonic指向一个向下的计数器，该计数器限制在我们到达正则链之前要单独检查的块的数量。
//注意：祖先==0返回同一块，1返回其父块，依此类推。
func (hc *HeaderChain) GetAncestor(hash entity.Hash, number, ancestor uint64, maxNonCanonical *uint64) (entity.Hash, uint64) {
	if ancestor > number {
		return entity.Hash{}, 0
	}
	if ancestor == 1 {
		// in this case it is cheaper to just read the header
		if header := hc.GetHeader(hash, number); header != nil {
			return header.ParentHash, number - 1
		}
		return entity.Hash{}, 0
	}
	for ancestor != 0 {
		if rawdb.ReadCanonicalHash(hc.chainDb, number) == hash {
			ancestorHash := rawdb.ReadCanonicalHash(hc.chainDb, number-ancestor)
			if rawdb.ReadCanonicalHash(hc.chainDb, number) == hash {
				number -= ancestor
				return ancestorHash, number
			}
		}
		if *maxNonCanonical == 0 {
			return entity.Hash{}, 0
		}
		*maxNonCanonical--
		ancestor--
		header := hc.GetHeader(hash, number)
		if header == nil {
			return entity.Hash{}, 0
		}
		hash = header.ParentHash
		number--
	}
	return hash, number
}

func (hc *HeaderChain) GetCanonicalHash(number uint64) entity.Hash {
	return rawdb.ReadCanonicalHash(hc.chainDb, number)
}

func (hc *HeaderChain) GetTd(hash entity.Hash, number uint64) *big.Int {
	// 如果td已经在缓存中，则短路，否则检索
	if cached, ok := hc.tdCache.Get(hash); ok {
		return cached.(*big.Int)
	}
	td := rawdb.ReadTd(hc.chainDb, hash, number)
	if td == nil {
		return nil
	}
	// 缓存找到的尸体以备下次使用并返回
	hc.tdCache.Add(hash, td)
	return td
}

// GetBlockNumber从缓存或数据库中检索属于给定哈希的块号
func (hc *HeaderChain) GetBlockNumber(hash entity.Hash) *uint64 {
	if cached, ok := hc.numberCache.Get(hash); ok {
		number := cached.(uint64)
		return &number
	}
	number := rawdb.ReadHeaderNumber(hc.chainDb, hash)
	if number != nil {
		hc.numberCache.Add(hash, *number)
	}
	return number
}

// SetCurrentHeader将规范通道的内存标头标记设置为给定标头。
func (hc *HeaderChain) SetCurrentHeader(head *block2.Header) {
	hc.currentHeader.Store(head)
	hc.currentHeaderHash = head.Hash()
	//headHeaderGauge.Update(head.Number.Int64())
}

// SetGenesis为链设置新的genesis块标题
func (hc *HeaderChain) SetGenesis(head *block2.Header) {
	hc.genesisHeader = head
}

// NewHeaderChain创建新的HeaderChain结构。ProcInterrupt指向父级的中断信号量。
func NewHeaderChain(chainDb typedb.Database, engine consensus.Engine, procInterrupt func() bool) (*HeaderChain, error) {
	headerCache, _ := lru.New(headerCacheLimit)
	tdCache, _ := lru.New(tdCacheLimit)
	numberCache, _ := lru.New(numberCacheLimit)

	// 给一个快速但加密的随机生成器种子
	seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return nil, err
	}
	hc := &HeaderChain{
		//config:        config,
		chainDb:       chainDb,
		headerCache:   headerCache,
		tdCache:       tdCache,
		numberCache:   numberCache,
		procInterrupt: procInterrupt,
		rand:          mrand.New(mrand.NewSource(seed.Int64())),
		engine:        engine,
	}
	hc.genesisHeader = hc.GetHeaderByNumber(0)
	if hc.genesisHeader == nil {
		return nil, errors.New("genesis not found in chain")
	}
	hc.currentHeader.Store(hc.genesisHeader)
	if head := rawdb.ReadHeadBlockHash(hc.chainDb); head != (entity.Hash{}) {
		if chead := hc.GetHeaderByHash(head); chead != nil {
			hc.currentHeader.Store(chead)
		}
	}
	hc.currentHeaderHash = hc.CurrentHeader().Hash()
	//headHeaderGauge.Update(hc.CurrentHeader().Number.Int64())
	return hc, nil
}
