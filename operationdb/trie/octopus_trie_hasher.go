package trie

import (
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"golang.org/x/crypto/sha3"
	"sync"
)

// 哈希器是用于trie哈希操作的类型。哈希程序有一些内部预分配的临时空间
type hasher struct {
	sha      crypto.KeccakState
	tmp      []byte
	encbuf   rlp.EncoderBuffer
	parallel bool // 哈希时是否使用并行线程
}

// hasherPool包含pureHasher
var hasherPool = sync.Pool{
	New: func() interface{} {
		return &hasher{
			tmp:    make([]byte, 0, 550), // cap与完整的fullNode一样大。
			sha:    sha3.NewLegacyKeccak256().(crypto.KeccakState),
			encbuf: rlp.NewEncoderBuffer(nil),
		}
	},
}

func newHasher(parallel bool) *hasher {
	h := hasherPool.Get().(*hasher)
	h.parallel = parallel
	return h
}

// hashShortNodeChildren折叠短节点。返回的折叠节点包含对键的实时引用，不能修改。缓存的
func (h *hasher) hashShortNodeChildren(n *shortNode) (collapsed, cached *shortNode) {
	// 散列短节点的子节点，缓存新散列的子树
	collapsed, cached = n.copy(), n.copy()
	// 之前，我们确实复制了这个。我们似乎实际上不需要这样做，因为我们不覆盖/重用缓存的密钥。键=公共。CopyBytes（n.Key）
	collapsed.Key = hexToCompact(n.Key)
	// 除非子节点是valuenode或hashnode，否则对其进行哈希运算
	switch n.Val.(type) {
	case *fullNode, *shortNode:
		collapsed.Val, cached.Val = h.hash(n.Val, false)
	}
	return collapsed, cached
}

//encodedBytes 返回h.encbuf上最后一次编码操作的结果。这也会重置编码器缓冲区。所有节点编码都必须如下所示：
//node。编码（h.encbuf）
//enc：=h.encodedBytes（）
//此约定存在，因为节点。仅当在具体接收器类型上调用时，才能内联/转义分析encode。
func (h *hasher) encodedBytes() []byte {
	h.tmp = h.encbuf.AppendToBytes(h.tmp[:0])
	h.encbuf.Reset(nil)
	return h.tmp
}

// hashData对提供的数据进行哈希运算
func (h *hasher) hashData(data []byte) hashNode {
	n := make(hashNode, 32)
	h.sha.Reset()
	h.sha.Write(data)
	h.sha.Read(n)
	return n
}

// proofHash用于构造trie证明，并返回“折叠”节点（用于以后的RLP编码）以及散列节点——除非节点小于32字节，
//在这种情况下，它将按原样返回。该方法在值节点或哈希节点上不做任何事情。
func (h *hasher) proofHash(original node) (collapsed, hashed node) {
	switch n := original.(type) {
	case *shortNode:
		sn, _ := h.hashShortNodeChildren(n)
		return sn, h.shortnodeToHash(sn, false)
	case *fullNode:
		fn, _ := h.hashFullNodeChildren(n)
		return fn, h.fullnodeToHash(fn, false)
	default:
		// 值和散列节点没有子节点，因此它们保持原样
		return n, n
	}
}

// shortnodeToHash从shortNode创建hashNode。
//所提供的shortnode应具有十六进制类型的密钥，该密钥将被转换（无需修改）为紧凑形式以进行RLP编码。
//如果rlp数据小于32字节，则返回“nil”。
func (h *hasher) shortnodeToHash(n *shortNode, force bool) node {
	n.encode(h.encbuf)
	enc := h.encodedBytes()

	if len(enc) < 32 && !force {
		return n // 小于32字节的节点存储在其父节点中
	}
	return h.hashData(enc)
}

func (h *hasher) hashFullNodeChildren(n *fullNode) (collapsed *fullNode, cached *fullNode) {
	// 散列完整节点的子节点，缓存新散列的子树
	cached = n.copy()
	collapsed = n.copy()
	if h.parallel {
		var wg sync.WaitGroup
		wg.Add(16)
		for i := 0; i < 16; i++ {
			go func(i int) {
				hasher := newHasher(false)
				if child := n.Children[i]; child != nil {
					collapsed.Children[i], cached.Children[i] = hasher.hash(child, false)
				} else {
					collapsed.Children[i] = nilValueNode
				}
				returnHasherToPool(hasher)
				wg.Done()
			}(i)
		}
		wg.Wait()
	} else {
		for i := 0; i < 16; i++ {
			if child := n.Children[i]; child != nil {
				collapsed.Children[i], cached.Children[i] = h.hash(child, false)
			} else {
				collapsed.Children[i] = nilValueNode
			}
		}
	}
	return collapsed, cached
}

// shortnodeToHash用于从一组hashNodes（可能包含nil值）创建hashNode
func (h *hasher) fullnodeToHash(n *fullNode, force bool) node {
	n.encode(h.encbuf)
	enc := h.encodedBytes()

	if len(enc) < 32 && !force {
		return n // 小于32字节的节点存储在其父节点中
	}
	return h.hashData(enc)
}

// 散列将节点向下折叠为散列节点，同时返回用计算的散列初始化的原始节点的副本以替换原始节点。
func (h *hasher) hash(n node, force bool) (hashed node, cached node) {
	// 返回缓存的哈希（如果可用）
	if hash, _ := n.cache(); hash != nil {
		return hash, n
	}
	// Trie尚未处理，带孩子们走
	switch n := n.(type) {
	case *shortNode:
		collapsed, cached := h.hashShortNodeChildren(n)
		hashed := h.shortnodeToHash(collapsed, force)
		// 我们需要保留可能未哈希的节点，以防它太小而无法哈希
		if hn, ok := hashed.(hashNode); ok {
			cached.flags.hash = hn
		} else {
			cached.flags.hash = nil
		}
		return hashed, cached
	case *fullNode:
		collapsed, cached := h.hashFullNodeChildren(n)
		hashed = h.fullnodeToHash(collapsed, force)
		if hn, ok := hashed.(hashNode); ok {
			cached.flags.hash = hn
		} else {
			cached.flags.hash = nil
		}
		return hashed, cached
	default:
		// 值和哈希节点没有子节点，因此它们保持原样
		return n, n
	}
}
