package operationdb

type node interface {
	cache() (hashNode, bool)
	//encode(w rlp.EncoderBuffer)
	fstring(string) string
}

type (
	fullNode struct {
		Children [17]node // Actual trie node data to encode/decode (needs custom encoder)
		flags    nodeFlag
	}
	shortNode struct {
		Key   []byte
		Val   node
		flags nodeFlag
	}
	hashNode  []byte
	valueNode []byte
)

// nodeFlag包含有关节点的缓存相关元数据。
type nodeFlag struct {
	hash  hashNode // 节点的缓存哈希（可能为零）
	dirty bool     // 节点是否具有必须写入数据库的更改
}
