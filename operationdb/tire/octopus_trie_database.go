package tire

import (
	"errors"
	"fmt"
	"github.com/VictoriaMetrics/fastcache"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/entity/rawdb"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus/log"
	"github.com/radiation-octopus/octopus/utils"
	"io"
	"reflect"
	"sync"
	"time"
)

var (
	// emptyRoot是空trie的已知根哈希。
	emptyRoot = entity.BytesToHash(utils.Hex2Bytes("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"))

	emptyCodeHash = crypto.Keccak256(nil)
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
	diskdb typedb.KeyValueStore // 成熟trie节点的持久存储

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

//cachedNodeSize是不包含任何节点数据的cachedNode数据结构的原始大小。这是一个近似的大小，但应该比不计算好得多。
var cachedNodeSize = int(reflect.TypeOf(cachedNode{}).Size())

// cachedNodeChildrenSize是已初始化但为空的外部引用映射的原始大小。
const cachedNodeChildrenSize = 48

// rawNode是一个简单的二进制blob，用于区分折叠的trie节点和已编码的RLP二进制blob（同时将它们存储在相同的缓存字段中）。
type rawNode []byte

func (n rawNode) cache() (hashNode, bool)   { panic("this should never end up in a live trie") }
func (n rawNode) fstring(ind string) string { panic("this should never end up in a live trie") }

func (n rawNode) EncodeRLP(w io.Writer) error {
	_, err := w.Write(n)
	return err
}

func (n rawNode) encode(w rlp.EncoderBuffer) {
	w.Write(n)
}

// rawFullNode只表示完整节点的有用数据内容，并去掉缓存和标志以最小化其数据存储。此类型采用与原始父级相同的RLP编码。
type rawFullNode [17]node

func (n rawFullNode) cache() (hashNode, bool)   { panic("this should never end up in a live trie") }
func (n rawFullNode) fstring(ind string) string { panic("this should never end up in a live trie") }

func (n rawFullNode) EncodeRLP(w io.Writer) error {
	eb := rlp.NewEncoderBuffer(w)
	n.encode(eb)
	return eb.Flush()
}

func (n rawFullNode) encode(w rlp.EncoderBuffer) {
	offset := w.List()
	for _, c := range n {
		if c != nil {
			c.encode(w)
		} else {
			w.Write(rlp.EmptyString)
		}
	}
	w.ListEnd(offset)
}

// rawShortNode只表示短节点的有用数据内容，去掉缓存和标志以最小化其数据存储。此类型采用与原始父级相同的RLP编码。
type rawShortNode struct {
	Key []byte
	Val node
}

func (n rawShortNode) cache() (hashNode, bool)   { panic("this should never end up in a live trie") }
func (n rawShortNode) fstring(ind string) string { panic("this should never end up in a live trie") }

func (n *rawShortNode) encode(w rlp.EncoderBuffer) {
	offset := w.List()
	w.WriteBytes(n.Key)
	if n.Val != nil {
		n.Val.encode(w)
	} else {
		w.Write(rlp.EmptyString)
	}
	w.ListEnd(offset)
}

// NewDatabase创建一个新的trie数据库来存储临时trie内容，然后再将其写入磁盘或进行垃圾回收。
//没有创建读缓存，因此所有数据检索都将命中底层磁盘数据库。
func NewTrieDatabase(diskdb typedb.KeyValueStore) *TrieDatabase {
	return TrieNewDatabaseWithConfig(diskdb, nil)
}

// NewDatabaseWithConfig创建一个新的trie数据库来存储临时trie内容，然后再将其写入磁盘或进行垃圾回收。
//它还充当从磁盘加载的节点的读缓存。
func TrieNewDatabaseWithConfig(diskdb typedb.KeyValueStore, config *Config) *TrieDatabase {
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

// 节点从内存中检索编码的缓存trie节点。如果找不到缓存，该方法将查询持久数据库中的内容。
func (db *TrieDatabase) Node(hash entity.Hash) ([]byte, error) {
	// 检索元根是没有意义的
	if hash == (entity.Hash{}) {
		return nil, errors.New("not found")
	}
	//从干净缓存中检索节点（如果可用）
	if db.cleans != nil {
		if enc := db.cleans.Get(nil, hash[:]); enc != nil {
			//memcacheCleanHitMeter.Mark(1)
			//memcacheCleanReadMeter.Mark(int64(len(enc)))
			return enc, nil
		}
	}
	// 从脏缓存中检索节点（如果可用）
	db.lock.RLock()
	dirty := db.dirties[hash]
	db.lock.RUnlock()

	if dirty != nil {
		//memcacheDirtyHitMeter.Mark(1)
		//memcacheDirtyReadMeter.Mark(int64(dirty.size))
		return dirty.rlp(), nil
	}
	//memcacheDirtyMissMeter.Mark(1)

	// 内存中的内容不可用，尝试从磁盘中检索
	enc := rawdb.ReadTrieNode(db.diskdb, hash)
	if len(enc) != 0 {
		if db.cleans != nil {
			db.cleans.Set(hash[:], enc)
			//memcacheCleanMissMeter.Mark(1)
			//memcacheCleanWriteMeter.Mark(int64(len(enc)))
		}
		return enc, nil
	}
	return nil, errors.New("not found")
}

// 节点从内存中检索缓存的trie节点，如果在内存缓存中找不到任何节点，则返回nil。
func (db *TrieDatabase) node(hash entity.Hash) node {
	// 从干净缓存中检索节点（如果可用）
	if db.cleans != nil {
		if enc := db.cleans.Get(nil, hash[:]); enc != nil {
			//memcacheCleanHitMeter.Mark(1)
			//memcacheCleanReadMeter.Mark(int64(len(enc)))
			return mustDecodeNode(hash[:], enc)
		}
	}
	// 从脏缓存中检索节点（如果可用）
	db.lock.RLock()
	dirty := db.dirties[hash]
	db.lock.RUnlock()

	if dirty != nil {
		//memcacheDirtyHitMeter.Mark(1)
		//memcacheDirtyReadMeter.Mark(int64(dirty.size))
		return dirty.obj(hash)
	}
	//memcacheDirtyMissMeter.Mark(1)

	// 内存中的内容不可用，请尝试从磁盘检索
	enc, err := db.diskdb.Get(hash[:])
	if err != nil || enc == nil {
		return nil
	}
	if db.cleans != nil {
		db.cleans.Set(hash[:], enc)
		//memcacheCleanMissMeter.Mark(1)
		//memcacheCleanWriteMeter.Mark(int64(len(enc)))
	}
	return mustDecodeNode(hash[:], enc)
}

// DiskDB 检索支持trie数据库的持久存储。
func (db *TrieDatabase) DiskDB() typedb.KeyValueStore {
	return db.diskdb
}

//如果未知，insertPreimage会将新的trie节点pre映像写入内存数据库。该方法不会复制切片，仅在以后不更改前图像时使用。
//注意，此方法假定数据库的锁被持有！
func (db *TrieDatabase) insertPreimage(hash entity.Hash, preimage []byte) {
	// 禁用前图像采集时短路
	if db.preimages == nil {
		return
	}
	// 如果前图像未知，则跟踪前图像
	if _, ok := db.preimages[hash]; ok {
		return
	}
	db.preimages[hash] = preimage
	db.preimagesSize += utils.StorageSize(entity.HashLength + len(preimage))
}

// insert将折叠的trie节点插入内存数据库。必须指定blob大小以允许正确的大小跟踪。
//此函数插入的所有节点都将被引用跟踪，理论上只能用于**trie节点**插入。
func (db *TrieDatabase) insert(hash entity.Hash, size int, node node) {
	db.lock.Lock()
	defer db.lock.Unlock()

	// 如果节点已缓存，请跳过
	if _, ok := db.dirties[hash]; ok {
		return
	}
	//memcacheDirtyWriteMeter.Mark(int64(size))

	// 为此节点创建缓存项
	entry := &cachedNode{
		node:      simplifyNode(node),
		size:      uint16(size),
		flushPrev: db.newest,
	}
	entry.forChilds(func(child entity.Hash) {
		if c := db.dirties[child]; c != nil {
			c.parents++
		}
	})
	db.dirties[hash] = entry

	// 更新刷新列表端点
	if db.oldest == (entity.Hash{}) {
		db.oldest, db.newest = hash, hash
	} else {
		db.dirties[db.newest].flushNext, db.newest = hash, hash
	}
	db.dirtiesSize += utils.StorageSize(entity.HashLength + entry.size)
}

// 引用将新引用从父节点添加到子节点。
//此函数用于在内部trie节点和外部节点（例如存储trie根）之间添加引用，所有内部trie节点由数据库本身一起引用。
func (db *TrieDatabase) Reference(child entity.Hash, parent entity.Hash) {
	db.lock.Lock()
	defer db.lock.Unlock()

	db.reference(child, parent)
}

// Cap迭代刷新旧但仍引用的trie节点，直到总内存使用量低于给定阈值。
//注意，该方法是一个非同步的变异器。将其与其他变异子同时调用是不安全的。
func (db *TrieDatabase) Cap(limit utils.StorageSize) error {
	// 创建数据库批处理以清除持久数据。重要的是，外部代码不会看到不一致的状态（提交期间从内存缓存中删除但尚未在持久存储中的引用数据）。
	//这是通过在数据库写入完成时仅取消对现有数据的缓存来确保的。
	nodes, storage, start := len(db.dirties), db.dirtiesSize, time.Now()
	batch := db.diskdb.NewBatch()

	// db。dirtiesSize只包含缓存中的有用数据，但在报告总内存消耗时，还需要统计维护元数据。
	size := db.dirtiesSize + utils.StorageSize((len(db.dirties)-1)*cachedNodeSize)
	size += db.childrenSize - utils.StorageSize(len(db.dirties[entity.Hash{}].children)*(entity.HashLength+2))

	// 如果预映像缓存足够大，请推送到磁盘。如果仍然很小，请留待以后进行重复数据消除写入。
	flushPreimages := db.preimagesSize > 4*1024*1024
	if flushPreimages {
		if db.preimages == nil {
			log.Error("Attempted to write preimages whilst disabled")
		} else {
			//rawdb.WritePreimages(batch, db.preimages)
			if batch.ValueSize() > typedb.IdealBatchSize {
				if err := batch.Write(); err != nil {
					return err
				}
				batch.Reset()
			}
		}
	}
	// 继续从刷新列表中提交节点，直到低于允许值
	oldest := db.oldest
	for size > limit && oldest != (entity.Hash{}) {
		// 获取最旧的引用节点并推入批处理
		node := db.dirties[oldest]
		rawdb.WriteTrieNode(batch, oldest, node.rlp())

		// 如果我们超过了理想的批量大小，请提交并重置
		if batch.ValueSize() >= typedb.IdealBatchSize {
			if err := batch.Write(); err != nil {
				log.Error("Failed to write flush list to disk", "err", err)
				return err
			}
			batch.Reset()
		}
		// 迭代到下一个刷新项，如果达到大小上限，则中止。
		//Size是总大小，包括有用的缓存数据（哈希->blob）、缓存项元数据以及外部子映射。
		size -= utils.StorageSize(entity.HashLength + int(node.size) + cachedNodeSize)
		if node.children != nil {
			size -= utils.StorageSize(cachedNodeChildrenSize + len(node.children)*(entity.HashLength+2))
		}
		oldest = node.flushNext
	}
	// 清除最后一批中的所有剩余数据
	if err := batch.Write(); err != nil {
		log.Error("Failed to write flush list to disk", "err", err)
		return err
	}
	// 写入成功，清除刷新的数据
	db.lock.Lock()
	defer db.lock.Unlock()

	if flushPreimages {
		if db.preimages == nil {
			log.Error("Attempted to reset preimage cache whilst disabled")
		} else {
			db.preimages, db.preimagesSize = make(map[entity.Hash][]byte), 0
		}
	}
	for db.oldest != oldest {
		node := db.dirties[db.oldest]
		delete(db.dirties, db.oldest)
		db.oldest = node.flushNext

		db.dirtiesSize -= utils.StorageSize(entity.HashLength + int(node.size))
		if node.children != nil {
			db.childrenSize -= utils.StorageSize(cachedNodeChildrenSize + len(node.children)*(entity.HashLength+2))
		}
	}
	if db.oldest != (entity.Hash{}) {
		db.dirties[db.oldest].flushPrev = entity.Hash{}
	}
	db.flushnodes += uint64(nodes - len(db.dirties))
	db.flushsize += storage - db.dirtiesSize
	db.flushtime += time.Since(start)

	//memcacheFlushTimeTimer.Update(time.Since(start))
	//memcacheFlushSizeMeter.Mark(int64(storage - db.dirtiesSize))
	//memcacheFlushNodesMeter.Mark(int64(nodes - len(db.dirties)))

	log.Debug("Persisted nodes from memory database", "nodes", nodes-len(db.dirties), "size", storage-db.dirtiesSize, "time", time.Since(start),
		"flushnodes", db.flushnodes, "flushsize", db.flushsize, "flushtime", db.flushtime, "livenodes", len(db.dirties), "livesize", db.dirtiesSize)

	return nil
}

// reference是引用的私有锁定版本。
func (db *TrieDatabase) reference(child entity.Hash, parent entity.Hash) {
	// 如果该节点不存在，则是从磁盘中提取的节点，跳过
	node, ok := db.dirties[child]
	if !ok {
		return
	}
	// 如果引用已存在，则仅对根进行复制
	if db.dirties[parent].children == nil {
		db.dirties[parent].children = make(map[entity.Hash]uint16)
		db.childrenSize += cachedNodeChildrenSize
	} else if _, ok = db.dirties[parent].children[child]; ok && parent != (entity.Hash{}) {
		return
	}
	node.parents++
	db.dirties[parent].children[child]++
	if db.dirties[parent].children[child] == 1 {
		db.childrenSize += entity.HashLength + 2 // uint16计数器
	}
}

// 取消引用从根节点删除现有引用。
func (db *TrieDatabase) Dereference(root entity.Hash) {
	// 健全性检查以确保未删除元根
	if root == (entity.Hash{}) {
		log.Error("Attempted to dereference the trie cache meta root")
		return
	}
	db.lock.Lock()
	defer db.lock.Unlock()

	nodes, storage, start := len(db.dirties), db.dirtiesSize, time.Now()
	db.dereference(root, entity.Hash{})

	db.gcnodes += uint64(nodes - len(db.dirties))
	db.gcsize += storage - db.dirtiesSize
	db.gctime += time.Since(start)

	//memcacheGCTimeTimer.Update(time.Since(start))
	//memcacheGCSizeMeter.Mark(int64(storage - db.dirtiesSize))
	//memcacheGCNodesMeter.Mark(int64(nodes - len(db.dirties)))

	//log.Debug("Dereferenced trie from memory database", "nodes", nodes-len(db.dirties), "size", storage-db.dirtiesSize, "time", time.Since(start),
	//	"gcnodes", db.gcnodes, "gcsize", db.gcsize, "gctime", db.gctime, "livenodes", len(db.dirties), "livesize", db.dirtiesSize)
}

//解引用是解引用的私有锁定版本。
func (db *TrieDatabase) dereference(child entity.Hash, parent entity.Hash) {
	// 取消引用父子关系
	node := db.dirties[parent]

	if node.children != nil && node.children[child] > 0 {
		node.children[child]--
		if node.children[child] == 0 {
			delete(node.children, child)
			db.childrenSize -= (entity.HashLength + 2) // uint16计数器
		}
	}
	// 如果子节点不存在，则它是以前提交的节点。
	node, ok := db.dirties[child]
	if !ok {
		return
	}
	// 如果不再有对子级的引用，请将其删除并级联
	if node.parents > 0 {
		// 这是一种特殊情况，从磁盘加载的节点（即不再位于memcache中）被重新注入为新节点（短节点拆分为完整节点，然后还原为短节点），导致缓存节点没有父节点。
		//这本身没有问题，但不要让maxint成为父母。
		node.parents--
	}
	if node.parents == 0 {
		// 从刷新列表中删除节点
		switch child {
		case db.oldest:
			db.oldest = node.flushNext
			db.dirties[node.flushNext].flushPrev = entity.Hash{}
		case db.newest:
			db.newest = node.flushPrev
			db.dirties[node.flushPrev].flushNext = entity.Hash{}
		default:
			db.dirties[node.flushPrev].flushNext = node.flushNext
			db.dirties[node.flushNext].flushPrev = node.flushPrev
		}
		// 取消引用所有子节点并删除节点
		node.forChilds(func(hash entity.Hash) {
			db.dereference(hash, child)
		})
		delete(db.dirties, child)
		db.dirtiesSize -= utils.StorageSize(entity.HashLength + int(node.size))
		if node.children != nil {
			db.childrenSize -= cachedNodeChildrenSize
		}
	}
}

// Commit对特定节点的所有子节点进行迭代，并将其写入磁盘，强制从两个方向删除所有引用。
//作为一种副作用，也会写入到目前为止积累的所有预图像。
//注意，这个方法是一个非同步的变异器。将其与其他突变同时调用是不安全的。
func (db *TrieDatabase) Commit(node entity.Hash, report bool, callback func(entity.Hash)) error {
	// 创建数据库批处理以清除持久数据。
	//重要的是，外部代码不会看到不一致的状态（提交期间从内存缓存中删除的引用数据，但尚未在持久存储中）。
	//只有在数据库写入完成时才取消对现有数据的缓存，才能确保这一点。
	//start := time.Now()
	batch := db.diskdb.NewBatch()

	// 将所有累积的前映像移动到写入批中
	if db.preimages != nil {
		//WritePreimages(batch, db.preimages)
		// 因为我们将重播trie节点写入干净缓存的操作，所以请在继续之前清除所有批处理的预映像。
		if err := batch.Write(); err != nil {
			return err
		}
		batch.Reset()
	}
	// 将trie自身移动到批次中，如果积累了足够的数据，则进行刷新
	//nodes, storage := len(db.dirties), db.dirtiesSize

	uncacher := &cleaner{db}
	if err := db.commit(node, batch, uncacher, callback); err != nil {
		log.Error("Failed to commit trie from trie database", "err", err)
		return err
	}
	// Trie主要致力于磁盘，清除任何批剩余
	if err := batch.Write(); err != nil {
		log.Error("Failed to write trie to disk", "err", err)
		return err
	}
	// 打开最后一批中的所有残存物
	db.lock.Lock()
	defer db.lock.Unlock()

	batch.Replay(uncacher)
	batch.Reset()

	// 重置存储计数器和缓冲指标
	if db.preimages != nil {
		db.preimages, db.preimagesSize = make(map[entity.Hash][]byte), 0
	}
	//memcacheCommitTimeTimer.Update(time.Since(start))
	//memcacheCommitSizeMeter.Mark(int64(storage - db.dirtiesSize))
	//memcacheCommitNodesMeter.Mark(int64(nodes - len(db.dirties)))

	//logger := log.Info
	//if !report {
	//	logger = log.Debug
	//}
	//123logger("Persisted trie from memory database", "nodes", nodes-len(db.dirties)+int(db.flushnodes), "size", storage-db.dirtiesSize+db.flushsize, "time", time.Since(start)+db.flushtime,
	//	"gcnodes", db.gcnodes, "gcsize", db.gcsize, "gctime", db.gctime, "livenodes", len(db.dirties), "livesize", db.dirtiesSize)

	// 重置垃圾收集统计信息
	db.gcnodes, db.gcsize, db.gctime = 0, 0, 0
	db.flushnodes, db.flushsize, db.flushtime = 0, 0, 0

	return nil
}

//commit是commit的私有锁定版本。
func (db *TrieDatabase) commit(hash entity.Hash, batch typedb.Batch, uncacher *cleaner, callback func(entity.Hash)) error {
	// 如果节点不存在，则它是以前提交的节点
	node, ok := db.dirties[hash]
	//123fmt.Println("------------",db.dirties)
	if !ok {
		return nil
	}
	var err error
	node.forChilds(func(child entity.Hash) {
		if err == nil {
			err = db.commit(child, batch, uncacher, callback)
		}
	})
	if err != nil {
		return err
	}
	//fmt.Println(node.node)
	// 如果我们已达到最佳批量大小，请提交并重新开始
	rawdb.WriteTrieNode(batch, hash, node.rlp())

	if callback != nil {
		callback(hash)
	}
	if batch.ValueSize() >= typedb.IdealBatchSize {
		if err := batch.Write(); err != nil {
			return err
		}
		db.lock.Lock()
		batch.Replay(uncacher)
		batch.Reset()
		db.lock.Unlock()
	}
	return nil
}

//Size返回持久数据库层前面的内存缓存的当前存储大小。
func (db *TrieDatabase) Size() (utils.StorageSize, utils.StorageSize) {
	db.lock.RLock()
	defer db.lock.RUnlock()

	// 数据库。dirtiesSize只包含缓存中的有用数据，但在报告总内存消耗时，还需要统计维护元数据。
	var metadataSize = utils.StorageSize((len(db.dirties) - 1) * cachedNodeSize)
	var metarootRefs = utils.StorageSize(len(db.dirties[entity.Hash{}].children) * (entity.HashLength + 2))
	return db.dirtiesSize + db.childrenSize + metadataSize - metarootRefs, db.preimagesSize
}

// simplifyNode遍历扩展内存节点的层次结构并丢弃所有内部缓存，返回仅包含原始数据的节点。
func simplifyNode(n node) node {
	switch n := n.(type) {
	case *shortNode:
		// 短节点丢弃标志并级联
		return &rawShortNode{Key: n.Key, Val: simplifyNode(n.Val)}

	case *fullNode:
		// 完整节点丢弃标志并级联
		node := rawFullNode(n.Children)
		for i := 0; i < len(node); i++ {
			if node[i] != nil {
				node[i] = simplifyNode(node[i])
			}
		}
		return node

	case valueNode, hashNode, rawNode:
		return n

	default:
		panic(fmt.Sprintf("unknown node type: %T", n))
	}
}

// cleaner是一个数据库批处理重放器，它接受一批写入操作，并从写入磁盘的任何内容中清除trie数据库。
type cleaner struct {
	db *TrieDatabase
}

// Put对数据库写入作出反应，并实现脏数据取消缓存。
//这是提交操作的后处理步骤，其中已持久化的trie将从脏缓存中移除并移动到干净缓存中。
//两阶段提交背后的原因是在从内存移动到磁盘时确保数据可用性。
func (c *cleaner) Put(key []byte, rlp []byte) error {
	hash := entity.BytesToHash(key)

	// 如果该节点不存在，则在此路径上完成
	node, ok := c.db.dirties[hash]
	if !ok {
		return nil
	}
	// 节点仍然存在，请将其从刷新列表中删除
	switch hash {
	case c.db.oldest:
		c.db.oldest = node.flushNext
		c.db.dirties[node.flushNext].flushPrev = entity.Hash{}
	case c.db.newest:
		c.db.newest = node.flushPrev
		c.db.dirties[node.flushPrev].flushNext = entity.Hash{}
	default:
		c.db.dirties[node.flushPrev].flushNext = node.flushNext
		c.db.dirties[node.flushNext].flushPrev = node.flushPrev
	}
	// 从脏缓存中删除节点
	delete(c.db.dirties, hash)
	c.db.dirtiesSize -= utils.StorageSize(entity.HashLength + int(node.size))
	if node.children != nil {
		c.db.childrenSize -= utils.StorageSize(cachedNodeChildrenSize + len(node.children)*(entity.HashLength+2))
	}
	// 将刷新的节点移动到干净的缓存中，以防止insta重新加载
	if c.db.cleans != nil {
		c.db.cleans.Set(hash[:], rlp)
		//memcacheCleanWriteMeter.Mark(int64(len(rlp)))
	}
	return nil
}

func (c *cleaner) Delete(key []byte) error {
	panic("not implemented")
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

// obj直接从缓存返回已解码和扩展的trie节点，或通过从rlp编码的blob重新生成该节点。
func (n *cachedNode) obj(hash entity.Hash) node {
	if node, ok := n.node.(rawNode); ok {
		return mustDecodeNode(hash[:], node)
	}
	return expandNode(hash[:], n.node)
}

// rlp直接从缓存返回缓存的trie节点的原始rlp编码blob，或者从折叠的节点重新生成它。
func (n *cachedNode) rlp() []byte {
	if node, ok := n.node.(rawNode); ok {
		return node
	}
	return nodeToBytes(n.node)
}

// expandNode遍历折叠存储节点的节点层次结构，并将所有字段和键转换为扩展内存形式。
func expandNode(hash hashNode, n node) node {
	switch n := n.(type) {
	case *rawShortNode:
		// 短节点需要密钥和子节点扩展
		return &shortNode{
			Key: compactToHex(n.Key),
			Val: expandNode(nil, n.Val),
			flags: nodeFlag{
				hash: hash,
			},
		}

	case rawFullNode:
		// 完整节点需要子扩展
		node := &fullNode{
			flags: nodeFlag{
				hash: hash,
			},
		}
		for i := 0; i < len(node.Children); i++ {
			if n[i] != nil {
				node.Children[i] = expandNode(nil, n[i])
			}
		}
		return node

	case valueNode, hashNode:
		return n

	default:
		panic(fmt.Sprintf("unknown node type: %T", n))
	}
}

// forChilds为该节点的所有跟踪子节点调用回调，包括来自节点内部的隐式子节点和来自节点外部的显式子节点。
func (n *cachedNode) forChilds(onChild func(hash entity.Hash)) {
	//判断是否还有孩子节点，有则继续递归提交commit
	for child := range n.children {
		onChild(child)
	}
	//判断本节点是否为折叠trie节点
	if _, ok := n.node.(rawNode); !ok {
		forGatherChildren(n.node, onChild)
	}
}

//forGatherChildren遍历折叠存储节点的节点层次结构，并调用所有hashnode子节点的回调。
func forGatherChildren(n node, onChild func(hash entity.Hash)) {
	switch n := n.(type) {
	case *rawShortNode:
		forGatherChildren(n.Val, onChild)
	case rawFullNode:
		for i := 0; i < 16; i++ {
			forGatherChildren(n[i], onChild)
		}
	case hashNode:
		onChild(entity.BytesToHash(n))
	case valueNode, nil, rawNode:
	default:
		panic(fmt.Sprintf("unknown node type: %T", n))
	}
}
