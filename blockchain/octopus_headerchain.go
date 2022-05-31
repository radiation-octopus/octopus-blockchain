package blockchain

import (
	lru "github.com/hashicorp/golang-lru"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"math/big"
	mrand "math/rand"
	"sync/atomic"
)

type HeaderChain struct {
	//config        *params.ChainConfig
	chainDb       operationdb.Database
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
	header := operationdb.ReadHeader(hc.chainDb, hash, number)
	if header == nil {
		return nil
	}
	// Cache the found header for next time and return
	hc.headerCache.Add(hash, header)
	return header
}
func (hc *HeaderChain) GetHeaderByNumber(number uint64) *block.Header {
	return nil
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
