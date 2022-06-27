package vm

import (
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus/log"
	"math/big"
)

type BlockContext struct {
	//判断是否有足够的gas转账
	CanTransfer CanTransferFunc
	//转账
	Transfer TransferFunc
	// 获取hash
	GetHash GetHashFunc

	// 区块信息
	Coinbase    entity.Address
	GasLimit    uint64
	BlockNumber *big.Int
	Time        *big.Int
	Difficulty  *big.Int
	BaseFee     *big.Int
	Random      *entity.Hash
}

type (
	// 是否有足够的余额
	CanTransferFunc func(*operationdb.OperationDB, entity.Address, *big.Int) bool
	// 交易执行函数
	TransferFunc func(*operationdb.OperationDB, entity.Address, entity.Address, *big.Int)
	// 返回第几块的hash
	GetHashFunc func(uint64) entity.Hash
)

// 事务信息
type TxContext struct {
	Origin   entity.Address
	GasPrice *big.Int
}

type OVM struct {
	// 区块配置信息
	Context BlockContext
	TxContext
	// 操作数据库访问配置
	Operationdb *operationdb.OperationDB
	// 当前调用堆栈深度
	depth int

	//链信息
	//chainConfig *entity.ChainConfig
	// 链规则
	//chainRules params.Rules
	// 初始化虚拟机配置选项
	Config Config
	// 整个事务中使用的全局辐射章鱼虚拟机
	interpreter *OVMInterpreter
	// 终止虚拟机调用操作
	abort int32
	// callGasTemp保存当前调用的可用gas
	callGasTemp uint64
}

type ChainContext interface {
	// 共识引擎
	Engine() consensus.Engine

	// 返回其对应hash
	GetHeader(entity.Hash, uint64) *block.Header
}

type StateDB interface {
	CreateAccount(entity.Address)

	SubBalance(entity.Address, *big.Int)
	AddBalance(entity.Address, *big.Int)
	GetBalance(entity.Address) *big.Int

	GetNonce(entity.Address) uint64
	SetNonce(entity.Address, uint64)

	GetCodeHash(entity.Address) entity.Hash
	GetCode(entity.Address) []byte
	SetCode(entity.Address, []byte)
	GetCodeSize(entity.Address) int

	AddRefund(uint64)
	SubRefund(uint64)
	GetRefund() uint64

	GetCommittedState(entity.Address, entity.Hash) entity.Hash
	GetState(entity.Address, entity.Hash) entity.Hash
	SetState(entity.Address, entity.Hash, entity.Hash)

	Suicide(entity.Address) bool
	HasSuicided(entity.Address) bool

	// Exist报告给定帐户是否存在于状态。值得注意的是，对于自杀账户，这也应该是真的。
	Exist(entity.Address) bool
	// Empty返回给定帐户是否为空。根据EIP161定义为空（余额=nonce=代码=0）。
	Empty(entity.Address) bool

	//PrepareAccessList（发送方区块链地址，目的地*区块链地址，预编译[]区块链地址，TXAccess数据库访问列表）
	AddressInAccessList(addr entity.Address) bool
	SlotInAccessList(addr entity.Address, slot entity.Hash) (addressOk bool, slotOk bool)
	// AddAddressToAccessList将给定地址添加到访问列表中。即使功能/分叉尚未激活，也可以安全执行此操作
	AddAddressToAccessList(addr entity.Address)
	// AddSlotToAccessList将给定的（地址、插槽）添加到访问列表中。即使功能/分叉尚未激活，也可以安全执行此操作
	AddSlotToAccessList(addr entity.Address, slot entity.Hash)

	RevertToSnapshot(int)
	Snapshot() int

	AddLog(*log.OctopusLog)
	AddPreimage(entity.Hash, []byte)

	ForEachStorage(entity.Address, func(entity.Hash, entity.Hash) bool) error
}

func NewOVM(blockCtx BlockContext, txCtx TxContext, operation *operationdb.OperationDB, config Config) *OVM {
	evm := &OVM{
		Context:     blockCtx,
		TxContext:   txCtx,
		Operationdb: operation,
		Config:      config,
	}
	evm.interpreter = NewOVMInterpreter(evm, config)
	return evm
}

// Reset resets the EVM with a new transaction context.Reset
// This is not threadsafe and should only be done very cautiously.
func (ovm *OVM) Reset(txCtx TxContext, operationdb *operationdb.OperationDB) {
	ovm.TxContext = txCtx
	ovm.Operationdb = operationdb
}

func NewOVMBlockContext(header *block.Header, chain ChainContext, author *entity.Address) BlockContext {
	var (
		beneficiary entity.Address
		baseFee     *big.Int
		random      *entity.Hash
	)

	//
	if author == nil {
		beneficiary, _ = chain.Engine().Author(header) // 忽略terr，我们已通过页眉验证
	} else {
		beneficiary = *author
	}
	if header.BaseFee != nil {
		baseFee = new(big.Int).Set(header.BaseFee)
	}
	if header.Difficulty.Cmp(operationutils.Big0) == 0 {
		random = &header.MixDigest
	}
	return BlockContext{
		CanTransfer: CanTransfer,
		Transfer:    Transfer,
		GetHash:     GetHashFn(header, chain),
		Coinbase:    beneficiary,
		BlockNumber: new(big.Int).Set(header.Number),
		Time:        new(big.Int).SetUint64(header.Time),
		Difficulty:  new(big.Int).Set(header.Difficulty),
		BaseFee:     baseFee,
		GasLimit:    header.GasLimit,
		Random:      random,
	}
}

// NewEVMTxContext为单个事务创建新的事务配置。
func NewEVMTxContext(msg block.Message) TxContext {
	return TxContext{
		Origin:   msg.From(),
		GasPrice: new(big.Int).Set(msg.GasPrice()),
	}
}

func (ovm *OVM) Call(caller ContractRef, addr entity.Address, input []byte, gas uint64, value *big.Int) (ret []byte, leftOverGas uint64, err error) {
	//深度限制
	if ovm.depth > int(entity.CallCreateDepth) {
		return nil, gas, errors.New("超过最大呼叫深度")
	}
	fmt.Println("from:", caller.Address().String())
	fmt.Println("to:", addr.String())
	ovm.Context.Transfer(ovm.Operationdb, caller.Address(), addr, value)
	ovm.Context.CanTransfer(ovm.Operationdb, addr, big.NewInt(20))

	p, isPrecompile := ovm.precompile(addr)
	if isPrecompile {
		ret, gas, err = RunPrecompiledContract(p, input, gas)
	} else {
		// 初始化新合同并设置EVM要使用的代码
		code := ovm.Operationdb.GetCode(addr)
		if len(code) == 0 {
			ret, err = nil, nil // gas不变
		} else {
			addrCopy := addr
			// 如果帐户没有代码，我们可以在这里中止深度检查，并在上面处理预编译
			contract := NewContract(caller, AccountRef(addrCopy), value, gas)
			contract.SetCallCode(&addrCopy, ovm.Operationdb.GetCodeHash(addrCopy), code)
			ret, err = ovm.interpreter.Run(contract, input, false)
			gas = contract.Gas
		}
	}
	return nil, 0, err
}

func GetHashFn(ref *block.Header, chain ChainContext) func(n uint64) entity.Hash {
	var cache []entity.Hash

	return func(n uint64) entity.Hash {
		if len(cache) == 0 {
			cache = append(cache, ref.ParentHash)
		}
		if idx := ref.Number.Uint64() - n - 1; idx < uint64(len(cache)) {
			return cache[idx]
		}
		//我们可以从已知的最后一个元素开始迭代
		lastKnownHash := cache[len(cache)-1]
		lastKnownNumber := ref.Number.Uint64() - uint64(len(cache))

		for {
			header := chain.GetHeader(lastKnownHash, lastKnownNumber)
			if header == nil {
				break
			}
			cache = append(cache, header.ParentHash)
			lastKnownHash = header.ParentHash
			lastKnownNumber = header.Number.Uint64() - 1
			if n == lastKnownNumber {
				return lastKnownHash
			}
		}
		return entity.Hash{}
	}
}

func CanTransfer(db *operationdb.OperationDB, addr entity.Address, amount *big.Int) bool {
	fmt.Println("ovmdb:", db.GetBalance(addr))
	return db.GetBalance(addr).Cmp(amount) >= 0
}

// Transfer使用给定的Db从发送方减去金额，然后将金额添加到接收方
func Transfer(db *operationdb.OperationDB, sender, recipient entity.Address, amount *big.Int) {
	db.SubBalance(sender, amount)
	db.AddBalance(recipient, amount)
}

//预编译
func (ovm *OVM) precompile(addr entity.Address) (PrecompiledContract, bool) {
	var precompiles map[entity.Address]PrecompiledContract
	precompiles = PrecompiledContractsHomestead
	p, ok := precompiles[addr]
	return p, ok
}
