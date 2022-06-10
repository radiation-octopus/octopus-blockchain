package operationdb

import (
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
)

var (
	// emptyState是空状态trie项的已知哈希。
	emptyState = crypto.Keccak256Hash(nil)
)

// Trie是Merkle Patricia Trie。零值是没有数据库的空trie。
//使用New创建位于数据库顶部的trie。同时使用Trie不安全。
type Trie struct {
	db   *TrieDatabase
	root node

	//跟踪自上次哈希操作以来插入的叶数。
	//此数字不会直接映射到实际未剪切的节点数
	unhashed int

	// 跟踪器是状态差异跟踪器，可用于跟踪新添加/删除的trie节点。它将在每次提交操作后重置。
	tracer *tracer
}

// Copy返回Trie的副本。
func (t *Trie) Copy() *Trie {
	return &Trie{
		db:       t.db,
		root:     t.root,
		unhashed: t.unhashed,
		tracer:   t.tracer.copy(),
	}
}

// NewSecure使用备份数据库中的现有根节点和内存节点池中的可选中间节点创建一个trie。
//如果root是空字符串的零哈希或sha3哈希，则trie最初为空。
//否则，如果db为nil，New将死机，如果找不到根节点，则返回MissingNodeError。
//访问trie会根据需要从数据库或节点池加载节点。加载的节点将一直保留到其“缓存生成”过期。
//每次调用提交都会创建一个新的缓存生成。cachelimit设置要保留的过去缓存生成数。
func NewSecure(root entity.Hash, db *TrieDatabase) (*SecureTrie, error) {
	if db == nil {
		panic("NewSecure called without a Triedatabase")
	}
	trie, err := New(root, db)
	if err != nil {
		return nil, err
	}
	return &SecureTrie{trie: *trie}, nil
}

// New使用db中的现有根节点创建trie。
//如果root是空字符串的零哈希或sha3哈希，则trie最初为空，不需要数据库。
//否则，如果db为nil，New将死机，如果数据库中不存在root，则返回MissingNodeError。
//访问trie会根据需要从db加载节点。
func New(root entity.Hash, db *TrieDatabase) (*Trie, error) {
	if db == nil {
		panic("trie.New called without a database")
	}
	trie := &Trie{
		db: db,
		//tracer: newTracer(),
	}
	//if root != (entity.Hash{}) && root != emptyRoot {
	//	rootnode, err := trie.resolveHash(root[:], nil)
	//	if err != nil {
	//		return nil, err
	//	}
	//	trie.root = rootnode
	//}
	return trie, nil
}

// SecureTrie对于并发使用不安全。
type SecureTrie struct {
	trie             Trie
	hashKeyBuf       [entity.HashLength]byte
	secKeyCache      map[string][]byte
	secKeyCacheOwner *SecureTrie // 指向self的指针，在不匹配时替换密钥缓存
}

func (s *SecureTrie) GetKey(bytes []byte) []byte {
	panic("implement me")
}

func (s *SecureTrie) TryGet(key []byte) ([]byte, error) {
	panic("implement me")
}

func (s *SecureTrie) TryUpdateAccount(key []byte, account *entity.StateAccount) error {
	panic("implement me")
}

func (s *SecureTrie) TryUpdate(key, value []byte) error {
	panic("implement me")
}

func (s *SecureTrie) TryDelete(key []byte) error {
	panic("implement me")
}

func (s *SecureTrie) Hash() entity.Hash {
	panic("implement me")
}

// Copy返回SecureTrie的副本。
func (t *SecureTrie) Copy() *SecureTrie {
	return &SecureTrie{
		trie:        *t.trie.Copy(),
		secKeyCache: t.secKeyCache,
	}
}
