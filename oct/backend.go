package oct

import (
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/accounts"
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/blockchain/blockchainconfig"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/miner"
	"github.com/radiation-octopus/octopus-blockchain/oct/octconfig"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus-blockchain/operationdb/tire"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus-blockchain/vm"
	"github.com/radiation-octopus/octopus/log"
	"math/big"
	"sync"
	"time"
)

type Octopus struct {
	config *octconfig.Config

	// Handlers
	txPool     *blockchain.TxPool
	Blockchain *blockchain.BlockChain `autoInjectLang:"blockchain.BlockChain"`
	//handler            *blockchain.handler
	//ethDialCandidates  enode.Iterator
	//snapDialCandidates enode.Iterator
	//merger             *consensus.Merger

	// DB interfaces
	chainDb typedb.Database // 区块链数据库

	//eventMux       *event.TypeMux
	Engine         consensus.Engine `autoInjectLang:"octell.Octell"`
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

func (s *Octopus) GetCfg() *octconfig.Config          { return s.config }
func (s *Octopus) AccountManager() *accounts.Manager  { return s.accountManager }
func (s *Octopus) ChainDb() typedb.Database           { return s.chainDb }
func (s *Octopus) BlockChain() *blockchain.BlockChain { return s.Blockchain }
func (s *Octopus) TxPool() *blockchain.TxPool         { return s.txPool }
func (oct *Octopus) StateAtBlock(b *block2.Block, reexec uint64, base *operationdb.OperationDB, checkLive bool, preferDisk bool) (db *operationdb.OperationDB, err error) {
	var (
		current  *block2.Block
		database operationdb.DatabaseI
		report   = true
		origin   = b.NumberU64()
	)
	// 首先检查实时数据库如果状态完全可用，请使用该状态。
	if checkLive {
		db, err = oct.Blockchain.StateAt(b.Root())
		if err == nil {
			return db, nil
		}
	}
	if base != nil {
		if preferDisk {
			// 创建短暂的trie。用于隔离活动数据库。否则，通过跟踪创建的内部垃圾将保留到磁盘中。
			database = operationdb.NewDatabaseWithConfig(oct.chainDb, &tire.Config{Cache: 16})
			if db, err = operationdb.NewOperationDb(b.Root(), database); err == nil {
				log.Info("Found disk backend for operation trie", "root", b.Root(), "number", b.Number())
				return db, nil
			}
		}
		// 给出了可选的基本状态数据库，将起点标记为父块
		db, database, report = base, base.Database(), false
		var number uint64
		current = oct.Blockchain.GetBlock(b.ParentHash(), number)
	} else {
		// 否则，尝试reexec块，直到找到状态或达到极限
		current = b

		// 创建短暂的trie。用于隔离活动数据库。否则，通过跟踪创建的内部垃圾将保留到磁盘中。
		database = operationdb.NewDatabaseWithConfig(oct.chainDb, &tire.Config{Cache: 16})

		// 如果我们没有检查脏数据库，一定要检查干净的数据库，否则我们会倒转经过一个持久化的块（特定的角案例是来自genesis的链跟踪）。
		if !checkLive {
			db, err = operationdb.NewOperationDb(current.Root(), database)
			if err == nil {
				return db, nil
			}
		}
		// 数据库没有给定块的状态，请尝试重新生成
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

			db, err = operationdb.NewOperationDb(current.Root(), database)
			if err == nil {
				break
			}
		}
		if err != nil {
			switch err.(type) {
			case *tire.MissingNodeError:
				return nil, fmt.Errorf("required historical state unavailable (reexec=%d)", reexec)
			default:
				return nil, err
			}
		}
	}
	// 状态在历史点可用，重新生成
	var (
		start  = time.Now()
		logged time.Time
		parent entity.Hash
	)
	for current.NumberU64() < origin {
		// 如果经过足够长的时间，则打印进度日志
		if time.Since(logged) > 8*time.Second && report {
			log.Info("Regenerating historical state", "block", current.NumberU64()+1, "target", origin, "remaining", origin-current.NumberU64()-1, "elapsed", time.Since(start))
			logged = time.Now()
		}
		//检索下一个要重新生成并处理的块
		next := current.NumberU64() + 1
		if current = oct.Blockchain.GetBlockByNumber(next); current == nil {
			return nil, fmt.Errorf("block #%d not found", next)
		}
		_, _, _, terr := oct.Blockchain.Processor().Process(current, db, vm.Config{})
		if terr != nil {
			return nil, fmt.Errorf("processing block %d failed: %v", current.NumberU64(), terr)
		}
		// 最终确定状态，以便将任何修改写入trie
		root, terr := db.Commit(oct.Blockchain.Config().IsEIP158(current.Number()))
		if terr != nil {
			return nil, fmt.Errorf("stateAtBlock commit failed, number %d root %v: %w",
				current.NumberU64(), current.Root().Hex(), terr)
		}
		db, terr = operationdb.NewOperationDb(root, database)
		if terr != nil {
			return nil, fmt.Errorf("state reset after block %d failed: %v", current.NumberU64(), terr)
		}
		database.TrieDB().Reference(root, entity.Hash{})
		if parent != (entity.Hash{}) {
			database.TrieDB().Dereference(parent)
		}
		parent = root
	}
	if report {
		nodes, imgs := database.TrieDB().Size()
		log.Info("Historical state regenerated", "block", current.NumberU64(), "elapsed", time.Since(start), "nodes", nodes, "preimages", imgs)
	}
	return db, nil
}

func (oct *Octopus) start(config *octconfig.Config) {
	log.Info("oct starting")
	New(oct, config)
	log.Info("oct 启动完成")
}

func (oct *Octopus) close() {

}

func New(oct *Octopus, cfg *octconfig.Config) (*Octopus, error) {

	var (
		backends []accounts.Backend
		n, p     = accounts.StandardScryptN, accounts.StandardScryptP
	)
	backends = append(backends, accounts.NewKeyStore("keystore", n, p))
	oct.config = cfg
	oct.accountManager = accounts.NewManager(&accounts.Config{InsecureUnlockAllowed: true}, backends...)
	oct.closeBloomHandler = make(chan struct{})
	oct.txPool = blockchain.NewTxPool(blockchainconfig.DefaultTxPoolConfig, oct.Blockchain)
	oct.networkID = cfg.NetworkId
	oct.gasPrice = cfg.Miner.GasPrice
	oct.chainDb = oct.Blockchain.GetDB()
	oct.miner = miner.New(oct, &cfg.Miner, oct.Blockchain.Config(), oct.Engine)
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
	if th, ok := s.Engine.(threaded); ok {
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
