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

// ReadCode检索所提供代码哈希的合同代码。
func ReadCode(db typedb.KeyValueReader, hash entity.Hash) []byte {
	// 首先尝试使用带前缀的代码方案，如果没有，则尝试使用旧方案。
	data := ReadCodeWithPrefix(db, hash)
	if len(data) != 0 {
		return data
	}
	data, _ = db.Get(hash.Bytes())
	return data
}

// ReadCodeWithPrefix检索所提供代码哈希的合同代码。
//此函数与ReadCode之间的主要区别在于，此函数仅检查最新方案（带前缀）的存在性。
func ReadCodeWithPrefix(db typedb.KeyValueReader, hash entity.Hash) []byte {
	data, _ := db.Get(codeKey(hash))
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

// WritePreimages writes the provided set of preimages to the database.
func WritePreimages(db typedb.KeyValueWriter, preimages map[entity.Hash][]byte) {
	for hash, preimage := range preimages {
		if err := db.Put(preimageKey(hash), preimage); err != nil {
			log.Crit("Failed to store trie preimage", "err", err)
		}
	}
	//preimageCounter.Inc(int64(len(preimages)))
	//preimageHitCounter.Inc(int64(len(preimages)))
}
