package consensus

import "errors"

var (

	// 当验证块需要未知的祖先时，将返回ErrUnknownAncestor。
	ErrUnknownAncestor = errors.New("unknown ancestor")
	// 根据当前节点，当块的时间戳在未来时，将返回ErrFutureBlock。
	ErrFutureBlock = errors.New("block in the future")
	// 如果块的编号不等于其父块的编号加1，则返回ErrInvalidNumber。
	ErrInvalidNumber = errors.New("invalid block number")
	//当验证块需要已知但状态不可用的祖先时，返回ErrPrunedAncestor。
	ErrPrunedAncestor = errors.New("pruned ancestor")
	// 如果块wrt无效，则返回ErrInvalidTerminalBlock。终端总难度。
	ErrInvalidTerminalBlock = errors.New("invalid terminal block")
)
