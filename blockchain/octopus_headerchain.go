package blockchain

import (
	crand "crypto/rand"
	"errors"
	lru "github.com/hashicorp/golang-lru"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
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
	//config        *params.ChainConfig
	chainDb       operationdb.OperationDB
	genesisHeader *block.Header //创世区块的当前头部

	currentHeader     atomic.Value //头链的当前头部
	currentHeaderHash entity.Hash  //头链的当前头的hash

	headerCache *lru.Cache // 缓存最近的块头
	tdCache     *lru.Cache // 缓存最近的块总困难数
	numberCache *lru.Cache // 缓存最近的块号

	procInterrupt func() bool

	rand   *mrand.Rand
	engine consensus.Engine
}

func (hc *HeaderChain) CurrentHeader() *block.Header {
	return hc.currentHeader.Load().(*block.Header)
}
func (hc *HeaderChain) GetHeader(hash entity.Hash, number uint64) *block.Header {
	//先查看缓存通道是否存在
	if header, ok := hc.headerCache.Get(hash); ok {
		return header.(*block.Header)
	}
	//检索数据库查询
	header := operationdb.ReadHeader(hash, number)
	if header == nil {
		return nil
	}
	// Cache the found header for next time and return
	hc.headerCache.Add(hash, header)
	return header
}
func (hc *HeaderChain) GetHeaderByNumber(number uint64) *block.Header {
	hash := operationdb.ReadCanonicalHash(number)
	if hash == (entity.Hash{}) {
		return nil
	}
	return hc.GetHeader(hash, number)
}
func (hc *HeaderChain) GetHeaderByHash(hash entity.Hash) *block.Header {
	return nil
}
func (hc *HeaderChain) GetTd(hash entity.Hash, number uint64) *big.Int {
	return nil
}

// GetBlockNumber从缓存或数据库中检索属于给定哈希的块号
func (hc *HeaderChain) GetBlockNumber(hash entity.Hash) *big.Int {
	if cached, ok := hc.numberCache.Get(hash); ok {
		number := cached.(big.Int)
		return &number
	}
	number := operationdb.ReadHeaderNumber(hc.chainDb, hash)
	if number != nil {
		hc.numberCache.Add(hash, *number)
	}
	return number
}

// SetCurrentHeader将规范通道的内存标头标记设置为给定标头。
func (hc *HeaderChain) SetCurrentHeader(head *block.Header) {
	hc.currentHeader.Store(head)
	hc.currentHeaderHash = head.Hash()
	//headHeaderGauge.Update(head.Number.Int64())
}

// SetGenesis为链设置新的genesis块标题
func (hc *HeaderChain) SetGenesis(head *block.Header) {
	hc.genesisHeader = head
}

// NewHeaderChain创建新的HeaderChain结构。ProcInterrupt指向父级的中断信号量。
func NewHeaderChain(engine consensus.Engine, procInterrupt func() bool) (*HeaderChain, error) {
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
	if head := operationdb.ReadHeadBlockHash(); head != (entity.Hash{}) {
		if chead := hc.GetHeaderByHash(head); chead != nil {
			hc.currentHeader.Store(chead)
		}
	}
	hc.currentHeaderHash = hc.CurrentHeader().Hash()
	//headHeaderGauge.Update(hc.CurrentHeader().Number.Int64())
	return hc, nil
}
