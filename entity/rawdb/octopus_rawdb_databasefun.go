package rawdb

import (
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus-blockchain/typedb/leveldb"
	"github.com/radiation-octopus/octopus-blockchain/typedb/memorydb"
)

var (
	// 如果数据库不支持所需的操作，则返回errNotSupported。
	errNotSupported = errors.New("this operation is not supported")
)

// nofreezedb是一种数据库包装器，用于禁用冻结器数据检索。
type nofreezedb struct {
	typedb.KeyValueStore
}

func (db *nofreezedb) Stat(property string) (string, error) {
	panic("implement me")
}

func (db *nofreezedb) AncientDatadir() (string, error) {
	panic("implement me")
}

func (db *nofreezedb) Compact(start []byte, limit []byte) error {
	panic("implement me")
}

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

func (db *nofreezedb) ReadAncients(fn func(reader typedb.AncientReaderOp) error) (err error) {
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

//NewDatabase在给定的键值数据存储上创建一个高级数据库，而无需将不可变的链段移动到冷藏库中。
func NewDatabase(db typedb.KeyValueStore) typedb.Database {
	return &nofreezedb{KeyValueStore: db}
}

// NewDatabase在给定的键值数据存储上创建一个高级数据库
func NewnofreeDatabase(db typedb.KeyValueStore) typedb.Database {
	return &nofreezedb{KeyValueStore: db}
}

// NewMemoryDatabase创建了一个短暂的内存键值数据库
func NewMemoryDatabase() typedb.Database {
	return NewnofreeDatabase(memorydb.New())
}

// NewLevelDBDatabase创建了一个持久的键值数据库，而无需将不可变的链段移动到冷藏库中。
func NewLevelDBDatabase(file string, cache int, handles int, namespace string, readonly bool) (typedb.Database, error) {
	db, err := leveldb.New(file, cache, handles, namespace, readonly)
	if err != nil {
		return nil, err
	}
	return NewDatabase(db), nil
}
