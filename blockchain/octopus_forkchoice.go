package blockchain

import (
	crand "crypto/rand"
	"errors"
	"github.com/ethereum/go-ethereum/log"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"math"
	"math/big"
	mrand "math/rand"
)

type ChainReader interface {
	// 链配置
	Config() *entity.ChainConfig

	// 返回本地块总难度
	GetTd(entity.Hash, uint64) *big.Int
}

type ForkChoice struct {
	chain ChainReader
	rand  *mrand.Rand

	// preserve是td fork choice中使用的辅助函数。
	//如果本地td等于外部td，则矿工更愿意选择本地开采区块。对于轻型客户端，它可以为零
	preserve func(header *block2.Header) bool
}

func NewForkChoice(chainReader ChainReader, preserve func(header *block2.Header) bool) *ForkChoice {
	// 种子一个快速但密码源随机生成器
	seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		log.Info("Failed to initialize random seed", "terr", err)
	}
	return &ForkChoice{
		chain:    chainReader,
		rand:     mrand.New(mrand.NewSource(seed.Int64())),
		preserve: preserve,
	}
}

//ReorgNeeded返回reorg是否应该被应用
//基于给定的外部头和当地规范链。
//在td模式下,新负责人如果相应的选择
//总体难度较高。在外面的模式下,信任
//头总是选为头。
func (f *ForkChoice) ReorgNeeded(current *block2.Header, header *block2.Header) (bool, error) {
	var (
		localTD  = f.chain.GetTd(current.Hash(), current.Number.Uint64())
		externTd = f.chain.GetTd(header.Hash(), header.Number.Uint64())
	)
	if localTD == nil || externTd == nil {
		return false, errors.New("missing td")
	}
	//如果已触发转换，则接受新标题作为链头。我们假设转换后的所有头文件都来自可信共识层。
	if ttd := f.chain.Config().TerminalTotalDifficulty; ttd != nil && ttd.Cmp(externTd) <= 0 {
		return true, nil
	}
	// 如果总难度高于已知难度，则将其添加到If语句中的规范链第二子句中，以减少自私挖掘的脆弱性。
	reorg := externTd.Cmp(localTD) > 0
	if !reorg && externTd.Cmp(localTD) == 0 {
		number, headNumber := header.Number.Uint64(), current.Number.Uint64()
		if number < headNumber {
			reorg = true
		} else if number == headNumber {
			var currentPreserve, externPreserve bool
			if f.preserve != nil {
				currentPreserve, externPreserve = f.preserve(current), f.preserve(header)
			}
			reorg = !currentPreserve && (externPreserve || f.rand.Float64() < 0.5)
		}
	}
	return reorg, nil
}
