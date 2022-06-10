package operationdb

import (
	"github.com/VictoriaMetrics/fastcache"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus/utils"
	"sync"
	"time"
)

// Config定义数据库的所有必要选项。
type Config struct {
	Cache     int    // 用于在内存中缓存trie节点的内存余量（MB）
	Journal   string // 节点重新启动后的清除缓存日志
	Preimages bool   // 标记是否记录trie键的前图像
}

//数据库是trie数据结构和磁盘数据库之间的中间写入层。
//其目的是在内存中累积trie写操作，并且只定期刷新几次磁盘尝试，对剩余的进行垃圾收集。
//注意，trie数据库在其突变中**不是**线程安全的，但在提供单独、独立的节点访问时**是**线程安全的。
//这种拆分设计的基本原理是，即使在trie执行昂贵的垃圾收集时，也可以提供对RPC处理程序和同步服务器的读取访问。
type TrieDatabase struct {
	diskdb KeyValueStore // 成熟trie节点的持久存储

	cleans  *fastcache.Cache            // 干净节点RLP的GC友好内存缓存
	dirties map[entity.Hash]*cachedNode // 脏trie节点的数据和引用关系
	oldest  entity.Hash                 // 最旧的跟踪节点，刷新列表头
	newest  entity.Hash                 // 最新跟踪节点，刷新列表尾部

	preimages map[entity.Hash][]byte // 来自安全trie的节点的前映像

	gctime  time.Duration     // 自上次提交以来垃圾收集花费的时间
	gcnodes uint64            // 自上次提交后垃圾收集的节点
	gcsize  utils.StorageSize // 自上次提交后收集的数据存储垃圾

	flushtime  time.Duration     // 自上次提交以来数据刷新所花费的时间
	flushnodes uint64            // 自上次提交后刷新的节点
	flushsize  utils.StorageSize // 自上次提交后刷新的数据存储

	dirtiesSize   utils.StorageSize // 脏节点缓存的存储大小（元数据除外）
	childrenSize  utils.StorageSize // 外部子跟踪的存储大小
	preimagesSize utils.StorageSize // 预映像缓存的存储大小

	lock sync.RWMutex
}

// NewDatabaseWithConfig创建一个新的trie数据库来存储临时trie内容，然后再将其写入磁盘或进行垃圾回收。
//它还充当从磁盘加载的节点的读缓存。
func trieNewDatabaseWithConfig(diskdb KeyValueStore, config *Config) *TrieDatabase {
	var cleans *fastcache.Cache
	if config != nil && config.Cache > 0 {
		if config.Journal == "" {
			cleans = fastcache.New(config.Cache * 1024 * 1024)
		} else {
			cleans = fastcache.LoadFromFileOrNew(config.Journal, config.Cache*1024*1024)
		}
	}
	db := &TrieDatabase{
		diskdb: diskdb,
		cleans: cleans,
		dirties: map[entity.Hash]*cachedNode{{}: {
			children: make(map[entity.Hash]uint16),
		}},
	}
	if config == nil || config.Preimages {
		db.preimages = make(map[entity.Hash][]byte)
	}
	return db
}

// cachedNode是我们所知道的关于内存数据库写入层中单个缓存trie节点的所有信息。
type cachedNode struct {
	node node   // 缓存的折叠trie节点或原始rlp数据
	size uint16 // 有用缓存数据的字节大小

	parents  uint32                 // 引用此节点的活动节点数
	children map[entity.Hash]uint16 // 此节点引用的外部子级

	flushPrev entity.Hash // 刷新列表中的上一个节点
	flushNext entity.Hash // 刷新列表中的下一个节点
}
