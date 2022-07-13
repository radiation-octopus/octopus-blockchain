package operationdb

import (
	"bytes"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
)

var (
	// emptyRoot是空trie的已知根哈希。
	emptyRoot = entity.BytesToHash(utils.Hex2Bytes("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"))

	emptyCodeHash = crypto.Keccak256(nil)
)

//数据库存储对象
type OperationObject struct {
	address  entity.Address
	addrHash entity.Hash
	data     entity.StateAccount
	db       *OperationDB

	// DB错误。状态对象由无法处理数据库级错误的一致性核心和VM使用。在数据库读取过程中发生的任何错误都会在此处进行记忆，并最终由StateDB返回。犯罪
	dbErr error

	// 写入缓存。
	trieI TrieI // 存储trie，首次访问时变为非零
	code  Code  // 合同字节码，在加载代码时设置

	originStorage  Storage //用于重复数据消除的原始条目的存储缓存重写、重置每个事务
	pendingStorage Storage //需要在整个块末尾刷新到磁盘的存储条目
	dirtyStorage   Storage //在当前事务执行中已修改的存储条目
	fakeStorage    Storage //调用者为调试目的构造的假存储。

	// 缓存标志。当一个对象被标记为自杀时，它将在状态转换的“更新”阶段从trie中删除。
	dirtyCode bool // 如果代码已更新，则为true
	suicided  bool
	deleted   bool
}

// empty返回帐户是否为空。
func (s *OperationObject) empty() bool {
	return s.data.Nonce == 0 && s.data.Balance.Sign() == 0 && bytes.Equal(s.data.CodeHash, emptyCodeHash)
}

// 返回合同/帐户的地址
func (s *OperationObject) Address() entity.Address {
	return s.address
}

// 代码返回与此对象关联的合同代码（如果有）。
func (s *OperationObject) Code(db DatabaseI) []byte {
	if s.code != nil {
		return s.code
	}
	if bytes.Equal(s.CodeHash(), emptyCodeHash) {
		return nil
	}
	code, err := db.ContractCode(s.addrHash, entity.BytesToHash(s.CodeHash()))
	if err != nil {
		log.Error("can't load code hash %x: %v", s.CodeHash(), err)
	}
	s.code = code
	return code
}

func (s *OperationObject) CodeHash() []byte {
	return s.data.CodeHash
}

func (s *OperationObject) Nonce() uint64 {
	return s.data.Nonce
}

func (s *OperationObject) SetNonce(nonce uint64) {
	s.db.journal.append(nonceChange{
		account: &s.address,
		prev:    s.data.Nonce,
	})
	s.setNonce(nonce)
}

func (s *OperationObject) setNonce(nonce uint64) {
	s.data.Nonce = nonce
}

func (s *OperationObject) Balance() *big.Int {
	return s.data.Balance
}

// 子平衡从s的余额中删除金额。它用于从转账的原始帐户中删除资金。
func (s *OperationObject) SubBalance(amount *big.Int) {
	if amount.Sign() == 0 {
		return
	}
	s.SetBalance(new(big.Int).Sub(s.Balance(), amount))
}

// AddBalance将金额添加到s的余额中。它用于将资金添加到转账的目标帐户。
func (s *OperationObject) AddBalance(amount *big.Int) {
	// EIP161：我们必须检查对象的空性，以便账户清算（0,0,0个对象）能够生效。
	if amount.Sign() == 0 {
		if s.empty() {
			s.touch()
		}
		return
	}
	s.SetBalance(new(big.Int).Add(s.Balance(), amount))
}

func (s *OperationObject) SetBalance(amount *big.Int) {
	s.db.journal.append(balanceChange{
		account: &s.address,
		prev:    new(big.Int).Set(s.data.Balance),
	})
	s.setBalance(amount)
}

// 设置存储用给定的状态存储替换整个状态存储。
//调用此函数后，将忽略所有原始状态，并且状态查找仅在伪状态存储中发生。
//注意：此功能仅用于调试目的。
func (s *OperationObject) SetStorage(storage map[entity.Hash]entity.Hash) {
	// 如果为零，则分配假存储。
	if s.fakeStorage == nil {
		s.fakeStorage = make(Storage)
	}
	for key, value := range storage {
		s.fakeStorage[key] = value
	}
	// 不要打扰日志，因为这个函数应该只用于调试，并且“伪”存储不会提交给数据库。
}

func (s *OperationObject) setBalance(amount *big.Int) {
	s.data.Balance = amount
}

func (s *OperationObject) touch() {
	//s.db.journal.append(touchChange{
	//	account: &s.address,
	//})
	//if s.address == ripemd {
	//	//显式地将其放在脏缓存中，否则会从平铺日志生成脏缓存。
	//	s.db.journal.dirty(s.address)
	//}
}

// Finalize将所有脏存储插槽移动到挂起区域，以便稍后散列或提交。它在每个事务结束时调用。
func (s *OperationObject) finalise(prefetch bool) {
	slotsToPrefetch := make([][]byte, 0, len(s.dirtyStorage))
	for key, value := range s.dirtyStorage {
		s.pendingStorage[key] = value
		if value != s.originStorage[key] {
			slotsToPrefetch = append(slotsToPrefetch, utils.CopyBytes(key[:])) // 关闭所需的副本
		}
	}
	if s.db.prefetcher != nil && prefetch && len(slotsToPrefetch) > 0 && s.data.Root != emptyRoot {
		s.db.prefetcher.prefetch(s.addrHash, s.data.Root, slotsToPrefetch)
	}
	if len(s.dirtyStorage) > 0 {
		s.dirtyStorage = make(Storage)
	}
}

// UpdateRoot将trie根设置为的当前根哈希
func (s *OperationObject) updateRoot(db DatabaseI) {
	// 如果没有任何更改，则不必费心对任何内容进行哈希运算
	if s.updateTrie(db) == nil {
		return
	}
	// 跟踪哈希存储trie所浪费的时间量
	//if metrics.EnabledExpensive {
	//	defer func(start time.Time) { s.db.StorageHashes += time.Since(start) }(time.Now())
	//}
	s.data.Root = s.trieI.Hash()
}

// 将对象的存储trie提交给db。这将更新trie根目录。
func (o *OperationObject) CommitTrie(db DatabaseI) (int, error) {
	//如果没有任何更改，则不必费心对任何内容进行哈希运算
	if o.updateTrie(db) == nil {
		return 0, nil
	}
	if o.dbErr != nil {
		return 0, o.dbErr
	}
	// 跟踪提交存储trie所浪费的时间
	//if metrics.EnabledExpensive {
	//	defer func(start time.Time) { o.db.StorageCommits += time.Since(start) }(time.Now())
	//}
	root, committed, err := o.trieI.Commit(nil)
	if err == nil {
		o.data.Root = root
	}
	return committed, err
}

// updateTrie将缓存的存储修改写入对象的存储trie。如果尚未加载trie且未进行任何更改，则返回nil
func (s *OperationObject) updateTrie(db DatabaseI) TrieI {
	// 确保所有脏插槽最终进入挂起的存储区域
	s.finalise(false) // 不再预取，如果需要，直接拉
	if len(s.pendingStorage) == 0 {
		return s.trieI
	}
	// 跟踪更新存储trie所浪费的时间
	//if metrics.EnabledExpensive {
	//	defer func(start time.Time) { s.db.StorageUpdates += time.Since(start) }(time.Now())
	//}
	// 对象的快照存储映射
	//var storage map[entity.Hash][]byte
	// 将所有挂起的更新插入trie
	tr := s.getTrie(db)
	//hasher := s.db.hasher

	usedStorage := make([][]byte, 0, len(s.pendingStorage))
	for key, value := range s.pendingStorage {
		// 跳过noop更改，保留实际更改
		if value == s.originStorage[key] {
			continue
		}
		s.originStorage[key] = value

		var v []byte
		if (value == entity.Hash{}) {
			s.setError(tr.TryDelete(key[:]))
			s.db.StorageDeleted += 1
		} else {
			// 编码[]字节不能失败，确定忽略错误。
			v, _ = rlp.EncodeToBytes(utils.TrimLeftZeroes(value[:]))
			s.setError(tr.TryUpdate(key[:], v))
			s.db.StorageUpdated += 1
		}
		// 如果状态快照处于活动状态，请缓存数据直到提交
		//if s.db.snap != nil {
		//	if storage == nil {
		//		// 检索旧的存储映射（如果可用），否则创建新的存储映射
		//		if storage = s.db.snapStorage[s.addrHash]; storage == nil {
		//			storage = make(map[entity.Hash][]byte)
		//			s.db.snapStorage[s.addrHash] = storage
		//		}
		//	}
		//	storage[crypto.HashData(hasher, key[:])] = v // 如果删除，v将为零
		//}
		usedStorage = append(usedStorage, utils.CopyBytes(key[:])) // 关闭所需的副本
	}
	if s.db.prefetcher != nil {
		s.db.prefetcher.used(s.addrHash, s.data.Root, usedStorage)
	}
	if len(s.pendingStorage) > 0 {
		s.pendingStorage = make(Storage)
	}
	return tr
}

func (s *OperationObject) getTrie(db DatabaseI) TrieI {
	if s.trieI == nil {
		// 首先尝试从预回迁器中回迁我们不预回迁空的尝试
		if s.data.Root != emptyRoot && s.db.prefetcher != nil {
			// 当矿工创建挂起状态时，没有预取器
			s.trieI = s.db.prefetcher.trie(s.addrHash, s.data.Root)
		}
		if s.trieI == nil {
			var err error
			s.trieI, err = db.OpenStorageTrie(s.addrHash, s.data.Root)
			if err != nil {
				s.trieI, _ = db.OpenStorageTrie(s.addrHash, entity.Hash{})
				s.setError(fmt.Errorf("can't create storage trie: %v", err))
			}
		}
	}
	return s.trieI
}

func (s *OperationObject) SetCode(codeHash entity.Hash, code []byte) {
	prevcode := s.Code(s.db.db)
	s.db.journal.append(codeChange{
		account:  &s.address,
		prevhash: s.CodeHash(),
		prevcode: prevcode,
	})
	s.setCode(codeHash, code)
}

func (s *OperationObject) setCode(codeHash entity.Hash, code []byte) {
	s.code = code
	s.data.CodeHash = codeHash[:]
	s.dirtyCode = true
}

// setError记住调用它时使用的第一个非零错误。
func (s *OperationObject) setError(err error) {
	if s.dbErr == nil {
		s.dbErr = err
	}
}

// newObject创建操作对象。
func newObject(db *OperationDB, address entity.Address, data entity.StateAccount) *OperationObject {
	if data.Balance == nil {
		data.Balance = new(big.Int)
		data.Balance = big.NewInt(20000)
	}
	if data.CodeHash == nil {
		data.CodeHash = emptyCodeHash
	}
	if data.Root == (entity.Hash{}) {
		data.Root = emptyRoot
	}
	return &OperationObject{
		db:       db,
		address:  address,
		addrHash: crypto.Keccak256Hash(address[:]),
		data:     data,
		//originStorage:  make(Storage),
		//pendingStorage: make(Storage),
		//dirtyStorage:   make(Storage),
	}
}

type Code []byte

type Storage map[entity.Hash]entity.Hash

func (s Storage) String() (str string) {
	for key, value := range s {
		str += fmt.Sprintf("%X : %X\n", key, value)
	}

	return
}

func (s *OperationObject) deepCopy(db *OperationDB) *OperationObject {
	operationObject := newObject(db, s.address, s.data)
	//if s.trie != nil {
	//	stateObject.trie = db.db.CopyTrie(s.trie)
	//}
	//stateObject.code = s.code
	//stateObject.dirtyStorage = s.dirtyStorage.Copy()
	//stateObject.originStorage = s.originStorage.Copy()
	//stateObject.pendingStorage = s.pendingStorage.Copy()
	//stateObject.suicided = s.suicided
	//stateObject.dirtyCode = s.dirtyCode
	//stateObject.deleted = s.deleted
	return operationObject
}
