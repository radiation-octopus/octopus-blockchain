package entity

import "math/big"

const (
	GasLimitBoundDivisor uint64 = 1024               // 气体极限的界限除数，用于更新计算。
	MinGasLimit          uint64 = 5000               // 最低gas限值
	MaxGasLimit          uint64 = 0x7fffffffffffffff // 最高gas限值
	CallCreateDepth      uint64 = 1024               // 最大深度
	EcrecoverGas         uint64 = 3000               //椭圆曲线算法返回gas价格

	GenesisGasLimit uint64 = 4712388 // 创世区块Gas限制

	BaseFeeChangeDenominator = 8          // 限制基本费用在区块之间可以更改的金额。
	ElasticityMultiplier     = 2          // 限制EIP-1559区块可能具有的最大gas限制。
	InitialBaseFee           = 1000000000 // EIP-1559区块的初始基本费用。

	cao    = 1
	Gcao   = 1e9
	Octcao = 1e18
)

var (
	DifficultyBoundDivisor = big.NewInt(2048)   // 难度的界限除数，用于更新计算。
	GenesisDifficulty      = big.NewInt(131072) //创世区块难度值
	MinimumDifficulty      = big.NewInt(131072) // 困难可能达到的最低限度。
	DurationLimit          = big.NewInt(13)     // blocktime持续时间上的决策边界，用于确定是否应该提高难度。

	AllOctellProtocolChanges = &ChainConfig{big.NewInt(1337), big.NewInt(0), nil, false, big.NewInt(0), Hash{}, big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), big.NewInt(0), nil, nil, ""}
)

// DAOFOKEXTRANGE是从DAO分叉点开始的连续块数，用于覆盖中的额外数据以防止无分叉攻击。
var DAOForkExtraRange = big.NewInt(10)

const (
	// FullImmutabilityThreshold是链段被视为不可变的块数（即软终结性）。
	//它被下载程序用作针对深层祖先的硬限制，被区块链用作针对深层reorgs的硬限制，
	//被冻结器用作截止阈值，被集团用作快照信任限制。
	FullImmutabilityThreshold = 90000
)
