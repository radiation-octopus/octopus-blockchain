package rawdb

import (
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
)

// WriteTrieNode写入提供的trie节点数据库。
func WriteTrieNode(db typedb.KeyValueWriter, hash entity.Hash, node []byte) {
	//fmt.Println(utils.ToStringKey(node))
	if err := db.Put(hash.Bytes(), node); err != nil {
		log.Info("Failed to store trie node", "err", err)
	}
}

// ReadTrieNode检索所提供哈希的trie节点。
func ReadTrieNode(db typedb.KeyValueReader, hash entity.Hash) []byte {
	data, _ := db.Get(hash.Bytes())
	return data
}

// hastreenode检查数据库中是否存在具有所提供哈希的trie节点。
func HasTrieNode(db typedb.KeyValueReader, hash entity.Hash) bool {
	ok, _ := db.IsHas(hash.Bytes())
	return ok
}

// HasCodeWithPrefix检查数据库中是否存在与提供的代码哈希对应的合同代码。
//此函数将仅使用前缀方案检查是否存在。
func HasCodeWithPrefix(db typedb.KeyValueReader, hash entity.Hash) bool {
	ok, _ := db.IsHas(codeKey(hash))
	return ok
}
