package blockchainStart

import (
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/blockchain/blockchainconfig"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	genesis2 "github.com/radiation-octopus/octopus-blockchain/entity/genesis"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/node"
	"github.com/radiation-octopus/octopus-blockchain/oct/octconfig"
	"github.com/radiation-octopus/octopus/db"
	"math/big"
	"time"
)

//区块链启动配置cfg结构体
type BlockChainStart struct {
	Db        *db.DbStart            `autoRelyonLang:"db.DbStart"`
	Bc        *blockchain.BlockChain `autoInjectLang:"blockchain.BlockChain"`
	NodeStart *node.NodeStart        `autoRelyonLang:"node.NodeStart"`
	OctConfig *octconfig.Config      `autoInjectLang:"octconfig.Config"`
}

func (bc *BlockChainStart) Start() {
	log.Info("blockchain Starting")
	//构造创世区块
	genesis := genesis2.MakeGenesis()
	bc.OctConfig.Genesis = genesis
	bc.OctConfig.NetworkId = genesis.Config.ChainID.Uint64()
	bc.OctConfig.DatabaseCache = 256
	bc.OctConfig.DatabaseHandles = 256
	bc.OctConfig.TxPool = blockchainconfig.DefaultTxPoolConfig
	bc.OctConfig.Miner.Octerbase = entity.Address{1}
	bc.OctConfig.Miner.GasCeil = genesis.GasLimit * 11 / 10
	bc.OctConfig.Miner.GasPrice = big.NewInt(1)
	bc.OctConfig.Miner.Recommit = time.Second
	bc.OctConfig.RPCTxFeeCap = 1
	//组装辐射章鱼对象
	chainDb, err := bc.NodeStart.Node.OpenDatabaseWithFreezer("chaindata", bc.OctConfig.DatabaseCache, bc.OctConfig.DatabaseHandles, bc.OctConfig.DatabaseFreezer, "oct/db/chaindata/", false)
	if err != nil {
		errors.New("chainDb start failed")
	}
	//bc.Bc.db = chainDb
	chainConfig, _, _ := genesis2.SetupGenesisBlockWithOverride(chainDb, genesis, nil, nil)
	var cacheConfig = &blockchain.CacheConfig{
		//TrieCleanLimit:      config.TrieCleanCache,
		//TrieCleanJournal:    stack.ResolvePath(config.TrieCleanCacheJournal),
		//TrieCleanRejournal:  config.TrieCleanCacheRejournal,
		//TrieCleanNoPrefetch: config.NoPrefetch,
		//TrieDirtyLimit:      config.TrieDirtyCache,
		//TrieDirtyDisabled:   config.NoPruning,
		//TrieTimeLimit:       config.TrieTimeout,
		//SnapshotLimit:       config.SnapshotCache,
		//Preimages:           config.Preimages,
	}
	//初始化区块链
	_, err = blockchain.NewBlockChain(bc.Bc, chainDb, cacheConfig, chainConfig, nil, nil)
	if err != nil {
		errors.New("blockchain start fail")
	}
	log.Info("blockchain 启动完成")
}
