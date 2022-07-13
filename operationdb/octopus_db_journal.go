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
)

func (ch codeChange) revert(o *OperationDB) {
	o.getStateObject(*ch.account).setCode(entity.BytesToHash(ch.prevhash), ch.prevcode)
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
	s.getStateObject(*ch.account).setBalance(ch.prev)
}

func (ch balanceChange) dirtied() *entity.Address {
	return ch.account
}

func (ch nonceChange) revert(s *OperationDB) {
	s.getStateObject(*ch.account).setNonce(ch.prev)
}

func (ch nonceChange) dirtied() *entity.Address {
	return ch.account
}
