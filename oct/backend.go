package oct

import (
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/accounts"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/miner"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus/log"
	"math/big"
	"sync"
	"time"
)

type Octopus struct {
	config *Config

	// Handlers
	txPool     *blockchain.TxPool
	Blockchain *blockchain.BlockChain `autoInjectLang:"blockchain.BlockChain"`
	//handler            *blockchain.handler
	//ethDialCandidates  enode.Iterator
	//snapDialCandidates enode.Iterator
	//merger             *consensus.Merger

	// DB interfaces
	chainDb operationdb.Database // 区块链数据库

	//eventMux       *event.TypeMux
	engine         consensus.Engine
	accountManager *accounts.Manager

	//bloomRequests     chan chan *bloombits.Retrieval // 接收bloom数据检索请求的通道
	//bloomIndexer      *core.ChainIndexer             // Bloom索引器在块导入期间运行
	closeBloomHandler chan struct{}

	//APIBackend *operationconsole.OctAPIBackend

	miner    *miner.Miner
	gasPrice *big.Int
	octWork  entity.Address

	networkID uint64
	//netRPCService *ethapi.PublicNetAPI

	//p2pServer *p2p.Server

	lock sync.RWMutex // 保护可变字段（如gas价格和oct）

	//shutdownTracker *shutdowncheck.ShutdownTracker //跟踪节点是否已非正常关闭以及何时关闭
}

func (s *Octopus) GetCfg() *Config                    { return s.config }
func (s *Octopus) AccountManager() *accounts.Manager  { return s.accountManager }
func (s *Octopus) ChainDb() operationdb.Database      { return s.chainDb }
func (s *Octopus) BlockChain() *blockchain.BlockChain { return s.Blockchain }
func (s *Octopus) TxPool() *blockchain.TxPool         { return s.txPool }
func (oct *Octopus) StateAtBlock(b *block.Block, reexec uint64, base *operationdb.OperationDB, checkLive bool, preferDisk bool) (statedb *operationdb.OperationDB, err error) {
	var (
		current  *block.Block
		database operationdb.DatabaseI
		//report   = true
		//origin   = b.NumberU64()
	)
	// 首先检查实时数据库如果状态完全可用，请使用该状态。
	if checkLive {
		statedb, err = oct.Blockchain.StateAt(b.Root())
		if err == nil {
			return statedb, nil
		}
	}
	if base != nil {
		if preferDisk {
			// Create an ephemeral trie.Database for isolating the live one. Otherwise
			// the internal junks created by tracing will be persisted into the disk.
			//database = blockchain.NewDatabaseWithConfig(eth.chainDb, &trie.Config{Cache: 16})
			//if statedb, terr = state.New(b.Root(), database, nil); terr == nil {
			//	log.Info("Found disk backend for state trie", "root", b.Root(), "number", b.Number())
			//	return statedb, nil
			//}
		}
		// 给出了可选的基本状态数据库，将起点标记为父块
		//statedb, database = base, base.Database()
		var number uint64
		current = oct.Blockchain.GetBlock(b.ParentHash(), number)
	} else {
		// 否则，尝试reexec块，直到找到状态或达到极限
		current = b

		// 创建短暂的trie。用于隔离活动数据库。否则，通过跟踪创建的内部垃圾将保留到磁盘中。
		database = operationdb.NewDatabaseWithConfig(oct.chainDb, &operationdb.Config{Cache: 16})

		// 如果我们没有检查脏数据库，一定要检查干净的数据库，否则我们会倒转经过一个持久化的块（特定的角案例是来自genesis的链跟踪）。
		if !checkLive {
			//statedb, terr = state.New(current.Root(), database, nil)
			//if terr == nil {
			//	return statedb, nil
			//}
		}
		// Database does not have the state for the given block, try to regenerate
		for i := uint64(0); i < reexec; i++ {
			if current.NumberU64() == 0 {
				return nil, errors.New("genesis state is missing")
			}
			var number uint64
			parent := oct.Blockchain.GetBlock(current.ParentHash(), number)
			if parent == nil {
				return nil, fmt.Errorf("missing block %v %d", current.ParentHash(), current.NumberU64()-1)
			}
			current = parent

			statedb, err = operationdb.NewOperationDb(current.Root(), database)
			if err == nil {
				break
			}
		}
		//if terr != nil {
		//	switch terr.(type) {
		//	case *trie.MissingNodeError:
		//		return nil, fmt.Errorf("required historical state unavailable (reexec=%d)", reexec)
		//	default:
		//		return nil, terr
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
	//	_, _, _, terr := eth.blockchain.Processor().Process(current, statedb, vm.Config{})
	//	if terr != nil {
	//		return nil, fmt.Errorf("processing block %d failed: %v", current.NumberU64(), terr)
	//	}
	//	// Finalize the state so any modifications are written to the trie
	//	root, terr := statedb.Commit(eth.blockchain.Config().IsEIP158(current.Number()))
	//	if terr != nil {
	//		return nil, fmt.Errorf("stateAtBlock commit failed, number %d root %v: %w",
	//			current.NumberU64(), current.Root().Hex(), terr)
	//	}
	//	statedb, terr = state.New(root, database, nil)
	//	if terr != nil {
	//		return nil, fmt.Errorf("state reset after block %d failed: %v", current.NumberU64(), terr)
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

type Config struct {
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

	//事务池选项
	TxPool blockchain.TxPoolConfig

	Miner miner.Config

	// RPCTxFeeCap是发送交易变体的全局交易费（价格*gaslimit）上限。单位为oct。
	RPCTxFeeCap float64
}

func (oct *Octopus) start() {
	log.Info("oct starting")
	New(oct)
	log.Info("oct 启动完成")
}

func (oct *Octopus) close() {

}

func New(oct *Octopus) (*Octopus, error) {
	genesis := blockchain.MakeGenesis()
	cfg := &Config{
		Genesis:   genesis,
		NetworkId: genesis.Config.ChainID.Uint64(),
		//SyncMode:        downloader.FullSync,
		DatabaseCache:   256,
		DatabaseHandles: 256,
		TxPool:          blockchain.DefaultTxPoolConfig,
		//GPO:             ethconfig.Defaults.GPO,
		//Octell:          ethconfig.Defaults.Octell,
		Miner: miner.Config{
			Etherbase: entity.Address{1},
			GasCeil:   genesis.GasLimit * 11 / 10,
			GasPrice:  big.NewInt(1),
			Recommit:  time.Second,
		},
		RPCTxFeeCap: 1,
	}
	var (
		backends []accounts.Backend
		n, p     = accounts.StandardScryptN, accounts.StandardScryptP
	)
	backends = append(backends, accounts.NewKeyStore("keystore", n, p))
	//oct = &Octopus{
	//	config : 			cfg,
	//	//merger:            merger,
	//	//eventMux:          stack.EventMux(),
	//	accountManager:    	accounts.NewManager(&accounts.Config{InsecureUnlockAllowed: true},backends...),
	//	//engine:            ethconfig.CreateConsensusEngine(stack, chainConfig, &ethashConfig, config.Miner.Notify, config.Miner.Noverify, chainDb),
	//	closeBloomHandler: 	make(chan struct{}),
	//	txPool: 			blockchain.NewTxPool(blockchain.DefaultTxPoolConfig, oct.Blockchain),
	//	//miner :				miner.New(oct, &cfg.Miner, oct.engine),
	//	networkID:         cfg.NetworkId,
	//	gasPrice:          	cfg.Miner.GasPrice,
	//	//etherbase:         config.Miner.Etherbase,
	//	//bloomRequests:     make(chan chan *bloombits.Retrieval),
	//	//bloomIndexer:      core.NewBloomIndexer(chainDb, params.BloomBitsBlocks, params.BloomConfirms),
	//	//p2pServer:         stack.Server(),
	//	//shutdownTracker:   shutdowncheck.NewShutdownTracker(chainDb),
	//}
	oct.config = cfg
	oct.accountManager = accounts.NewManager(&accounts.Config{InsecureUnlockAllowed: true}, backends...)
	oct.closeBloomHandler = make(chan struct{})
	oct.txPool = blockchain.NewTxPool(blockchain.DefaultTxPoolConfig, oct.Blockchain)
	oct.networkID = cfg.NetworkId
	oct.gasPrice = cfg.Miner.GasPrice
	oct.miner = miner.New(oct, &cfg.Miner, oct.engine)
	return oct, nil
}

func (s *Octopus) IsMining() bool { return s.miner.Mining() }

// StartMining使用给定数量的CPU线程启动miner。
//如果挖掘已在运行，此方法将调整允许使用的线程数，并更新事务池所需的最低价格。
func (s *Octopus) StartMining(threads int) error {
	// 更新共识引擎中的线程数
	type threaded interface {
		SetThreads(threads int)
	}
	if th, ok := s.engine.(threaded); ok {
		log.Info("Updated mining threads", "threads", threads)
		if threads == 0 {
			threads = -1 // 从内部禁用矿工
		}
		th.SetThreads(threads)
	}
	// 如果矿工没有运行，初始化它
	if !s.IsMining() {
		// 将初始价格点传播到交易池
		s.lock.RLock()
		price := s.gasPrice
		s.lock.RUnlock()
		s.txPool.SetGasPrice(price)

		// 配置本地工作地址
		eb, err := s.OctWork()
		if err != nil {
			log.Error("Cannot start mining without etherbase", "err", err)
			return fmt.Errorf("etherbase missing: %v", err)
		}
		//var cli *clique.Clique
		//if c, ok := s.engine.(*clique.Clique); ok {
		//	cli = c
		//} else if cl, ok := s.engine.(*beacon.Beacon); ok {
		//	if c, ok := cl.InnerEngine().(*clique.Clique); ok {
		//		cli = c
		//	}
		//}
		//if cli != nil {
		//	wallet, err := s.accountManager.Find(accounts.Account{Address: eb})
		//	if wallet == nil || err != nil {
		//		log.Error("Etherbase account unavailable locally", "err", err)
		//		return fmt.Errorf("signer missing: %v", err)
		//	}
		//	cli.Authorize(eb, wallet.SignData)
		//}
		//// 如果开始挖掘，我们可以禁用为加快同步时间而引入的事务拒绝机制。
		//atomic.StoreUint32(&s.handler.acceptTxs, 1)

		go s.miner.Start(eb)
	}
	return nil
}

func (s *Octopus) OctWork() (eb entity.Address, err error) {
	s.lock.RLock()
	octWork := s.octWork
	s.lock.RUnlock()

	if octWork != (entity.Address{}) {
		return octWork, nil
	}
	if wallets := s.AccountManager().Wallets(); len(wallets) > 0 {
		if accounts := wallets[0].Accounts(); len(accounts) > 0 {
			octWork := accounts[0].Address

			s.lock.Lock()
			s.octWork = octWork
			s.lock.Unlock()

			log.Info("OctWork automatically configured", "address", octWork)
			return octWork, nil
		}
	}
	return entity.Address{}, fmt.Errorf("etherbase must be explicitly specified")
}
