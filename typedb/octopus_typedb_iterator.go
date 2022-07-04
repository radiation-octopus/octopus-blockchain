package typedb

// 迭代器按键的升序遍历数据库的键/值对。当它遇到错误时，任何seek都将返回false，并且不会产生键/值对。可以通过调用error方法查询错误。仍然需要调用Release。
//迭代器必须在使用后释放，但在迭代器耗尽之前，无需读取迭代器。迭代器对于并发使用是不安全的，但同时使用多个迭代器是安全的。
type Iterator interface {
	// 下一步将迭代器移动到下一个键/值对。它返回迭代器是否耗尽。
	Next() bool

	// Error返回任何累积错误。耗尽所有键/值对不被视为错误。
	Error() error

	// Key返回当前键/值对的键，如果完成，则返回nil。调用者不应修改返回切片的内容，其内容可能会在下一次调用next时更改。
	Key() []byte

	// Value返回当前键/值对的值，如果完成，则返回nil。调用者不应修改返回切片的内容，其内容可能会在下一次调用next时更改。
	Value() []byte

	// Release释放关联的资源。发布应该总是成功的，并且可以多次调用，而不会导致错误。
	Release()
}

// Iteratee包装了支持数据存储的NewIterator方法。
type Iteratee interface {
	// NewIterator在具有特定键前缀的数据库内容子集上创建一个二进制字母迭代器，从特定的初始键开始（如果不存在，则在其之后）。
	//注意：此方法假定前缀不是start的一部分，因此调用方无需将前缀前置到start
	NewIterator(prefix []byte, start []byte) Iterator
}
