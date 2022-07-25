package oct

import (
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/consensus/beacon"
	"github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/entity/forkid"
	"github.com/radiation-octopus/octopus-blockchain/oct/fetcher"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/event"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/oct/downloader"
	"github.com/radiation-octopus/octopus-blockchain/oct/protocols/eth"
	"github.com/radiation-octopus/octopus-blockchain/oct/protocols/snap"
	"github.com/radiation-octopus/octopus-blockchain/p2p"
	"github.com/radiation-octopus/octopus-blockchain/params"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
)

const (
	// txChanSize是侦听NewTxsEvent的通道的大小。该数字参考tx池的大小。
	txChanSize = 4096
)

var (
	syncChallengeTimeout = 15 * time.Second // 节点回复同步进度质询的时间余量
)

// txPool定义了事务池实现所需的方法，以支持辐射章鱼链协议所需的所有操作。
type txPool interface {
	// Has返回一个指示符，指示txpool是否使用给定哈希缓存了事务。
	Has(hash entity.Hash) bool

	// Get使用给定的tx哈希从本地txpool检索事务。
	Get(hash entity.Hash) *block.Transaction

	// AddRemotes应该将给定的事务添加到池中。
	AddRemotes([]*block.Transaction) []error

	// 挂起应返回挂起的事务。调用方应该可以修改切片。
	Pending(enforceTips bool) map[entity.Address]block.Transactions

	// SubscribeNewTxsEvent应返回NewTxsEvent的事件订阅，并将事件发送到给定通道。
	SubscribeNewTxsEvent(chan<- event.NewTxsEvent) event.Subscription
}

// handlerConfig是初始化参数的集合，用于创建完整的节点网络处理程序。
type handlerConfig struct {
	Database       typedb.Database           // 用于直接同步插入的数据库
	Chain          *blockchain.BlockChain    // 提供数据的区块链
	TxPool         txPool                    // 要从中传播的事务池
	Merger         *consensus.Merger         // oct1/2转换经理
	Network        uint64                    // adfvertise的网络标识符
	Sync           downloader.SyncMode       // 是快照还是完全同步
	BloomCache     uint64                    // 将兆字节分配给snap sync bloom
	EventMux       *event.TypeMux            // 旧式事件多路复用器，不推荐用于“feed”`
	Checkpoint     *params.TrustedCheckpoint // 用于同步挑战的硬编码检查点
	RequiredBlocks map[uint64]entity.Hash    // 同步挑战所需块哈希的硬编码映射
}

type handler struct {
	networkID  uint64
	forkFilter forkid.Filter // Fork ID filter，在节点生存期内保持不变

	snapSync  uint32 // 标记是否启用快照同步（如果我们已经有块，则禁用）
	acceptTxs uint32 // 标记我们是否被视为同步（启用事务处理）

	checkpointNumber uint64      // 同步进度验证器交叉引用的块编号
	checkpointHash   entity.Hash // 阻止同步进度验证程序交叉引用的哈希

	database typedb.Database
	txpool   txPool
	chain    *blockchain.BlockChain
	maxPeers int

	downloader   *downloader.Downloader
	blockFetcher *fetcher.BlockFetcher
	txFetcher    *fetcher.TxFetcher
	peers        *peerSet
	merger       *consensus.Merger

	eventMux      *event.TypeMux
	txsCh         chan event.NewTxsEvent
	txsSub        event.Subscription
	minedBlockSub *event.TypeMuxSubscription

	requiredBlocks map[uint64]entity.Hash

	// 取数器、同步器、txsyncLoop的通道
	quitSync chan struct{}

	chainSync *chainSyncer
	wg        sync.WaitGroup
	peerWG    sync.WaitGroup
}

// newHandler返回所有辐射章鱼链管理协议的处理程序。
func newHandler(config *handlerConfig) (*handler, error) {
	// 使用基本字段创建协议管理器
	if config.EventMux == nil {
		config.EventMux = new(event.TypeMux) // 测试的精确初始化
	}
	h := &handler{
		networkID:      config.Network,
		forkFilter:     forkid.NewFilter(config.Chain),
		eventMux:       config.EventMux,
		database:       config.Database,
		txpool:         config.TxPool,
		chain:          config.Chain,
		peers:          newPeerSet(),
		merger:         config.Merger,
		requiredBlocks: config.RequiredBlocks,
		quitSync:       make(chan struct{}),
	}
	if config.Sync == downloader.FullSync {
		// 数据库似乎是空的，因为当前块是genesis。
		//然而，捕捉块在前面，因此在某个点为该节点启用了捕捉同步。
		//如果用户手动（或通过坏块）将快照同步节点回滚到同步点下方，则可能发生这种情况。
		//*上次快照同步未完成，而用户这次指定了完全同步。但我们没有任何最近的状态进行完全同步。
		//然而，在这些情况下，重新启用snap sync是安全的。
		fullBlock := h.chain.CurrentBlock()
		if fullBlock.NumberU64() == 0 {
			h.snapSync = uint32(1)
			log.Warn("Switch sync mode from full sync to snap sync")
		}
	} else {
		if h.chain.CurrentBlock().NumberU64() > 0 {
			// 如果数据库不为空，则打印警告日志以运行快照同步。
			log.Warn("Switch sync mode from snap sync to full sync")
		} else {
			// 如果请求了快照同步，而我们的数据库为空，请授予它
			h.snapSync = uint32(1)
		}
	}
	// 如果我们有可信的检查点，就在链上强制执行它们
	if config.Checkpoint != nil {
		h.checkpointNumber = (config.Checkpoint.SectionIndex+1)*entity.CHTFrequency - 1
		h.checkpointHash = config.Checkpoint.SectionHead
	}
	// 如果同步成功，则传递回调以潜在地禁用快照同步模式并启用事务传播。
	success := func() {
		// 如果我们正在运行snap sync并且它已完成，请禁用在下一个同步周期执行另一轮
		if atomic.LoadUint32(&h.snapSync) == 1 {
			log.Info("Snap sync complete, auto disabling")
			atomic.StoreUint32(&h.snapSync, 0)
		}
		// 如果我们成功完成了同步周期并通过了任何所需的检查点，则启用从网络接受事务
		head := h.chain.CurrentBlock()
		if head.NumberU64() >= h.checkpointNumber {
			// 检查点已通过，健全性检查时间戳，以对非检查点（数字=0）专用网络具有回退机制。
			if head.Time() >= uint64(time.Now().AddDate(0, -1, 0).Unix()) {
				atomic.StoreUint32(&h.acceptTxs, 1)
			}
		}
	}
	// 如果请求快照同步，则构建下载程序（长同步）及其备份状态bloom。下载程序负责在完成后释放状态bloom。
	h.downloader = downloader.New(h.checkpointNumber, config.Database, h.eventMux, h.chain, nil, h.removePeer, success)

	// 构造提取程序（短同步）
	validator := func(header *block.Header) error {
		// 转换后，应禁用所有块提取程序活动。打印警告日志。
		if h.merger.PoSFinalized() {
			log.Warn("Unexpected validation activity", "hash", header.Hash(), "number", header.Number)
			return errors.New("unexpected behavior after transition")
		}
		// 首先拒绝所有PoS样式标题。
		//无论链是否完成了转换，PoS头都应该只来自可信共识层，而不是p2p网络。
		if beacon, ok := h.chain.Engine().(*beacon.Beacon); ok {
			if beacon.IsPoSHeader(header) {
				return errors.New("unexpected post-merge header")
			}
		}
		return h.chain.Engine().VerifyHeader(h.chain, header, true)
	}
	heighter := func() uint64 {
		return h.chain.CurrentBlock().NumberU64()
	}
	inserter := func(blocks block.Blocks) (int, error) {
		// 转换后，应禁用所有块提取程序活动。打印警告日志。
		if h.merger.PoSFinalized() {
			var ctx []interface{}
			ctx = append(ctx, "blocks", len(blocks))
			if len(blocks) > 0 {
				ctx = append(ctx, "firsthash", blocks[0].Hash())
				ctx = append(ctx, "firstnumber", blocks[0].Number())
				ctx = append(ctx, "lasthash", blocks[len(blocks)-1].Hash())
				ctx = append(ctx, "lastnumber", blocks[len(blocks)-1].Number())
			}
			log.Warn("Unexpected insertion activity", ctx...)
			return 0, errors.New("unexpected behavior after transition")
		}
		//如果同步尚未到达检查点，请拒绝导入奇怪的块。
		//理想情况下，我们还将比较头部块的时间戳，如果头部太旧，则类似地拒绝传播的块。
		//不幸的是，在启动新网络时出现了一个极端情况，即起源可能很古老（0 unix），这将阻止整个节点接受它。
		if h.chain.CurrentBlock().NumberU64() < h.checkpointNumber {
			log.Warn("Unsynced yet, discarded propagated block", "number", blocks[0].Number(), "hash", blocks[0].Hash())
			return 0, nil
		}
		// 如果snap sync正在运行，请拒绝导入奇怪的块。
		//在启动新网络时，这是一个有问题的条款，因为快照同步矿工在重新启动之前可能不会接受彼此的块。
		//不幸的是，我们还没有找到一种方法，节点可以单方面决定网络是否是新的。
		//如果我们找到一个解决方案，这个问题应该得到解决。
		if atomic.LoadUint32(&h.snapSync) == 1 {
			log.Warn("Snap syncing, discarded propagated block", "number", blocks[0].Number(), "hash", blocks[0].Hash())
			return 0, nil
		}
		if h.merger.TDDReached() {
			// The blocks from the p2p network is regarded as untrusted
			// after the transition. In theory block gossip should be disabled
			// entirely whenever the transition is started. But in order to
			// handle the transition boundary reorg in the consensus-layer,
			// the legacy blocks are still accepted, but only for the terminal
			// pow blocks. Spec: https://github.com/ethereum/EIPs/blob/master/EIPS/eip-3675.md#halt-the-importing-of-pow-blocks
			//for i, block := range blocks {
			//	ptd := h.chain.GetTd(block.ParentHash(), block.NumberU64()-1)
			//	if ptd == nil {
			//		return 0, nil
			//	}
			//	td := new(big.Int).Add(ptd, block.Difficulty())
			//	if !h.chain.Config().IsTerminalPoWBlock(ptd, td) {
			//		log.Info("Filtered out non-termimal pow block", "number", block.NumberU64(), "hash", block.Hash())
			//		return 0, nil
			//	}
			//	if err := h.chain.InsertBlockWithoutSetHead(block); err != nil {
			//		return i, err
			//	}
			//}
			return 0, nil
		}
		n, err := h.chain.InsertChain(blocks)
		if err == nil {
			atomic.StoreUint32(&h.acceptTxs, 1) // 在任何抓取器导入时标记初始同步完成
		}
		return n, err
	}
	h.blockFetcher = fetcher.NewBlockFetcher(false, nil, h.chain.GetBlockByHash, validator, h.BroadcastBlock, heighter, nil, inserter, h.removePeer)

	fetchTx := func(peer string, hashes []entity.Hash) error {
		p := h.peers.peer(peer)
		if p == nil {
			return errors.New("unknown peer")
		}
		return p.RequestTxs(hashes)
	}
	h.txFetcher = fetcher.NewTxFetcher(h.txpool.Has, h.txpool.AddRemotes, fetchTx)
	h.chainSync = newChainSyncer(h)
	return h, nil
}

// runOctPeer将eth对等注册到联合eth/snap对等集中，将其添加到各个子系统并开始处理消息。
func (h *handler) runOctPeer(peer *eth.Peer, handler eth.Handler) error {
	// 如果对等端有一个“snap”扩展，等待它连接，这样我们就可以有一个统一的初始化/拆卸机制
	snap, err := h.peers.waitSnapExtension(peer)
	if err != nil {
		peer.Log().Error("Snapshot extension barrier failed", "err", err)
		return err
	}
	if !h.chainSync.handlePeerEvent(peer) {
		return p2p.DiscQuitting
	}
	h.peerWG.Add(1)
	defer h.peerWG.Done()

	// 执行辐射章鱼握手
	var (
		genesis = h.chain.Genesis()
		head    = h.chain.CurrentHeader()
		hash    = head.Hash()
		number  = head.Number.Uint64()
		td      = h.chain.GetTd(hash, number)
	)
	forkID := forkid.NewID(h.chain.Config(), h.chain.Genesis().Hash(), h.chain.CurrentHeader().Number.Uint64())
	if err := peer.Handshake(h.networkID, td, hash, genesis.Hash(), forkID, h.forkFilter); err != nil {
		peer.Log().Debug("Ethereum handshake failed", "err", err)
		return err
	}
	reject := false // 保留的对等插槽
	if atomic.LoadUint32(&h.snapSync) == 1 {
		if snap == nil {
			// 如果我们运行snap sync，我们希望为支持snap协议的对等机保留大约一半的对等机插槽。
			//这里的逻辑是；我们只允许最多5个非snap对等点比snap对等点多。
			if all, snp := h.peers.len(), h.peers.snapLen(); all-snp > snp+5 {
				reject = true
			}
		}
	}
	// 如果这是受信任的对等点，则忽略maxPeers
	if !peer.Peer.Info().Network.Trusted {
		if reject || h.peers.len() >= h.maxPeers {
			return p2p.DiscTooManyPeers
		}
	}
	peer.Log().Debug("Ethereum peer connected", "name", peer.Name())

	// 在本地注册对等方
	if err := h.peers.registerPeer(peer, snap); err != nil {
		peer.Log().Error("Ethereum peer registration failed", "err", err)
		return err
	}
	defer h.unregisterPeer(peer.ID())

	p := h.peers.peer(peer.ID())
	if p == nil {
		return errors.New("peer dropped during handling")
	}
	// 在下载程序中注册对等程序。如果下载程序认为它被禁止，我们将断开连接
	if err := h.downloader.RegisterPeer(peer.ID(), peer.Version(), peer); err != nil {
		peer.Log().Error("Failed to register peer in eth syncer", "err", err)
		return err
	}
	if snap != nil {
		if err := h.downloader.SnapSyncer.Register(snap); err != nil {
			peer.Log().Error("Failed to register peer in snap syncer", "err", err)
			return err
		}
	}
	h.chainSync.handlePeerEvent(peer)

	// 传播现有事务。之后出现的新事务将通过广播发送。
	h.syncTransactions(peer)

	// 如果对等方宕机，则为挂起的请求创建通知通道
	dead := make(chan struct{})
	defer close(dead)

	// 如果我们有一个受信任的CHT，拒绝低于该值的所有对等方（避免快速同步eclipse）
	if h.checkpointHash != (entity.Hash{}) {
		// 请求对等方的检查点标头进行链高度/重量验证
		resCh := make(chan *eth.Response)
		if _, err := peer.RequestHeadersByNumber(h.checkpointNumber, 1, 0, false, resCh); err != nil {
			return err
		}
		// 如果对方没有及时回复，启动计时器断开连接
		go func() {
			timeout := time.NewTimer(syncChallengeTimeout)
			defer timeout.Stop()

			select {
			case res := <-resCh:
				headers := ([]*block.Header)(*res.Res.(*eth.BlockHeadersPacket))
				if len(headers) == 0 {
					// 如果我们正在进行快照同步，我们必须强制执行检查点块以避免eclipse攻击。
					//在我们加入网络后，欢迎非同步节点进行连接。
					if atomic.LoadUint32(&h.snapSync) == 1 {
						peer.Log().Warn("Dropping unsynced node during sync", "addr", peer.RemoteAddr(), "type", peer.Name())
						res.Done <- errors.New("unsynced node cannot serve sync")
						return
					}
					res.Done <- nil
					return
				}
				// 验证标头并删除对等项或继续
				if len(headers) > 1 {
					res.Done <- errors.New("too many headers in checkpoint response")
					return
				}
				if headers[0].Hash() != h.checkpointHash {
					res.Done <- errors.New("checkpoint hash mismatch")
					return
				}
				res.Done <- nil

			case <-timeout.C:
				peer.Log().Warn("Checkpoint challenge timed out, dropping", "addr", peer.RemoteAddr(), "type", peer.Name())
				h.removePeer(peer.ID())

			case <-dead:
				// 对等处理程序终止，中止所有goroutine
			}
		}()
	}
	// 如果我们有任何明确的对等请求块哈希，请请求它们
	for number, hash := range h.requiredBlocks {
		resCh := make(chan *eth.Response)
		if _, err := peer.RequestHeadersByNumber(number, 1, 0, false, resCh); err != nil {
			return err
		}
		go func(number uint64, hash entity.Hash) {
			timeout := time.NewTimer(syncChallengeTimeout)
			defer timeout.Stop()

			select {
			case res := <-resCh:
				headers := ([]*block.Header)(*res.Res.(*eth.BlockHeadersPacket))
				if len(headers) == 0 {
					// 如果远程节点尚未同步，则允许缺少所需的块
					res.Done <- nil
					return
				}
				// 验证标头并删除对等项或继续
				if len(headers) > 1 {
					res.Done <- errors.New("too many headers in required block response")
					return
				}
				if headers[0].Number.Uint64() != number || headers[0].Hash() != hash {
					peer.Log().Info("Required block mismatch, dropping peer", "number", number, "hash", headers[0].Hash(), "want", hash)
					res.Done <- errors.New("required block mismatch")
					return
				}
				peer.Log().Debug("Peer required block verified", "number", number, "hash", hash)
				res.Done <- nil
			case <-timeout.C:
				peer.Log().Warn("Required block challenge timed out, dropping", "addr", peer.RemoteAddr(), "type", peer.Name())
				h.removePeer(peer.ID())
			}
		}(number, hash)
	}
	// 处理传入消息，直到连接断开
	return handler(peer)
}

// runSnapExtension将“snap”对等点注册到联合eth/snap对等点集中，并开始处理入站消息。
//由于“snap”只是“eth”的卫星协议，所有子系统注册和生命周期管理将由“eth”主处理程序完成，以防止奇怪的种族。
func (h *handler) runSnapExtension(peer *snap.Peer, handler snap.Handler) error {
	h.peerWG.Add(1)
	defer h.peerWG.Done()

	if err := h.peers.registerSnapExtension(peer); err != nil {
		peer.Log().Warn("Snapshot extension registration failed", "err", err)
		return err
	}
	return handler(peer)
}

// removePeer请求断开对等连接。
func (h *handler) removePeer(id string) {
	peer := h.peers.peer(id)
	if peer != nil {
		peer.Peer.Disconnect(p2p.DiscUselessPeer)
	}
}

// unregisterPeer从下载程序、抓取程序和主对等集中删除对等。
func (h *handler) unregisterPeer(id string) {
	// 创建自定义记录器以避免打印整个id
	var logger log.Logger
	if len(id) < 16 {
		// 测试使用短ID，不要被它们卡住
		logger = log.New("peer", id)
	} else {
		logger = log.New("peer", id[:8])
	}
	// 如果对等方不存在，则中止
	peer := h.peers.peer(id)
	if peer == nil {
		logger.Error("Ethereum peer removal failed", "err", errPeerNotRegistered)
		return
	}
	// 删除“oct”对等端（如果存在）
	logger.Debug("Removing Ethereum peer", "snap", peer.snapExt != nil)

	// 删除“snap”扩展名（如果存在）
	if peer.snapExt != nil {
		h.downloader.SnapSyncer.Unregister(id)
	}
	h.downloader.UnregisterPeer(id)
	h.txFetcher.Drop(id)

	if err := h.peers.unregisterPeer(id); err != nil {
		logger.Error("Ethereum peer removal failed", "err", err)
	}
}

func (h *handler) Start(maxPeers int) {
	h.maxPeers = maxPeers

	// 广播事务
	h.wg.Add(1)
	h.txsCh = make(chan event.NewTxsEvent, txChanSize)
	h.txsSub = h.txpool.SubscribeNewTxsEvent(h.txsCh)
	go h.txBroadcastLoop()

	// 广播开采区块
	h.wg.Add(1)
	h.minedBlockSub = h.eventMux.Subscribe(event.NewMinedBlockEvent{})
	go h.minedBroadcastLoop()

	// 启动同步处理程序
	h.wg.Add(1)
	go h.chainSync.loop()
}

func (h *handler) Stop() {
	h.txsSub.Unsubscribe()        // 退出txBroadcastLoop
	h.minedBlockSub.Unsubscribe() // 退出BlockBroadcasting循环

	// 退出chainSync和txsync64。完成后，不会接受新的对等点。
	close(h.quitSync)
	h.wg.Wait()

	// 断开现有会话的连接。这也关闭了对等集上任何新注册的大门。
	//已建立但未添加到h.peers的会话将在尝试注册时退出。
	h.peers.close()
	h.peerWG.Wait()

	log.Info("Ethereum protocol stopped")
}

// BroadcastBlock要么将一个块传播到其对等节点的子集，要么仅宣布其可用性（取决于请求的内容）。
func (h *handler) BroadcastBlock(block *block.Block, propagate bool) {
	// 如果链已进入PoS阶段，则禁用块传播。块传播被委托给共识层。
	if h.merger.PoSFinalized() {
		return
	}
	// 如果是合并后块，则禁用块传播。
	if beacon, ok := h.chain.Engine().(*beacon.Beacon); ok {
		if beacon.IsPoSHeader(block.Header()) {
			return
		}
	}
	hash := block.Hash()
	peers := h.peers.peersWithoutBlock(hash)

	// 如果请求传播，则发送到对等节点的子集
	if propagate {
		// 计算块的TD（尚未导入，因此块。TD无效）
		var td *big.Int
		if parent := h.chain.GetBlock(block.ParentHash(), block.NumberU64()-1); parent != nil {
			td = new(big.Int).Add(block.Difficulty(), h.chain.GetTd(block.ParentHash(), block.NumberU64()-1))
		} else {
			log.Error("Propagating dangling block", "number", block.Number(), "hash", hash)
			return
		}
		// 将块发送给我们的同龄人的子集
		transfer := peers[:int(math.Sqrt(float64(len(peers))))]
		for _, peer := range transfer {
			peer.AsyncSendNewBlock(block, td)
		}
		log.Trace("Propagated block", "hash", hash, "recipients", len(transfer), "duration", entity.PrettyDuration(time.Since(block.ReceivedAt)))
		return
	}
	// 否则，如果该块确实在自己的链中，则宣布它
	if h.chain.HasBlock(hash, block.NumberU64()) {
		for _, peer := range peers {
			peer.AsyncSendNewBlockHash(block)
		}
		log.Trace("Announced block", "hash", hash, "recipients", len(peers), "duration", entity.PrettyDuration(time.Since(block.ReceivedAt)))
	}
}

// BroadcastTransactions将传播一批事务
//-到所有对等方的平方根
//-并且，分别作为通知发送给所有已知尚未拥有给定交易的对等方。
func (h *handler) BroadcastTransactions(txs block.Transactions) {
	var (
		annoCount   int // 发布的公告数
		annoPeers   int
		directCount int // 直接发送给对等方的TX计数
		directPeers int // 直接发送事务的对等方计数

		txset = make(map[*ethPeer][]entity.Hash) // 将对等->哈希设置为直接传输
		annos = make(map[*ethPeer][]entity.Hash) // 将对等->哈希设置为通告

	)
	// 将事务广播给一批不知道它的对等方
	for _, tx := range txs {
		peers := h.peers.peersWithoutTransaction(tx.Hash())
		// 无条件地将tx发送给我们的对等方的子集
		numDirect := int(math.Sqrt(float64(len(peers))))
		for _, peer := range peers[:numDirect] {
			txset[peer] = append(txset[peer], tx.Hash())
		}
		// 对于其余对等方，仅发送公告
		for _, peer := range peers[numDirect:] {
			annos[peer] = append(annos[peer], tx.Hash())
		}
	}
	for peer, hashes := range txset {
		directPeers++
		directCount += len(hashes)
		peer.AsyncSendTransactions(hashes)
	}
	for peer, hashes := range annos {
		annoPeers++
		annoCount += len(hashes)
		peer.AsyncSendPooledTransactionHashes(hashes)
	}
	log.Debug("Transaction broadcast", "txs", len(txs),
		"announce packs", annoPeers, "announced hashes", annoCount,
		"tx packs", directPeers, "broadcast txs", directCount)
}

// minedBroadcastLoop将挖掘出的块发送给连接的对等方。
func (h *handler) minedBroadcastLoop() {
	defer h.wg.Done()

	for obj := range h.minedBlockSub.Chan() {
		if ev, ok := obj.Data.(event.NewMinedBlockEvent); ok {
			h.BroadcastBlock(ev.Block, true)  // 首先将块传播到对等点
			h.BroadcastBlock(ev.Block, false) // 然后才向其他人宣布
		}
	}
}

// txBroadcastLoop向连接的对等方宣布新的交易。
func (h *handler) txBroadcastLoop() {
	defer h.wg.Done()
	for {
		select {
		case event := <-h.txsCh:
			h.BroadcastTransactions(event.Txs)
		case <-h.txsSub.Err():
			return
		}
	}
}
