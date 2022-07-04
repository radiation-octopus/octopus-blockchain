package operationdb

import (
	"fmt"
	"github.com/VictoriaMetrics/fastcache"
	lru "github.com/hashicorp/golang-lru"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/entity/rawdb"
	"github.com/radiation-octopus/octopus-blockchain/operationdb/tire"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus/log"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
	"sync"
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
	TrieDB() *tire.TrieDatabase
}

type TrieI interface {
	// GetKey返回以前用于存储值的哈希键的sha3前映像。
	GetKey([]byte) ([]byte, error)

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
	Commit(onleaf tire.LeafCallback) (entity.Hash, int, error)

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

	// 退款计数器，也用于状态转换。
	refund uint64

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
	AccountUpdated int
	StorageUpdated int
	AccountDeleted int
	StorageDeleted int
}

//setError记住调用它时使用的第一个非零错误。
func (o *OperationDB) setError(err error) {
	if o.dbErr == nil {
		o.dbErr = err
	}
}

// 数据库检索支持较低级别trie操作的低级别数据库。
func (o *OperationDB) Database() DatabaseI {
	return o.db
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
	fmt.Println("sub", operationObject.Address().String())
	if operationObject != nil {
		operationObject.SubBalance(amount)
	}
}

//headerTDMark将金额添加到与addr关联的帐户。
func (o *OperationDB) AddBalance(address entity.Address, amount *big.Int) {
	operationObject := o.GetOrNewOperationObject(address)
	fmt.Println("add", operationObject.Address().String())
	if operationObject != nil {
		operationObject.AddBalance(amount)
	}
}

// GetOrNewOperationObject检索操作对象，如果为nil，则创建新的操作对象。
func (o *OperationDB) GetOrNewOperationObject(address entity.Address) *OperationObject {
	operationObject := o.getOperationObject(address)
	if operationObject == nil {
		operationObject, _ = o.createObject(address)
	}
	return operationObject
}

// createObject创建一个新的状态对象。如果存在具有给定地址的现有帐户，则会覆盖该帐户并将其作为第二个返回值返回。
func (s *OperationDB) createObject(addr entity.Address) (newobj, prev *OperationObject) {
	prev = s.getDeletedOperationObject(addr) // 注意，prev可能已被删除，我们需要它！

	var prevdestruct bool
	//if s.snap != nil && prev != nil {
	//	_, prevdestruct = s.snapDestructs[prev.addrHash]
	//	if !prevdestruct {
	//		s.snapDestructs[prev.addrHash] = struct{}{}
	//	}
	//}
	newobj = newObject(s, addr, entity.StateAccount{})
	if prev == nil {
		s.journal.append(createObjectChange{account: &addr})
	} else {
		s.journal.append(resetObjectChange{prev: prev, prevdestruct: prevdestruct})
	}
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

func (o *OperationDB) SetCode(address entity.Address, code []byte) {
	operationObject := o.GetOrNewOperationObject(address)
	if operationObject != nil {
		operationObject.SetCode(crypto.Keccak256Hash(code), code)
	}
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
	//id := o.nextRevisionId
	//o.nextRevisionId++
	//o.validRevisions = append(o.validRevisions, revision{id, o.journal.length()})
	//return id
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

// IntermediateRoot计算状态trie的当前根哈希。在事务之间调用它以获取进入事务收据的根哈希。
func (o *OperationDB) IntermediateRoot(deleteEmptyObjects bool) entity.Hash {
	// 确定所有脏存储状态，并将其写入尝试
	o.Finalise(deleteEmptyObjects)

	// 如果有一个trie预取器正在运行，那么在我们开始检索尝试之后，它将被中止并进行不可撤销的修改。在这一轮使用之后，将其从statedb中删除。
	//这在拜占庭之前是很奇怪的，因为第一个tx运行时有预取器，其余的没有，但拜占庭之前，即使是最初的预取器也是无用的，所以没有睡眠丢失。
	prefetcher := o.prefetcher
	if o.prefetcher != nil {
		defer func() {
			o.prefetcher.close()
			o.prefetcher = nil
		}()
	}
	// 虽然天真地检索帐户trie，然后按顺序执行契约存储和帐户更新是有意义的，但这会使帐户预取器短路。
	//相反，让我们先处理所有的存储更新，让帐户prefechs只需要几毫秒的时间就可以从磁盘中提取有用的数据。
	for addr := range o.OperationObjectsPending {
		if obj := o.OperationObjects[addr]; !obj.deleted {
			obj.updateRoot(o.db)
		}
	}
	// 现在，我们将开始编写对trie的更改。到目前为止，的黎波里还没有动过。
	//我们可以与预取器进行检查，如果它可以给我们一个具有相同根的trie，但也有一些内容加载到其中。
	if prefetcher != nil {
		if trie := prefetcher.trie(entity.Hash{}, o.originalRoot); trie != nil {
			o.trieI = trie
		}
	}
	usedAddrs := make([][]byte, 0, len(o.OperationObjectsPending))
	for addr := range o.OperationObjectsPending {
		if obj := o.OperationObjects[addr]; obj.deleted {
			o.deleteOperationObject(obj)
			o.AccountDeleted += 1
		} else {
			o.updateOperationObject(obj)
			o.AccountUpdated += 1
		}
		usedAddrs = append(usedAddrs, utils.CopyBytes(addr[:])) // 关闭所需的副本
	}
	if prefetcher != nil {
		prefetcher.used(entity.Hash{}, o.originalRoot, usedAddrs)
	}
	if len(o.OperationObjectsPending) > 0 {
		o.OperationObjectsPending = make(map[entity.Address]struct{})
	}
	// 跟踪对帐户trie进行哈希运算所浪费的时间量
	//if metrics.EnabledExpensive {
	//	defer func(start time.Time) { o.AccountHashes += time.Since(start) }(time.Now())
	//}
	return o.trieI.Hash()
}

//deleteOperationObject从状态trie中删除给定对象。
func (o *OperationDB) deleteOperationObject(obj *OperationObject) {
	// 跟踪从trie中删除帐户所浪费的时间
	//if metrics.EnabledExpensive {
	//	defer func(start time.Time) { o.AccountUpdates += time.Since(start) }(time.Now())
	//}
	// 从trie中删除帐户
	addr := obj.Address()
	if err := o.trieI.TryDelete(addr[:]); err != nil {
		o.setError(fmt.Errorf("deleteStateObject (%x) error: %v", addr[:], err))
	}
}

//
//设置、更新和删除操作对象方法。
//

// updateStateObject将给定对象写入trie。
func (s *OperationDB) updateOperationObject(obj *OperationObject) {
	// 跟踪从trie更新帐户所浪费的时间
	//if metrics.EnabledExpensive {
	//	defer func(start time.Time) { s.AccountUpdates += time.Since(start) }(time.Now())
	//}
	// 对帐户进行编码并更新帐户trie
	addr := obj.Address()
	if err := s.trieI.TryUpdateAccount(addr[:], &obj.data); err != nil {
		s.setError(fmt.Errorf("updateStateObject (%x) error: %v", addr[:], err))
	}
	data := new(entity.StateAccount)
	enc, _ := s.trieI.TryGet(addr[:])

	if err := rlp.DecodeBytes(enc, data); err != nil {
		log.Error("Failed to decode operation object", "addr", addr, "err", err)
	}
	fmt.Println(data.Balance)
	// 如果状态快照处于活动状态，请缓存数据直到提交。
	//注意，此更新机制与删除并不对称，因为虽然它足以在提交时跟踪帐户更新，但删除需要在事务边界级别进行跟踪，以确保捕获状态清除。
	//if s.snap != nil {
	//	s.snapAccounts[obj.addrHash] = snapshot.SlimAccountRLP(obj.data.Nonce, obj.data.Balance, obj.data.Root, obj.data.CodeHash)
	//}
}

// Finalize通过删除o销毁的对象来最终确定状态，并清除日记账和退款。
//不过，Finalize目前还不会将任何更新推送到试用中。只有IntermediateRoot或Commit会这样做。
func (o *OperationDB) Finalise(deleteEmptyObjects bool) {
	addressesToPrefetch := make([][]byte, 0, len(o.journal.Dirties))
	for addr := range o.journal.Dirties {
		obj, exist := o.OperationObjects[addr]
		if !exist {
			// ripeMD在德克萨斯州0x1237f737031e40bcde4a8b7e717b2d15e3ecadfe49bb1bbc71ee9deb09c6fcf2区块1714175处被“触碰”，
			//表示德克萨斯州已耗尽gas，尽管此处不存在“触碰”的概念，但触碰事件仍将记录在日志中。
			//由于ripeMD是一种特殊的雪花，即使日志被还原，它也会在日志中持续存在。
			//在这种特殊情况下，它可能存在于's.journal中。dirties`但不在's.OperationObjects'中。因此，我们可以放心地忽略它
			continue
		}
		if obj.suicided || (deleteEmptyObjects && obj.empty()) {
			obj.deleted = true

			// 如果状态快照处于活动状态，请在此处标记销毁。
			//请注意，我们不能仅在一个块的末尾执行此操作，因为同一块中的多个事务可能会自毁，然后重新恢复帐户；但拍摄者需要两个事件。
			//if o.snap != nil {
			//	o.snapDestructs[obj.addrHash] = struct{}{} // 我们需要明确维护帐户删除（将无限期保留）
			//	delete(o.snapAccounts, obj.addrHash)       // 清除所有以前更新的帐户数据（可通过Rescurrect重新创建）
			//	delete(o.snapStorage, obj.addrHash)        // 清除所有以前更新的存储数据（可以通过Rescurrect重新创建）
			//}
		} else {
			obj.finalise(true) // 在后台预回迁插槽
		}
		o.OperationObjectsPending[addr] = struct{}{}
		o.OperationObjectsDirty[addr] = struct{}{}

		// 此时，还要将地址发送给Precher。Precher将开始加载尝试，当更改最终提交时，提交阶段将快得多
		addressesToPrefetch = append(addressesToPrefetch, utils.CopyBytes(addr[:])) // 关闭所需的副本
	}
	if o.prefetcher != nil && len(addressesToPrefetch) > 0 {
		o.prefetcher.prefetch(entity.Hash{}, o.originalRoot, addressesToPrefetch)
	}
	// 使日记帐无效，因为不允许跨事务还原。
	o.clearJournalAndRefund()
}

func (o *OperationDB) clearJournalAndRefund() {
	if len(o.journal.entries) > 0 {
		o.journal = NewJournal()
		o.refund = 0
	}
	//o.validRevisions = o.validRevisions[:0] // 可以在没有日志实体的情况下创建快照
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
	//if obj := o.OperationObjects[addr]; obj != nil {
	//	return obj
	//}
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
		enc, err := o.trieI.TryGet(addr.Bytes())
		//if metrics.EnabledExpensive {
		//	o.AccountReads += time.Since(start)
		//}
		if err != nil {
			fmt.Errorf("getDeleteOperationObject (%x) error: %v", addr.Bytes(), err)
			return nil
		}
		if len(enc) == 0 {
			return nil
		}
		data = new(entity.StateAccount)
		if err := rlp.DecodeBytes(enc, data); err != nil {
			log.Error("Failed to decode operation object", "addr", addr, "err", err)
			return nil
		}
	}
	// 插入到活动集中
	obj := newObject(o, addr, *data)
	o.setOperationObject(obj)
	return obj
}

// getStateObject检索地址给定的状态对象，如果在此执行上下文中找不到或删除了该对象，则返回nil。如果需要区分不存在/刚刚删除，请使用getDeletedStateObject。
func (o *OperationDB) getStateObject(addr entity.Address) *OperationObject {
	if obj := o.getDeletedStateObject(addr); obj != nil && !obj.deleted {
		return obj
	}
	return nil
}

// getDeletedStateObject类似于getStateObject，但它不会为已删除的状态对象返回nil，而是返回设置了deleted标志的实际对象。
//状态日志需要这样才能恢复到正确的s-destructed对象，而不是擦除关于状态对象的所有知识。
func (o *OperationDB) getDeletedStateObject(addr entity.Address) *OperationObject {
	// 首选活动对象（如果有）
	if obj := o.OperationObjects[addr]; obj != nil {
		return obj
	}
	// 如果没有可用的活动对象，请尝试使用快照
	var data *entity.StateAccount
	//if s.snap != nil {
	//	start := time.Now()
	//	acc, err := s.snap.Account(crypto.HashData(s.hasher, addr.Bytes()))
	//	if metrics.EnabledExpensive {
	//		s.SnapshotAccountReads += time.Since(start)
	//	}
	//	if err == nil {
	//		if acc == nil {
	//			return nil
	//		}
	//		data = &types.StateAccount{
	//			Nonce:    acc.Nonce,
	//			Balance:  acc.Balance,
	//			CodeHash: acc.CodeHash,
	//			Root:     common.BytesToHash(acc.Root),
	//		}
	//		if len(data.CodeHash) == 0 {
	//			data.CodeHash = emptyCodeHash
	//		}
	//		if data.Root == (common.Hash{}) {
	//			data.Root = emptyRoot
	//		}
	//	}
	//}
	// 如果快照不可用或读取失败，请从数据库加载
	if data == nil {
		//start := time.Now()
		enc, err := o.trieI.TryGet(addr.Bytes())
		//if metrics.EnabledExpensive {
		//	o.AccountReads += time.Since(start)
		//}
		if err != nil {
			o.setError(fmt.Errorf("getDeleteStateObject (%x) error: %v", addr.Bytes(), err))
			return nil
		}
		if len(enc) == 0 {
			return nil
		}
		data = new(entity.StateAccount)
		if err := rlp.DecodeBytes(enc, data); err != nil {
			log.Error("Failed to decode state object", "addr", addr, "err", err)
			return nil
		}
	}
	// 插入到活动集中
	obj := newObject(o, addr, *data)
	o.setOperationObject(obj)
	return obj
}

func (o *OperationDB) setOperationObject(object *OperationObject) {
	o.OperationObjects[object.Address()] = object
}

// Commit将状态写入底层内存中的trie数据库。
func (o *OperationDB) Commit(deleteEmptyObjects bool) (entity.Hash, error) {
	if o.dbErr != nil {
		return entity.Hash{}, fmt.Errorf("commit aborted due to earlier error: %v", o)
	}
	// 完成所有挂起的更改并将所有内容合并到尝试中
	o.IntermediateRoot(deleteEmptyObjects)

	// 将对象提交到trie，测量运行时间
	var storageCommitted int
	codeWriter := o.db.TrieDB().DiskDB().NewBatch()
	for addr := range o.OperationObjectsDirty {
		if obj := o.OperationObjects[addr]; !obj.deleted {
			// 编写与状态对象关联的任何合同代码
			if obj.code != nil && obj.dirtyCode {
				rawdb.WriteCode(codeWriter, entity.BytesToHash(obj.CodeHash()), obj.code)
				obj.dirtyCode = false
			}
			// 将状态对象中的任何存储更改写入其存储trie
			committed, err := obj.CommitTrie(o.db)
			if err != nil {
				return entity.Hash{}, err
			}
			storageCommitted += committed
		}
	}
	if len(o.OperationObjectsDirty) > 0 {
		o.OperationObjectsDirty = make(map[entity.Address]struct{})
	}
	if codeWriter.ValueSize() > 0 {
		if err := codeWriter.Write(); err != nil {
			log.Info("Failed to commit dirty codes", "error", err)
		}
	}
	// 写下更改的帐户，测量浪费的时间量
	//var start time.Time
	//if metrics.EnabledExpensive {
	//	start = time.Now()
	//}
	//onleaf funct被称为\u serially\uuu0，因此每次都可以重用同一个帐户进行解组。
	var account entity.StateAccount
	root, _, err := o.trieI.Commit(func(_ [][]byte, _ []byte, leaf []byte, parent entity.Hash) error {
		if err := rlp.DecodeBytes(leaf, &account); err != nil {
			return nil
		}
		if account.Root != emptyRoot {
			o.db.TrieDB().Reference(account.Root, parent)
		}
		return nil
	})
	if err != nil {
		return entity.Hash{}, err
	}
	//if metrics.EnabledExpensive {
	//	o.AccountCommits += time.Since(start)
	//
	//	accountUpdatedMeter.Mark(int64(o.AccountUpdated))
	//	storageUpdatedMeter.Mark(int64(o.StorageUpdated))
	//	accountDeletedMeter.Mark(int64(o.AccountDeleted))
	//	storageDeletedMeter.Mark(int64(o.StorageDeleted))
	//	accountCommittedMeter.Mark(int64(accountCommitted))
	//	storageCommittedMeter.Mark(int64(storageCommitted))
	//	o.AccountUpdated, o.AccountDeleted = 0, 0
	//	o.StorageUpdated, o.StorageDeleted = 0, 0
	//}
	// 如果已启用快照，请使用此新版本更新快照树
	//if o.snap != nil {
	//	if metrics.EnabledExpensive {
	//		defer func(start time.Time) { o.SnapshotCommits += time.Since(start) }(time.Now())
	//	}
	//	// Only update if there's a state transition (skip empty Clique blocks)
	//	if parent := o.snap.Root(); parent != root {
	//		if err := o.snaps.Update(root, parent, o.snapDestructs, o.snapAccounts, o.snapStorage); err != nil {
	//			log.Warn("Failed to update snapshot tree", "from", parent, "to", root, "err", err)
	//		}
	//		// Keep 128 diff layers in the memory, persistent layer is 129th.
	//		// - head layer is paired with HEAD state
	//		// - head-1 layer is paired with HEAD-1 state
	//		// - head-127 layer(bottom-most diff layer) is paired with HEAD-127 state
	//		if err := o.snaps.Cap(root, 128); err != nil {
	//			log.Warn("Failed to cap snapshot tree", "root", root, "layers", 128, "err", err)
	//		}
	//	}
	//	o.snap, o.snapDestructs, o.snapAccounts, o.snapStorage = nil, nil, nil, nil
	//}
	o.originalRoot = root
	return root, err
}

/*
Ancient的实现
*/
func (db *OperationDB) ReadAncients(fn func(op typedb.AncientReaderOp) error) (err error) {
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

//Prepare设置EVM发出新状态日志时使用的当前事务哈希和索引。
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
		s.prefetcher.close()
		s.prefetcher = nil
	}
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
	db       DatabaseI              // 用于获取trie节点的数据库
	root     entity.Hash            // 度量的Account trie的根哈希
	fetches  map[string]TrieI       // 部分或完全获取程序尝试
	fetchers map[string]*subfetcher // 每个trie的Subfetchers

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
		fetches: make(map[string]TrieI), // 活动预取器使用回迁映射

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

// 预回迁计划一批要预回迁的trie项目。
func (p *triePrefetcher) prefetch(owner entity.Hash, root entity.Hash, keys [][]byte) {
	// 如果预取器处于非活动状态，请退出
	if p.fetches != nil {
		return
	}
	// 活动获取程序，计划检索
	id := p.trieID(owner, root)
	fetcher := p.fetchers[id]
	if fetcher == nil {
		fetcher = newSubfetcher(p.db, owner, root)
		p.fetchers[id] = fetcher
	}
	fetcher.schedule(keys)
}

//used标记一批状态项，用于创建有关预取器的有用性或浪费性的统计信息。
func (p *triePrefetcher) used(owner entity.Hash, root entity.Hash, used [][]byte) {
	if fetcher := p.fetchers[p.trieID(owner, root)]; fetcher != nil {
		fetcher.used = used
	}
}

//trieID返回唯一的trie标识符，该标识符由trie所有者和根哈希组成。
func (p *triePrefetcher) trieID(owner entity.Hash, root entity.Hash) string {
	return string(append(owner.Bytes(), root.Bytes()...))
}

//close迭代所有子蚀刻器，中止任何剩余的旋转，并将统计信息报告给metrics子系统。
func (p *triePrefetcher) close() {
	for _, fetcher := range p.fetchers {
		fetcher.abort() // 可多次安全操作

		for _, key := range fetcher.used {
			delete(fetcher.seen, string(key))
		}
		//if metrics.Enabled {
		//	if fetcher.root == p.root {
		//		p.accountLoadMeter.Mark(int64(len(fetcher.seen)))
		//		p.accountDupMeter.Mark(int64(fetcher.dups))
		//		p.accountSkipMeter.Mark(int64(len(fetcher.tasks)))
		//
		//		for _, key := range fetcher.used {
		//			delete(fetcher.seen, string(key))
		//		}
		//		p.accountWasteMeter.Mark(int64(len(fetcher.seen)))
		//	} else {
		//		p.storageLoadMeter.Mark(int64(len(fetcher.seen)))
		//		p.storageDupMeter.Mark(int64(fetcher.dups))
		//		p.storageSkipMeter.Mark(int64(len(fetcher.tasks)))
		//
		//		for _, key := range fetcher.used {
		//			delete(fetcher.seen, string(key))
		//		}
		//		p.storageWasteMeter.Mark(int64(len(fetcher.seen)))
		//	}
		//}
	}
	// 清除所有抓取程序（第二次调用时会崩溃，注意）
	p.fetchers = nil
}

// trie返回与根哈希匹配的trie，如果预取程序没有，则返回nil。
func (p *triePrefetcher) trie(owner entity.Hash, root entity.Hash) TrieI {
	// 如果预取器处于非活动状态，请从现有深度副本返回
	id := p.trieID(owner, root)
	if p.fetches != nil {
		trie := p.fetches[id]
		if trie == nil {
			//p.deliveryMissMeter.Mark(1)
			return nil
		}
		return p.db.CopyTrie(trie)
	}
	// 否则，预取程序处于活动状态，如果没有为此根预取trie
	fetcher := p.fetchers[id]
	if fetcher == nil {
		//p.deliveryMissMeter.Mark(1)
		return nil
	}
	// 如果预取程序仍在运行，请中断预取程序，并返回任何预加载trie的副本。
	fetcher.abort() // 可多次安全操作

	trie := fetcher.peek()
	if trie == nil {
		//p.deliveryMissMeter.Mark(1)
		return nil
	}
	return trie
}

/**

 */
type subfetcher struct {
	db    DatabaseI   // 要加载trie NodeRough的数据库
	owner entity.Hash //trie的所有者，通常是帐户哈希
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

// newSubfetcher创建一个goroutine来预取属于特定根哈希的状态项。
func newSubfetcher(db DatabaseI, owner entity.Hash, root entity.Hash) *subfetcher {
	sf := &subfetcher{
		db:    db,
		owner: owner,
		root:  root,
		wake:  make(chan struct{}, 1),
		stop:  make(chan struct{}),
		term:  make(chan struct{}),
		copy:  make(chan chan TrieI),
		seen:  make(map[string]struct{}),
	}
	go sf.loop()
	return sf
}

//计划将一批trie密钥添加到队列以进行预取。
func (sf *subfetcher) schedule(keys [][]byte) {
	// 将任务附加到当前队列
	sf.lock.Lock()
	sf.tasks = append(sf.tasks, keys...)
	sf.lock.Unlock()

	// 通知预取器，如果已经终止就可以了
	select {
	case sf.wake <- struct{}{}:
	default:
	}
}

//中止立即中断子蚀刻器。多次调用abort是安全的，但它不是线程安全的。
func (sf *subfetcher) abort() {
	select {
	case <-sf.stop:
	default:
		close(sf.stop)
	}
	<-sf.term
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

// 循环等待调度新任务，并一直加载它们，直到用完任务或检索到其基础trie以进行提交。
func (sf *subfetcher) loop() {
	// 无论循环如何停止，向任何等待它终止的人发出信号
	defer close(sf.term)

	// 首先打开trie，如果失败，则停止处理
	if sf.owner == (entity.Hash{}) {
		trie, err := sf.db.OpenTrie(sf.root)
		if err != nil {
			log.Warn("Trie prefetcher failed opening trie", "root", sf.root, "err", err)
			return
		}
		sf.trieI = trie
	} else {
		trie, err := sf.db.OpenStorageTrie(sf.owner, sf.root)
		if err != nil {
			log.Warn("Trie prefetcher failed opening trie", "root", sf.root, "err", err)
			return
		}
		sf.trieI = trie
	}
	//Trie已成功打开，保留预取项目
	for {
		select {
		case <-sf.wake:
			// Subtetcher被唤醒，检索任何任务以避免旋转锁
			sf.lock.Lock()
			tasks := sf.tasks
			sf.tasks = nil
			sf.lock.Unlock()

			// 预回迁任何任务，直到循环中断
			for i, task := range tasks {
				select {
				case <-sf.stop:
					// 如果要求终止，则添加任何剩余物并返回
					sf.lock.Lock()
					sf.tasks = append(sf.tasks, tasks[i:]...)
					sf.lock.Unlock()
					return

				case ch := <-sf.copy:
					// 有人想要一份当前trie的副本，答应他们
					ch <- sf.db.CopyTrie(sf.trieI)

				default:
					// 尚未收到终止请求，请预取下一个条目
					if _, ok := sf.seen[string(task)]; ok {
						sf.dups++
					} else {
						sf.trieI.TryGet(task)
						sf.seen[string(task)] = struct{}{}
					}
				}
			}

		case ch := <-sf.copy:
			// 有人想要一份当前trie的副本，答应他们
			ch <- sf.db.CopyTrie(sf.trieI)

		case <-sf.stop:
			// 请求终止，中止并保留剩余任务
			return
		}
	}
}

type cachingDB struct {
	db            *tire.TrieDatabase
	codeSizeCache *lru.Cache
	codeCache     *fastcache.Cache
}

func (c *cachingDB) OpenTrie(root entity.Hash) (TrieI, error) {
	tr, err := tire.NewSecure(entity.Hash{}, root, c.db)
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
	case *tire.SecureTrie:
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

func (c *cachingDB) TrieDB() *tire.TrieDatabase {
	return c.db
}
