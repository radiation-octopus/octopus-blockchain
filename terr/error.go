package terr

import (
	"errors"
	"syscall"
)

//evm呼叫消息预检查错误列表。所有状态转换消息将在执行前进行预检查。如果检测到任何无效，应返回此处定义的相应错误。
//-如果预检查发生在矿工身上，那么交易将不会打包。
//-如果预检查发生在块处理过程中，则应发出“坏块”错误。
var (
	// 如果事务的nonce低于本地链中的nonce，则返回ErrNonceTooLow。
	ErrNonceTooLow = errors.New("nonce too low")

	// 如果事务的nonce高于基于本地链的下一个预期值，则返回ErrNonceTooHigh。
	ErrNonceTooHigh = errors.New("nonce too high")

	// 如果事务发送方帐户的nonce具有允许的最大值，则返回ErrNonceMax，如果递增，则返回ErrNonceMax。
	ErrNonceMax = errors.New("nonce has max value")

	// 如果交易所需的gas高于区块中剩余的gas，则天然气池将返回ErrgasLimitReach。
	ErrGasLimitReached = errors.New("gas limit reached")

	// 如果交易发送方没有足够的资金进行转账，则返回ErrInsufficientFundsForTransfer（仅限最上面的调用）。
	ErrInsufficientFundsForTransfer = errors.New("insufficient funds for transfer")

	// 如果执行事务的总成本高于用户帐户的余额，则返回errInventFunds。
	ErrInsufficientFunds = errors.New("insufficient funds for gas * price + value")

	// 计算gas使用量时返回ErrGasUintOverflow。
	ErrGasUintOverflow = errors.New("gas uint64 overflow")

	// 如果指定事务使用的gas少于启动调用所需的gas，则返回ErrIntrinsicGas。
	ErrIntrinsicGas = errors.New("intrinsic gas too low")

	// 如果当前网络配置中不支持事务，则返回ErrTxTypeNotSupported。
	ErrTxTypeNotSupported = errors.New("transaction type not supported")

	// ErrTipAboveFeeCap是一个健全错误，用于确保任何人都无法指定小费高于总费用上限的交易。
	ErrTipAboveFeeCap = errors.New("max priority fee per gas higher than max fee per gas")

	// ErrTipVeryHigh是一个健全错误，用于避免在提示字段中指定过大的数字。
	ErrTipVeryHigh = errors.New("max priority fee per gas higher than 2^256-1")

	// ErrFeeCapVeryHigh是一种理智错误，用于避免在费用上限字段中指定非常大的数字。
	ErrFeeCapVeryHigh = errors.New("max fee per gas higher than 2^256-1")

	// 如果交易费用上限小于块的基本费用，则返回ErrFeeCapTooLow。
	ErrFeeCapTooLow = errors.New("max fee per gas less than block base fee")

	// 如果事务的发送方是合同，则返回ErrSenderNoEOA。
	ErrSenderNoEOA = errors.New("sender not an eoa")
)

/**
node
*/
var (
	ErrDatadirUsed    = errors.New("datadir already used by another process")
	ErrNodeStopped    = errors.New("node not started")
	ErrNodeRunning    = errors.New("node already running")
	ErrServiceUnknown = errors.New("unknown service")

	datadirInUseErrnos = map[uint]bool{11: true, 32: true, 35: true}
)

func ConvertFileLockError(err error) error {
	if errno, ok := err.(syscall.Errno); ok && datadirInUseErrnos[uint(errno)] {
		return ErrDatadirUsed
	}
	return err
}
