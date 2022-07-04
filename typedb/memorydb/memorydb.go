package memorydb

import (
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus/utils"
	"sort"
	"strings"
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

//NewIterator在具有特定键前缀的数据库内容子集上创建一个二进制字母迭代器，从特定的初始键开始（如果不存在，则在其之后）。
func (db *Database) NewIterator(prefix []byte, start []byte) typedb.Iterator {
	db.lock.RLock()
	defer db.lock.RUnlock()

	var (
		pr     = string(prefix)
		st     = string(append(prefix, start...))
		keys   = make([]string, 0, len(db.db))
		values = make([][]byte, 0, len(db.db))
	)
	// 从与给定前缀对应的内存数据库中收集密钥并启动
	for key := range db.db {
		if !strings.HasPrefix(key, pr) {
			continue
		}
		if key >= st {
			keys = append(keys, key)
		}
	}
	// 对项目进行排序并检索关联的值
	sort.Strings(keys)
	for _, key := range keys {
		values = append(values, db.db[key])
	}
	return &iterator{
		index:  -1,
		keys:   keys,
		values: values,
	}
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

//Put将给定值插入键值存储区。
func (db *Database) Put(key []byte, value []byte) error {
	db.lock.Lock()
	defer db.lock.Unlock()

	if db.db == nil {
		return errMemorydbClosed
	}
	db.db[string(key)] = utils.CopyBytes(value)
	return nil
}

//Delete从键值存储中删除键。
func (db *Database) Delete(key []byte) error {
	db.lock.Lock()
	defer db.lock.Unlock()

	if db.db == nil {
		return errMemorydbClosed
	}
	delete(db.db, string(key))
	return nil
}

func (db *Database) NewBatch() typedb.Batch {
	return &batch{
		db: db,
	}
}

func (d *Database) NewBatchWithSize(size int) typedb.Batch {
	panic("implement me")
}

func (d *Database) Close() error {
	panic("implement me")
}

// keyvalue是一个带有删除字段标记的键值元组，用于创建内存数据库写入批处理。
type keyvalue struct {
	key    []byte
	value  []byte
	delete bool
}

// 批处理是一个只写内存批处理，在调用write时将更改提交到其主机数据库。批处理不能同时使用。
type batch struct {
	db     *Database
	writes []keyvalue
	size   int
}

func (b *batch) Put(key []byte, value []byte) error {
	b.writes = append(b.writes, keyvalue{key, value, false})
	b.size += len(key) + len(value)
	return nil
}

func (b *batch) Delete(key []byte) error {
	b.writes = append(b.writes, keyvalue{key, nil, true})
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
			delete(b.db.db, string(keyvalue.key))
			continue
		}
		b.db.db[string(keyvalue.key)] = keyvalue.value
	}
	return nil
}

func (b *batch) Reset() {
	b.writes = b.writes[:0]
	b.size = 0
}

func (b *batch) Replay(w typedb.KeyValueWriter) error {
	for _, keyvalue := range b.writes {
		if keyvalue.delete {
			if err := w.Delete(keyvalue.key); err != nil {
				return err
			}
			continue
		}
		if err := w.Put(keyvalue.key, keyvalue.value); err != nil {
			return err
		}
	}
	return nil
}

// 迭代器可以遍历内存键值存储的（可能是部分的）键空间。在内部，它是整个迭代状态的深度副本，按键排序。
type iterator struct {
	index  int
	keys   []string
	values [][]byte
}

// 下一步将迭代器移动到下一个键/值对。它返回迭代器是否耗尽。
func (it *iterator) Next() bool {
	// 如果迭代器已在前进方向耗尽，则短路。
	if it.index >= len(it.keys) {
		return false
	}
	it.index += 1
	return it.index < len(it.keys)
}

// Error返回任何累积错误。耗尽所有键/值对不被视为错误。内存迭代器不会遇到错误。
func (it *iterator) Error() error {
	return nil
}

// Key返回当前键/值对的键，如果完成，则返回nil。调用者不应修改返回切片的内容，其内容可能会在下一次调用next时更改。
func (it *iterator) Key() []byte {
	// 迭代器不在有效位置时短路
	if it.index < 0 || it.index >= len(it.keys) {
		return nil
	}
	return []byte(it.keys[it.index])
}

// Value返回当前键/值对的值，如果完成，则返回nil。调用者不应修改返回切片的内容，其内容可能会在下一次调用next时更改。
func (it *iterator) Value() []byte {
	// 迭代器不在有效位置时短路
	if it.index < 0 || it.index >= len(it.keys) {
		return nil
	}
	return it.values[it.index]
}

// Release释放关联的资源。发布应该总是成功的，并且可以多次调用，而不会导致错误。
func (it *iterator) Release() {
	it.index, it.keys, it.values = -1, nil, nil
}

func getKeyStr(mark string, key string) string {
	return mark + "-" + key
}

// New返回一个包装映射，其中实现了所有必需的数据库接口方法。
func New() *Database {
	return &Database{
		db: make(map[string][]byte),
	}
}
