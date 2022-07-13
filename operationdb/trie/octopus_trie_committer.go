package trie

import (
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"sync"
)

//leafChanSize是叶子的大小。这是一个非常任意的数字，允许一些并行性，但不会产生太多内存开销。
const leafChanSize = 200

//叶表示trie叶值
type leaf struct {
	size int         // rlp数据的大小（估计）
	hash entity.Hash // rlp数据哈希
	node node        // 要提交的节点
}

//committer是用于trie提交操作的类型。提交者有一些内部预分配的临时空间，还有一个在提交叶子时调用的回调。
//leaf通过“leafCh”传递，以允许某种程度的并行性。
//通过“某种程度的”并行性，仍然会按顺序处理所有叶-onleaf永远不会被并行调用或无序调用。
type committer struct {
	onleaf LeafCallback
	leafCh chan *leaf
}

// 提交者生活在全球同步中。pool
var committerPool = sync.Pool{
	New: func() interface{} {
		return &committer{}
	},
}

// 新提交者创建一个新提交者或从池中选择一个提交者。
func newCommitter() *committer {
	return committerPool.Get().(*committer)
}

func returnCommitterToPool(h *committer) {
	h.onleaf = nil
	h.leafCh = nil
	committerPool.Put(h)
}

//commitLoop为节点执行实际的插入+叶回调。
func (c *committer) commitLoop(db *TrieDatabase) {
	for item := range c.leafCh {
		var (
			hash = item.hash
			size = item.size
			n    = item.node
		)
		// 我们正在将trie节点汇集到一个中间内存缓存中
		db.insert(hash, size, n)

		if c.onleaf != nil {
			switch n := n.(type) {
			case *shortNode:
				if child, ok := n.Val.(valueNode); ok {
					c.onleaf(nil, nil, child, hash)
				}
			case *fullNode:
				//对于[0，15]范围内的子级，不可能包含valueNode。只检查第17个孩子。
				if n.Children[16] != nil {
					c.onleaf(nil, nil, n.Children[16].(valueNode), hash)
				}
			}
		}
	}
}

// 提交将节点向下折叠为哈希节点并将其插入数据库
func (c *committer) Commit(n node, db *TrieDatabase) (hashNode, int, error) {
	if db == nil {
		return nil, 0, errors.New("no db provided")
	}
	h, committed, err := c.commit(n, db)
	if err != nil {
		return nil, 0, err
	}
	return h.(hashNode), committed, nil
}

// 提交将节点向下折叠为哈希节点并将其插入数据库
func (c *committer) commit(n node, db *TrieDatabase) (node, int, error) {
	// 如果此路径是干净的，请使用可用的缓存数据
	hash, dirty := n.cache()
	if hash != nil && !dirty {
		return hash, 0, nil
	}
	// 提交子级，然后提交父级，并删除脏标志。
	switch cn := n.(type) {
	case *shortNode:
		// 提交子项
		collapsed := cn.copy()

		// 如果子节点是fullNode，则递归提交，否则它只能是hashNode或valueNode。
		var childCommitted int
		if _, ok := cn.Val.(*fullNode); ok {
			childV, committed, err := c.commit(cn.Val, db)
			if err != nil {
				return nil, 0, err
			}
			collapsed.Val, childCommitted = childV, committed
		}
		// 密钥需要复制，因为我们正在将其传递到数据库
		collapsed.Key = hexToCompact(cn.Key)
		hashedNode := c.store(collapsed, db)
		if hn, ok := hashedNode.(hashNode); ok {
			return hn, childCommitted + 1, nil
		}
		return collapsed, childCommitted, nil
	case *fullNode:
		hashedKids, childCommitted, err := c.commitChildren(cn, db)
		if err != nil {
			return nil, 0, err
		}
		collapsed := cn.copy()
		collapsed.Children = hashedKids

		hashedNode := c.store(collapsed, db)
		if hn, ok := hashedNode.(hashNode); ok {
			return hn, childCommitted + 1, nil
		}
		return collapsed, childCommitted, nil
	case hashNode:
		return cn, 0, nil
	default:
		// 无，不应提交valuenode
		panic(fmt.Sprintf("%T: invalid node: %v", n, n))
	}
}

//commitChildren提交给定fullnode的子级
func (c *committer) commitChildren(n *fullNode, db *TrieDatabase) ([17]node, int, error) {
	var (
		committed int
		children  [17]node
	)
	for i := 0; i < 16; i++ {
		child := n.Children[i]
		if child == nil {
			continue
		}
		// 如果是哈希子级，则直接保存哈希值。注意：范围[0，15]中的子级不可能是valueNode。
		if hn, ok := child.(hashNode); ok {
			children[i] = hn
			continue
		}
		// 递归地提交子级并存储“哈希”值。请注意，返回的节点可以是一些嵌入的节点，因此类型可能不是hashNode。
		hashed, childCommitted, err := c.commit(child, db)
		if err != nil {
			return children, 0, err
		}
		children[i] = hashed
		committed += childCommitted
	}
	// 对于第17个子级，类型可能是valuenode。
	if n.Children[16] != nil {
		children[16] = n.Children[16]
	}
	return children, committed, nil
}

//store散列节点n，如果指定了存储层，它会将键/值对写入其中，并跟踪任何节点->子引用以及任何节点->外部trie引用。
func (c *committer) store(n node, db *TrieDatabase) node {
	// 较大的节点将被其哈希替换并存储在数据库中。
	var (
		hash, _ = n.cache()
		size    int
	)
	if hash == nil {
		// 这不是生成的-必须是存储在父节点中的小节点。
		//理论上，如果不是nil（嵌入节点通常包含值），我们应该在这里应用leafCall。
		//但小值（小于32字节）不是我们的目标。
		return n
	} else {
		// 我们已经有了哈希，估计节点的RLP编码大小。
		//大小用于mem跟踪，不需要精确
		size = estimateSize(n)
	}
	// 如果我们使用基于通道的叶报告，请发送到通道。只有在存在活动叶回调时，叶通道才会处于活动状态
	if c.leafCh != nil {
		c.leafCh <- &leaf{
			size: size,
			hash: entity.BytesToHash(hash),
			node: n,
		}
	} else if db != nil {
		// 没有使用叶回调，但仍有一个数据库。执行串行插入
		db.insert(entity.BytesToHash(hash), size, n)
	}
	return hash
}

// estimateSize估计rlp编码节点的大小，而不实际对其进行rlp编码（零分配）。
//这种方法已经在实验中进行了尝试，对于具有1000个叶子的trie，只有在小的短节点上，误差才超过1%，而这种方法高估了2或3个字节（例如，37而不是35）
func estimateSize(n node) int {
	switch n := n.(type) {
	case *shortNode:
		// 短节点包含压缩键和值。
		return 3 + len(n.Key) + estimateSize(n.Val)
	case *fullNode:
		//一个完整节点最多包含16个哈希（一些为零）和一个键
		s := 3
		for i := 0; i < 16; i++ {
			if child := n.Children[i]; child != nil {
				s += estimateSize(child)
			} else {
				s++
			}
		}
		return s
	case valueNode:
		return 1 + len(n)
	case hashNode:
		return 1 + len(n)
	default:
		panic(fmt.Sprintf("node type %T", n))
	}
}
