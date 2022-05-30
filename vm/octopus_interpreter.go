package vm

import (
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/operationUtils"
)

type Config struct {
	Debug bool // 启用调试
	//Tracer                  EVMLogger // 操作码日志
	NoBaseFee               bool // 强制将费用设置为0
	EnablePreimageRecording bool // 是否运行录制sha3前录像

	JumpTable *JumpTable // 虚拟机指令表，未设置自动填充

	ExtraEips []int // 其他eip
}

//OVM解释器
type OVMInterpreter struct {
	ovm *OVM
	cfg Config

	//hasher    keccakState // Keccak256 实例跨操作码共享
	hasherBuf operationUtils.Hash // Keccak256 hasher 结果数组共享aross操作码

	readOnly   bool   //是否只读
	returnData []byte // 最后一次调用的返回数据，以供后续重用
}

type ScopeContext struct {
	Memory   *Memory
	Stack    *Stack
	Contract *Contract
}

func NewOVMInterpreter(ovm *OVM, cfg Config) *OVMInterpreter {
	// If jump table was not initialised we set the default one.
	//if cfg.JumpTable == nil {
	//	switch {
	//	case evm.chainRules.IsMerge:
	//		cfg.JumpTable = &mergeInstructionSet
	//	case evm.chainRules.IsLondon:
	//		cfg.JumpTable = &londonInstructionSet
	//	case evm.chainRules.IsBerlin:
	//		cfg.JumpTable = &berlinInstructionSet
	//	case evm.chainRules.IsIstanbul:
	//		cfg.JumpTable = &istanbulInstructionSet
	//	case evm.chainRules.IsConstantinople:
	//		cfg.JumpTable = &constantinopleInstructionSet
	//	case evm.chainRules.IsByzantium:
	//		cfg.JumpTable = &byzantiumInstructionSet
	//	case evm.chainRules.IsEIP158:
	//		cfg.JumpTable = &spuriousDragonInstructionSet
	//	case evm.chainRules.IsEIP150:
	//		cfg.JumpTable = &tangerineWhistleInstructionSet
	//	case evm.chainRules.IsHomestead:
	//		cfg.JumpTable = &homesteadInstructionSet
	//	default:
	//		cfg.JumpTable = &frontierInstructionSet
	//	}
	//	for i, eip := range cfg.ExtraEips {
	//		copy := *cfg.JumpTable
	//		if err := EnableEIP(eip, &copy); err != nil {
	//			// Disable it, so caller can check if it's activated or not
	//			cfg.ExtraEips = append(cfg.ExtraEips[:i], cfg.ExtraEips[i+1:]...)
	//			log.Error("EIP activation failed", "eip", eip, "error", err)
	//		}
	//		cfg.JumpTable = &copy
	//	}
	//}

	return &OVMInterpreter{
		ovm: ovm,
		cfg: cfg,
	}
}

func (in *OVMInterpreter) Run(contract *Contract, input []byte, readOnly bool) (ret []byte, err error) {
	//增加限制为1024的呼叫深度
	in.ovm.depth++
	defer func() { in.ovm.depth-- }()
	//如果我们还没有处于只读状态，请确保只读设置为only。
	if readOnly && !in.readOnly {
		in.readOnly = true
		defer func() { in.readOnly = false }()
	}
	//清空返回值缓存
	in.returnData = nil

	// 判断是否存在代码
	if len(contract.Code) == 0 {
		return nil, nil
	}

	var (
		op          operationUtils.OpCode // current opcode
		mem         = NewMemory()         //绑定内存
		stack       = newstack()          // 本地堆栈
		callContext = &ScopeContext{
			Memory:   mem,
			Stack:    stack,
			Contract: contract,
		}
		// 使用unit64作为程序计数器，
		pc   = uint64(0) // 程序计数器
		cost uint64
		// 计数器追踪副本
		//pcCopy  uint64 // 延迟虚拟机需要
		//gasCopy uint64 // 虚拟机日志在执行前记录剩余gas
		//logged  bool   // 延迟的虚拟机日志应忽略已记录步骤
		res []byte // 操作码执行功能的结果
	)

	//最后关闭堆栈
	//defer func() {
	//	returnStack(stack)
	//}()

	contract.Input = input

	//解释器主运行循环，直至执行显示停止，返回或者销毁
	for {
		// 从操作表获取操作并验证堆栈，以确保有最足够的堆栈可用于执行该操作
		op = contract.GetOp(pc)
		operation := in.cfg.JumpTable[op]
		cost = operation.constantGas // 用于跟踪
		// 验证堆栈
		//if sLen := stack.len(); sLen < operation.minStack {
		//	return nil, &ErrStackUnderflow{stackLen: sLen, required: operation.minStack}
		//} else if sLen > operation.maxStack {
		//	return nil, &ErrStackOverflow{stackLen: sLen, limit: operation.maxStack}
		//}
		if !contract.UseGas(cost) {
			return nil, errors.New("gas用完")
		}
		//if operation.dynamicGas != nil {
		//	//所有具有动态内存使用率的操作也会有动态gas成本
		//	var memorySize uint64
		//	// 计算新的内存大小，并扩展内存以适应在评估动态gas部分之前需要进行的操作内存检查，
		//	//检测计算溢出
		//	if operation.memorySize != nil {
		//		memSize, overflow := operation.memorySize(stack)
		//		if overflow {
		//			return nil, ErrGasUintOverflow
		//		}
		//		// 内存扩展为32字节的字。gas也以文字计算。
		//		if memorySize, overflow = math.SafeMul(toWordSize(memSize), 32); overflow {
		//			return nil, ErrGasUintOverflow
		//		}
		//	}
		//	//消耗气体，如果没有足够的气体，则返回错误。显式设置成本，以便捕获状态延迟方法可以获得适当的成本
		//	var dynamicCost uint64
		//	dynamicCost, err = operation.dynamicGas(in.evm, contract, stack, mem, memorySize)
		//	cost += dynamicCost // for tracing
		//	if err != nil || !contract.UseGas(dynamicCost) {
		//		return nil, ErrOutOfGas
		//	}
		//	if memorySize > 0 {
		//		mem.Resize(memorySize)
		//	}
		//}
		//if in.cfg.Debug {
		//	in.cfg.Tracer.CaptureState(pc, op, gasCopy, cost, callContext, in.returnData, in.evm.depth, err)
		//	logged = true
		//}
		// 执行操作码
		res, err = operation.execute(&pc, in, callContext)
		if err != nil {
			break
		}
		pc++
	}

	return res, err
}
