package tire

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
	origin map[string][]byte
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
		origin = make(map[string][]byte)
	)
	for key := range t.insert {
		insert[key] = struct{}{}
	}
	for key := range t.delete {
		delete[key] = struct{}{}
	}
	for key, val := range t.origin {
		origin[key] = val
	}
	return &tracer{
		insert: insert,
		delete: delete,
		origin: origin,
	}
}

// 重置清除跟踪器跟踪的内容。
func (t *tracer) reset() {
	// 当前未使用跟踪程序，请稍后删除此检查。
	if t == nil {
		return
	}
	t.insert = make(map[string]struct{})
	t.delete = make(map[string]struct{})
	t.origin = make(map[string][]byte)
}

// onInsert跟踪新插入的trie节点。
//如果它已经在删除集中（复活的节点），那么只需将其作为“未触及”从删除集中擦除即可。
func (t *tracer) onInsert(key []byte) {
	// 当前未使用跟踪程序，请稍后删除此检查。
	if t == nil {
		return
	}
	if _, present := t.delete[string(key)]; present {
		delete(t.delete, string(key))
		return
	}
	t.insert[string(key)] = struct{}{}
}

// onDelete跟踪新删除的trie节点。如果它已经在加法集中，那么只需将其从加法集中擦除，因为它未被触及。
func (t *tracer) onDelete(key []byte) {
	// 当前未使用跟踪程序，请稍后删除此检查。
	if t == nil {
		return
	}
	if _, present := t.insert[string(key)]; present {
		delete(t.insert, string(key))
		return
	}
	t.delete[string(key)] = struct{}{}
}
