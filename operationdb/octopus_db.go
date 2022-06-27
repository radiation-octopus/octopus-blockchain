package operationdb

import (
	"io"
)

type Database interface {
	Reader
	Writer
	Batcher
	//Iteratee
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
	Put(mark string, key string, value []byte) error

	// Delete从键值数据存储中删除键。
	Delete(mark string, key string) error
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

// KeyValueStore包含允许处理支持高级数据库的不同键值数据存储所需的所有方法。
type KeyValueStore interface {
	KeyValueReader
	KeyValueWriter
	//KeyValueStater
	Batcher
	//Iteratee
	//Compacter
	//Snapshotter
	io.Closer
}
