package operationdb

import (
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
)

var (
	// emptyRoot是空trie的已知根哈希。
	emptyRoot = entity.BytesToHash(utils.Hex2Bytes("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"))
)

type Database struct {
}

func (op *Database) OpenTrie(root entity.Hash) (Trie, error) {
	panic("implement me")
}

func (op *Database) OpenStorageTrie(addrHash, root entity.Hash) (Trie, error) {
	panic("implement me")
}

func (op *Database) CopyTrie(trie Trie) Trie {
	panic("implement me")
}

func (op *Database) ContractCode(addrHash, codeHash entity.Hash) ([]byte, error) {
	panic("implement me")
}

func (op *Database) ContractCodeSize(addrHash, codeHash entity.Hash) (int, error) {
	panic("implement me")
}

func (op *Database) TrieDB() *Database {
	panic("implement me")
}

//数据库存储对象
type OperationObject struct {
	address  entity.Address
	addrHash entity.Hash
	data     entity.StateAccount
	db       *OperationDB

	code Code // 合同字节码，在加载代码时设置

}

// newObject创建操作对象。
func newObject(db *OperationDB, address entity.Address, data entity.StateAccount) *OperationObject {
	if data.Balance == nil {
		data.Balance = new(big.Int)
	}
	//if data.CodeHash == nil {
	//	data.CodeHash = emptyCodeHash
	//}
	if data.Root == (entity.Hash{}) {
		data.Root = emptyRoot
	}
	return &OperationObject{
		db:      db,
		address: address,
		//addrHash:       crypto.Keccak256Hash(address[:]),
		data: data,
		//originStorage:  make(Storage),
		//pendingStorage: make(Storage),
		//dirtyStorage:   make(Storage),
	}
}

type Code []byte

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