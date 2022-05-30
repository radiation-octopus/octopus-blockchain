package operationDB

import (
	operationUtils "github.com/radiation-octopus/octopus-blockchain/operationUtils"
	"github.com/radiation-octopus/octopus/log"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
	"sync"
)

type DatabaseI interface {
	// 打开主帐户trie。
	OpenTrie(root operationUtils.Hash) (Trie, error)

	// 打开帐户的存储trie。
	OpenStorageTrie(addrHash, root operationUtils.Hash) (Trie, error)

	// CopyTrie returns an independent copy of the given trie.
	CopyTrie(Trie) Trie

	// ContractCode retrieves a particular contract's code.
	ContractCode(addrHash, codeHash operationUtils.Hash) ([]byte, error)

	// ContractCodeSize retrieves a particular contracts code's size.
	ContractCodeSize(addrHash, codeHash operationUtils.Hash) (int, error)

	// TrieDB retrieves the low level trie database used for data storage.
	TrieDB() *Database
}

type Trie interface {
	// GetKey returns the sha3 preimage of a hashed key that was previously used
	// to store a value.
	//
	// TODO(fjl): remove this when SecureTrie is removed
	GetKey([]byte) []byte

	// TryGet returns the value for key stored in the trie. The value bytes must
	// not be modified by the caller. If a node was not found in the database, a
	// trie.MissingNodeError is returned.
	TryGet(key []byte) ([]byte, error)

	// TryUpdateAccount abstract an account write in the trie.
	TryUpdateAccount(key []byte, account *StateAccount) error

	// TryUpdate associates key with value in the trie. If value has length zero, any
	// existing value is deleted from the trie. The value bytes must not be modified
	// by the caller while they are stored in the trie. If a node was not found in the
	// database, a trie.MissingNodeError is returned.
	TryUpdate(key, value []byte) error

	// TryDelete removes any existing value for key from the trie. If a node was not
	// found in the database, a trie.MissingNodeError is returned.
	TryDelete(key []byte) error

	// Hash returns the root hash of the trie. It does not write to the database and
	// can be used even if the trie doesn't have one.
	Hash() operationUtils.Hash

	// Commit writes all nodes to the trie's memory database, tracking the internal
	// and external (for account tries) references.
	//Commit(onleaf trie.LeafCallback) (blockchain.Hash, int, error)

	// NodeIterator returns an iterator that returns nodes of the trie. Iteration
	// starts at the key after the given start key.
	//NodeIterator(startKey []byte) trie.NodeIterator

	// Prove constructs a Merkle proof for key. The result contains all encoded nodes
	// on the path to the value at key. The value itself is also included in the last
	// node and can be retrieved by verifying the proof.
	//
	// If the trie does not contain a value for key, the returned proof contains all
	// nodes of the longest existing prefix of the key (at least the root), ending
	// with the node that proves the absence of the key.
	//Prove(key []byte, fromLevel uint, proofDb KeyValueWriter) error
}

//数据库操作结构体
type OperationDB struct {
	db           DatabaseI
	prefetcher   *triePrefetcher
	originalRoot operationUtils.Hash //根hash
	trie         Trie
	//hasher       crypto.KeccakState

	OperationObjects        map[operationUtils.Address]*OperationObject // 数据库活动对象缓存map集合
	OperationObjectsPending map[operationUtils.Address]struct{}         //状态对象已完成但尚未写入trie
	OperationObjectsDirty   map[operationUtils.Address]struct{}         // 当前执行中修改的状态对象

	dbErr error // 数据库操作错误记录存储

	thash   operationUtils.Hash
	txIndex int
	logs    map[operationUtils.Hash][]*log.OctopusLog //日志
	logSize uint

	preimages map[operationUtils.Hash][]byte

	accessList *accessList // 事务访问进程列表

	journal *Journal

	//AccountReads         time.Duration
	//AccountHashes        time.Duration
	//AccountUpdates       time.Duration
	//AccountCommits       time.Duration
	//StorageReads         time.Duration
	//StorageHashes        time.Duration
	//StorageUpdates       time.Duration
	//StorageCommits       time.Duration
	//SnapshotAccountReads time.Duration
	//SnapshotStorageReads time.Duration
	//SnapshotCommits      time.Duration
	//
	//AccountUpdated int
	//StorageUpdated int
	//AccountDeleted int
	//StorageDeleted int
}

func (o OperationDB) CreateAccount(address operationUtils.Address) {

	panic("implement me")
}

func (o OperationDB) SubBalance(address operationUtils.Address, b *big.Int) {
	panic("implement me")
}

func (o OperationDB) AddBalance(address operationUtils.Address, b *big.Int) {
	panic("implement me")
}

func (o OperationDB) GetBalance(address operationUtils.Address) *big.Int {
	panic("implement me")
}

func (o OperationDB) GetNonce(address operationUtils.Address) uint64 {
	panic("implement me")
}

func (o OperationDB) SetNonce(address operationUtils.Address, u uint64) {
	panic("implement me")
}

func (o OperationDB) GetCodeHash(address operationUtils.Address) operationUtils.Hash {
	panic("implement me")
}

func (o OperationDB) GetCode(address operationUtils.Address) []byte {
	panic("implement me")
}

func (o OperationDB) SetCode(address operationUtils.Address, bytes []byte) {
	panic("implement me")
}

func (o OperationDB) GetCodeSize(address operationUtils.Address) int {
	panic("implement me")
}

func (o OperationDB) AddRefund(u uint64) {
	panic("implement me")
}

func (o OperationDB) SubRefund(u uint64) {
	panic("implement me")
}

func (o OperationDB) GetRefund() uint64 {
	panic("implement me")
}

func (o OperationDB) GetCommittedState(address operationUtils.Address, hash operationUtils.Hash) operationUtils.Hash {
	panic("implement me")
}

func (o OperationDB) GetState(address operationUtils.Address, hash operationUtils.Hash) operationUtils.Hash {
	panic("implement me")
}

func (o OperationDB) SetState(address operationUtils.Address, hash operationUtils.Hash, hash2 operationUtils.Hash) {
	panic("implement me")
}

func (o OperationDB) Suicide(address operationUtils.Address) bool {
	panic("implement me")
}

func (o OperationDB) HasSuicided(address operationUtils.Address) bool {
	panic("implement me")
}

func (o OperationDB) Exist(address operationUtils.Address) bool {
	panic("implement me")
}

func (o OperationDB) Empty(address operationUtils.Address) bool {
	panic("implement me")
}

func (o OperationDB) AddressInAccessList(addr operationUtils.Address) bool {
	panic("implement me")
}

func (o OperationDB) SlotInAccessList(addr operationUtils.Address, slot operationUtils.Hash) (addressOk bool, slotOk bool) {
	panic("implement me")
}

func (o OperationDB) AddAddressToAccessList(addr operationUtils.Address) {
	panic("implement me")
}

func (o OperationDB) AddSlotToAccessList(addr operationUtils.Address, slot operationUtils.Hash) {
	panic("implement me")
}

func (o OperationDB) RevertToSnapshot(i int) {
	panic("implement me")
}

func (o OperationDB) Snapshot() int {
	panic("implement me")
}

func (o OperationDB) AddLog(octopusLog *log.OctopusLog) {
	panic("implement me")
}

func (o OperationDB) AddPreimage(hash operationUtils.Hash, bytes []byte) {
	panic("implement me")
}

func (o OperationDB) ForEachStorage(address operationUtils.Address, f func(operationUtils.Hash, operationUtils.Hash) bool) error {
	panic("implement me")
}

//func (s *OperationDB) StartPrefetcher(namespace string) {
//	if s.prefetcher != nil {
//		s.prefetcher.close()
//		s.prefetcher = nil
//	}
//	if s.snap != nil {
//		s.prefetcher = newTriePrefetcher(s.db, s.originalRoot, namespace)
//	}
//}

// Prepare sets the current transaction hash and index which are
// used when the EVM emits new state logs.
func (s *OperationDB) Prepare(thash operationUtils.Hash, ti int) {
	s.thash = thash
	s.txIndex = ti
}

//创建根状态数据库
func New(root operationUtils.Hash, db DatabaseI) (*OperationDB, error) {
	//tr, err := db.OpenTrie(root)
	//if err != nil {
	//	return nil, err
	//}
	sdb := &OperationDB{
		db: db,
		//trie:             tr,
		originalRoot:     root,
		OperationObjects: make(map[operationUtils.Address]*OperationObject),
		logs:             make(map[operationUtils.Hash][]*log.OctopusLog),
		preimages:        make(map[operationUtils.Hash][]byte),
		accessList:       newAccessList(),
	}
	return sdb, nil
}

// 复制创建状态的深度、独立副本。复制状态的快照无法应用于副本。
func (s *OperationDB) Copy() *OperationDB {
	// 复制所有基本字段，初始化内存字段
	state := &OperationDB{
		db:                      s.db,
		trie:                    s.db.CopyTrie(s.trie),
		OperationObjects:        make(map[operationUtils.Address]*OperationObject, len(s.journal.Dirties)),
		OperationObjectsPending: make(map[operationUtils.Address]struct{}, len(s.OperationObjectsPending)),
		OperationObjectsDirty:   make(map[operationUtils.Address]struct{}, len(s.journal.Dirties)),
		//refund:              s.refund,
		logs:      make(map[operationUtils.Hash][]*log.OctopusLog, len(s.logs)),
		logSize:   s.logSize,
		preimages: make(map[operationUtils.Hash][]byte, len(s.preimages)),
		journal:   NewJournal(),
		//hasher:              crypto.NewKeccakState(),
	}
	// 复制脏状态、日志和前映像
	for addr := range s.journal.Dirties {
		// 在Finalize方法中，存在一种情况，即对象在日志中，但不在状态对象中：在拜占庭之前触摸ripeMD后的OOG。因此，我们需要检查零
		if object, exist := s.OperationObjects[addr]; exist {
			// 即使原始对象是脏的，我们也不会复制日志，因此我们需要确保日志在提交（或类似操作）期间可能会产生的任何副作用都已应用于副本。
			state.OperationObjects[addr] = object.deepCopy(state)

			state.OperationObjectsDirty[addr] = struct{}{}   // 将副本标记为脏以强制内部（代码/状态）提交
			state.OperationObjectsPending[addr] = struct{}{} // 标记副本挂起以强制外部（帐户）提交
		}
	}
	// 如上所述，我们不复制实际日记账。这意味着，如果复制副本，则上面的循环将是no op，因为副本的日志为空。
	//因此，这里我们迭代OperationObjects，以启用副本的副本
	for addr := range s.OperationObjectsPending {
		if _, exist := state.OperationObjects[addr]; !exist {
			state.OperationObjects[addr] = s.OperationObjects[addr].deepCopy(state)
		}
		state.OperationObjectsPending[addr] = struct{}{}
	}
	for addr := range s.OperationObjectsDirty {
		if _, exist := state.OperationObjects[addr]; !exist {
			state.OperationObjects[addr] = s.OperationObjects[addr].deepCopy(state)
		}
		state.OperationObjectsDirty[addr] = struct{}{}
	}
	for hash, logs := range s.logs {
		cpy := make([]*log.OctopusLog, len(logs))
		for i, l := range logs {
			cpy[i] = new(log.OctopusLog)
			*cpy[i] = *l
		}
		state.logs[hash] = cpy
	}
	for hash, preimage := range s.preimages {
		state.preimages[hash] = preimage
	}
	// 我们需要复制访问列表吗？实际上：否。在事务开始时，访问列表为空。实际上，我们只会在事务/块之间复制状态，而不会在事务的中间复制。然而，复制一个空列表并不会花费我们太多的成本，所以不管怎样，如果我们决定在交易过程中复制它，我们都会这样做，以避免爆炸
	state.accessList = s.accessList.Copy()

	// 如果预取程序正在运行，则制作一个只能访问数据但不能主动预加载的非活动副本（因为用户不知道他们需要显式终止活动副本）。
	if s.prefetcher != nil {
		state.prefetcher = s.prefetcher.copy()
	}
	//if s.snaps != nil {
	//	// In order for the miner to be able to use and make additions
	//	// to the snapshot tree, we need to copy that aswell.
	//	// Otherwise, any block mined by ourselves will cause gaps in the tree,
	//	// and force the miner to operate trie-backed only
	//	state.snaps = s.snaps
	//	state.snap = s.snap
	//	// deep copy needed
	//	state.snapDestructs = make(map[common.Hash]struct{})
	//	for k, v := range s.snapDestructs {
	//		state.snapDestructs[k] = v
	//	}
	//	state.snapAccounts = make(map[common.Hash][]byte)
	//	for k, v := range s.snapAccounts {
	//		state.snapAccounts[k] = v
	//	}
	//	state.snapStorage = make(map[common.Hash]map[common.Hash][]byte)
	//	for k, v := range s.snapStorage {
	//		temp := make(map[common.Hash][]byte)
	//		for kk, vv := range v {
	//			temp[kk] = vv
	//		}
	//		state.snapStorage[k] = temp
	//	}
	//}
	return state
}

func (s *OperationDB) Database() DatabaseI {
	return s.db
}

// StopPrefetcher terminates a running prefetcher and reports any leftover stats
// from the gathered metrics.
func (s *OperationDB) StopPrefetcher() {
	if s.prefetcher != nil {
		//s.prefetcher.close()
		s.prefetcher = nil
	}
}

// ReadHeaderNumber返回分配给哈希的标头编号。
func ReadHeaderNumber(db Database, hash operationUtils.Hash) *big.Int {
	data := db.Query(headerMark, utils.GetInToStr(hash))
	int := data.Number
	return int
}

/**
accessList
*/
type accessList struct {
	addresses map[operationUtils.Address]int
	slots     []map[operationUtils.Hash]struct{}
}

// Copy创建访问列表的独立副本。
func (a *accessList) Copy() *accessList {
	cp := newAccessList()
	for k, v := range a.addresses {
		cp.addresses[k] = v
	}
	cp.slots = make([]map[operationUtils.Hash]struct{}, len(a.slots))
	for i, slotMap := range a.slots {
		newSlotmap := make(map[operationUtils.Hash]struct{}, len(slotMap))
		for k := range slotMap {
			newSlotmap[k] = struct{}{}
		}
		cp.slots[i] = newSlotmap
	}
	return cp
}

//创建集合
func newAccessList() *accessList {
	return &accessList{
		addresses: make(map[operationUtils.Address]int),
	}
}

type triePrefetcher struct {
	db       Database                            // 用于获取trie节点的数据库
	root     operationUtils.Hash                 // 度量的Account trie的根哈希
	fetches  map[operationUtils.Hash]Trie        // 部分或完全获取程序尝试
	fetchers map[operationUtils.Hash]*subfetcher // 每个trie的Subfetchers

	//deliveryMissMeter metrics.Meter
	//accountLoadMeter  metrics.Meter
	//accountDupMeter   metrics.Meter
	//accountSkipMeter  metrics.Meter
	//accountWasteMeter metrics.Meter
	//storageLoadMeter  metrics.Meter
	//storageDupMeter   metrics.Meter
	//storageSkipMeter  metrics.Meter
	//storageWasteMeter metrics.Meter
}

// copy创建trie预取器的深度但不活动的副本。将复制已加载的任何trie数据，但不会启动goroutines。这主要用于miner，它创建一个要密封的活动变异状态副本，同时可能进一步变异状态。
func (p *triePrefetcher) copy() *triePrefetcher {
	copy := &triePrefetcher{
		db:      p.db,
		root:    p.root,
		fetches: make(map[operationUtils.Hash]Trie), // Active prefetchers use the fetches map

		//deliveryMissMeter: p.deliveryMissMeter,
		//accountLoadMeter:  p.accountLoadMeter,
		//accountDupMeter:   p.accountDupMeter,
		//accountSkipMeter:  p.accountSkipMeter,
		//accountWasteMeter: p.accountWasteMeter,
		//storageLoadMeter:  p.storageLoadMeter,
		//storageDupMeter:   p.storageDupMeter,
		//storageSkipMeter:  p.storageSkipMeter,
		//storageWasteMeter: p.storageWasteMeter,
	}
	// If the prefetcher is already a copy, duplicate the data
	if p.fetches != nil {
		//for root, fetch := range p.fetches {
		//	copy.fetches[root] = p.db.CopyTrie(fetch)
		//}
		return copy
	}
	// Otherwise we're copying an active fetcher, retrieve the current states
	for root, fetcher := range p.fetchers {
		copy.fetches[root] = fetcher.peek()
	}
	return copy
}

/**

 */
type subfetcher struct {
	db   Database            // Database to load trie nodrough
	root operationUtils.Hash // Root hash of the trie to prefetch
	trie Trie                // Trie being populated with nodes

	tasks [][]byte   // Items queued up for retrieval
	lock  sync.Mutex // Lock protecting the task queue

	wake chan struct{}  // Wake channel if a new task is scheduled
	stop chan struct{}  // Channel to interrupt processing
	term chan struct{}  // Channel to signal iterruption
	copy chan chan Trie // Channel to request a copy of the current trie

	seen map[string]struct{} // Tracks the entries already loaded
	dups int                 // Number of duplicate preload tasks
	used [][]byte            // Tracks the entries used in the end
}

//peek尝试以当前的任何形式检索fetcher的trie的深度副本。
func (sf *subfetcher) peek() Trie {
	ch := make(chan Trie)
	select {
	case sf.copy <- ch:
		// 子蚀刻器仍处于活动状态，请从中返回副本
		return <-ch

	case <-sf.term:
		// 子蚀刻程序已终止，请直接返回副本
		if sf.trie == nil {
			return nil
		}
		return nil
	}
}
