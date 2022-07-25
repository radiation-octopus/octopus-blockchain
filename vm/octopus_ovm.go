package vm

import (
	"errors"
	"fmt"
	"github.com/holiman/uint256"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"math/big"
	"sync/atomic"
)

// create使用emptyCodeHash来确保不允许对已部署的合同地址进行部署（在帐户抽象之后相关）。
var emptyCodeHash = crypto.Keccak256Hash(nil)

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
	chainConfig *entity.ChainConfig
	//链规则
	chainRules entity.Rules
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
	GetHeader(entity.Hash, uint64) *block2.Header
}

type Operationdb interface {
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

	AddLog(*block2.Log)
	AddPreimage(entity.Hash, []byte)

	ForEachStorage(entity.Address, func(entity.Hash, entity.Hash) bool) error
}

func NewOVM(blockCtx BlockContext, txCtx TxContext, operation *operationdb.OperationDB, chainConfig *entity.ChainConfig, config Config) *OVM {
	ovm := &OVM{
		Context:     blockCtx,
		TxContext:   txCtx,
		Operationdb: operation,
		Config:      config,
		chainConfig: chainConfig,
		chainRules:  chainConfig.Rules(blockCtx.BlockNumber, blockCtx.Random != nil),
	}
	ovm.interpreter = NewOVMInterpreter(ovm, config)
	return ovm
}

// 重置使用新的事务上下文重置EVM。重置这不是线程安全的，只能非常谨慎地执行。
func (ovm *OVM) Reset(txCtx TxContext, operationdb *operationdb.OperationDB) {
	ovm.TxContext = txCtx
	ovm.Operationdb = operationdb
}

func NewOVMBlockContext(header *block2.Header, chain ChainContext, author *entity.Address) BlockContext {
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
func NewEVMTxContext(msg block2.Message) TxContext {
	return TxContext{
		Origin:   msg.From(),
		GasPrice: new(big.Int).Set(msg.GasPrice()),
	}
}

type codeAndHash struct {
	code []byte
	hash entity.Hash
}

func (c *codeAndHash) Hash() entity.Hash {
	if c.hash == (entity.Hash{}) {
		c.hash = crypto.Keccak256Hash(c.code)
	}
	return c.hash
}

// create使用代码作为部署代码创建新合同。
func (ovm *OVM) create(caller ContractRef, codeAndHash *codeAndHash, gas uint64, value *big.Int, address entity.Address, typ OpCode) ([]byte, entity.Address, uint64, error) {
	// 深度检查执行。如果我们试图执行超过限制，则失败。
	if ovm.depth > int(entity.CallCreateDepth) {
		return nil, entity.Address{}, gas, ErrDepth
	}
	if !ovm.Context.CanTransfer(ovm.Operationdb, caller.Address(), value) {
		return nil, entity.Address{}, gas, ErrInsufficientBalance
	}
	nonce := ovm.Operationdb.GetNonce(caller.Address())
	if nonce+1 < nonce {
		return nil, entity.Address{}, gas, ErrNonceUintOverflow
	}
	ovm.Operationdb.SetNonce(caller.Address(), nonce+1)
	// 在拍摄快照之前，我们将其添加到访问列表中。
	//即使创建失败，也不应回滚访问列表更改
	if ovm.chainRules.IsBerlin {
		ovm.Operationdb.AddAddressToAccessList(address)
	}
	// 确保指定地址没有现有合同
	contractHash := ovm.Operationdb.GetCodeHash(address)
	if ovm.Operationdb.GetNonce(address) != 0 || (contractHash != (entity.Hash{}) && contractHash != emptyCodeHash) {
		return nil, entity.Address{}, 0, ErrContractAddressCollision
	}
	// 在该州创建新帐户
	//snapshot := ovm.Operationdb.Snapshot()
	ovm.Operationdb.CreateAccount(address)
	if ovm.chainRules.IsEIP158 {
		ovm.Operationdb.SetNonce(address, 1)
	}
	ovm.Context.Transfer(ovm.Operationdb, caller.Address(), address, value)

	// 初始化新合同并设置EVM使用的代码。
	//合同仅是此执行上下文的作用域环境。
	contract := NewContract(caller, AccountRef(address), value, gas)
	contract.SetCodeOptionalHash(&address, codeAndHash)

	//if ovm.Config.Debug {
	//	if ovm.depth == 0 {
	//		ovm.Config.Tracer.CaptureStart(ovm, caller.Address(), address, true, codeAndHash.code, gas, value)
	//	} else {
	//		ovm.Config.Tracer.CaptureEnter(typ, caller.Address(), address, codeAndHash.code, gas, value)
	//	}
	//}

	//start := time.Now()

	ret, err := ovm.interpreter.Run(contract, nil, false)

	// 检查是否已超过最大代码大小，如果是，则分配err。
	if err == nil && ovm.chainRules.IsEIP158 && len(ret) > entity.MaxCodeSize {
		err = ErrMaxCodeSizeExceeded
	}

	// 如果启用了EIP-3541，则拒绝以0xEF开头的代码。
	if err == nil && len(ret) >= 1 && ret[0] == 0xEF && ovm.chainRules.IsLondon {
		err = ErrInvalidCode
	}

	// 如果合同创建成功运行且未返回错误，则计算存储代码所需的gas。
	//如果由于gas不足而无法存储代码，则设置错误，并通过以下错误检查条件进行处理。
	if err == nil {
		createDataGas := uint64(len(ret)) * entity.CreateDataGas
		if contract.UseGas(createDataGas) {
			ovm.Operationdb.SetCode(address, ret)
		} else {
			err = ErrCodeStoreOutOfGas
		}
	}

	// 当EVM返回错误或在上面设置创建代码时，我们返回快照并消耗剩余的gas。
	//此外，当我们在homestead时，这也会计入代码存储错误。
	//if err != nil && (ovm.chainRules.IsHomestead || err != ErrCodeStoreOutOfGas) {
	//	ovm.Operationdb.RevertToSnapshot(snapshot)
	//	if err != ErrExecutionReverted {
	//		contract.UseGas(contract.Gas)
	//	}
	//}

	//if ovm.Config.Debug {
	//	if ovm.depth == 0 {
	//		ovm.Config.Tracer.CaptureEnd(ret, gas-contract.Gas, time.Since(start), err)
	//	} else {
	//		ovm.Config.Tracer.CaptureExit(ret, gas-contract.Gas, err)
	//	}
	//}
	return ret, address, contract.Gas, err
}

// Create使用代码作为部署代码创建新合同。
func (ovm *OVM) Create(caller ContractRef, code []byte, gas uint64, value *big.Int) (ret []byte, contractAddr entity.Address, leftOverGas uint64, err error) {
	contractAddr = crypto.CreateAddress(caller.Address(), ovm.Operationdb.GetNonce(caller.Address()))
	return ovm.create(caller, &codeAndHash{code: code}, gas, value, contractAddr, CREATE)
}

// Create2使用代码作为部署代码创建一个新契约。
//Create2与Create的不同之处在于Create2使用keccak256（0xff++msg.sender++salt++keccak256（init_代码））[12:]，而不是通常的sender和nonce散列作为合同初始化的地址。
func (ovm *OVM) Create2(caller ContractRef, code []byte, gas uint64, endowment *big.Int, salt *uint256.Int) (ret []byte, contractAddr entity.Address, leftOverGas uint64, err error) {
	codeAndHash := &codeAndHash{code: code}
	contractAddr = crypto.CreateAddress2(caller.Address(), salt.Bytes32(), codeAndHash.Hash().Bytes())
	return ovm.create(caller, codeAndHash, gas, endowment, contractAddr, CREATE2)
}

func (ovm *OVM) Call(caller ContractRef, addr entity.Address, input []byte, gas uint64, value *big.Int) (ret []byte, leftOverGas uint64, err error) {
	//深度限制
	if ovm.depth > int(entity.CallCreateDepth) {
		return nil, gas, errors.New("超过最大呼叫深度")
	}
	fmt.Println("from:", caller.Address().String())
	fmt.Println("to:", addr.String())
	ovm.Context.CanTransfer(ovm.Operationdb, addr, big.NewInt(20))
	ovm.Context.Transfer(ovm.Operationdb, caller.Address(), addr, value)
	p, isPrecompile := ovm.precompile(addr)
	if isPrecompile {
		ret, gas, err = RunPrecompiledContract(p, input, gas)
	} else {
		// 初始化新合同并设置EVM要使用的代码
		code := ovm.Operationdb.GetCode(addr)
		// 如果帐户没有代码，我们可以在这里中止深度检查，并在上面处理预编译
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
	return ret, gas, err
}

// CallCode使用给定的输入作为参数来执行与addr关联的契约。它还处理所需的任何必要的价值转移，并采取必要步骤创建帐户，并在执行错误或价值转移失败时反转状态。
//CallCode与Call的不同之处在于，它以调用方作为上下文来执行给定的地址代码。
func (ovm *OVM) CallCode(caller ContractRef, addr entity.Address, input []byte, gas uint64, value *big.Int) (ret []byte, leftOverGas uint64, err error) {
	// 如果我们试图在调用深度限制以上执行，则失败
	if ovm.depth > int(entity.CallCreateDepth) {
		return nil, gas, ErrDepth
	}
	// 如果我们试图转移超过可用余额的票据，则失败，尽管将X以太转移到调用方本身是不可能的。
	//但若呼叫方并没有足够的余额，那个么允许过度充电本身就是一个错误。所以这里的检查是必要的。
	if !ovm.Context.CanTransfer(ovm.Operationdb, caller.Address(), value) {
		return nil, gas, ErrInsufficientBalance
	}
	var snapshot = ovm.Operationdb.Snapshot()

	// 调用跟踪钩子，该钩子发出进入/退出调用帧的信号
	//if ovm.Config.Debug {
	//	ovm.Config.Tracer.CaptureEnter(CALLCODE, caller.Address(), addr, input, gas, value)
	//	defer func(startGas uint64) {
	//		ovm.Config.Tracer.CaptureExit(ret, startGas-gas, err)
	//	}(gas)
	//}

	//允许调用预编译，即使是通过delegatecall
	if p, isPrecompile := ovm.precompile(addr); isPrecompile {
		ret, gas, err = RunPrecompiledContract(p, input, gas)
	} else {
		addrCopy := addr
		// 初始化新合同并设置EVM使用的代码。合同仅是此执行上下文的作用域环境。
		contract := NewContract(caller, AccountRef(caller.Address()), value, gas)
		contract.SetCallCode(&addrCopy, ovm.Operationdb.GetCodeHash(addrCopy), ovm.Operationdb.GetCode(addrCopy))
		ret, err = ovm.interpreter.Run(contract, input, false)
		gas = contract.Gas
	}
	if err != nil {
		ovm.Operationdb.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
			gas = 0
		}
	}
	return ret, gas, err
}

// DelegateCall使用给定的输入作为参数来执行与addr关联的约定。它在执行错误时反转状态。
//DelegateCall与CallCode的不同之处在于，它以调用者为上下文执行给定的地址代码，并且调用者被设置为调用者的调用者。
func (ovm *OVM) DelegateCall(caller ContractRef, addr entity.Address, input []byte, gas uint64) (ret []byte, leftOverGas uint64, err error) {
	//如果我们试图在调用深度限制以上执行，则失败
	if ovm.depth > int(entity.CallCreateDepth) {
		return nil, gas, ErrDepth
	}
	var snapshot = ovm.Operationdb.Snapshot()

	// 调用跟踪钩子，该钩子发出进入/退出调用帧的信号
	//if ovm.Config.Debug {
	//	ovm.Config.Tracer.CaptureEnter(DELEGATECALL, caller.Address(), addr, input, gas, nil)
	//	defer func(startGas uint64) {
	//		ovm.Config.Tracer.CaptureExit(ret, startGas-gas, err)
	//	}(gas)
	//}

	// 允许调用预编译，即使是通过delegatecall
	if p, isPrecompile := ovm.precompile(addr); isPrecompile {
		ret, gas, err = RunPrecompiledContract(p, input, gas)
	} else {
		addrCopy := addr
		// 初始化新合同，并初始化代表值
		contract := NewContract(caller, AccountRef(caller.Address()), nil, gas).AsDelegate()
		contract.SetCallCode(&addrCopy, ovm.Operationdb.GetCodeHash(addrCopy), ovm.Operationdb.GetCode(addrCopy))
		ret, err = ovm.interpreter.Run(contract, input, false)
		gas = contract.Gas
	}
	if err != nil {
		ovm.Operationdb.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
			gas = 0
		}
	}
	return ret, gas, err
}

// 取消取消任何正在运行的EVM操作。这可以同时调用，多次调用是安全的。
func (ovm *OVM) Cancel() {
	atomic.StoreInt32(&ovm.abort, 1)
}

// 如果调用了Cancel，则Cancelled返回true
func (ovm *OVM) Cancelled() bool {
	return atomic.LoadInt32(&ovm.abort) == 1
}

func GetHashFn(ref *block2.Header, chain ChainContext) func(n uint64) entity.Hash {
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
	fmt.Println("add", db.GetOrNewOperationObject(recipient).Address().String())
}

// StaticCall使用给定的输入作为参数来执行与addr关联的契约，同时不允许在调用期间对状态进行任何修改。
//尝试执行此类修改的操作码将导致异常，而不是执行修改。
func (ovm *OVM) StaticCall(caller ContractRef, addr entity.Address, input []byte, gas uint64) (ret []byte, leftOverGas uint64, err error) {
	// 如果我们试图在调用深度限制以上执行，则失败
	if ovm.depth > int(entity.CallCreateDepth) {
		return nil, gas, ErrDepth
	}
	// 我们在这里拍一张快照。这有点违反直觉，可能可以跳过。
	//然而，即使是静态调用也被视为“触摸”。在mainnet上，静态调用是在删除所有空帐户后引入的，因此这不是必需的。
	//然而，如果我们忽略了这一点，那么某些测试就会失败；
	//stRevertTest/RevertPrecompiledTouchExactOOG。
	//json。我们可以改变这一点，但现在这是遗留下来的原因
	var snapshot = ovm.Operationdb.Snapshot()

	// 我们在这里做一个零的AddBalance，只是为了触发一次触摸。
	//这在拜占庭时代的主网上无关紧要，在其他网络、测试和潜在的未来场景中，这是正确的做法，也很重要
	ovm.Operationdb.AddBalance(addr, operationutils.Big0)

	// 调用跟踪钩子，该钩子发出进入/退出调用帧的信号
	//if ovm.Config.Debug {
	//	ovm.Config.Tracer.CaptureEnter(STATICCALL, caller.Address(), addr, input, gas, nil)
	//	defer func(startGas uint64) {
	//		ovm.Config.Tracer.CaptureExit(ret, startGas-gas, err)
	//	}(gas)
	//}

	if p, isPrecompile := ovm.precompile(addr); isPrecompile {
		ret, gas, err = RunPrecompiledContract(p, input, gas)
	} else {
		// 此时，我们使用地址的副本。如果我们不这样做，go编译器将把“contract”泄漏到外部范围，
		//并为“contract”进行分配，即使实际执行在上面的RunPrecompiled上结束。
		addrCopy := addr
		// 初始化新合同并设置EVM使用的代码。合同仅是此执行上下文的作用域环境。
		contract := NewContract(caller, AccountRef(addrCopy), new(big.Int), gas)
		contract.SetCallCode(&addrCopy, ovm.Operationdb.GetCodeHash(addrCopy), ovm.Operationdb.GetCode(addrCopy))
		// 当EVM返回错误或在上面设置创建代码时，我们返回快照并消耗剩余的气体。
		//此外，当我们在Homestead时，这也会计入代码存储错误。
		ret, err = ovm.interpreter.Run(contract, input, true)
		gas = contract.Gas
	}
	if err != nil {
		ovm.Operationdb.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
			gas = 0
		}
	}
	return ret, gas, err
}

//预编译
func (ovm *OVM) precompile(addr entity.Address) (PrecompiledContract, bool) {
	var precompiles map[entity.Address]PrecompiledContract
	precompiles = PrecompiledContractsHomestead
	//switch {
	//case ovm.chainRules.IsBerlin:
	//	precompiles = PrecompiledContractsBerlin
	//case ovm.chainRules.IsIstanbul:
	//	precompiles = PrecompiledContractsIstanbul
	//case ovm.chainRules.IsByzantium:
	//	precompiles = PrecompiledContractsByzantium
	//default:
	//	precompiles = PrecompiledContractsHomestead
	//}
	p, ok := precompiles[addr]
	return p, ok
}
