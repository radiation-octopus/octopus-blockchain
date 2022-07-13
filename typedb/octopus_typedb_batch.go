package typedb

// IdealBatchSize定义理想情况下应在一次写入中添加的数据批的大小。
const IdealBatchSize = 100 * 1024

// Batch是一个只写数据库，在调用write时将更改提交到其主机数据库。批处理不能同时使用。
type Batch interface {
	KeyValueWriter

	// ValueSize检索排队等待写入的数据量。
	ValueSize() int

	// 写入将所有累积数据刷新到磁盘。
	Write() error

	// 重置重置批以供重用。
	Reset()

	// Replay重播批处理内容。
	Replay(w KeyValueWriter) error
}

// Batcher包装了备份数据存储的NewBatch方法。
type Batcher interface {
	// NewBatch创建一个只写数据库，该数据库缓冲对其主机数据库的更改，直到调用最后一次写入。
	NewBatch() Batch

	// NewBatchWithSize使用预先分配的缓冲区创建一个只写数据库批处理。
	NewBatchWithSize(size int) Batch
}

// HookedBatch包装了一个任意的批处理，其中每个操作都可以挂接到该批处理中，以从黑匣子代码进行监视。
type HookedBatch struct {
	Batch
	OnPut    func(key []byte, value []byte) // 插入键时回调
	OnDelete func(key []byte)               // 删除密钥时回调
}
