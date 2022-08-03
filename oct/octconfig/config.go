package octconfig

import (
	"github.com/radiation-octopus/octopus-blockchain/blockchain/blockchainconfig"
	"github.com/radiation-octopus/octopus-blockchain/consensus/octell"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/entity/genesis"
	"github.com/radiation-octopus/octopus-blockchain/oct/downloader"
	"github.com/radiation-octopus/octopus-blockchain/oct/gasprice"
	"github.com/radiation-octopus/octopus-blockchain/params"
	"math/big"
	"time"
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

	NoPruning  bool // 是否禁用修剪并将所有内容刷新到磁盘
	NoPrefetch bool // 是否禁用预取并仅按需加载状态

	TxLookupLimit uint64 `toml:",omitempty"` // 保留发送索引的头的最大块数。

	// RequiredBlocks是一组块数->哈希映射，必须位于所有远程对等点的规范链中。设置该选项使geth验证每个新对等连接中是否存在这些块。
	RequiredBlocks map[uint64]entity.Hash `toml:"-"`

	//轻型客户端选项
	LightServ          int  `toml:",omitempty"` // 服务LES请求所允许的最大时间百分比
	LightIngress       int  `toml:",omitempty"` // light服务器的传入带宽限制
	LightEgress        int  `toml:",omitempty"` // light服务器的传出带宽限制
	LightPeers         int  `toml:",omitempty"` // LES客户端对等点的最大数量
	LightNoPrune       bool `toml:",omitempty"` // 是否禁用轻链修剪
	LightNoSyncServe   bool `toml:",omitempty"` // 同步前是否为轻客户端提供服务
	SyncFromCheckpoint bool `toml:",omitempty"` // 是否从配置的检查点同步头链

	//超轻客户端选项
	UltraLightServers      []string `toml:",omitempty"` // 受信任的超轻型服务器列表
	UltraLightFraction     int      `toml:",omitempty"` // 接受公告的受信任服务器的百分比
	UltraLightOnlyAnnounce bool     `toml:",omitempty"` // 是只公布标题，还是同时提供标题

	// 数据库选项
	SkipBcVersionCheck bool `toml:"-"`
	DatabaseHandles    int  `toml:"-"`
	DatabaseCache      int
	DatabaseFreezer    string

	TrieCleanCache          int
	TrieCleanCacheJournal   string        `toml:",omitempty"` // trie缓存在节点重新启动后生存的磁盘日志目录
	TrieCleanCacheRejournal time.Duration `toml:",omitempty"` // 为干净缓存重新生成日志的时间间隔
	TrieDirtyCache          int
	TrieTimeout             time.Duration
	SnapshotCache           int
	Preimages               bool

	Miner blockchainconfig.Config

	// octell 属性
	Octell octell.Config

	//事务池选项
	TxPool blockchainconfig.TxPoolConfig

	// gas价格Oracle选项
	GPO gasprice.Config

	// 支持在虚拟机中跟踪SHA3前映像
	EnablePreimageRecording bool

	// 其他选项
	DocRoot string `toml:"-"`

	// RPCGasCap是oct呼叫变体的gas上限。
	RPCGasCap uint64

	// RPCEVMTimeout是eth调用的全局超时。
	RPCEVMTimeout time.Duration

	// RPCTxFeeCap是发送交易变体的全局交易费（价格*gaslimit）上限。单位为oct。
	RPCTxFeeCap float64

	// 检查点是一个硬编码的检查点，可以为零。
	Checkpoint *params.TrustedCheckpoint `toml:",omitempty"`

	// CheckpointOracle是检查点oracle的配置。
	CheckpointOracle *params.CheckpointOracleConfig `toml:",omitempty"`

	// Gray Glacier block override
	OverrideGrayGlacier *big.Int `toml:",omitempty"`

	// OverrideTerminalTotalDifficulty
	OverrideTerminalTotalDifficulty *big.Int `toml:",omitempty"`
}

// FullNodeGPO包含完整节点的默认oracle设置。
var FullNodeGPO = gasprice.Config{
	Blocks:           20,
	Percentile:       60,
	MaxHeaderHistory: 1024,
	MaxBlockHistory:  1024,
	MaxPrice:         gasprice.DefaultMaxPrice,
	IgnorePrice:      gasprice.DefaultIgnorePrice,
}

// LightClientGPO包含light client的默认oracle设置。
var LightClientGPO = gasprice.Config{
	Blocks:           2,
	Percentile:       60,
	MaxHeaderHistory: 300,
	MaxBlockHistory:  5,
	MaxPrice:         gasprice.DefaultMaxPrice,
	IgnorePrice:      gasprice.DefaultIgnorePrice,
}

// Defaults contains default settings for use on the Ethereum main net.
var Defaults = Config{
	SyncMode: downloader.SnapSync,
	Octell: octell.Config{
		CacheDir:         "octell",
		CachesInMem:      2,
		CachesOnDisk:     3,
		CachesLockMmap:   false,
		DatasetsInMem:    1,
		DatasetsOnDisk:   2,
		DatasetsLockMmap: false,
	},
	NetworkId:               1,
	TxLookupLimit:           2350000,
	LightPeers:              100,
	UltraLightFraction:      75,
	DatabaseCache:           512,
	TrieCleanCache:          154,
	TrieCleanCacheJournal:   "triecache",
	TrieCleanCacheRejournal: 60 * time.Minute,
	TrieDirtyCache:          256,
	TrieTimeout:             60 * time.Minute,
	SnapshotCache:           102,
	Miner: blockchainconfig.Config{
		GasCeil:  30000000,
		GasPrice: big.NewInt(entity.Gcao),
		Recommit: 3 * time.Second,
	},
	TxPool:        blockchainconfig.DefaultTxPoolConfig,
	RPCGasCap:     50000000,
	RPCEVMTimeout: 5 * time.Second,
	GPO:           FullNodeGPO,
	RPCTxFeeCap:   1, // 1 octer
}
