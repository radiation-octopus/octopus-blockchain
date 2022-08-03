package nodeentity

import (
	"crypto/ecdsa"
	"github.com/radiation-octopus/octopus-blockchain/accounts"
	"github.com/radiation-octopus/octopus-blockchain/blockchain/blockchainconfig"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/entity/genesis"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/node"
	"github.com/radiation-octopus/octopus-blockchain/oct"
	"github.com/radiation-octopus/octopus-blockchain/oct/downloader"
	"github.com/radiation-octopus/octopus-blockchain/oct/octconfig"
	"github.com/radiation-octopus/octopus-blockchain/p2p"
	"github.com/radiation-octopus/octopus-blockchain/p2p/enode"
	"github.com/radiation-octopus/octopus-blockchain/params"
	"math/big"
	"os"
	"time"
)

type nodetype int

const (
	legacyMiningNode nodetype = iota
	legacyNormalNode
	octMiningNode
	octNormalNode
	octLightClient
)

func start(n *node.Node) {

	//New(oct)
}

type OctNode struct {
	typ   nodetype
	Stack *node.Node
	enode *enode.Node
	//api        *ethcatalyst.ConsensusAPI
	OctBackend *oct.Octopus
	//lapi       *lescatalyst.ConsensusAPI
	//lesBackend *les.LightEthereum
}

func newNode(typ nodetype, genesis *genesis.Genesis, enodes []*enode.Node) *OctNode {
	var (
		err error
		//api        *ethcatalyst.ConsensusAPI
		//lapi       *lescatalyst.ConsensusAPI
		stack      *node.Node
		octBackend *oct.Octopus
		//lesBackend *les.LightEthereum
	)
	// 启动节点并等待它启动
	//if typ == eth2LightClient {
	//	stack, lesBackend, lapi, err = makeLightNode(genesis)
	//} else {
	//	stack, ethBackend, api, err = makeFullNode(genesis)
	//}
	stack, octBackend, err = makeFullNode(genesis)
	if err != nil {
		panic(err)
	}
	for stack.Server().NodeInfo().Ports.Listener == 0 {
		time.Sleep(250 * time.Millisecond)
	}
	// 将节点连接到之前的所有节点
	for _, n := range enodes {
		stack.Server().AddPeer(n)
	}
	enode := stack.Server().Self()

	// 注入签名者密钥并开始密封
	stack.AccountManager().AddBackend(accounts.NewPlaintextKeyStore("beacon-stress"))
	store := stack.AccountManager().Backends(accounts.KeyStoreType)[0].(*accounts.KeyStore)
	if _, err := store.NewAccount(""); err != nil {
		panic(err)
	}
	return &OctNode{
		typ: typ,
		//api:        api,
		OctBackend: octBackend,
		//lapi:       lapi,
		//lesBackend: lesBackend,
		Stack: stack,
		enode: enode,
	}
}

type nodeManager struct {
	genesis      *genesis.Genesis
	genesisBlock *block.Block
	nodes        []*OctNode
	enodes       []*enode.Node
	close        chan struct{}
}

func newNodeManager(genesis *genesis.Genesis) *nodeManager {
	return &nodeManager{
		close:        make(chan struct{}),
		genesis:      genesis,
		genesisBlock: genesis.ToBlock(nil),
	}
}

func (mgr *nodeManager) createNode(typ nodetype) *OctNode {
	node := newNode(typ, mgr.genesis, mgr.enodes)
	mgr.nodes = append(mgr.nodes, node)
	mgr.enodes = append(mgr.enodes, node.enode)
	return node
}

func MainNode() (*OctNode, *genesis.Genesis) {
	// 生成一批要封存和提供资金的账户
	faucets := make([]*ecdsa.PrivateKey, 16)
	for i := 0; i < len(faucets); i++ {
		faucets[i], _ = crypto.GenerateKey()
	}
	genesis := node.MakeGenesis(faucets)
	manager := newNodeManager(genesis)
	node := manager.createNode(octNormalNode)
	//manager.createNode(octMiningNode)
	return node, genesis
}

func makeFullNode(genesis *genesis.Genesis) (*node.Node, *oct.Octopus, error) {
	//定义章鱼节点的基本配置
	datadir, _ := os.MkdirTemp("", "")
	//定义oct节点的基本配置
	//datadir := os.TempDir()
	config := &node.Config{
		Name:    "geth",
		Version: params.Version,
		DataDir: datadir,
		P2P: p2p.Config{
			ListenAddr:  "0.0.0.0:0",
			NoDiscovery: true,
			MaxPeers:    25,
		},
		UseLightweightKDF: true,
	}
	// 创建节点并在其上配置完整的章鱼节点
	stack, err := node.NewNodeCfg(config)
	if err != nil {
		return nil, nil, err
	}

	oconfig := &octconfig.Config{
		Genesis:         genesis,
		NetworkId:       genesis.Config.ChainID.Uint64(),
		SyncMode:        downloader.FullSync,
		DatabaseCache:   256,
		DatabaseHandles: 256,
		TxPool:          blockchainconfig.DefaultTxPoolConfig,
		//GPO:             ethconfig.Defaults.GPO,
		Octell: octconfig.Defaults.Octell,
		Miner: blockchainconfig.Config{
			GasFloor: genesis.GasLimit * 9 / 10,
			GasCeil:  genesis.GasLimit * 11 / 10,
			GasPrice: big.NewInt(1),
			Recommit: 10 * time.Second, //禁用重新提交
		},
		//LightServ:        100,
		//LightPeers:       10,
		//LightNoSyncServe: true,
	}
	octBackend, err := oct.New(stack, oconfig)
	if err != nil {
		return nil, nil, err
	}
	//_, err = les.NewLesServer(stack, ethBackend, econfig)
	if err != nil {
		log.Info("Failed to create the LES server", "err", err)
	}
	err = stack.Start()
	return stack, octBackend, err
}
