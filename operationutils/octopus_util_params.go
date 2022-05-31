package operationutils

const (
	GasLimitBoundDivisor uint64 = 1024               // 气体极限的界限除数，用于更新计算。
	MinGasLimit          uint64 = 5000               // 最低gas限值
	MaxGasLimit          uint64 = 0x7fffffffffffffff // 最高gas限值
	CallCreateDepth      uint64 = 1024               // 最大深度
	EcrecoverGas         uint64 = 3000               //椭圆曲线算法返回gas价格

	cao    = 1
	Gcao   = 1e9
	Octcao = 1e18
)
