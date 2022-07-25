package operationdb

import (
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"math/big"
)

//journalEntry是状态更改日志中的修改条目，可以根据需要还原。
type journalEntry interface {
	// 还原撤消此日记账分录引入的更改。
	revert(db *OperationDB)

	// dirtied返回此日志条目修改的章鱼地址。
	dirtied() *entity.Address
}

//日志包含自上次状态提交以来应用的状态修改列表。这些被跟踪，以便在执行异常或请求撤销时能够恢复。
type Journal struct {
	entries []journalEntry         // 日记帐跟踪的当前更改
	Dirties map[entity.Address]int // 脏账户和更改数量
}

// newJournal创建一个新的初始化日志。
func NewJournal() *Journal {
	return &Journal{
		Dirties: make(map[entity.Address]int),
	}
}

// append在更改日志的末尾插入新的修改条目。
func (j *Journal) append(entry journalEntry) {
	j.entries = append(j.entries, entry)
	if addr := entry.dirtied(); addr != nil {
		j.Dirties[*addr]++
	}
}

type (
	// 对帐户trie的更改。
	createObjectChange struct {
		account *entity.Address
	}
	resetObjectChange struct {
		prev         *OperationObject
		prevdestruct bool
	}

	codeChange struct {
		account            *entity.Address
		prevcode, prevhash []byte
	}
	// 个人账户变更。
	balanceChange struct {
		account *entity.Address
		prev    *big.Int
	}
	nonceChange struct {
		account *entity.Address
		prev    uint64
	}
	storageChange struct {
		account       *entity.Address
		key, prevalue entity.Hash
	}
	suicideChange struct {
		account     *entity.Address
		prev        bool // 账户是否已经自杀
		prevbalance *big.Int
	}
	// 访问列表的更改
	accessListAddAccountChange struct {
		address *entity.Address
	}
	accessListAddSlotChange struct {
		address *entity.Address
		slot    *entity.Hash
	}
	// 对其他状态值的更改。
	refundChange struct {
		prev uint64
	}
	addLogChange struct {
		txhash entity.Hash
	}
)

func (ch codeChange) revert(o *OperationDB) {
	o.getOperationObject(*ch.account).setCode(entity.BytesToHash(ch.prevhash), ch.prevcode)
}

func (ch codeChange) dirtied() *entity.Address {
	return ch.account
}

func (ch createObjectChange) revert(db *OperationDB) {
	delete(db.OperationObjects, *ch.account)
	delete(db.OperationObjectsDirty, *ch.account)
}

func (ch createObjectChange) dirtied() *entity.Address {
	return ch.account
}

func (r resetObjectChange) revert(db *OperationDB) {
	db.setOperationObject(r.prev)
	//if !r.prevdestruct && db.snap != nil {
	//	delete(s.snapDestructs, ch.prev.addrHash)
	//}
}

func (r resetObjectChange) dirtied() *entity.Address {
	return nil
}

func (ch balanceChange) revert(s *OperationDB) {
	s.getOperationObject(*ch.account).setBalance(ch.prev)
}

func (ch balanceChange) dirtied() *entity.Address {
	return ch.account
}

func (ch nonceChange) revert(s *OperationDB) {
	s.getOperationObject(*ch.account).setNonce(ch.prev)
}

func (ch nonceChange) dirtied() *entity.Address {
	return ch.account
}

func (ch storageChange) revert(s *OperationDB) {
	s.getOperationObject(*ch.account).setState(ch.key, ch.prevalue)
}

func (ch storageChange) dirtied() *entity.Address {
	return ch.account
}

func (ch suicideChange) revert(s *OperationDB) {
	obj := s.getOperationObject(*ch.account)
	if obj != nil {
		obj.suicided = ch.prev
		obj.setBalance(ch.prevbalance)
	}
}

func (ch suicideChange) dirtied() *entity.Address {
	return ch.account
}
func (ch accessListAddAccountChange) revert(s *OperationDB) {
	/*
		这里一个重要的不变量是，每当添加（addr，slot）时，
		如果addr尚未存在，则添加会导致两个日记账分录：
		-一个代表地址，
		-一个用于（地址、插槽）
		因此，在展开更改时，我们始终可以在此时盲目删除（addr），因为在发生单个（addr）更改时，不会保留任何存储添加。
	*/
	s.accessList.DeleteAddress(*ch.address)
}

func (ch accessListAddAccountChange) dirtied() *entity.Address {
	return nil
}

func (ch accessListAddSlotChange) revert(s *OperationDB) {
	s.accessList.DeleteSlot(*ch.address, *ch.slot)
}

func (ch accessListAddSlotChange) dirtied() *entity.Address {
	return nil
}

func (ch refundChange) revert(s *OperationDB) {
	s.refund = ch.prev
}

func (ch refundChange) dirtied() *entity.Address {
	return nil
}

func (ch addLogChange) revert(s *OperationDB) {
	logs := s.logs[ch.txhash]
	if len(logs) == 1 {
		delete(s.logs, ch.txhash)
	} else {
		s.logs[ch.txhash] = logs[:len(logs)-1]
	}
	s.logSize--
}

func (ch addLogChange) dirtied() *entity.Address {
	return nil
}
