package rawdb

import (
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus/log"
)

// WriteTrieNode写入提供的trie节点数据库。
func WriteTrieNode(db typedb.KeyValueWriter, hash entity.Hash, node []byte) {
	if err := db.Put(hash.Bytes(), node); err != nil {
		log.Info("Failed to store trie node", "err", err)
	}
}
