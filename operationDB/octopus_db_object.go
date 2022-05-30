package operationDB

import (
	operationUtils "github.com/radiation-octopus/octopus-blockchain/operationUtils"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
)

var (
	// emptyRoot is the known root hash of an empty trie.
	emptyRoot = operationUtils.BytesToHash(utils.Hex2Bytes("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"))
)

type Database struct {
}

func (op Database) OpenTrie(root operationUtils.Hash) (Trie, error) {
	panic("implement me")
}

func (op Database) OpenStorageTrie(addrHash, root operationUtils.Hash) (Trie, error) {
	panic("implement me")
}

func (op Database) CopyTrie(trie Trie) Trie {
	panic("implement me")
}

func (op Database) ContractCode(addrHash, codeHash operationUtils.Hash) ([]byte, error) {
	panic("implement me")
}

func (op Database) ContractCodeSize(addrHash, codeHash operationUtils.Hash) (int, error) {
	panic("implement me")
}

func (op Database) TrieDB() *Database {
	panic("implement me")
}

//账户结构体
type StateAccount struct {
	Nonce    uint64
	Balance  *big.Int
	Root     operationUtils.Hash // 存储trie的merkle根
	CodeHash []byte
}

//数据库存储对象
type OperationObject struct {
	address  operationUtils.Address
	addrHash operationUtils.Hash
	data     StateAccount
	db       *OperationDB

	code Code // 合同字节码，在加载代码时设置

}

// newObject创建操作对象。
func newObject(db *OperationDB, address operationUtils.Address, data StateAccount) *OperationObject {
	if data.Balance == nil {
		data.Balance = new(big.Int)
	}
	//if data.CodeHash == nil {
	//	data.CodeHash = emptyCodeHash
	//}
	if data.Root == (operationUtils.Hash{}) {
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
