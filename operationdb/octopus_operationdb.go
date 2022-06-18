package operationdb

import (
	"fmt"
	"github.com/VictoriaMetrics/fastcache"
	lru "github.com/hashicorp/golang-lru"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus/db"
	"github.com/radiation-octopus/octopus/log"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
	"sync"
)

const (
	// 要保留的codehash->大小关联数。
	codeSizeCacheSize = 100000

	// 为缓存干净代码而授予的缓存大小。
	codeCacheSize = 64 * 1024 * 1024
)

type DatabaseI interface {
	// 打开主帐户trie。
	OpenTrie(root entity.Hash) (TrieI, error)

	// 打开帐户的存储trie。
	OpenStorageTrie(addrHash, root entity.Hash) (TrieI, error)

	// CopyTrie返回给定trie的独立副本。
	CopyTrie(TrieI) TrieI

	// ContractCode检索特定合同的代码。
	ContractCode(addrHash, codeHash entity.Hash) ([]byte, error)

	// ContractCodeSize检索特定合同代码的大小。
	ContractCodeSize(addrHash, codeHash entity.Hash) (int, error)

	// TrieDB检索用于数据存储的低级trie数据库。
	TrieDB() *Database
}

type TrieI interface {
	// GetKey返回以前用于存储值的哈希键的sha3前映像。
	GetKey([]byte) []byte

	//TryGet返回存储在trie中的键的值。调用者不得修改值字节。如果在数据库中找不到节点，则会创建一个trie。将返回MissingNodeError。
	TryGet(key []byte) ([]byte, error)

	// TryUpdateAccount摘要在trie中写入的帐户。
	TryUpdateAccount(key []byte, account *entity.StateAccount) error

	// TryUpdate将键与trie中的值相关联。如果值的长度为零，则会从trie中删除任何现有值。
	//值字节存储在trie中时，调用者不得修改它们。如果在数据库中找不到节点，则会创建一个trie。将返回MissingNodeError。
	TryUpdate(key, value []byte) error

	// TryDelete从trie中删除key的所有现有值。如果在数据库中找不到节点，则会创建一个trie。将返回MissingNodeError。
	TryDelete(key []byte) error

	// 哈希返回trie的根哈希。它不会写入数据库，即使trie没有数据库，也可以使用它。
	Hash() entity.Hash

	// Commit将所有节点写入trie的内存数据库，跟踪内部和外部（用于帐户尝试）引用。
	//Commit(onleaf trie.LeafCallback) (blockchain.Hash, int, terr)

	// NodeIterator返回一个迭代器，该迭代器返回trie的节点。迭代从给定开始键之后的键开始。
	//NodeIterator(startKey []byte) trie.NodeIterator

	// Prove为key构造了一个Merkle证明。结果包含指向键处值的路径上的所有编码节点。值本身也包含在最后一个节点中，可以通过验证证明来检索。
	//如果trie不包含key的值，则返回的证明将包含该key现有前缀最长的所有节点（至少是根节点），以证明没有该key的节点结束。
	//Prove(key []byte, fromLevel uint, proofDb KeyValueWriter) terr
}

//数据库操作结构体
type OperationDB struct {
	db           DatabaseI
	prefetcher   *triePrefetcher
	originalRoot entity.Hash //根hash
	trieI        TrieI
	//hasher       crypto.KeccakState

	OperationObjects        map[entity.Address]*OperationObject // 数据库活动对象缓存map集合
	OperationObjectsPending map[entity.Address]struct{}         //状态对象已完成但尚未写入trie
	OperationObjectsDirty   map[entity.Address]struct{}         // 当前执行中修改的状态对象

	dbErr error // 数据库操作错误记录存储

	thash   entity.Hash
	txIndex int
	logs    map[entity.Hash][]*log.OctopusLog //日志
	logSize uint

	preimages map[entity.Hash][]byte

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

func (o *OperationDB) CreateAccount(address entity.Address) {
	newObj, prev := o.createObject(address)
	if prev != nil {
		newObj.setBalance(prev.data.Balance)
	}
}

//子平衡从与addr关联的帐户中减去金额。
func (o *OperationDB) SubBalance(address entity.Address, amount *big.Int) {
	operationObject := o.GetOrNewOperationObject(address)
	if operationObject != nil {
		operationObject.SubBalance(amount)
	}
}

//AddBalance将金额添加到与addr关联的帐户。
func (o *OperationDB) AddBalance(address entity.Address, amount *big.Int) {
	operationObject := o.GetOrNewOperationObject(address)
	if operationObject != nil {
		operationObject.AddBalance(amount)
	}
}

// GetOrNewOperationObject检索操作对象，如果为nil，则创建新的操作对象。
func (o *OperationDB) GetOrNewOperationObject(address entity.Address) *OperationObject {
	stateObject := o.getOperationObject(address)
	if stateObject == nil {
		stateObject, _ = o.createObject(address)
	}
	return stateObject
}

// createObject创建一个新的状态对象。如果存在具有给定地址的现有帐户，则会覆盖该帐户并将其作为第二个返回值返回。
func (s *OperationDB) createObject(addr entity.Address) (newobj, prev *OperationObject) {
	prev = s.getDeletedOperationObject(addr) // 注意，prev可能已被删除，我们需要它！

	//var prevdestruct bool
	//if s.snap != nil && prev != nil {
	//	_, prevdestruct = s.snapDestructs[prev.addrHash]
	//	if !prevdestruct {
	//		s.snapDestructs[prev.addrHash] = struct{}{}
	//	}
	//}
	newobj = newObject(s, addr, entity.StateAccount{})
	//if prev == nil {
	//	s.journal.append(createObjectChange{account: &addr})
	//} else {
	//	s.journal.append(resetObjectChange{prev: prev, prevdestruct: prevdestruct})
	//}
	s.setOperationObject(newobj)
	if prev != nil {
		return newobj, prev
	}
	return newobj, nil
}

func (o *OperationDB) GetBalance(address entity.Address) *big.Int {
	operationObject := o.getOperationObject(address)
	if operationObject != nil {
		return operationObject.Balance()
	}
	return operationutils.Big0
}

func (o *OperationDB) GetNonce(address entity.Address) uint64 {
	operationObject := o.getOperationObject(address)
	if operationObject != nil {
		return operationObject.Nonce()
	}
	return 0
}

func (o *OperationDB) SetNonce(address entity.Address, nonce uint64) {
	operationObject := o.GetOrNewOperationObject(address)
	if operationObject != nil {
		operationObject.SetNonce(nonce)
	}
}

func (o *OperationDB) GetCodeHash(address entity.Address) entity.Hash {
	operationObject := o.getOperationObject(address)
	if operationObject == nil {
		return entity.Hash{}
	}
	return entity.BytesToHash(operationObject.CodeHash())
}

func (o *OperationDB) GetCode(address entity.Address) []byte {
	operationObject := o.getOperationObject(address)
	if operationObject != nil {
		return operationObject.Code(o.db)
	}
	return nil
}

func (o *OperationDB) SetCode(address entity.Address, bytes []byte) {
	panic("implement me")
}

func (o *OperationDB) GetCodeSize(address entity.Address) int {
	panic("implement me")
}

func (o *OperationDB) AddRefund(u uint64) {
	panic("implement me")
}

func (o *OperationDB) SubRefund(u uint64) {
	panic("implement me")
}

func (o *OperationDB) GetRefund() uint64 {
	panic("implement me")
}

func (o *OperationDB) GetCommittedState(address entity.Address, hash entity.Hash) entity.Hash {
	panic("implement me")
}

func (o *OperationDB) GetState(address entity.Address, hash entity.Hash) entity.Hash {
	panic("implement me")
}

func (o *OperationDB) SetState(address entity.Address, hash entity.Hash, hash2 entity.Hash) {
	panic("implement me")
}

func (o *OperationDB) Suicide(address entity.Address) bool {
	panic("implement me")
}

func (o *OperationDB) HasSuicided(address entity.Address) bool {
	panic("implement me")
}

func (o *OperationDB) Exist(address entity.Address) bool {
	panic("implement me")
}

func (o *OperationDB) Empty(address entity.Address) bool {
	panic("implement me")
}

func (o *OperationDB) AddressInAccessList(addr entity.Address) bool {
	panic("implement me")
}

func (o *OperationDB) SlotInAccessList(addr entity.Address, slot entity.Hash) (addressOk bool, slotOk bool) {
	panic("implement me")
}

func (o *OperationDB) AddAddressToAccessList(addr entity.Address) {
	panic("implement me")
}

func (o *OperationDB) AddSlotToAccessList(addr entity.Address, slot entity.Hash) {
	panic("implement me")
}

func (o *OperationDB) RevertToSnapshot(i int) {
	panic("implement me")
}

func (o *OperationDB) Snapshot() int {
	panic("implement me")
}

func (o *OperationDB) AddLog(octopusLog *log.OctopusLog) {
	panic("implement me")
}

func (o *OperationDB) AddPreimage(hash entity.Hash, bytes []byte) {
	panic("implement me")
}

func (o *OperationDB) ForEachStorage(address entity.Address, f func(entity.Hash, entity.Hash) bool) error {
	panic("implement me")
}

// getOperationObject检索地址给定的状态对象，如果在此执行上下文中找不到或删除了该对象，则返回nil。
//如果需要区分不存在/刚刚删除，请使用getDeletedOperationObject。
func (o *OperationDB) getOperationObject(addr entity.Address) *OperationObject {
	if obj := o.getDeletedOperationObject(addr); obj != nil {
		return obj
	}
	return nil
}

// getDeletedOperationObject类似于getOperationObject，但它不会为已删除的状态对象返回nil，而是返回设置了deleted标志的实际对象。
//状态日志需要这样才能恢复到正确的s-destructed对象，而不是擦除关于状态对象的所有知识。
func (o *OperationDB) getDeletedOperationObject(addr entity.Address) *OperationObject {
	// 首选活动对象（如果有）
	if obj := o.OperationObjects[addr]; obj != nil {
		return obj
	}
	// 如果没有可用的活动对象，请尝试使用快照
	var data *entity.StateAccount
	//if o.snap != nil {
	//	start := time.Now()
	//	acc, err := o.snap.Account(crypto.HashData(s.hasher, addr.Bytes()))
	//	if metrics.EnabledExpensive {
	//		o.SnapshotAccountReads += time.Since(start)
	//	}
	//	if err == nil {
	//		if acc == nil {
	//			return nil
	//		}
	//		data = &entity.StateAccount{
	//			Nonce:    acc.Nonce,
	//			Balance:  acc.Balance,
	//			CodeHash: acc.CodeHash,
	//			Root:     common.BytesToHash(acc.Root),
	//		}
	//		if len(data.CodeHash) == 0 {
	//			data.CodeHash = emptyCodeHash
	//		}
	//		if data.Root == (entity.Hash{}) {
	//			data.Root = emptyRoot
	//		}
	//	}
	//}
	//如果快照不可用或读取失败，请从数据库加载
	if data == nil {
		//start := time.Now()
		//enc, err := o.trie.TryGet(addr.Bytes())
		//if metrics.EnabledExpensive {
		//	o.AccountReads += time.Since(start)
		//}
		//if err != nil {
		//	fmt.Errorf("getDeleteOperationObject (%x) error: %v", addr.Bytes(), err)
		//	return nil
		//}
		//if len(enc) == 0 {
		//	return nil
		//}
		data = new(entity.StateAccount)
		//if err := rlp.DecodeBytes(enc, data); err != nil {
		//	log.Error("Failed to decode operation object", "addr", addr, "err", err)
		//	return nil
		//}
	}
	// 插入到活动集中
	obj := newObject(o, addr, *data)
	o.setOperationObject(obj)
	return obj
}

func (o *OperationDB) setOperationObject(object *OperationObject) {
	o.OperationObjects[object.Address()] = object
}

/*
Ancient的实现
*/
func (db *OperationDB) ReadAncients(fn func(op AncientReaderOp) error) (err error) {
	return fn(db)
}

func (o *OperationDB) HasAncient(kind string, number uint64) (bool, error) {
	panic("implement me")
}

func (o *OperationDB) Ancient(kind string, number uint64) ([]byte, error) {
	panic("implement me")
}

func (o *OperationDB) AncientRange(kind string, start, count, maxBytes uint64) ([][]byte, error) {
	panic("implement me")
}

func (o *OperationDB) Ancients() (uint64, error) {
	panic("implement me")
}

func (o *OperationDB) Tail() (uint64, error) {
	panic("implement me")
}

func (o *OperationDB) AncientSize(kind string) (uint64, error) {
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
func (s *OperationDB) Prepare(thash entity.Hash, ti int) {
	s.thash = thash
	s.txIndex = ti
}

//创建根状态数据库
func NewOperationDb(root entity.Hash, db DatabaseI) (*OperationDB, error) {
	tr, terr := db.OpenTrie(root)
	if terr != nil {
		return nil, terr
	}
	sdb := &OperationDB{
		db:                      db,
		trieI:                   tr,
		originalRoot:            root,
		OperationObjects:        make(map[entity.Address]*OperationObject),
		OperationObjectsPending: make(map[entity.Address]struct{}),
		OperationObjectsDirty:   make(map[entity.Address]struct{}),
		logs:                    make(map[entity.Hash][]*log.OctopusLog),
		preimages:               make(map[entity.Hash][]byte),
		journal:                 NewJournal(),
		accessList:              newAccessList(),
	}
	return sdb, nil
}

// 复制创建状态的深度、独立副本。复制状态的快照无法应用于副本。
func (s *OperationDB) Copy() *OperationDB {
	// 复制所有基本字段，初始化内存字段
	state := &OperationDB{
		db:                      s.db,
		trieI:                   s.db.CopyTrie(s.trieI),
		OperationObjects:        make(map[entity.Address]*OperationObject, len(s.journal.Dirties)),
		OperationObjectsPending: make(map[entity.Address]struct{}, len(s.OperationObjectsPending)),
		OperationObjectsDirty:   make(map[entity.Address]struct{}, len(s.journal.Dirties)),
		//refund:              s.refund,
		logs:      make(map[entity.Hash][]*log.OctopusLog, len(s.logs)),
		logSize:   s.logSize,
		preimages: make(map[entity.Hash][]byte, len(s.preimages)),
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

// StopRefetcher终止正在运行的预取程序，并报告收集的度量中的任何剩余统计信息。
func (s *OperationDB) StopPrefetcher() {
	if s.prefetcher != nil {
		//s.prefetcher.close()
		s.prefetcher = nil
	}
}

// ReadHeaderNumber返回分配给哈希的标头编号。
func ReadHeaderNumber(dbdb OperationDB, hash entity.Hash) *big.Int {
	he := block.Header{}
	data := db.Query(headerMark, utils.GetInToStr(hash), &he).(*block.Header)
	int := data.Number
	return int
}

/**
accessList
*/
type accessList struct {
	addresses map[entity.Address]int
	slots     []map[entity.Hash]struct{}
}

// Copy创建访问列表的独立副本。
func (a *accessList) Copy() *accessList {
	cp := newAccessList()
	for k, v := range a.addresses {
		cp.addresses[k] = v
	}
	cp.slots = make([]map[entity.Hash]struct{}, len(a.slots))
	for i, slotMap := range a.slots {
		newSlotmap := make(map[entity.Hash]struct{}, len(slotMap))
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
		addresses: make(map[entity.Address]int),
	}
}

type triePrefetcher struct {
	db       Database                    // 用于获取trie节点的数据库
	root     entity.Hash                 // 度量的Account trie的根哈希
	fetches  map[entity.Hash]TrieI       // 部分或完全获取程序尝试
	fetchers map[entity.Hash]*subfetcher // 每个trie的Subfetchers

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
		fetches: make(map[entity.Hash]TrieI), // 活动预取器使用回迁映射

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
	// 如果预取器已经是副本，请复制数据
	if p.fetches != nil {
		//for root, fetch := range p.fetches {
		//	copy.fetches[root] = p.db.CopyTrie(fetch)
		//}
		return copy
	}
	// 否则，我们将复制一个活动的抓取程序，检索当前状态
	for root, fetcher := range p.fetchers {
		copy.fetches[root] = fetcher.peek()
	}
	return copy
}

/**

 */
type subfetcher struct {
	db    Database    // 要加载trie NodeRough的数据库
	root  entity.Hash // 要预取的trie的根哈希
	trieI TrieI       // 正在使用节点填充Trie

	tasks [][]byte   // 排队等待检索的项目
	lock  sync.Mutex // 锁定以保护任务队列

	wake chan struct{}   // 如果计划了新任务，则唤醒通道
	stop chan struct{}   // 中断处理的通道
	term chan struct{}   // 信号iterruption的通道
	copy chan chan TrieI // 请求当前trie副本的通道

	seen map[string]struct{} // 跟踪已加载的条目
	dups int                 // 重复预加载任务数
	used [][]byte            // 跟踪最后使用的条目
}

//peek尝试以当前的任何形式检索fetcher的trie的深度副本。
func (sf *subfetcher) peek() TrieI {
	ch := make(chan TrieI)
	select {
	case sf.copy <- ch:
		// 子蚀刻器仍处于活动状态，请从中返回副本
		return <-ch

	case <-sf.term:
		// 子蚀刻程序已终止，请直接返回副本
		if sf.trieI == nil {
			return nil
		}
		return nil
	}
}

type cachingDB struct {
	db            *TrieDatabase
	codeSizeCache *lru.Cache
	codeCache     *fastcache.Cache
}

func (c *cachingDB) OpenTrie(root entity.Hash) (TrieI, error) {
	tr, err := NewSecure(root, c.db)
	if err != nil {
		return nil, err
	}
	return tr, nil
}

func (c *cachingDB) OpenStorageTrie(addrHash, root entity.Hash) (TrieI, error) {
	panic("implement me")
}

//CopyTrie返回给定trie的独立副本。
func (c *cachingDB) CopyTrie(trieI TrieI) TrieI {
	switch t := trieI.(type) {
	case *SecureTrie:
		return t.Copy()
	default:
		panic(fmt.Errorf("unknown trie type %T", t))
	}
}

func (c *cachingDB) ContractCode(addrHash, codeHash entity.Hash) ([]byte, error) {
	panic("implement me")
}

func (c *cachingDB) ContractCodeSize(addrHash, codeHash entity.Hash) (int, error) {
	panic("implement me")
}

func (c *cachingDB) TrieDB() *Database {
	panic("implement me")
}
