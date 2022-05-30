package blockchain

import (
	"github.com/radiation-octopus/octopus-blockchain/operationUtils"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
)

type Genesis struct {
	Config     *ChainConfig           `json:"config"`
	Nonce      uint64                 `json:"nonce"`
	Timestamp  uint64                 `json:"timestamp"`
	ExtraData  []byte                 `json:"extraData"`
	GasLimit   uint64                 `json:"gasLimit"   gencodec:"required"`
	Difficulty *big.Int               `json:"difficulty" gencodec:"required"`
	Mixhash    operationUtils.Hash    `json:"mixHash"`
	Coinbase   operationUtils.Address `json:"coinbase"`
	//Alloc      GenesisAlloc        `json:"alloc"      gencodec:"required"`

	//这些字段用于一致性测试。请不要在实际的genesis区块中使用它们。
	Number     uint64              `json:"number"`
	GasUsed    uint64              `json:"gasUsed"`
	ParentHash operationUtils.Hash `json:"parentHash"`
	BaseFee    *big.Int            `json:"baseFeePerGas"`
}

// DefaultRopstenGenesisBlock返回Ropsten network genesis块。
func DefaultRopstenGenesisBlock() *Genesis {
	return &Genesis{
		Config:     &ChainConfig{},
		Nonce:      66,
		ExtraData:  utils.Hex2Bytes("0x3535353535353535353535353535353535353535353535353535353535353535"),
		GasLimit:   16777216,
		Difficulty: big.NewInt(1048576),
		//Alloc:      decodePrealloc(ropstenAllocData),
	}
}

func MakeGenesis() *Genesis {
	genesis := DefaultRopstenGenesisBlock()

	//genesis.Config = params.AllEthashProtocolChanges
	//genesis.Config.LondonBlock = londonBlock
	genesis.Difficulty = big.NewInt(131072)

	// 较小的gas限制，便于基本费用移动测试。
	genesis.GasLimit = 8_000_000

	//genesis.Config.ChainID = big.NewInt(18)
	//genesis.Config.EIP150Hash = common.Hash{}

	//genesis.Alloc = core.GenesisAlloc{}
	//for _, faucet := range faucets {
	//	genesis.Alloc[crypto.PubkeyToAddress(faucet.PublicKey)] = core.GenesisAccount{
	//		Balance: new(big.Int).Exp(big.NewInt(2), big.NewInt(128), nil),
	//	}
	//}
	//if londonBlock.Sign() == 0 {
	//	log.Info("Enabled the eip 1559 by default")
	//} else {
	//	log.Info("Registered the london fork", "number", londonBlock)
	//}
	return genesis
}
