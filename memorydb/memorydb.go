package memorydb

import (
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus/utils"
	"sync"
)

var (
	// 如果在调用数据访问操作时已关闭内存数据库，则返回errMemorydbClosed。
	errMemorydbClosed = errors.New("database closed")

	// 如果请求的密钥在提供的内存数据库中找不到，则返回errMemorydbNotFound。
	errMemorydbNotFound = errors.New("not found")

	// 如果调用方希望从已发布的快照中检索数据，则返回errSnapshotReleased。
	errSnapshotReleased = errors.New("snapshot released")
)

//数据库是一个短暂的键值存储。除了基本的数据存储功能外，它还支持按二进制字母顺序对键空间进行批写入和迭代。
type Database struct {
	db   map[string][]byte
	lock sync.RWMutex
}

//IsHas检索键值存储中是否存在键。
func (db *Database) IsHas(key []byte) (bool, error) {
	db.lock.RLock()
	defer db.lock.RUnlock()

	if db.db == nil {
		return false, errMemorydbClosed
	}
	_, ok := db.db[string(key)]
	return ok, nil
}

//Get检索给定的键（如果它存在于键值存储中）。
func (db *Database) Get(key []byte) ([]byte, error) {
	db.lock.RLock()
	defer db.lock.RUnlock()

	if db.db == nil {
		return nil, errMemorydbClosed
	}
	if entry, ok := db.db[string(key)]; ok {
		return utils.CopyBytes(entry), nil
	}
	return nil, errMemorydbNotFound
}

func (db *Database) Put(mark string, key string, value []byte) error {
	db.lock.Lock()
	defer db.lock.Unlock()

	if db.db == nil {
		return errMemorydbClosed
	}
	db.db[getKeyStr(mark, key)] = utils.CopyBytes(value)
	return nil
}

func (db *Database) Delete(mark string, key string) error {
	db.lock.Lock()
	defer db.lock.Unlock()

	if db.db == nil {
		return errMemorydbClosed
	}
	delete(db.db, getKeyStr(mark, key))
	return nil
}

func (db *Database) NewBatch() operationdb.Batch {
	return &batch{
		db: db,
	}
}

func (d *Database) NewBatchWithSize(size int) operationdb.Batch {
	panic("implement me")
}

func (d *Database) Close() error {
	panic("implement me")
}

// keyvalue是一个带有删除字段标记的键值元组，用于创建内存数据库写入批处理。
type keyvalue struct {
	mark   string
	key    string
	value  []byte
	delete bool
}

// 批处理是一个只写内存批处理，在调用write时将更改提交到其主机数据库。批处理不能同时使用。
type batch struct {
	db     *Database
	writes []keyvalue
	size   int
}

func (b *batch) Put(mark string, key string, value []byte) error {
	b.writes = append(b.writes, keyvalue{mark, key, value, false})
	b.size += len(key) + len(value)
	return nil
}

func (b *batch) Delete(mark string, key string) error {
	b.writes = append(b.writes, keyvalue{mark, key, nil, true})
	b.size += len(key)
	return nil
}

func (b *batch) ValueSize() int {
	return b.size
}

func (b *batch) Write() error {
	b.db.lock.Lock()
	defer b.db.lock.Unlock()

	for _, keyvalue := range b.writes {
		if keyvalue.delete {
			delete(b.db.db, keyvalue.key)
			continue
		}
		b.db.db[keyvalue.key] = keyvalue.value
	}
	return nil
}

func (b *batch) Reset() {
	b.writes = b.writes[:0]
	b.size = 0
}

func (b *batch) Replay(w operationdb.KeyValueWriter) error {
	for _, keyvalue := range b.writes {
		if keyvalue.delete {
			if err := w.Delete(keyvalue.mark, keyvalue.key); err != nil {
				return err
			}
			continue
		}
		if err := w.Put(keyvalue.mark, keyvalue.key, keyvalue.value); err != nil {
			return err
		}
	}
	return nil
}

func getKeyStr(mark string, key string) string {
	return mark + "-" + key
}

// nofreezedb是一种数据库包装器，用于禁用冻结器数据检索。
type nofreezedb struct {
	operationdb.KeyValueStore
}

var errNotSupported = errors.New("this operation is not supported")

// Hasagular返回一个错误，因为我们没有背链冷冻柜。
func (db *nofreezedb) HasAncient(kind string, number uint64) (bool, error) {
	return false, errNotSupported
}

// 因为我们没有背链式冷冻柜，所以Ancient会返回一个错误。
func (db *nofreezedb) Ancient(kind string, number uint64) ([]byte, error) {
	return nil, errNotSupported
}

// Ancentrange返回一个错误，因为我们没有背链冷冻柜。
func (db *nofreezedb) AncientRange(kind string, start, max, maxByteSize uint64) ([][]byte, error) {
	return nil, errNotSupported
}

// Ancentrange返回一个错误，因为我们没有背链冷冻柜。
func (db *nofreezedb) Ancients() (uint64, error) {
	return 0, errNotSupported
}

// Tail返回错误，因为我们没有背链冷冻柜。
func (db *nofreezedb) Tail() (uint64, error) {
	return 0, errNotSupported
}

// AncientSize返回错误，因为我们没有背链冷冻柜。
func (db *nofreezedb) AncientSize(kind string) (uint64, error) {
	return 0, errNotSupported
}

func (db *nofreezedb) ReadAncients(fn func(reader operationdb.AncientReaderOp) error) (err error) {
	// 与其他Ancient的相关方法不同，此方法不会返回
	//调用时不支持errNotSupported。
	//原因是调用者可能想做几件事：
	// 1. 检查冰箱里是否有东西，
	// 2. 如果没有，请检查leveldb。
	//这将起作用，因为“fn”中的Ancient检查将返回错误，
	//leveldb的工作将继续进行。
	//如果我们在此处返回errNotSupported，那么调用方将
	//必须显式检查，使用额外的子句来执行
	//非Ancient操作。
	return fn(db)
}

// New返回一个包装映射，其中实现了所有必需的数据库接口方法。
func New() *Database {
	return &Database{
		db: make(map[string][]byte),
	}
}

// NewMemoryDatabase创建了一个短暂的内存键值数据库
func NewMemoryDatabase() operationdb.Database {
	return NewDatabase(New())
}

// NewDatabase在给定的键值数据存储上创建一个高级数据库
func NewDatabase(db operationdb.KeyValueStore) operationdb.Database {
	return &nofreezedb{KeyValueStore: db}
}
