package blockchain

import (
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus/log"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
)

type Genesis struct {
	Config     *ChainConfig   `json:"config"`
	Nonce      uint64         `json:"nonce"`
	Timestamp  uint64         `json:"timestamp"`
	ExtraData  []byte         `json:"extraData"`
	GasLimit   uint64         `json:"gasLimit"   gencodec:"required"`
	Difficulty *big.Int       `json:"difficulty" gencodec:"required"`
	Mixhash    entity.Hash    `json:"mixHash"`
	Coinbase   entity.Address `json:"coinbase"`
	//Alloc      GenesisAlloc        `json:"alloc"      gencodec:"required"`

	//这些字段用于一致性测试。请不要在实际的genesis区块中使用它们。
	Number     uint64      `json:"number"`
	GasUsed    uint64      `json:"gasUsed"`
	ParentHash entity.Hash `json:"parentHash"`
	BaseFee    *big.Int    `json:"baseFeePerGas"`
}

// Commit将genesis规范的块和状态写入数据库。该块作为规范头块提交。
func (g *Genesis) Commit(db operationdb.Database) (*block.Block, error) {
	block := g.ToBlock(db)
	if block.Number().Sign() != 0 {
		return nil, errors.New("can't commit genesis block with number > 0")
	}
	config := g.Config
	if config == nil {
		config = AllEthashProtocolChanges
	}
	//if err := config.CheckConfigForkOrder(); err != nil {
	//	return nil, err
	//}
	//if config.Clique != nil && len(block.Extra()) < 32+crypto.SignatureLength {
	//	return nil, errors.New("can't start clique chain without signers")
	//}
	//if err := g.Alloc.write(db, block.Hash()); err != nil {
	//	return nil, err
	//}
	operationdb.WriteTd(block.Hash(), block.NumberU64(), block.Difficulty())
	operationdb.WriteBlock(block)
	operationdb.WriteReceipts(block.Hash(), nil)
	operationdb.WriteCanonicalHash(block.Hash(), block.NumberU64())
	operationdb.WriteHeadBlockHash(block.Hash())
	//operationdb.WriteHeadFastBlockHash(db, block.Hash())
	operationdb.WriteHeadHeaderHash(block.Hash())
	//operationdb.WriteChainConfig(db, block.Hash(), config)
	return block, nil
}

// ToBlock创建genesis块并将genesis规范的状态写入给定数据库（如果为nil，则丢弃）。
func (g *Genesis) ToBlock(db operationdb.Database) *block.Block {
	//if db == nil {
	//	db = rawdb.NewMemoryDatabase()
	//}
	//root, err := g.Alloc.flush(db)
	//if err != nil {
	//	panic(err)
	//}
	head := &block.Header{
		Number:     new(big.Int).SetUint64(g.Number),
		Nonce:      block.EncodeNonce(g.Nonce),
		Time:       g.Timestamp,
		ParentHash: g.ParentHash,
		//Extra:      g.ExtraData,
		GasLimit:   g.GasLimit,
		GasUsed:    g.GasUsed,
		BaseFee:    g.BaseFee,
		Difficulty: g.Difficulty,
		MixDigest:  g.Mixhash,
		Coinbase:   g.Coinbase,
		//Root:       root,
	}
	if g.GasLimit == 0 {
		head.GasLimit = entity.GenesisGasLimit
	}
	if g.Difficulty == nil && g.Mixhash == (entity.Hash{}) {
		head.Difficulty = entity.GenesisDifficulty
	}
	//if g.Config != nil && g.Config.IsLondon(operationUtils.Big0) {
	//	if g.BaseFee != nil {
	//		head.BaseFee = g.BaseFee
	//	} else {
	//		head.BaseFee = new(big.Int).SetUint64(params.InitialBaseFee)
	//	}
	//}
	return block.NewBlock(head, nil, nil)
}

// DefaultRopstenGenesisBlock返回Ropsten network genesis块。
func DefaultRopstenGenesisBlock() *Genesis {
	return &Genesis{
		Config:     &ChainConfig{ChainID: big.NewInt(3)},
		Nonce:      66,
		ExtraData:  utils.Hex2Bytes("0x3535353535353535353535353535353535353535353535353535353535353535"),
		GasLimit:   16777216,
		Difficulty: big.NewInt(1048576),
		//Alloc:      decodePrealloc(ropstenAllocData),
	}
}

// DefaultGenesisBlock返回以太坊主网络genesis块。
func DefaultGenesisBlock() *Genesis {
	return &Genesis{
		Config:     &ChainConfig{},
		Nonce:      66,
		ExtraData:  utils.Hex2Bytes("0x11bbe8db4e347b4e8c937c1c8370e4b5ed33adb3db69cbdb7a38e1e50b1b82fa"),
		GasLimit:   5000,
		Difficulty: big.NewInt(17179869184),
		//Alloc:      decodePrealloc(mainnetAllocData),
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

func SetupGenesisBlockWithOverride(db operationdb.Database, genesis *Genesis, overrideArrowGlacier, overrideTerminalTotalDifficulty *big.Int) (*ChainConfig, entity.Hash, error) {
	//if genesis != nil && genesis.Config == nil {
	//	return params.AllEthashProtocolChanges, entity.Hash{}, errGenesisNoConfig
	//}
	// 如果没有存储的genesis块，只需提交新块即可。
	stored := operationdb.ReadCanonicalHash(0)
	if (stored == entity.Hash{}) {
		if genesis == nil {
			log.Info("Writing default main-net genesis block")
			genesis = DefaultGenesisBlock()
		} else {
			log.Info("Writing custom genesis block")
		}
		block, err := genesis.Commit(db)
		if err != nil {
			return genesis.Config, entity.Hash{}, err
		}
		return genesis.Config, block.Hash(), nil
	}
	// 数据库中有genesis块（可能在以前数据库中），但缺少相应的状态。
	//header := operationdb.ReadHeader(&db, stored, 0)
	//if _, err := state.New(header.Root, state.NewDatabaseWithConfig(db, nil), nil); err != nil {
	//	if genesis == nil {
	//		genesis = DefaultGenesisBlock()
	//	}
	//	// 确保存储的genesis与给定的genesis匹配。
	//	hash := genesis.ToBlock(db).Hash()
	//	if hash != stored {
	//		return genesis.Config, hash, &GenesisMismatchError{stored, hash}
	//	}
	//	block, err := genesis.Commit(db)
	//	if err != nil {
	//		return genesis.Config, hash, err
	//	}
	//	return genesis.Config, block.Hash(), nil
	//}
	//// 检查genesis块是否已写入。
	//if genesis != nil {
	//	hash := genesis.ToBlock(nil).Hash()
	//	if hash != stored {
	//		return genesis.Config, hash, &GenesisMismatchError{stored, hash}
	//	}
	//}
	////
	//newcfg := genesis.configOrDefault(stored)
	//if overrideArrowGlacier != nil {
	//	newcfg.ArrowGlacierBlock = overrideArrowGlacier
	//}
	//if overrideTerminalTotalDifficulty != nil {
	//	newcfg.TerminalTotalDifficulty = overrideTerminalTotalDifficulty
	//}
	//if err := newcfg.CheckConfigForkOrder(); err != nil {
	//	return newcfg, common.Hash{}, err
	//}
	//storedcfg := rawdb.ReadChainConfig(db, stored)
	//if storedcfg == nil {
	//	log.Warn("Found genesis block without chain config")
	//	rawdb.WriteChainConfig(db, stored, newcfg)
	//	return newcfg, stored, nil
	//}
	//// 特殊情况：如果正在使用专用网络（数据库中没有genesis，也没有mainnet哈希），
	////我们不能应用'configOrDefault'链配置，因为这将是AllProtocolChanges（在现有专用网络genesis块上应用任何新分支）。
	////在这种情况下，仅应用替代。
	//if genesis == nil && stored != params.MainnetGenesisHash {
	//	newcfg = storedcfg
	//	if overrideArrowGlacier != nil {
	//		newcfg.ArrowGlacierBlock = overrideArrowGlacier
	//	}
	//	if overrideTerminalTotalDifficulty != nil {
	//		newcfg.TerminalTotalDifficulty = overrideTerminalTotalDifficulty
	//	}
	//}
	//// 检查配置兼容性并写入配置。兼容性错误将返回给调用者，除非我们已经处于块零。
	//height := rawdb.ReadHeaderNumber(db, rawdb.ReadHeadHeaderHash(db))
	//if height == nil {
	//	return newcfg, stored, fmt.Errorf("missing block number for head header hash")
	//}
	//compatErr := storedcfg.CheckCompatible(newcfg, *height)
	//if compatErr != nil && *height != 0 && compatErr.RewindTo != 0 {
	//	return newcfg, stored, compatErr
	//}
	//rawdb.WriteChainConfig(db, stored, newcfg)
	return nil, stored, nil
}
