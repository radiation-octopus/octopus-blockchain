package blockchain

import (
	crand "crypto/rand"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/operationUtils"
	"math"
	"math/big"
	mrand "math/rand"
)

type ChainReader interface {
	// 链配置
	//Config() *params.ChainConfig

	// 返回本地块总难度
	GetTd(operationUtils.Hash, uint64) *big.Int
}

type ForkChoice struct {
	chain ChainReader
	rand  *mrand.Rand

	// preserve is a helper function used in td fork choice.
	// Miners will prefer to choose the local mined block if the
	// local td is equal to the extern one. It can be nil for light
	// client
	preserve func(header *block.Header) bool
}

func NewForkChoice(chainReader ChainReader, preserve func(header *block.Header) bool) *ForkChoice {
	// Seed a fast but crypto originating random generator
	seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		//log.Crit("Failed to initialize random seed", "err", err)
	}
	return &ForkChoice{
		chain:    chainReader,
		rand:     mrand.New(mrand.NewSource(seed.Int64())),
		preserve: preserve,
	}
}
