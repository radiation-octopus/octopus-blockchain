package vm

//指令操作结构体
type operation struct {
	// 操作执行函数
	execute     executionFunc
	constantGas uint64
	dynamicGas  gasFunc
	// minStack显示需要多少堆栈项
	minStack int
	// maxStack指定堆栈可用于此操作的最大长度
	//避免堆栈溢出。
	maxStack int

	// memorySize返回操作所需的内存大小
	memorySize memorySizeFunc
}

// 包含虚拟机操作码
type JumpTable [256]*operation

type (
	executionFunc func(pc *uint64, interpreter *OVMInterpreter, callContext *ScopeContext) ([]byte, error)
	gasFunc       func(*OVM, *Contract, *Stack, *Memory, uint64) (uint64, error) // 最后一个参数是uint64请求的内存大小
	// memorySizeFunc返回所需的大小，以及操作是否溢出uint64
	memorySizeFunc func(*Stack) (size uint64, overflow bool)
)
