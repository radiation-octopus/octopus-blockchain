package rawdb

import (
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus/log"
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
