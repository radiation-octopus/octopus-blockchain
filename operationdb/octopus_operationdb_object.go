package operationdb

import (
	"bytes"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus/log"
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

	code Code // 合同字节码，在加载代码时设置

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
	//s.db.journal.append(nonceChange{
	//	account: &s.address,
	//	prev:    s.data.Nonce,
	//})
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
	//s.db.journal.append(balanceChange{
	//	account: &s.address,
	//	prev:    new(big.Int).Set(s.data.Balance),
	//})
	s.setBalance(amount)
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

// newObject创建操作对象。
func newObject(db *OperationDB, address entity.Address, data entity.StateAccount) *OperationObject {
	if data.Balance == nil {
		data.Balance = new(big.Int)
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
