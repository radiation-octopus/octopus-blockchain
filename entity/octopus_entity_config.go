package entity

import "math/big"

type ChainConfig struct {
	ChainID *big.Int `json:"chainId"` // chainId标识当前链并用于重播保护

	HomesteadBlock *big.Int `json:"homesteadBlock,omitempty"` // 宅地开关块（无=无叉，0=已宅地）

	DAOForkBlock   *big.Int `json:"daoForkBlock,omitempty"`   // DAO硬拨叉开关块（无=无拨叉）
	DAOForkSupport bool     `json:"daoForkSupport,omitempty"` // 节点是否支持或反对刀硬叉

	// EIP150实现gas价格变化
	EIP150Block *big.Int `json:"eip150Block,omitempty"` // EIP150 HF块（无=无叉）
	EIP150Hash  Hash     `json:"eip150Hash,omitempty"`  // EIP150 HF散列（仅表头客户端需要，因为仅gas定价已更改）

	EIP155Block *big.Int `json:"eip155Block,omitempty"` // EIP155 HF块
	EIP158Block *big.Int `json:"eip158Block,omitempty"` // EIP158 HF块

	ByzantiumBlock      *big.Int `json:"byzantiumBlock,omitempty"`      // 拜占庭开关块（nil=无分叉，0=已在拜占庭上）
	ConstantinopleBlock *big.Int `json:"constantinopleBlock,omitempty"` // 君士坦丁堡开关块（nil=无叉，0=已激活）
	PetersburgBlock     *big.Int `json:"petersburgBlock,omitempty"`     // 彼得堡开关柜（无=与君士坦丁堡相同）
	IstanbulBlock       *big.Int `json:"istanbulBlock,omitempty"`       // 伊斯坦布尔开关块（无=无分叉，0=已在伊斯坦布尔）
	MuirGlacierBlock    *big.Int `json:"muirGlacierBlock,omitempty"`    // Eip-2384（炸弹延迟）开关块（无=无分叉，0=已激活）
	BerlinBlock         *big.Int `json:"berlinBlock,omitempty"`         // Berlin开关块（无=无叉，0=已在Berlin上）
	LondonBlock         *big.Int `json:"londonBlock,omitempty"`         // 伦敦道岔闭塞（无=无分叉，0=已在伦敦）
	ArrowGlacierBlock   *big.Int `json:"arrowGlacierBlock,omitempty"`   // Eip-4345（炸弹延迟）开关块（无=无分叉，0=已激活）
	MergeNetsplitBlock  *big.Int `json:"mergeNetsplitBlock,omitempty"`  // 合并后用作网络拆分器的虚拟分叉

	// TerminalTotalDifficity是触发一致升级的网络达到的总难度。
	TerminalTotalDifficulty *big.Int `json:"terminalTotalDifficulty,omitempty"`

	// 各种共识引擎
	Engine string `json:"ethash,omitempty"`
	//Clique *CliqueConfig `json:"clique,omitempty"`
}

//isForked返回在块s上调度的fork是否在给定的头块上处于活动状态。
func isForked(s, head *big.Int) bool {
	if s == nil || head == nil {
		return false
	}
	return s.Cmp(head) <= 0
}

// IsEIP158返回num是否等于EIP158 fork块或更大。
func (c *ChainConfig) IsEIP158(num *big.Int) bool {
	return isForked(c.EIP158Block, num)
}

// IsArrowGlacier返回num是否等于Arrow Glacier（EIP-4345）分叉块或更大。
func (c *ChainConfig) IsArrowGlacier(num *big.Int) bool {
	return isForked(c.ArrowGlacierBlock, num)
}

// IsLondon返回num是否等于或大于London fork块。
func (c *ChainConfig) IsLondon(num *big.Int) bool {
	return isForked(c.LondonBlock, num)
}

// IsMuirGlacier返回num是否等于Muir Glacier（EIP-2384）分叉块或更大。
func (c *ChainConfig) IsMuirGlacier(num *big.Int) bool {
	return isForked(c.MuirGlacierBlock, num)
}

//IsConstantinople返回num是否等于或大于君士坦丁堡叉块。
func (c *ChainConfig) IsConstantinople(num *big.Int) bool {
	return isForked(c.ConstantinopleBlock, num)
}

// IsByzantium返回num是否等于或大于Byzantium fork块。
func (c *ChainConfig) IsByzantium(num *big.Int) bool {
	return isForked(c.ByzantiumBlock, num)
}

//IsHomestead返回num是否等于homestead块或更大。
func (c *ChainConfig) IsHomestead(num *big.Int) bool {
	return isForked(c.HomesteadBlock, num)
}