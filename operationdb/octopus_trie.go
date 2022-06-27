package operationdb

import (
	"bytes"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus/utils"
	"sync"
)

var (
	// emptyState是空状态trie项的已知哈希。
	emptyState = crypto.Keccak256Hash(nil)
)

// LeafCallback 是当trie操作到达叶节点时调用的回调类型。
//路径是一个路径元组，用于标识单个trie（帐户）或分层trie（帐户->存储）中的特定trie节点。元组中的每个路径都是原始格式（32字节）。
//hexpath是标识trie节点的复合hexary路径。如果trie节点位于分层trie中，则所有密钥字节都将转换为六进制半字节，并与父路径合成。
//状态同步和提交使用它来处理帐户和存储尝试之间的外部引用。在状态修复中，它还用于提取具有相应路径的原始状态（叶节点）。
type LeafCallback func(paths [][]byte, hexpath []byte, leaf []byte, parent entity.Hash) error

// Trie是Merkle Patricia Trie。零值是没有数据库的空trie。
//使用New创建位于数据库顶部的trie。同时使用Trie不安全。
type Trie struct {
	db    *TrieDatabase
	root  node
	owner entity.Hash

	//跟踪自上次哈希操作以来插入的叶数。
	//此数字不会直接映射到实际未剪切的节点数
	unhashed int

	// 跟踪器是状态差异跟踪器，可用于跟踪新添加/删除的trie节点。它将在每次提交操作后重置。
	tracer *tracer
}

func (t *Trie) resolveHash(n hashNode, prefix []byte) (node, error) {
	hash := entity.BytesToHash(n)
	if node := t.db.node(hash); node != nil {
		return node, nil
	}
	return nil, &MissingNodeError{Owner: t.owner, NodeHash: hash, Path: prefix}
}

//newFlag返回新创建节点的缓存标志值。
func (t *Trie) newFlag() nodeFlag {
	return nodeFlag{dirty: true}
}

func (t *Trie) tryGet(origNode node, key []byte, pos int) (value []byte, newnode node, didResolve bool, err error) {
	switch n := (origNode).(type) {
	case nil:
		return nil, nil, false, nil
	case valueNode:
		return n, n, false, nil
	case *shortNode:
		if len(key)-pos < len(n.Key) || !bytes.Equal(n.Key, key[pos:pos+len(n.Key)]) {
			//在trie中找不到密钥
			return nil, n, false, nil
		}
		value, newnode, didResolve, err = t.tryGet(n.Val, key, pos+len(n.Key))
		if err == nil && didResolve {
			n = n.copy()
			n.Val = newnode
		}
		return value, n, didResolve, err
	case *fullNode:
		value, newnode, didResolve, err = t.tryGet(n.Children[key[pos]], key, pos+1)
		if err == nil && didResolve {
			n = n.copy()
			n.Children[key[pos]] = newnode
		}
		return value, n, didResolve, err
	case hashNode:
		child, err := t.resolveHash(n, key[:pos])
		if err != nil {
			return nil, n, true, err
		}
		value, newnode, _, err := t.tryGet(child, key, pos)
		return value, newnode, true, err
	default:
		panic(fmt.Sprintf("%T: invalid node: %v", origNode, origNode))
	}
}

// TryGet返回存储在trie中的键的值。调用者不得修改值字节。如果在数据库中找不到节点，则返回MissingNodeError。
func (t *Trie) TryGet(key []byte) ([]byte, error) {
	value, newroot, didResolve, err := t.tryGet(t.root, keybytesToHex(key), 0)
	if err == nil && didResolve {
		t.root = newroot
	}
	return value, err
}

// TryUpdate将键与trie中的值相关联。对Get的后续调用将返回值。
//如果值的长度为零，则会从trie中删除任何现有值，并且对Get的调用将返回nil。
//值字节存储在trie中时，调用者不得修改它们。如果在数据库中找不到节点，则返回MissingNodeError。
func (t *Trie) TryUpdate(key, value []byte) error {
	t.unhashed++
	k := keybytesToHex(key)
	if len(value) != 0 {
		_, n, err := t.insert(t.root, nil, k, valueNode(value))
		if err != nil {
			return err
		}
		t.root = n
	} else {
		_, n, err := t.delete(t.root, nil, k)
		if err != nil {
			return err
		}
		t.root = n
	}
	return nil
}

func (t *Trie) insert(n node, prefix, key []byte, value node) (bool, node, error) {
	if len(key) == 0 {
		if v, ok := n.(valueNode); ok {
			return !bytes.Equal(v, value.(valueNode)), value, nil
		}
		return true, value, nil
	}
	switch n := n.(type) {
	case *shortNode:
		matchlen := prefixLen(key, n.Key)
		//如果整个键匹配，则保持此短节点不变，只更新值。
		if matchlen == len(n.Key) {
			dirty, nn, err := t.insert(n.Val, append(prefix, key[:matchlen]...), key[matchlen:], value)
			if !dirty || err != nil {
				return false, n, err
			}
			return true, &shortNode{n.Key, nn, t.newFlag()}, nil
		}
		// 否则，在它们不同的索引处进行分支。
		branch := &fullNode{flags: t.newFlag()}
		var err error
		_, branch.Children[n.Key[matchlen]], err = t.insert(nil, append(prefix, n.Key[:matchlen+1]...), n.Key[matchlen+1:], n.Val)
		if err != nil {
			return false, nil, err
		}
		_, branch.Children[key[matchlen]], err = t.insert(nil, append(prefix, key[:matchlen+1]...), key[matchlen+1:], value)
		if err != nil {
			return false, nil, err
		}
		// 如果此shortNode出现在索引0处，请将其替换为分支。
		if matchlen == 0 {
			return true, branch, nil
		}
		// 新分支节点作为原始短节点的子节点创建。跟踪跟踪程序中新插入的节点。传递的节点标识符是来自根节点的路径。
		t.tracer.onInsert(append(prefix, key[:matchlen]...))

		// 将其替换为通向分支的短节点。
		return true, &shortNode{key[:matchlen], branch, t.newFlag()}, nil

	case *fullNode:
		dirty, nn, err := t.insert(n.Children[key[0]], append(prefix, key[0]), key[1:], value)
		if !dirty || err != nil {
			return false, n, err
		}
		n = n.copy()
		n.flags = t.newFlag()
		n.Children[key[0]] = nn
		return true, n, nil

	case nil:
		// 创建新的短节点并在跟踪器中跟踪它。传递的节点标识符是来自根节点的路径。请注意，不会跟踪valueNode，因为它始终嵌入在其父节点中。
		t.tracer.onInsert(prefix)

		return true, &shortNode{key, value, t.newFlag()}, nil

	case hashNode:
		// 我们找到了trie中尚未加载的部分。加载节点并插入其中。这会将所有子节点保留在指向trie中的值的路径上。
		rn, err := t.resolveHash(n, prefix)
		if err != nil {
			return false, nil, err
		}
		dirty, nn, err := t.insert(rn, prefix, key, value)
		if !dirty || err != nil {
			return false, rn, err
		}
		return true, nn, nil

	default:
		panic(fmt.Sprintf("%T: invalid node: %v", n, n))
	}
}

// delete返回键已删除的trie的新根。
//它通过在递归删除后简化向上的节点，将trie简化为最小形式。
func (t *Trie) delete(n node, prefix, key []byte) (bool, node, error) {
	switch n := n.(type) {
	case *shortNode:
		matchlen := prefixLen(key, n.Key)
		if matchlen < len(n.Key) {
			return false, n, nil // 不匹配时不替换n
		}
		if matchlen == len(key) {
			// 将完全删除匹配的短节点，并在删除集中跟踪它。同样，valueNode根本不需要被跟踪，因为它总是嵌入的。
			t.tracer.onDelete(prefix)

			return true, nil, nil // 整个匹配完全删除n
		}
		// 键比n键长。从子项中删除剩余的后缀。这里的子项永远不能为零，因为子项必须包含至少两个键长于n.Key的其他值。
		dirty, child, err := t.delete(n.Val, append(prefix, key[:len(n.Key)]...), key[len(n.Key):])
		if !dirty || err != nil {
			return false, n, err
		}
		switch child := child.(type) {
		case *shortNode:
			// 子shortNode将合并到其父节点中，轨迹也将被删除。
			t.tracer.onDelete(append(prefix, n.Key...))

			// 从子节点删除会将其减少到另一个短节点。
			//合并节点以避免创建shortNode{…，shortNode{…}}。
			//使用concat（它总是创建一个新切片）而不是append，以避免修改n.Key，因为它可能与其他节点共享。
			return true, &shortNode{concat(n.Key, child.Key...), child.Val, t.newFlag()}, nil
		default:
			return true, &shortNode{n.Key, child, t.newFlag()}, nil
		}

	case *fullNode:
		dirty, nn, err := t.delete(n.Children[key[0]], append(prefix, key[0]), key[1:])
		if !dirty || err != nil {
			return false, n, err
		}
		n = n.copy()
		n.flags = t.newFlag()
		n.Children[key[0]] = nn

		// 因为n是一个完整节点，所以在执行删除操作之前，它必须至少包含两个子节点。
		//如果新的子节点值为非nil，则n在删除后仍至少有两个子节点，并且不能减少为短节点。
		if nn != nil {
			return true, n, nil
		}
		// 减少：
		//检查删除后剩下多少个非nil条目，如果只剩下一个条目，则将完整节点减少为短节点。因为n在删除之前必须至少包含两个子节点（否则它将不是完整节点），所以n永远不能减少为零。
		//循环完成后，pos包含单个值的索引，如果n至少包含两个值，则该索引保留在n或-2中。
		pos := -1
		for i, cld := range &n.Children {
			if cld != nil {
				if pos == -1 {
					pos = i
				} else {
					pos = -2
					break
				}
			}
		}
		if pos >= 0 {
			if pos != 16 {
				// 如果剩下的条目是一个短节点，它将替换n，并且其键将丢失的半字节钉在前面。
				//这样可以避免创建无效的shortNode{…，shortNode{…}}。
				//由于该条目可能尚未加载，请仅为此检查解析它。
				cnode, err := t.resolve(n.Children[pos], prefix)
				if err != nil {
					return false, nil, err
				}
				if cnode, ok := cnode.(*shortNode); ok {
					// 用短节点替换整个完整节点。将原始短节点标记为已删除，因为该值现在嵌入到父节点中。
					t.tracer.onDelete(append(prefix, byte(pos)))

					k := append([]byte{byte(pos)}, cnode.Key...)
					return true, &shortNode{k, cnode.Val, t.newFlag()}, nil
				}
			}
			// 否则，n将替换为包含子节点的一个半字节短节点。
			return true, &shortNode{[]byte{byte(pos)}, n.Children[pos], t.newFlag()}, nil
		}
		// n仍然包含至少两个值，不能减少。
		return true, n, nil

	case valueNode:
		return true, nil, nil

	case nil:
		return false, nil, nil

	case hashNode:
		// 我们找到了trie中尚未加载的部分。加载节点并从中删除。这会将所有子节点保留在指向trie中的值的路径上。
		rn, err := t.resolveHash(n, prefix)
		if err != nil {
			return false, nil, err
		}
		dirty, nn, err := t.delete(rn, prefix, key)
		if !dirty || err != nil {
			return false, rn, err
		}
		return true, nn, nil

	default:
		panic(fmt.Sprintf("%T: invalid node: %v (%v)", n, n, key))
	}
}

func concat(s1 []byte, s2 ...byte) []byte {
	r := make([]byte, len(s1)+len(s2))
	copy(r, s1)
	copy(r[len(s1):], s2)
	return r
}

func (t *Trie) resolve(n node, prefix []byte) (node, error) {
	if n, ok := n.(hashNode); ok {
		return t.resolveHash(n, prefix)
	}
	return n, nil
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

// 哈希返回trie的根哈希。它不会写入数据库，即使trie没有数据库，也可以使用它。
func (t *Trie) Hash() entity.Hash {
	hash, cached, _ := t.hashRoot()
	t.root = cached
	return entity.BytesToHash(hash.(hashNode))
}

//hashRoot计算给定trie的根哈希
func (t *Trie) hashRoot() (node, node, error) {
	if t.root == nil {
		return hashNode(emptyRoot.Bytes()), nil, nil
	}
	// 如果更改数低于100，我们让一个线程来处理它
	h := newHasher(t.unhashed >= 100)
	defer returnHasherToPool(h)
	hashed, cached := h.hash(t.root, true)
	t.unhashed = 0
	return hashed, cached, nil
}

// Commit将所有节点写入trie的内存数据库，跟踪内部和外部（用于帐户尝试）引用。
func (t *Trie) Commit(onleaf LeafCallback) (entity.Hash, int, error) {
	if t.db == nil {
		panic("commit called on trie with nil database")
	}
	defer t.tracer.reset()

	if t.root == nil {
		return emptyRoot, 0, nil
	}
	// 首先派生所有脏节点的哈希。在下面的过程中，我们假设所有节点都是散列的。
	rootHash := t.Hash()
	h := newCommitter()
	defer returnCommitterToPool(h)

	// 在启动goroutines之前，如果我们真的需要提交，请快速检查一下。这可能会发生，例如，如果我们加载一个用于读取存储值的trie，但不向其写入。
	if hashedNode, dirty := t.root.cache(); !dirty {
		// 用源哈希替换根节点，以确保在提交后删除所有已解析的节点。
		t.root = hashedNode
		return rootHash, 0, nil
	}
	var wg sync.WaitGroup
	if onleaf != nil {
		h.onleaf = onleaf
		h.leafCh = make(chan *leaf, leafChanSize)
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.commitLoop(t.db)
		}()
	}
	newRoot, committed, err := h.Commit(t.root, t.db)
	if onleaf != nil {
		// 如果提供了onleaf回调，则在newCommitter中创建leafch。commitLoop仅从中读取，而commit操作是唯一的编写器。因此，在此关闭此通道是安全的。
		close(h.leafCh)
		wg.Wait()
	}
	if err != nil {
		return entity.Hash{}, 0, err
	}
	t.root = newRoot
	return rootHash, committed, nil
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
	if root != (entity.Hash{}) && root != emptyRoot {
		rootnode, err := trie.resolveHash(root[:], nil)
		if err != nil {
			return nil, err
		}
		trie.root = rootnode
	}
	return trie, nil
}

// SecureTrie对于并发使用不安全。
type SecureTrie struct {
	trie             Trie
	hashKeyBuf       [entity.HashLength]byte
	secKeyCache      map[string][]byte
	secKeyCacheOwner *SecureTrie // 指向self的指针，在不匹配时替换密钥缓存
}

func (s *SecureTrie) GetKey(bytes []byte) ([]byte, error) {
	return s.trie.TryGet(s.hashKey(bytes))
}

// Commit 将所有节点和安全哈希预映像写入trie的数据库。节点以其sha3哈希作为密钥进行存储。提交会刷新内存中的节点。后续的Get调用将从数据库加载节点。
func (t *SecureTrie) Commit(onleaf LeafCallback) (entity.Hash, int, error) {
	// 将所有预映像写入实际磁盘数据库
	if len(t.getSecKeyCache()) > 0 {
		if t.trie.db.preimages != nil { // 丑陋的直接检查，但避免以下写入锁定
			t.trie.db.lock.Lock()
			for hk, key := range t.secKeyCache {
				t.trie.db.insertPreimage(entity.BytesToHash([]byte(hk)), key)
			}
			t.trie.db.lock.Unlock()
		}
		t.secKeyCache = make(map[string][]byte)
	}
	// 将trie提交到其中间节点数据库
	return t.trie.Commit(onleaf)
}

func (s *SecureTrie) TryGet(key []byte) ([]byte, error) {
	return s.trie.TryGet(s.hashKey(key))
}

func (s *SecureTrie) TryUpdateAccount(key []byte, account *entity.StateAccount) error {
	hk := s.hashKey(key)
	data, err := rlp.EncodeToBytes(account)
	if err != nil {
		return err
	}
	if err := s.trie.TryUpdate(hk, data); err != nil {
		return err
	}
	s.getSecKeyCache()[string(hk)] = utils.CopyBytes(key)
	return nil
}

func (s *SecureTrie) TryUpdate(key, value []byte) error {
	panic("implement me")
}

func (s *SecureTrie) TryDelete(key []byte) error {
	panic("implement me")
}

func (s *SecureTrie) Hash() entity.Hash {
	return s.trie.Hash()
}

// Copy返回SecureTrie的副本。
func (t *SecureTrie) Copy() *SecureTrie {
	return &SecureTrie{
		trie:        *t.trie.Copy(),
		secKeyCache: t.secKeyCache,
	}
}

// hashKey将密钥的哈希作为临时缓冲区返回。调用方不能保留返回值，因为它在下次调用hashKey或secKey时将变得无效。
func (t *SecureTrie) hashKey(key []byte) []byte {
	h := newHasher(false)
	h.sha.Reset()
	h.sha.Write(key)
	h.sha.Read(t.hashKeyBuf[:])
	returnHasherToPool(h)
	return t.hashKeyBuf[:]
}

func returnHasherToPool(h *hasher) {
	hasherPool.Put(h)
}

// 如果本地数据库中不存在trie节点，则trie函数（TryGet、TryUpdate、TryDelete）将返回MissingNodeError。它包含检索丢失节点所需的信息。
type MissingNodeError struct {
	Owner    entity.Hash // trie的所有者（如果是2层trie）
	NodeHash entity.Hash // 缺少节点的哈希
	Path     []byte      // 缺少节点的十六进制编码路径
	err      error       // 缺少trie节点的具体错误
}

// Unwrap返回缺少trie节点的具体错误，以便我们在外部进行进一步分析。
func (err *MissingNodeError) Unwrap() error {
	return err.err
}

func (err *MissingNodeError) Error() string {
	if err.Owner == (entity.Hash{}) {
		return fmt.Sprintf("missing trie node %x (path %x) %v", err.NodeHash, err.Path, err.err)
	}
	return fmt.Sprintf("missing trie node %x (owner %x) (path %x) %v", err.NodeHash, err.Owner, err.Path, err.err)
}

//getSecKeyCache返回当前的安全密钥缓存，如果所有权发生更改，则创建一个新的安全密钥缓存（即当前的安全trie是拥有实际缓存的另一个安全密钥缓存的副本）。
func (t *SecureTrie) getSecKeyCache() map[string][]byte {
	if t != t.secKeyCacheOwner {
		t.secKeyCacheOwner = t
		t.secKeyCache = make(map[string][]byte)
	}
	return t.secKeyCache
}
