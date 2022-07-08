package tire

import (
	"fmt"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus/log"
	"github.com/radiation-octopus/octopus/utils"
	"sync"
)

// 列出StackTrie#nodeType可以保存的所有值
const (
	emptyNode = iota
	branchNode
	extNode
	leafNode
	hashedNode
)

var stPool = sync.Pool{
	New: func() interface{} {
		return NewStackTrie(nil)
	},
}

func stackTrieFromPool(db typedb.KeyValueWriter, owner entity.Hash) *StackTrie {
	st := stPool.Get().(*StackTrie)
	st.db = db
	st.owner = owner
	return st
}

func returnToPool(st *StackTrie) {
	st.Reset()
	stPool.Put(st)
}

// StackTrie是一种trie实现，它期望按顺序插入密钥。
//一旦确定不再插入子树，它将对其进行哈希运算并释放所使用的内存。
type StackTrie struct {
	owner    entity.Hash          // trie的所有者
	nodeType uint8                //节点类型（如分支、外部、叶）
	val      []byte               // 如果是叶子，则此节点包含的值
	key      []byte               // 此（叶| ext）节点覆盖的关键块
	children [16]*StackTrie       // 子项列表（用于分支和外部）
	db       ethdb.KeyValueWriter //指向提交数据库的指针，可以为零
}

func (st *StackTrie) Reset() {
	st.owner = entity.Hash{}
	st.db = nil
	st.key = st.key[:0]
	st.val = nil
	for i := range st.children {
		st.children[i] = nil
	}
	st.nodeType = emptyNode
}

// NewStackTrieWithOwner分配并初始化一个空的trie，但带有额外的owner字段。
func NewStackTrieWithOwner(db typedb.KeyValueWriter, owner entity.Hash) *StackTrie {
	return &StackTrie{
		owner:    owner,
		nodeType: emptyNode,
		db:       db,
	}
}

func newLeaf(owner entity.Hash, key, val []byte, db typedb.KeyValueWriter) *StackTrie {
	st := stackTrieFromPool(db, owner)
	st.nodeType = leafNode
	st.key = append(st.key, key...)
	st.val = val
	return st
}

func newExt(owner entity.Hash, key []byte, child *StackTrie, db typedb.KeyValueWriter) *StackTrie {
	st := stackTrieFromPool(db, owner)
	st.nodeType = extNode
	st.key = append(st.key, key...)
	st.children[0] = child
	return st
}

// Helper函数，在给定完整键的情况下，该函数确定st.keyOffset指向的块与完整键中的同一块不同的索引。
func (st *StackTrie) getDiffIndex(key []byte) int {
	for idx, nibble := range st.key {
		if nibble != key[idx] {
			return idx
		}
	}
	return len(st.key)
}

//如果可能，哈希将st转换为“hashedNode”。可能的结果：
//1. rlp编码值>=32字节：
//-然后可以在“st.val”中访问32字节的“hash”。
//-并且“st.type”将是“hashedNode”
// 2. rlp编码值<32字节
//-然后可以在“st.val”中访问<32字节rlp编码值。
//-并且“st.type”将再次为“hashedNode”
//此方法还将“st.type”设置为hashedNode，并清除“st.key”。
func (st *StackTrie) hash() {
	h := newHasher(false)
	defer returnHasherToPool(h)

	st.hashRec(h)
}

// 将（键，值）对插入到trie中的Helper函数。
func (st *StackTrie) insert(key, value []byte) {
	switch st.nodeType {
	case branchNode: /* 分支 */
		idx := int(key[0])

		// 无法解决的兄妹
		for i := idx - 1; i >= 0; i-- {
			if st.children[i] != nil {
				if st.children[i].nodeType != hashedNode {
					st.children[i].hash()
				}
				break
			}
		}

		// 添加新子项
		if st.children[idx] == nil {
			st.children[idx] = newLeaf(st.owner, key[1:], value, st.db)
		} else {
			st.children[idx].insert(key[1:], value)
		}

	case extNode: /* 提取 */
		// 比较两个关键块，看看它们的不同之处
		diffidx := st.getDiffIndex(key)

		// 检查区块是否相同。如果是，则递归到子节点。
		//否则，密钥必须拆分为1）可选公共前缀，2）表示两条不同路径的fullnode，以及3）每个差异子树的叶子。
		if diffidx == len(st.key) {
			// Ext key和key segment相同，递归到子节点。
			st.children[0].insert(key[diffidx:], value)
			return
		}
		// 保存原始零件。根据中断是否在扩展的最后一个字节，创建一个中间扩展或直接使用扩展的子节点。
		var n *StackTrie
		if diffidx < len(st.key)-1 {
			n = newExt(st.owner, st.key[diffidx+1:], st.children[0], st.db)
		} else {
			// 断开最后一个字节，无需插入扩展节点：重用当前节点
			n = st.children[0]
		}
		// 转换为哈希
		n.hash()
		var p *StackTrie
		if diffidx == 0 {
			// 中断位于第一个字节，因此当前节点转换为分支节点。
			st.children[0] = nil
			p = st
			st.nodeType = branchNode
		} else {
			// 公共前缀至少有一个字节长，插入一个新的中间分支节点。
			st.children[0] = stackTrieFromPool(st.db, st.owner)
			st.children[0].nodeType = branchNode
			p = st.children[0]
		}
		// 为插入的零件创建叶
		o := newLeaf(st.owner, key[diffidx+1:], value, st.db)

		// 将两个子叶子插入它们所属的位置：
		origIdx := st.key[diffidx]
		newIdx := key[diffidx]
		p.children[origIdx] = n
		p.children[newIdx] = o
		st.key = st.key[:diffidx]

	case leafNode: /* 叶子 */
		// 比较两个关键块，看看它们的不同之处
		diffidx := st.getDiffIndex(key)

		// 不支持覆盖密钥，这意味着当前叶将被分割为
		//1）这两个密钥的公共前缀的可选扩展，
		//2）选择密钥不同路径的完整节点，以及3）每个密钥的差异组件的一个叶。
		if diffidx >= len(st.key) {
			panic("Trying to insert into existing key")
		}

		// 检查分割是否发生在块的第一个半字节。在这种情况下，不需要前缀extnode。否则，创建
		var p *StackTrie
		if diffidx == 0 {
			// 将当前叶转换为分支
			st.nodeType = branchNode
			p = st
			st.children[0] = nil
		} else {
			// 将当前节点转换为外部节点，并插入子分支节点。
			st.nodeType = extNode
			st.children[0] = NewStackTrieWithOwner(st.db, st.owner)
			st.children[0].nodeType = branchNode
			p = st.children[0]
		}

		// 创建两个子叶子：一个包含原始值，另一个包含新值。为了释放一些内存，直接对子叶进行散列。
		origIdx := st.key[diffidx]
		p.children[origIdx] = newLeaf(st.owner, st.key[diffidx+1:], st.val, st.db)
		p.children[origIdx].hash()

		newIdx := key[diffidx]
		p.children[newIdx] = newLeaf(st.owner, key[diffidx+1:], value, st.db)

		// 最后，剪掉传给孩子们的关键部分。
		st.key = st.key[:diffidx]
		st.val = nil

	case emptyNode: /* 空的 */
		st.nodeType = leafNode
		st.key = key
		st.val = value

	case hashedNode:
		panic("trying to insert into hash")

	default:
		panic("invalid type")
	}
}

//TryUpdate将（键，值）对插入堆栈trie
func (st *StackTrie) TryUpdate(key, value []byte) error {
	k := keybytesToHex(key)
	if len(value) == 0 {
		panic("deletion not supported")
	}
	st.insert(k[:len(k)-1], value)
	return nil
}
func (st *StackTrie) Update(key []byte, value []byte) {
	if err := st.TryUpdate(key, value); err != nil {
		log.Error(fmt.Sprintf("Unhandled trie error: %v", err))
	}
}

func (st *StackTrie) hashRec(hasher *hasher) {
	// 下面的开关将其设置为此节点的RLP编码。
	var encodedNode []byte

	switch st.nodeType {
	case hashedNode:
		return

	case emptyNode:
		st.val = emptyRoot.Bytes()
		st.key = st.key[:0]
		st.nodeType = hashedNode
		return

	case branchNode:
		var nodes rawFullNode
		for i, child := range st.children {
			if child == nil {
				nodes[i] = nilValueNode
				continue
			}

			child.hashRec(hasher)
			if len(child.val) < 32 {
				nodes[i] = rawNode(child.val)
			} else {
				nodes[i] = hashNode(child.val)
			}

			// 将子项释放回池。
			st.children[i] = nil
			returnToPool(child)
		}

		nodes.encode(hasher.encbuf)
		encodedNode = hasher.encodedBytes()

	case extNode:
		st.children[0].hashRec(hasher)

		sz := hexToCompactInPlace(st.key)
		n := rawShortNode{Key: st.key[:sz]}
		if len(st.children[0].val) < 32 {
			n.Val = rawNode(st.children[0].val)
		} else {
			n.Val = hashNode(st.children[0].val)
		}

		n.encode(hasher.encbuf)
		encodedNode = hasher.encodedBytes()

		// 将子项释放回池。
		returnToPool(st.children[0])
		st.children[0] = nil

	case leafNode:
		st.key = append(st.key, byte(16))
		sz := hexToCompactInPlace(st.key)
		n := rawShortNode{Key: st.key[:sz], Val: valueNode(st.val)}

		n.encode(hasher.encbuf)
		encodedNode = hasher.encodedBytes()

	default:
		panic("invalid node type")
	}

	st.nodeType = hashedNode
	st.key = st.key[:0]
	if len(encodedNode) < 32 {
		st.val = utils.CopyBytes(encodedNode)
		return
	}

	// 将哈希写入“val”。我们在这里分配一个新的val，以不改变输入值
	st.val = hasher.hashData(encodedNode)
	if st.db != nil {
		// 所有db实现是否都复制了提供的值？///
		st.db.Put(st.val, encodedNode)
	}
}

func (st *StackTrie) Hash() (h entity.Hash) {
	hasher := newHasher(false)
	defer returnHasherToPool(hasher)

	st.hashRec(hasher)
	if len(st.val) == 32 {
		copy(h[:], st.val)
		return h
	}

	//如果节点的RLP不是32字节长，则不会对节点进行哈希运算，而是包含节点的RLP编码。
	//对于顶级节点，我们需要强制哈希。
	hasher.sha.Reset()
	hasher.sha.Write(st.val)
	hasher.sha.Read(h[:])
	return h
}

// NewStackTrie分配并初始化空trie。
func NewStackTrie(db typedb.KeyValueWriter) *StackTrie {
	return &StackTrie{
		nodeType: emptyNode,
		db:       db,
	}
}
