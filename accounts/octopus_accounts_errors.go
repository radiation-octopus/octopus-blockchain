package accounts

import (
	"errors"
	"fmt"
)

//对于没有后端提供指定帐户的任何请求的操作，都会返回ErrUnknownAccount。
var ErrUnknownAccount = errors.New("unknown account")

// 对于没有后端提供指定钱包的任何请求操作，都会返回ErrUnknownWallet。
var ErrUnknownWallet = errors.New("unknown wallet")

// 当从不支持的帐户后端请求操作时，将返回ErrNotSupported。
var ErrNotSupported = errors.New("not supported")

// 当解密操作接收到错误的密码短语时，将返回errInvalidPassphase。
var ErrInvalidPassphrase = errors.New("invalid password")

// 如果第二次尝试打开钱包，则返回ErrWalletAlreadyOpen。
var ErrWalletAlreadyOpen = errors.New("wallet already open")

// 如果钱包脱机，则返回ErrWalletClosed。
var ErrWalletClosed = errors.New("wallet closed")

// AuthNeededError由签名请求的后端返回，其中要求用户在签名成功之前提供进一步的身份验证。
//这通常意味着要么需要提供密码，要么可能是某个硬件设备显示的一次性PIN码。
type AuthNeededError struct {
	Needed string // 用户需要提供的额外身份验证
}

// NewAuthNeededError创建一个新的身份验证错误，其中包含有关所需字段集的额外详细信息。
func NewAuthNeededError(needed string) error {
	return &AuthNeededError{
		Needed: needed,
	}
}

// Error实现标准错误接口。
func (err *AuthNeededError) Error() string {
	return fmt.Sprintf("authentication needed: %s", err.Needed)
}
