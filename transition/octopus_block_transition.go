package transition

import (
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"math"
	"math/big"

	"github.com/radiation-octopus/octopus-blockchain/vm"
)

/**
gaspool
*/
// GasPool跟踪交易执行期间的可用气体量
type GasPool uint64

//gas可用于执行
func (gp *GasPool) AddGas(amount uint64) *GasPool {
	if uint64(*gp) > math.MaxUint64-amount {
		panic("gas pool pushed above uint64")
	}
	*(*uint64)(gp) += amount
	return gp
}

func (gp *GasPool) SubGas(amount uint64) error {
	if uint64(*gp) < amount {
		return errors.New("gas 达到上限")
	}
	*(*uint64)(gp) -= amount
	return nil
}

// gas返回池中剩余的gas量。
func (gp *GasPool) Gas() uint64 {
	return uint64(*gp)
}

//交易过渡结构体
type StateTransition struct {
	gp         *GasPool
	msg        block.Message
	gas        uint64
	gasPrice   *big.Int
	gasFeeCap  *big.Int
	gasTipCap  *big.Int
	initialGas uint64
	value      *big.Int
	data       []byte
	state      vm.StateDB
	ovm        *vm.OVM
}

//执行结果返回体
type ExecutionResult struct {
	UsedGas    uint64 // 总gas，包括退还gas
	Err        error  // 执行过程中遇到的任何错误
	ReturnData []byte // 从evm返回的数据
}

//交易过渡结构创建
func NewTransition(msg block.Message, gp *GasPool) *StateTransition {
	return &StateTransition{
		gp: gp,
		//evm:       evm,
		msg:       msg,
		gasPrice:  msg.GasPrice(),
		gasFeeCap: msg.GasFeeCap(),
		gasTipCap: msg.GasTipCap(),
		value:     msg.Value(),
		data:      msg.Data(),
		//state:     evm.StateDB,
	}
}

//请求虚拟机处理，返回gas费用，账单
func ApplyMessage(msg block.Message, gp *GasPool) (*ExecutionResult, error) {
	return NewTransition(msg, gp).TransitionDb()
}

//当前核心消息处理并返回结果
//1.使用的gas总量
//2.evm返回的数据
//3.错误信息
func (st *StateTransition) TransitionDb() (*ExecutionResult, error) {

	//1。消息调用方的nonce正确
	//2。呼叫者有足够的余额支付交易费（gaslimit*gasprice）
	//3。区块内有可用的所需gas
	//4。购买的gas足以满足内在使用
	//5。计算gas时没有溢出
	//6。呼叫方有足够的余额支付**最顶层**呼叫的资产转移
	var (
		msg    = st.msg
		sender = vm.AccountRef(msg.From())
		//rules            = st.evm.ChainConfig().Rules(st.evm.Context.BlockNumber, st.evm.Context.Random != nil)
		contractCreation = msg.To() == nil
	)

	//检查第1-3条，如果一切正常，购买gas
	if err := st.preCheck(); err != nil {
		return nil, err
	}
	// 检查第4-5条，如果一切正常，减去固有气体
	gas, err := IntrinsicGas(st.data)

	if err != nil {
		return nil, err
	}
	if st.gas < gas {
		return nil, fmt.Errorf("%w: have %d, want %d", errors.New("intrinsic gas too low"), st.gas, gas)
	}
	st.gas -= gas
	// Check clause 6
	//if msg.Value().Sign() > 0 && !st.evm.Context.CanTransfer(st.state, msg.From(), msg.Value()) {
	//	return nil, fmt.Errorf("%w: address %v", ErrInsufficientFundsForTransfer, msg.From().Hex())
	//}
	var (
		ret   []byte
		vmerr error // vm errors do not effect consensus and are therefore not assigned to err
	)

	if contractCreation { //创建合约
		//ret, _, st.gas, vmerr = st.evm.Create(sender, st.data, st.gas, st.value)
	} else { //事件
		// 设置nonce
		st.state.SetNonce(msg.From(), st.state.GetNonce(sender.Address())+1)
		//调用合约
		ret, st.gas, vmerr = st.ovm.Call(sender, st.to(), st.data, st.gas, st.value)
	}

	return &ExecutionResult{
		UsedGas:    st.gasUsed(),
		Err:        vmerr,
		ReturnData: ret,
	}, nil
}

func (st *StateTransition) preCheck() error {
	return st.buyGas()
}

// 返回接收者
func (st *StateTransition) to() entity.Address {
	if st.msg.To() == nil /* contract creation */ {
		return entity.Address{}
	}
	return *st.msg.To()
}

func (st *StateTransition) buyGas() error {
	mgval := new(big.Int).SetUint64(st.msg.Gas())
	mgval = mgval.Mul(mgval, st.gasPrice)
	balanceCheck := mgval
	if st.gasFeeCap != nil {
		balanceCheck = new(big.Int).SetUint64(st.msg.Gas())
		balanceCheck = balanceCheck.Mul(balanceCheck, st.gasFeeCap)
		balanceCheck.Add(balanceCheck, st.value) //所需余额
	}
	//if have, want := st.state.GetBalance(st.msg.From()), balanceCheck; have.Cmp(want) < 0 {
	//	return fmt.Errorf("%w: address %v have %v want %v", ErrInsufficientFunds, st.msg.From().Hex(), have, want)
	//}
	if err := st.gp.SubGas(st.msg.Gas()); err != nil {
		return err
	}
	st.gas += st.msg.Gas()

	st.initialGas = st.msg.Gas()
	//st.state.SubBalance(st.msg.From(), mgval)
	return nil
}

// 计算给定数据的gas费用
func IntrinsicGas(data []byte) (uint64, error) {
	// Set the starting gas for the raw transaction
	var gas uint64
	gas = entity.TxGas
	//if isContractCreation && isHomestead {
	//	gas = blockchain.TxGasContractCreation
	//} else {
	//	gas = blockchain.TxGas
	//}
	// 根据事务数据量增加所需的流量
	if len(data) > 0 {
		// 零字节和非零字节的定价不同
		var nz uint64
		for _, byt := range data {
			if byt != 0 {
				nz++
			}
		}
		// 确保所有数据组合不超过uint64
		nonZeroGas := entity.TxDataNonZeroGasFrontier
		//if isEIP2028 {
		//	nonZeroGas = blockchain.TxDataNonZeroGasEIP2028
		//}
		if (math.MaxUint64-gas)/nonZeroGas < nz {
			return 0, errors.New("gas uint64 overflow")
		}
		gas += nz * nonZeroGas

		z := uint64(len(data)) - nz
		if (math.MaxUint64-gas)/entity.TxDataZeroGas < z {
			return 0, errors.New("gas uint64 overflow")
		}
		gas += z * entity.TxDataZeroGas
	}
	return gas, nil
}

//gasUsed返回状态转换使用的gas量。
func (st *StateTransition) gasUsed() uint64 {
	return st.initialGas - st.gas
}
