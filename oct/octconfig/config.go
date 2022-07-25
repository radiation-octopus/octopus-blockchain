package octconfig

import (
	"github.com/radiation-octopus/octopus-blockchain/blockchain/blockchainconfig"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/entity/genesis"
	"github.com/radiation-octopus/octopus-blockchain/oct/downloader"
	"github.com/radiation-octopus/octopus-blockchain/params"
)

type Config struct {
	//genesis块，如果数据库为空，则插入该块。
	//如果为零，则使用以太坊主网络块。
	Genesis *genesis.Genesis `toml:",omitempty"`

	NetworkId uint64 // 用于选择要连接到的对等方的网络ID

	SyncMode downloader.SyncMode

	//将为要连接的节点查询这些URL。
	EthDiscoveryURLs  []string
	SnapDiscoveryURLs []string

	DatabaseHandles int `toml:"-"`
	DatabaseCache   int
	DatabaseFreezer string

	//事务池选项
	TxPool blockchainconfig.TxPoolConfig

	// 支持在虚拟机中跟踪SHA3前映像
	EnablePreimageRecording bool

	Miner blockchainconfig.Config

	// RPCTxFeeCap是发送交易变体的全局交易费（价格*gaslimit）上限。单位为oct。
	RPCTxFeeCap float64

	// 检查点是一个硬编码的检查点，可以为零。
	Checkpoint *params.TrustedCheckpoint `toml:",omitempty"`

	RequiredBlocks map[uint64]entity.Hash `toml:"-"`
}
