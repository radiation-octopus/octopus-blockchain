package blockchain

import (
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"math/big"
)

var (
	//初始交易数量限制
	TxLookupLimit uint64

	//blockchain 主方法
	BindingMethod string

	//blockchain 结构体
	BindingStruct string

	AllOctellProtocolChanges = &entity.ChainConfig{big.NewInt(1337), big.NewInt(0), nil, false, big.NewInt(0), entity.Hash{}, big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), nil, nil, ""}
)
