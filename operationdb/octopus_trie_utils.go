package operationdb

// tracer跟踪trie节点的更改。
//在trie操作期间，一些节点可以从trie中删除，而这些删除的节点不会被trie捕获。
//Hasher或trie。提交人。因此，这些已删除的节点根本不会从磁盘中删除。
//Tracer是一种辅助工具，用于跟踪trie的所有插入和删除操作，并最终捕获所有删除的节点。
//变化的节点主要分为两类：叶节点和中间节点。
//前者由呼叫者插入/删除，而后者插入/删除是为了遵循trie规则。
//无论节点是否嵌入其父节点，此工具都可以跟踪所有节点，但valueNode永远不会被跟踪。
//注意跟踪程序不是线程安全的，调用方应该自己负责处理并发问题。
type tracer struct {
	insert map[string]struct{}
	delete map[string]struct{}
}

// copy返回深度复制的跟踪器实例。
func (t *tracer) copy() *tracer {
	// 当前未使用跟踪程序，请稍后删除此检查。
	if t == nil {
		return nil
	}
	var (
		insert = make(map[string]struct{})
		delete = make(map[string]struct{})
	)
	for key := range t.insert {
		insert[key] = struct{}{}
	}
	for key := range t.delete {
		delete[key] = struct{}{}
	}
	return &tracer{
		insert: insert,
		delete: delete,
	}
}
