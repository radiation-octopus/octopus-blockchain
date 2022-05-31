package oct

import (
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/miner"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"math/big"
	"sync"
	"time"
)

type Octopus struct {
	// Handlers
	txPool     *blockchain.TxPool
	blockchain *blockchain.BlockChain
	//handler            *blockchain.handler
	//ethDialCandidates  enode.Iterator
	//snapDialCandidates enode.Iterator
	//merger             *consensus.Merger

	// DB interfaces
	chainDb operationdb.Database // 区块链数据库

	//eventMux       *event.TypeMux
	engine consensus.Engine
	//accountManager *accounts.Manager

	//bloomRequests     chan chan *bloombits.Retrieval // 接收bloom数据检索请求的通道
	//bloomIndexer      *core.ChainIndexer             // Bloom索引器在块导入期间运行
	closeBloomHandler chan struct{}

	//APIBackend *OctAPIBackend

	miner     *miner.Miner
	gasPrice  *big.Int
	etherbase entity.Address

	networkID uint64
	//netRPCService *ethapi.PublicNetAPI

	//p2pServer *p2p.Server

	lock sync.RWMutex // 保护可变字段（如gas价格和oct）

	//shutdownTracker *shutdowncheck.ShutdownTracker //跟踪节点是否已非正常关闭以及何时关闭
}

func (s *Octopus) BlockChain() *blockchain.BlockChain { return s.blockchain }
func (s *Octopus) TxPool() *blockchain.TxPool         { return s.txPool }
func (eth *Octopus) StateAtBlock(b *block.Block, reexec uint64, base *operationdb.OperationDB, checkLive bool, preferDisk bool) (statedb *operationdb.OperationDB, err error) {
	var (
		current  *block.Block
		database operationdb.Database
		//report   = true
		//origin   = b.NumberU64()
	)
	// 首先检查实时数据库如果状态完全可用，请使用该状态。
	if checkLive {
		statedb, err = eth.blockchain.StateAt(b.Root())
		if err == nil {
			return statedb, nil
		}
	}
	if base != nil {
		if preferDisk {
			// Create an ephemeral trie.Database for isolating the live one. Otherwise
			// the internal junks created by tracing will be persisted into the disk.
			//database = blockchain.NewDatabaseWithConfig(eth.chainDb, &trie.Config{Cache: 16})
			//if statedb, err = state.New(b.Root(), database, nil); err == nil {
			//	log.Info("Found disk backend for state trie", "root", b.Root(), "number", b.Number())
			//	return statedb, nil
			//}
		}
		// 给出了可选的基本状态数据库，将起点标记为父块
		//statedb, database = base, base.Database()
		current = eth.blockchain.GetBlock(b.ParentHash())
	} else {
		// Otherwise try to reexec blocks until we find a state or reach our limit
		current = b

		// 创建短暂的trie。用于隔离活动数据库。否则，通过跟踪创建的内部垃圾将保留到磁盘中。
		//database = state.NewDatabaseWithConfig(eth.chainDb, &trie.Config{Cache: 16})

		// 如果我们没有检查脏数据库，一定要检查干净的数据库，否则我们会倒转经过一个持久化的块（特定的角案例是来自genesis的链跟踪）。
		if !checkLive {
			//statedb, err = state.New(current.Root(), database, nil)
			//if err == nil {
			//	return statedb, nil
			//}
		}
		// Database does not have the state for the given block, try to regenerate
		for i := uint64(0); i < reexec; i++ {
			if current.NumberU64() == 0 {
				return nil, errors.New("genesis state is missing")
			}
			parent := eth.blockchain.GetBlock(current.ParentHash())
			if parent == nil {
				return nil, fmt.Errorf("missing block %v %d", current.ParentHash(), current.NumberU64()-1)
			}
			current = parent

			statedb, err = operationdb.New(current.Root(), &database)
			if err == nil {
				break
			}
		}
		//if err != nil {
		//	switch err.(type) {
		//	case *trie.MissingNodeError:
		//		return nil, fmt.Errorf("required historical state unavailable (reexec=%d)", reexec)
		//	default:
		//		return nil, err
		//	}
		//}
	}
	// State was available at historical point, regenerate
	//var (
	//	start  = time.Now()
	//	logged time.Time
	//	parent operationutils.Hash
	//)
	//for current.NumberU64() < origin {
	//	// Print progress logs if long enough time elapsed
	//	if time.Since(logged) > 8*time.Second && report {
	//		log.Info("Regenerating historical state", "block", current.NumberU64()+1, "target", origin, "remaining", origin-current.NumberU64()-1, "elapsed", time.Since(start))
	//		logged = time.Now()
	//	}
	//	// Retrieve the next block to regenerate and process it
	//	next := current.NumberU64() + 1
	//	if current = eth.blockchain.GetBlockByNumber(next); current == nil {
	//		return nil, fmt.Errorf("block #%d not found", next)
	//	}
	//	_, _, _, err := eth.blockchain.Processor().Process(current, statedb, vm.Config{})
	//	if err != nil {
	//		return nil, fmt.Errorf("processing block %d failed: %v", current.NumberU64(), err)
	//	}
	//	// Finalize the state so any modifications are written to the trie
	//	root, err := statedb.Commit(eth.blockchain.Config().IsEIP158(current.Number()))
	//	if err != nil {
	//		return nil, fmt.Errorf("stateAtBlock commit failed, number %d root %v: %w",
	//			current.NumberU64(), current.Root().Hex(), err)
	//	}
	//	statedb, err = state.New(root, database, nil)
	//	if err != nil {
	//		return nil, fmt.Errorf("state reset after block %d failed: %v", current.NumberU64(), err)
	//	}
	//	database.TrieDB().Reference(root, common.Hash{})
	//	if parent != (common.Hash{}) {
	//		database.TrieDB().Dereference(parent)
	//	}
	//	parent = root
	//}
	//if report {
	//	nodes, imgs := database.TrieDB().Size()
	//	log.Info("Historical state regenerated", "block", current.NumberU64(), "elapsed", time.Since(start), "nodes", nodes, "preimages", imgs)
	//}
	return statedb, nil
}

type config struct {
	//genesis块，如果数据库为空，则插入该块。
	//如果为零，则使用以太坊主网络块。
	Genesis *blockchain.Genesis `toml:",omitempty"`

	NetworkId uint64 // 用于选择要连接到的对等方的网络ID

	//SyncMode  downloader.SyncMode

	//将为要连接的节点查询这些URL。
	EthDiscoveryURLs  []string
	SnapDiscoveryURLs []string

	DatabaseHandles int `toml:"-"`
	DatabaseCache   int

	// Transaction pool options
	TxPool blockchain.TxPoolConfig

	Miner miner.Config
}

func (oct *Octopus) start() {
	//New(oct)
}

func (oct *Octopus) close() {

}

func New(oct *Octopus) (*Octopus, error) {
	genesis := blockchain.MakeGenesis()
	cfg := &config{
		Genesis: genesis,
		//NetworkId:       big.Int.Uint64(1),
		//SyncMode:        downloader.FullSync,
		DatabaseCache:   256,
		DatabaseHandles: 256,
		TxPool:          blockchain.DefaultTxPoolConfig,
		//GPO:             ethconfig.Defaults.GPO,
		//Ethash:          ethconfig.Defaults.Ethash,
		Miner: miner.Config{
			Etherbase: entity.Address{1},
			GasCeil:   genesis.GasLimit * 11 / 10,
			GasPrice:  big.NewInt(1),
			Recommit:  time.Second,
		},
	}
	oct = &Octopus{
		//config:            config,
		//merger:            merger,
		//eventMux:          stack.EventMux(),
		//accountManager:    stack.AccountManager(),
		//engine:            ethconfig.CreateConsensusEngine(stack, chainConfig, &ethashConfig, config.Miner.Notify, config.Miner.Noverify, chainDb),
		closeBloomHandler: make(chan struct{}),
		//networkID:         config.NetworkId,
		//gasPrice:          config.Miner.GasPrice,
		//etherbase:         config.Miner.Etherbase,
		////bloomRequests:     make(chan chan *bloombits.Retrieval),
		//bloomIndexer:      core.NewBloomIndexer(chainDb, params.BloomBitsBlocks, params.BloomConfirms)
		//p2pServer:         stack.Server(),
		//shutdownTracker:   shutdowncheck.NewShutdownTracker(chainDb),
	}
	oct.miner = miner.New(oct, &cfg.Miner, oct.engine)

	return oct, nil
}
