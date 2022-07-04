package typedb

import (
	"io"
)

type Database interface {
	Reader
	Writer
	Batcher
	Iteratee
	//Stater
	//Compacter
	//Snapshotter
	io.Closer
}

//定义读取数据所需的方法
type Reader interface {
	KeyValueReader
	AncientReader
}

type Writer interface {
	KeyValueWriter
	//AncientWriter
}

type KeyValueReader interface {
	//是否存在键
	IsHas(key []byte) (bool, error)

	//通过键检索header值
	Get(key []byte) ([]byte, error)
}

type KeyValueWriter interface {
	// Put将给定值插入键值数据存储。
	Put(key []byte, value []byte) error

	// Delete从键值数据存储中删除键。
	Delete(key []byte) error
}

//func (op OperationDB) IsHas(mark string, key string) (bool, error) {
//	return db.IsHas(mark, key)
//}
//func (op OperationDB) QueryHeader(mark string, key string) *block.Header {
//	u := block.Header{}
//	u = db.Query(mark, key, u).(block.Header)
//	return &u
//}
//
//
//func (op OperationDB) Put(mark string, key string, value *big.Int) error {
//	return db.Insert(mark, key, value)
//}
//
//func (op OperationDB) Delete(mark string, key string) {
//	db.Delete(mark, key)
//}

//老区块读取接口
type AncientReader interface {
	AncientReaderOp

	// ReadAncients运行给定的读取操作，同时确保在底层冻结器上不会发生写入操作。
	ReadAncients(fn func(AncientReaderOp) error) (err error)
}

// AncientReader包含从不变的老数据读取所需的方法。
type AncientReaderOp interface {
	// HASANGRAL返回一个指示器，指示指定的数据是否存在于Ancient存储中。
	HasAncient(kind string, number uint64) (bool, error)

	// Ancient从只附加不可变文件中检索Ancient二进制blob。
	Ancient(kind string, number uint64) ([]byte, error)

	// Ancentrange从索引“start”开始按顺序检索多个项目。
	//他将返回：
	//-最多“计数”项，
	//-至少1项（即使超过maxBytes），但在其他情况下
	//返回maxBytes中尽可能多的项目。
	AncientRange(kind string, start, count, maxBytes uint64) ([][]byte, error)

	// Ancients返回Ancients仓库中的Ancients物品编号。
	Ancients() (uint64, error)

	// Tail返回冷冻柜中第一个存储的项目数。此数字也可以解释为已删除的项目总数。
	Tail() (uint64, error)

	// AncientSize返回指定类别Ancient的大小。
	AncientSize(kind string) (uint64, error)
}

// AncientWriter包含写入不可变古代数据所需的方法。
type AncientWriter interface {
	// ModifyAncients在古代存储上运行写入操作。
	//如果函数返回错误，则会还原对基础存储的任何更改。整数返回值是写入数据的总大小。
	ModifyAncients(func(AncientWriteOp) error) (int64, error)

	// TruncateHead丢弃古代存储中除前n个古代数据外的所有古代数据。截断后，可以从item\n-1（从0开始）访问最新的项。
	TruncateHead(n uint64) error

	// TruncateTail丢弃古代存储中的前n个古代数据。
	//已删除的项目将被忽略。截断后，最早可以访问的项是item\n（从0开始）。
	//删除的项目可能不会立即从旧存储中删除，但只有当累积的删除数据达到阈值时，才会一起删除。
	TruncateTail(n uint64) error

	// 同步将所有内存中的古代存储数据刷新到磁盘。
	Sync() error

	// MigrateTable处理给定表的条目并将其迁移到新格式。
	//第二个参数是一个函数，它接受一个原始条目并以最新格式返回它。
	MigrateTable(string, func([]byte) ([]byte, error)) error
}

// 将AncientWriteOp指定给ModifyAncients的函数参数。
type AncientWriteOp interface {
	// Append添加RLP编码的项。
	Append(kind string, number uint64, item interface{}) error

	// AppendRaw添加了一个没有RLP编码的项。
	AppendRaw(kind string, number uint64, item []byte) error
}

// AncientStater包装备份数据存储的Stat方法。
type AncientStater interface {
	// AncientDatadir返回古代存储的根目录路径。
	AncientDatadir() (string, error)
}

// AncientStore包含允许处理支持不可变链数据存储的不同古代数据存储所需的所有方法。
type AncientStore interface {
	AncientReader
	AncientWriter
	AncientStater
	io.Closer
}

// KeyValueStore包含允许处理支持高级数据库的不同键值数据存储所需的所有方法。
type KeyValueStore interface {
	KeyValueReader
	KeyValueWriter
	//KeyValueStater
	Batcher
	Iteratee
	//Compacter
	//Snapshotter
	io.Closer
}
