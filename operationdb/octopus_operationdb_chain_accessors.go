package operationdb

import (
	"github.com/VictoriaMetrics/fastcache"
	lru "github.com/hashicorp/golang-lru"
	"github.com/radiation-octopus/octopus-blockchain/operationdb/tire"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
)

const (
	// 要保留的codehash->大小关联数。
	codeSizeCacheSize = 100000

	// 为缓存干净代码而授予的缓存大小。
	codeCacheSize = 64 * 1024 * 1024
)

// NewDatabase为状态创建备份存储。返回的数据库可以安全地并发使用，但不会在内存中保留任何最近的trie节点。
//要在内存中保留一些历史状态，请使用NewDatabaseWithConfig构造函数。
func NewDatabase(db typedb.Database) DatabaseI {
	return NewDatabaseWithConfig(db, nil)
}

// NewDatabaseWithConfig为状态创建备份存储。
//返回的数据库可以安全地并发使用，并在大型内存缓存中保留大量折叠的RLP trie节点。
func NewDatabaseWithConfig(db typedb.Database, config *tire.Config) DatabaseI {
	csc, _ := lru.New(codeSizeCacheSize)
	return &cachingDB{
		db:            tire.TrieNewDatabaseWithConfig(db, config),
		codeSizeCache: csc,
		codeCache:     fastcache.New(codeCacheSize),
	}
}
