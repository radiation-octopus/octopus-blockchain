package vm

import (
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/entity/math"
)

// memoryGasCost calculates the quadratic gas for memory expansion. It does so
// only for the memory region that is expanded, not the total memory.
func memoryGasCost(mem *Memory, newMemSize uint64) (uint64, error) {
	if newMemSize == 0 {
		return 0, nil
	}
	// The maximum that will fit in a uint64 is max_word_count - 1. Anything above
	// that will result in an overflow. Additionally, a newMemSize which results in
	// a newMemSizeWords larger than 0xFFFFFFFF will cause the square operation to
	// overflow. The constant 0x1FFFFFFFE0 is the highest number that can be used
	// without overflowing the gas calculation.
	if newMemSize > 0x1FFFFFFFE0 {
		return 0, ErrGasUintOverflow
	}
	newMemSizeWords := toWordSize(newMemSize)
	newMemSize = newMemSizeWords * 32

	if newMemSize > uint64(mem.Len()) {
		square := newMemSizeWords * newMemSizeWords
		linCoef := newMemSizeWords * entity.MemoryGas
		quadCoef := square / entity.QuadCoeffDiv
		newTotalFee := linCoef + quadCoef

		fee := newTotalFee - mem.lastGasCost
		mem.lastGasCost = newTotalFee

		return fee, nil
	}
	return 0, nil
}

// memoryCopierGas creates the gas functions for the following opcodes, and takes
// the stack position of the operand which determines the size of the data to copy
// as argument:
// CALLDATACOPY (stack position 2)
// CODECOPY (stack position 2)
// EXTCODECOPY (stack position 3)
// RETURNDATACOPY (stack position 2)
func memoryCopierGas(stackpos int) gasFunc {
	return func(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
		// Gas for expanding the memory
		gas, err := memoryGasCost(mem, memorySize)
		if err != nil {
			return 0, err
		}
		// And gas for copying data, charged per word at param.CopyGas
		words, overflow := stack.Back(stackpos).Uint64WithOverflow()
		if overflow {
			return 0, ErrGasUintOverflow
		}

		if words, overflow = math.SafeMul(toWordSize(words), entity.CopyGas); overflow {
			return 0, ErrGasUintOverflow
		}

		if gas, overflow = math.SafeAdd(gas, words); overflow {
			return 0, ErrGasUintOverflow
		}
		return gas, nil
	}
}

var (
	gasCallDataCopy   = memoryCopierGas(2)
	gasCodeCopy       = memoryCopierGas(2)
	gasExtCodeCopy    = memoryCopierGas(3)
	gasReturnDataCopy = memoryCopierGas(2)
)

func gasSStore(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	var (
		y, x    = stack.Back(1), stack.Back(0)
		current = ovm.Operationdb.GetState(contract.Address(), x.Bytes32())
	)
	// The legacy gas metering only takes into consideration the current state
	// Legacy rules should be applied if we are in Petersburg (removal of EIP-1283)
	// OR Constantinople is not active
	if ovm.chainRules.IsPetersburg || !ovm.chainRules.IsConstantinople {
		// This checks for 3 scenario's and calculates gas accordingly:
		//
		// 1. From a zero-value address to a non-zero value         (NEW VALUE)
		// 2. From a non-zero value address to a zero-value address (DELETE)
		// 3. From a non-zero to a non-zero                         (CHANGE)
		switch {
		case current == (entity.Hash{}) && y.Sign() != 0: // 0 => non 0
			return entity.SstoreSetGas, nil
		case current != (entity.Hash{}) && y.Sign() == 0: // non 0 => 0
			ovm.Operationdb.AddRefund(entity.SstoreRefundGas)
			return entity.SstoreClearGas, nil
		default: // non 0 => non 0 (or 0 => 0)
			return entity.SstoreResetGas, nil
		}
	}
	// The new gas metering is based on net gas costs (EIP-1283):
	//
	// 1. If current value equals new value (this is a no-op), 200 gas is deducted.
	// 2. If current value does not equal new value
	//   2.1. If original value equals current value (this storage slot has not been changed by the current execution context)
	//     2.1.1. If original value is 0, 20000 gas is deducted.
	// 	   2.1.2. Otherwise, 5000 gas is deducted. If new value is 0, add 15000 gas to refund counter.
	// 	2.2. If original value does not equal current value (this storage slot is dirty), 200 gas is deducted. Apply both of the following clauses.
	// 	  2.2.1. If original value is not 0
	//       2.2.1.1. If current value is 0 (also means that new value is not 0), remove 15000 gas from refund counter. We can prove that refund counter will never go below 0.
	//       2.2.1.2. If new value is 0 (also means that current value is not 0), add 15000 gas to refund counter.
	// 	  2.2.2. If original value equals new value (this storage slot is reset)
	//       2.2.2.1. If original value is 0, add 19800 gas to refund counter.
	// 	     2.2.2.2. Otherwise, add 4800 gas to refund counter.
	value := entity.Hash(y.Bytes32())
	if current == value { // noop (1)
		return entity.NetSstoreNoopGas, nil
	}
	original := ovm.Operationdb.GetCommittedState(contract.Address(), x.Bytes32())
	if original == current {
		if original == (entity.Hash{}) { // create slot (2.1.1)
			return entity.NetSstoreInitGas, nil
		}
		if value == (entity.Hash{}) { // delete slot (2.1.2b)
			ovm.Operationdb.AddRefund(entity.NetSstoreClearRefund)
		}
		return entity.NetSstoreCleanGas, nil // write existing slot (2.1.2)
	}
	if original != (entity.Hash{}) {
		if current == (entity.Hash{}) { // recreate slot (2.2.1.1)
			ovm.Operationdb.SubRefund(entity.NetSstoreClearRefund)
		} else if value == (entity.Hash{}) { // delete slot (2.2.1.2)
			ovm.Operationdb.AddRefund(entity.NetSstoreClearRefund)
		}
	}
	if original == value {
		if original == (entity.Hash{}) { // reset to original inexistent slot (2.2.2.1)
			ovm.Operationdb.AddRefund(entity.NetSstoreResetClearRefund)
		} else { // reset to original existing slot (2.2.2.2)
			ovm.Operationdb.AddRefund(entity.NetSstoreResetRefund)
		}
	}
	return entity.NetSstoreDirtyGas, nil
}

// 0. If *gasleft* is less than or equal to 2300, fail the current call.
// 1. If current value equals new value (this is a no-op), SLOAD_GAS is deducted.
// 2. If current value does not equal new value:
//   2.1. If original value equals current value (this storage slot has not been changed by the current execution context):
//     2.1.1. If original value is 0, SSTORE_SET_GAS (20K) gas is deducted.
//     2.1.2. Otherwise, SSTORE_RESET_GAS gas is deducted. If new value is 0, add SSTORE_CLEARS_SCHEDULE to refund counter.
//   2.2. If original value does not equal current value (this storage slot is dirty), SLOAD_GAS gas is deducted. Apply both of the following clauses:
//     2.2.1. If original value is not 0:
//       2.2.1.1. If current value is 0 (also means that new value is not 0), subtract SSTORE_CLEARS_SCHEDULE gas from refund counter.
//       2.2.1.2. If new value is 0 (also means that current value is not 0), add SSTORE_CLEARS_SCHEDULE gas to refund counter.
//     2.2.2. If original value equals new value (this storage slot is reset):
//       2.2.2.1. If original value is 0, add SSTORE_SET_GAS - SLOAD_GAS to refund counter.
//       2.2.2.2. Otherwise, add SSTORE_RESET_GAS - SLOAD_GAS gas to refund counter.
func gasSStoreEIP2200(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	// If we fail the minimum gas availability invariant, fail (0)
	if contract.Gas <= entity.SstoreSentryGasEIP2200 {
		return 0, errors.New("not enough gas for reentrancy sentry")
	}
	// Gas sentry honoured, do the actual gas calculation based on the stored value
	var (
		y, x    = stack.Back(1), stack.Back(0)
		current = ovm.Operationdb.GetState(contract.Address(), x.Bytes32())
	)
	value := entity.Hash(y.Bytes32())

	if current == value { // noop (1)
		return entity.SloadGasEIP2200, nil
	}
	original := ovm.Operationdb.GetCommittedState(contract.Address(), x.Bytes32())
	if original == current {
		if original == (entity.Hash{}) { // create slot (2.1.1)
			return entity.SstoreSetGasEIP2200, nil
		}
		if value == (entity.Hash{}) { // delete slot (2.1.2b)
			ovm.Operationdb.AddRefund(entity.SstoreClearsScheduleRefundEIP2200)
		}
		return entity.SstoreResetGasEIP2200, nil // write existing slot (2.1.2)
	}
	if original != (entity.Hash{}) {
		if current == (entity.Hash{}) { // recreate slot (2.2.1.1)
			ovm.Operationdb.SubRefund(entity.SstoreClearsScheduleRefundEIP2200)
		} else if value == (entity.Hash{}) { // delete slot (2.2.1.2)
			ovm.Operationdb.AddRefund(entity.SstoreClearsScheduleRefundEIP2200)
		}
	}
	if original == value {
		if original == (entity.Hash{}) { // reset to original inexistent slot (2.2.2.1)
			ovm.Operationdb.AddRefund(entity.SstoreSetGasEIP2200 - entity.SloadGasEIP2200)
		} else { // reset to original existing slot (2.2.2.2)
			ovm.Operationdb.AddRefund(entity.SstoreResetGasEIP2200 - entity.SloadGasEIP2200)
		}
	}
	return entity.SloadGasEIP2200, nil // dirty update (2.2)
}

func makeGasLog(n uint64) gasFunc {
	return func(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
		requestedSize, overflow := stack.Back(1).Uint64WithOverflow()
		if overflow {
			return 0, ErrGasUintOverflow
		}

		gas, err := memoryGasCost(mem, memorySize)
		if err != nil {
			return 0, err
		}

		if gas, overflow = math.SafeAdd(gas, entity.LogGas); overflow {
			return 0, ErrGasUintOverflow
		}
		if gas, overflow = math.SafeAdd(gas, n*entity.LogTopicGas); overflow {
			return 0, ErrGasUintOverflow
		}

		var memorySizeGas uint64
		if memorySizeGas, overflow = math.SafeMul(requestedSize, entity.LogDataGas); overflow {
			return 0, ErrGasUintOverflow
		}
		if gas, overflow = math.SafeAdd(gas, memorySizeGas); overflow {
			return 0, ErrGasUintOverflow
		}
		return gas, nil
	}
}

func gasKeccak256(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	gas, err := memoryGasCost(mem, memorySize)
	if err != nil {
		return 0, err
	}
	wordGas, overflow := stack.Back(1).Uint64WithOverflow()
	if overflow {
		return 0, ErrGasUintOverflow
	}
	if wordGas, overflow = math.SafeMul(toWordSize(wordGas), entity.Keccak256WordGas); overflow {
		return 0, ErrGasUintOverflow
	}
	if gas, overflow = math.SafeAdd(gas, wordGas); overflow {
		return 0, ErrGasUintOverflow
	}
	return gas, nil
}

// pureMemoryGascost is used by several operations, which aside from their
// static cost have a dynamic cost which is solely based on the memory
// expansion
func pureMemoryGascost(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	return memoryGasCost(mem, memorySize)
}

var (
	gasReturn  = pureMemoryGascost
	gasRevert  = pureMemoryGascost
	gasMLoad   = pureMemoryGascost
	gasMStore8 = pureMemoryGascost
	gasMStore  = pureMemoryGascost
	gasCreate  = pureMemoryGascost
)

func gasCreate2(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	gas, err := memoryGasCost(mem, memorySize)
	if err != nil {
		return 0, err
	}
	wordGas, overflow := stack.Back(2).Uint64WithOverflow()
	if overflow {
		return 0, ErrGasUintOverflow
	}
	if wordGas, overflow = math.SafeMul(toWordSize(wordGas), entity.Keccak256WordGas); overflow {
		return 0, ErrGasUintOverflow
	}
	if gas, overflow = math.SafeAdd(gas, wordGas); overflow {
		return 0, ErrGasUintOverflow
	}
	return gas, nil
}

func gasExpFrontier(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	expByteLen := uint64((stack.data[stack.len()-2].BitLen() + 7) / 8)

	var (
		gas      = expByteLen * entity.ExpByteFrontier // no overflow check required. Max is 256 * ExpByte gas
		overflow bool
	)
	if gas, overflow = math.SafeAdd(gas, entity.ExpGas); overflow {
		return 0, ErrGasUintOverflow
	}
	return gas, nil
}

func gasExpEIP158(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	expByteLen := uint64((stack.data[stack.len()-2].BitLen() + 7) / 8)

	var (
		gas      = expByteLen * entity.ExpByteEIP158 // no overflow check required. Max is 256 * ExpByte gas
		overflow bool
	)
	if gas, overflow = math.SafeAdd(gas, entity.ExpGas); overflow {
		return 0, ErrGasUintOverflow
	}
	return gas, nil
}

func gasCall(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	var (
		gas            uint64
		transfersValue = !stack.Back(2).IsZero()
		address        = entity.Address(stack.Back(1).Bytes20())
	)
	if ovm.chainRules.IsEIP158 {
		if transfersValue && ovm.Operationdb.Empty(address) {
			gas += entity.CallNewAccountGas
		}
	} else if !ovm.Operationdb.Exist(address) {
		gas += entity.CallNewAccountGas
	}
	if transfersValue {
		gas += entity.CallValueTransferGas
	}
	memoryGas, err := memoryGasCost(mem, memorySize)
	if err != nil {
		return 0, err
	}
	var overflow bool
	if gas, overflow = math.SafeAdd(gas, memoryGas); overflow {
		return 0, ErrGasUintOverflow
	}

	ovm.callGasTemp, err = callGas(ovm.chainRules.IsEIP150, contract.Gas, gas, stack.Back(0))
	if err != nil {
		return 0, err
	}
	if gas, overflow = math.SafeAdd(gas, ovm.callGasTemp); overflow {
		return 0, ErrGasUintOverflow
	}
	return gas, nil
}

func gasCallCode(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	memoryGas, err := memoryGasCost(mem, memorySize)
	if err != nil {
		return 0, err
	}
	var (
		gas      uint64
		overflow bool
	)
	if stack.Back(2).Sign() != 0 {
		gas += entity.CallValueTransferGas
	}
	if gas, overflow = math.SafeAdd(gas, memoryGas); overflow {
		return 0, ErrGasUintOverflow
	}
	ovm.callGasTemp, err = callGas(ovm.chainRules.IsEIP150, contract.Gas, gas, stack.Back(0))
	if err != nil {
		return 0, err
	}
	if gas, overflow = math.SafeAdd(gas, ovm.callGasTemp); overflow {
		return 0, ErrGasUintOverflow
	}
	return gas, nil
}

func gasDelegateCall(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	gas, err := memoryGasCost(mem, memorySize)
	if err != nil {
		return 0, err
	}
	ovm.callGasTemp, err = callGas(ovm.chainRules.IsEIP150, contract.Gas, gas, stack.Back(0))
	if err != nil {
		return 0, err
	}
	var overflow bool
	if gas, overflow = math.SafeAdd(gas, ovm.callGasTemp); overflow {
		return 0, ErrGasUintOverflow
	}
	return gas, nil
}

func gasStaticCall(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	gas, err := memoryGasCost(mem, memorySize)
	if err != nil {
		return 0, err
	}
	ovm.callGasTemp, err = callGas(ovm.chainRules.IsEIP150, contract.Gas, gas, stack.Back(0))
	if err != nil {
		return 0, err
	}
	var overflow bool
	if gas, overflow = math.SafeAdd(gas, ovm.callGasTemp); overflow {
		return 0, ErrGasUintOverflow
	}
	return gas, nil
}

func gasSelfdestruct(ovm *OVM, contract *Contract, stack *Stack, mem *Memory, memorySize uint64) (uint64, error) {
	var gas uint64
	// EIP150 homestead gas reprice fork:
	if ovm.chainRules.IsEIP150 {
		gas = entity.SelfdestructGasEIP150
		var address = entity.Address(stack.Back(0).Bytes20())

		if ovm.chainRules.IsEIP158 {
			// if empty and transfers value
			if ovm.Operationdb.Empty(address) && ovm.Operationdb.GetBalance(contract.Address()).Sign() != 0 {
				gas += entity.CreateBySelfdestructGas
			}
		} else if !ovm.Operationdb.Exist(address) {
			gas += entity.CreateBySelfdestructGas
		}
	}

	if !ovm.Operationdb.HasSuicided(contract.Address()) {
		ovm.Operationdb.AddRefund(entity.SelfdestructRefundGas)
	}
	return gas, nil
}
