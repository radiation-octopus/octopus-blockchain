package operationdb

import "github.com/radiation-octopus/octopus-blockchain/entity"

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