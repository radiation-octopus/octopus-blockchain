package blockchainconfig

import (
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"math/big"
	"time"
)

// 工作的配置参数
type Config struct {
	Octerbase  entity.Address `toml:",omitempty"` // 区块开采奖励的公共广播
	Notify     []string       `toml:",omitempty"` // 要通知新工作包http url列表
	NotifyFull bool           `toml:",omitempty"` // 使用挂起的块标题
	ExtraData  entity.Bytes   `toml:",omitempty"` // 阻止工作者的额外数据
	GasFloor   uint64         // 工作区块的目标gas底线
	GasCeil    uint64         // 工作区块的目标gas上限
	GasPrice   *big.Int       // 工作交易的最低gas价格
	Recommit   time.Duration  // 工作者重新工作的时间间隔
	Noverify   bool           // 禁止远程工作
}
