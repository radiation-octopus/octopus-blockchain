package node

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"github.com/prometheus/tsdb/fileutil"
	"github.com/radiation-octopus/octopus-blockchain/accounts"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/entity/genesis"
	"github.com/radiation-octopus/octopus-blockchain/entity/rawdb"
	"github.com/radiation-octopus/octopus-blockchain/terr"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus/log"
	"net/rpc"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrNodeStopped = errors.New("node not started")
)

func (node *Node) start() {
	log.Info("node Starting")
	makeFullNode(node)
	log.Info("node 启动完成")
	//New(oct)
}

func (node *Node) close() {

}

// OpenDatabaseWithFreezer从节点的数据目录中打开一个具有给定名称的现有数据库（如果找不到以前的名称，则创建一个），
//并向其附加一个链冻结器，该链冻结器将已有的的链数据从数据库移动到不可变的仅附加文件。
//如果节点是临时节点，则返回内存数据库。
func (n *Node) OpenDatabaseWithFreezer(name string, cache, handles int, freezer, namespace string, readonly bool) (typedb.Database, error) {
	n.lock.Lock()
	defer n.lock.Unlock()
	if n.state == closedState {
		return nil, ErrNodeStopped
	}

	var db typedb.Database
	var err error
	if n.config.DataDir == "" {
		db = rawdb.NewMemoryDatabase()
	} else {
		root := n.ResolvePath(name)
		switch {
		case freezer == "":
			freezer = filepath.Join(root, "ancient")
		case !filepath.IsAbs(freezer):
			freezer = n.ResolvePath(freezer)
		}
		db, err = rawdb.NewLevelDBDatabaseWithFreezer(root, cache, handles, freezer, namespace, readonly)
	}

	if err == nil {
		db = n.wrapDatabase(db)
	}
	return db, err
}

// ResolvePath返回实例目录中资源的绝对路径。
func (n *Node) ResolvePath(x string) string {
	return n.config.ResolvePath(x)
}

// wrapDatabase确保在节点关闭时自动关闭数据库。
func (n *Node) wrapDatabase(db typedb.Database) typedb.Database {
	wrapper := &closeTrackingDB{db, n}
	n.databases[wrapper] = struct{}{}
	return wrapper
}

const (
	datadirPrivateKey      = "nodekey"            // Path within the datadir to the node's private key
	datadirJWTKey          = "jwtsecret"          // Path within the datadir to the node's jwt secret
	datadirDefaultKeyStore = "keystore"           // Path within the datadir to the keystore
	datadirStaticNodes     = "static-nodes.json"  // Path within the datadir to the static node list
	datadirTrustedNodes    = "trusted-nodes.json" // Path within the datadir to the trusted node list
	datadirNodeDatabase    = "nodes"              // Path within the datadir to store the node infos
)

type Config struct {
	// Name设置节点的实例名称。它不能包含/字符，并在devp2p节点标识符中使用。geth的实例名为“geth”。如果未指定值，则使用当前可执行文件的basename。
	Name string `toml:"-"`

	// UserIdent（如果设置）用作devp2p节点标识符中的附加组件。
	UserIdent string `toml:",omitempty"`

	// 版本应设置为程序的版本号。它用于devp2p节点标识符中。
	Version string `toml:"-"`

	// DataDir是节点应用于任何数据存储要求的文件系统文件夹。配置的数据目录不会直接与注册的服务共享，
	//相反，这些服务可以使用实用工具方法创建/访问数据库或平面文件。这使得临时节点可以完全驻留在内存中。
	DataDir string

	// 对等网络的配置。
	//P2P p2p.Config

	// KeyStoreDir是包含私钥的文件系统文件夹。可以将目录指定为相对路径，在这种情况下，它将相对于当前目录进行解析。
	//如果KeyStoreDir为空，则默认位置为DataDir的“keystore”子目录。如果DataDir未指定，KeyStoreDir为空，则New会创建临时目录，并在节点停止时销毁。
	KeyStoreDir string `toml:",omitempty"`

	// ExternalSigner为clef类型签名者指定外部URI
	ExternalSigner string `toml:",omitempty"`

	// UseLightweightKDF降低了密钥存储scrypt KDF的内存和CPU需求，但牺牲了安全性。
	UseLightweightKDF bool `toml:",omitempty"`

	// UnsecureUnlockAllowed允许用户在不安全的http环境中解锁帐户。
	InsecureUnlockAllowed bool `toml:",omitempty"`

	// NoUSB禁用硬件钱包监控和连接。不推荐使用：默认情况下禁用USB监控，必须显式启用。
	NoUSB bool `toml:",omitempty"`

	// USB支持硬件钱包监控和连接。
	USB bool `toml:",omitempty"`

	// SmartCardDaemonPath是智能卡守护程序套接字的路径
	SmartCardDaemonPath string `toml:",omitempty"`

	// IPCPath是放置IPC端点的请求位置。如果路径是一个简单的文件名，
	//则将其放置在数据目录中（或Windows上的根管道路径上），而如果它是一个可解析的路径名（绝对或相对），则强制执行该特定路径。空路径禁用IPC。
	IPCPath string

	// HTTPHost是启动HTTP RPC服务器的主机接口。如果此字段为空，则不会启动HTTP API端点。
	HTTPHost string

	// HTTPPort是要在其上启动HTTP RPC服务器的TCP端口号。默认的零值为/有效，并将随机选取端口号（对临时节点有用）。
	HTTPPort int `toml:",omitempty"`

	// HTTPCors是发送给请求客户端的跨源资源共享头。请注意，CORS是一种浏览器强制执行的安全性，它对自定义HTTP客户端完全无用。
	HTTPCors []string `toml:",omitempty"`

	// HTTPVirtualHosts是传入请求允许的虚拟主机名列表。默认情况下，这是{'localhost'}。
	//使用此功能可以防止DNS重新绑定之类的攻击，DNS重新绑定通过简单地伪装为在同一来源内绕过SOP。
	//这些攻击不使用COR，因为它们不是跨域的。
	//通过显式检查主机标头，服务器将不允许对具有恶意主机域的服务器发出请求。直接使用ip地址的请求不受影响
	HTTPVirtualHosts []string `toml:",omitempty"`

	// HTTPModules是要通过HTTP-RPC接口公开的API模块列表。
	//如果模块列表为空，则将公开指定为public的所有RPC API端点。
	HTTPModules []string

	// HTTPTimeouts允许自定义HTTP RPC接口使用的超时值。
	//HTTPTimeouts rpc.HTTPTimeouts

	// HTTPPathPrefix指定要为其提供http rpc的路径前缀。
	HTTPPathPrefix string `toml:",omitempty"`

	// AuthAddr是提供经过身份验证的API的侦听地址。
	AuthAddr string `toml:",omitempty"`

	// AuthPort是提供经过身份验证的API的端口号。
	AuthPort int `toml:",omitempty"`

	// AuthVirtualHosts是对经过身份验证的api的传入请求允许的虚拟主机名列表。默认情况下，这是{'localhost'}。
	AuthVirtualHosts []string `toml:",omitempty"`

	// WSHost是启动websocket RPC服务器的主机接口。如果此字段为空，则不会启动websocket API端点。
	WSHost string

	// WSPort是启动websocket RPC服务器的TCP端口号。默认的零值为/有效，并将随机选取端口号（对临时节点有用）。
	WSPort int `toml:",omitempty"`

	// WSPathPrefix指定要为其提供ws-rpc服务的路径前缀。
	WSPathPrefix string `toml:",omitempty"`

	// WSOriginates是从中接受websocket请求的域列表。
	//请注意，服务器只能对客户端发送的HTTP请求进行操作，无法验证请求头的有效性。
	WSOrigins []string `toml:",omitempty"`

	// WSModules是要通过websocket RPC接口公开的API模块列表。
	//如果模块列表为空，则将公开指定为public的所有RPC API端点。
	WSModules []string

	// WSExposeAll通过WebSocket RPC接口而不仅仅是公共接口公开所有API模块*警告*仅当节点在受信任的网络中运行时才设置此选项，向不受信任的用户公开私有API是一个主要的安全风险。
	WSExposeAll bool `toml:",omitempty"`

	// GraphQLCors是要发送给请求客户端的跨源资源共享头。
	//请注意，CORS是一种浏览器强制执行的安全性，它对自定义HTTP客户端完全无用。
	GraphQLCors []string `toml:",omitempty"`

	// GraphQLVirtualHosts是传入请求允许的虚拟主机名列表。
	//默认情况下，这是{'localhost'}。使用此功能可以防止DNS重新绑定之类的攻击，DNS重新绑定通过简单地伪装为在同一来源内绕过SOP。
	//这些攻击不使用COR，因为它们不是跨域的。
	//通过显式检查主机标头，服务器将不允许对具有恶意主机域的服务器发出请求。直接使用ip地址的请求不受影响
	GraphQLVirtualHosts []string `toml:",omitempty"`

	// Logger是用于p2p的自定义记录器。服务器
	Logger log.OctopusLog `toml:",omitempty"`

	staticNodesWarning     bool
	trustedNodesWarning    bool
	oldGethResourceWarning bool

	// AllowUnprotectedTxs允许通过RPC发送非EIP-155保护的事务。
	AllowUnprotectedTxs bool `toml:",omitempty"`

	// JWTSecret是十六进制编码的jwt密钥。
	JWTSecret string `toml:",omitempty"`
}

//ResolvePath解析实例目录中的路径。
func (c *Config) ResolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if c.DataDir == "" {
		return ""
	}
	// 向后兼容性：确保使用geth 1.4创建的数据目录文件（如果存在）。
	if warn, isOld := isOldGethResource[path]; isOld {
		oldpath := ""
		if c.name() == "geth" {
			oldpath = filepath.Join(c.DataDir, path)
		}
		if oldpath != "" && entity.FileExist(oldpath) {
			if warn {
				c.warnOnce(&c.oldGethResourceWarning, "Using deprecated resource file %s, please move this file to the 'geth' subdirectory of datadir.", oldpath)
			}
			return oldpath
		}
	}
	return filepath.Join(c.instanceDir(), path)
}

//对于“geth”实例，这些资源的解析方式不同。
var isOldGethResource = map[string]bool{
	"chaindata":          true,
	"nodes":              true,
	"nodekey":            true,
	"static-nodes.json":  false, //没有警告，因为他们有
	"trusted-nodes.json": false, // 拥有单独的警告。
}

func (c *Config) name() string {
	if c.Name == "" {
		progname := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
		if progname == "" {
			panic("empty executable name, set Config.Name")
		}
		return progname
	}
	return c.Name
}

func (c *Config) instanceDir() string {
	if c.DataDir == "" {
		return ""
	}
	return filepath.Join(c.DataDir, c.name())
}

// KeyDirConfig确定keydirectory的设置
func (c *Config) KeyDirConfig() (string, error) {
	var (
		keydir string
		err    error
	)
	switch {
	case filepath.IsAbs(c.KeyStoreDir):
		keydir = c.KeyStoreDir
	case c.DataDir != "":
		if c.KeyStoreDir == "" {
			keydir = filepath.Join(c.DataDir, datadirDefaultKeyStore)
		} else {
			keydir, err = filepath.Abs(c.KeyStoreDir)
		}
	case c.KeyStoreDir != "":
		keydir, err = filepath.Abs(c.KeyStoreDir)
	}
	return keydir, err
}

var warnLock sync.Mutex

func (c *Config) warnOnce(w *bool, format string, args ...interface{}) {
	warnLock.Lock()
	defer warnLock.Unlock()

	if *w {
		return
	}
	//l := c.Logger
	//if l == nil {
	//	l = log.Root()
	//}
	//l.Warn(fmt.Sprintf(format, args...))
	*w = true
}

// getKeyStoreDir检索密钥目录，并在必要时创建临时目录。
func getKeyStoreDir(conf *Config) (string, bool, error) {
	keydir, err := conf.KeyDirConfig()
	if err != nil {
		return "", false, err
	}
	isEphemeral := false
	if keydir == "" {
		// 没有datadir。
		//keydir, err = os.MkdirTemp("", "keystore")
		isEphemeral = true
	}

	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(keydir, 0700); err != nil {
		return "", false, err
	}

	return keydir, isEphemeral, nil
}

const (
	initializingState = iota
	runningState
	closedState
)

// 节点是可以在其上注册服务的容器。
type Node struct {
	//eventmux      *event.TypeMux
	config     *Config
	accman     *accounts.Manager
	log        log.OctopusLog
	keyDir     string            // 密钥存储目录
	keyDirTemp bool              // 如果为true，则Stop将删除密钥目录
	dirLock    fileutil.Releaser // 防止并发使用实例目录
	stop       chan struct{}     // 等待终止通知的通道
	//server        *p2p.Server       // 当前正在运行P2P网络层
	startStopLock sync.Mutex // 启动/停止由附加锁保护
	state         int        // 跟踪节点生命周期的状态

	lock sync.Mutex
	//lifecycles    []Lifecycle // 具有生命周期的所有已注册后端、服务和辅助服务
	//rpcAPIs       []rpc.API   // 节点当前提供的API列表
	//http          *httpServer //
	//ws            *httpServer //
	//httpAuth      *httpServer //
	//wsAuth        *httpServer //
	//ipc           *ipcServer  // 存储有关ipc http服务器的信息
	inprocHandler *rpc.Server // 用于处理API请求的进程内RPC请求处理程序

	databases map[*closeTrackingDB]struct{} // 所有打开的数据库
}

// Start启动所有注册的生命周期、RPC服务和p2p网络。节点只能启动一次。
func (n *Node) Start() error {
	n.startStopLock.Lock()
	defer n.startStopLock.Unlock()

	//n.lock.Lock()
	switch n.state {
	case runningState:
		n.lock.Unlock()
		return terr.ErrNodeRunning
	case closedState:
		n.lock.Unlock()
		return terr.ErrNodeStopped
	}
	//n.state = runningState
	//打开网络和RPC端点
	//err := n.openEndpoints()
	//lifecycles := make([]Lifecycle, len(n.lifecycles))
	//copy(lifecycles, n.lifecycles)
	//n.lock.Unlock()

	//检查端点启动是否失败。
	//if err != nil {
	//	n.doClose(nil)
	//	return err
	//}
	// 启动所有注册的生命周期。
	//var started []Lifecycle
	//for _, lifecycle := range lifecycles {
	//	if err = lifecycle.Start(); err != nil {
	//		break
	//	}
	//	started = append(started, lifecycle)
	//}
	// 检查是否有任何生命周期未能启动。
	//if err != nil {
	//	n.stopServices(started)
	//	n.doClose(nil)
	//}
	return nil
}

//AccountManager检索协议堆栈使用的帐户管理器。
func (n *Node) AccountManager() *accounts.Manager {
	return n.accman
}

func (n *Node) openDataDir() error {
	if n.config.DataDir == "" {
		return nil // 短暂的
	}

	instdir := filepath.Join(n.config.DataDir, n.config.name())
	if err := os.MkdirAll(instdir, 0700); err != nil {
		return err
	}
	// 锁定实例目录，以防止另一个实例并发使用，以及意外使用实例目录作为数据库。
	release, _, err := fileutil.Flock(filepath.Join(instdir, "LOCK"))
	if err != nil {
		return terr.ConvertFileLockError(err)
	}
	n.dirLock = release
	return nil
}

// closeTrackingDB包装数据库的Close方法。
//当服务关闭数据库时，包装器会将其从节点的数据库映射中删除。
//这确保了如果数据库被打开它的服务关闭，节点不会自动关闭数据库。
type closeTrackingDB struct {
	typedb.Database
	n *Node
}

func (db *closeTrackingDB) Close() error {
	db.n.lock.Lock()
	delete(db.n.databases, db)
	db.n.lock.Unlock()
	return db.Database.Close()
}

func makeFullNode(node *Node) (*Node, error) {
	//定义章鱼节点的基本配置
	//datadir, _ := os.MkdirTemp("", "")

	// 生成一批用于封存和资金的帐户
	//faucets := make([]*ecdsa.PrivateKey, 16)
	//genesis := makeGenesis(faucets)
	//定义oct节点的基本配置
	datadir := os.TempDir()
	config := &Config{
		Name: "geth",
		//Version: params.Version,
		DataDir: datadir,
		//P2P: p2p.Config{
		//	ListenAddr:  "0.0.0.0:0",
		//	NoDiscovery: true,
		//	MaxPeers:    25,
		//},
		UseLightweightKDF: true,
	}
	// 创建节点并在其上配置完整的章鱼节点
	stack, err := NewNodeCfg(node, config)
	if err != nil {
		return nil, err
	}

	//econfig := &oct.Config{
	//	Genesis:         genesis,
	//	NetworkId:       genesis.Config.ChainID.Uint64(),
	//	//SyncMode:        downloader.FullSync,
	//	DatabaseCache:   256,
	//	DatabaseHandles: 256,
	//	TxPool:          blockchain.DefaultTxPoolConfig,
	//	//GPO:             ethconfig.Defaults.GPO,
	//	//Octell:          ethconfig.Defaults.Octell,
	//	Miner: miner.Config{
	//		GasFloor: genesis.GasLimit * 9 / 10,
	//		GasCeil:  genesis.GasLimit * 11 / 10,
	//		GasPrice: big.NewInt(1),
	//		Recommit: 10 * time.Second, //禁用重新提交
	//	},
	//	//LightServ:        100,
	//	//LightPeers:       10,
	//	//LightNoSyncServe: true,
	//}
	//ethBackend, err := oct.New(stack, econfig)
	if err != nil {
		return nil, err
	}
	//_, err = les.NewLesServer(stack, ethBackend, econfig)
	if err != nil {
		log.Info("Failed to create the LES server", "err", err)
	}
	err = stack.Start()
	return stack, err
}

// 新建创建一个新的P2P节点，为协议注册做好准备。
func NewNodeCfg(node *Node, conf *Config) (*Node, error) {
	// 复制config并解析datadir，以便将来对当前工作目录的更改不会影响节点。
	confCopy := *conf
	conf = &confCopy
	if conf.DataDir != "" {
		absdatadir, err := filepath.Abs(conf.DataDir)
		if err != nil {
			return nil, err
		}
		conf.DataDir = absdatadir
	}
	//if conf.Logger == nil {
	//	conf.Logger = log.New()
	//}

	// 确保实例名称不会与数据目录中的其他文件产生奇怪的冲突。
	if strings.ContainsAny(conf.Name, `/\`) {
		return nil, errors.New(`Config.Name must not contain '/' or '\'`)
	}
	if conf.Name == datadirDefaultKeyStore {
		return nil, errors.New(`Config.Name cannot be "` + datadirDefaultKeyStore + `"`)
	}
	if strings.HasSuffix(conf.Name, ".ipc") {
		return nil, errors.New(`Config.Name cannot end in ".ipc"`)
	}

	node.config = conf
	node.inprocHandler = rpc.NewServer()
	node.log = conf.Logger
	node.stop = make(chan struct{})
	node.databases = make(map[*closeTrackingDB]struct{})
	//node := &Node{
	//	config:        conf,
	//	inprocHandler: rpc.NewServer(),
	//	//eventmux:      new(event.TypeMux),
	//	log:  conf.Logger,
	//	stop: make(chan struct{}),
	//	//server:        &p2p.Server{Config: conf.P2P},
	//	databases:     make(map[*closeTrackingDB]struct{}),
	//}

	// 注册内置API。
	//node.rpcAPIs = append(node.rpcAPIs, node.apis()...)

	// 获取实例目录锁。
	if err := node.openDataDir(); err != nil {
		return nil, err
	}
	keyDir, isEphem, err := getKeyStoreDir(conf)
	if err != nil {
		return nil, err
	}
	node.keyDir = keyDir
	node.keyDirTemp = isEphem
	//创建没有后端的空AccountManager。稍后需要调用方（例如cmd/geth）添加后端。
	node.accman = accounts.NewManager(&accounts.Config{InsecureUnlockAllowed: conf.InsecureUnlockAllowed})

	// 初始化p2p服务器。这将创建节点密钥和发现数据库。
	//node.server.Config.PrivateKey = node.config.NodeKey()
	//node.server.Config.Name = node.config.NodeName()
	//node.server.Config.Logger = node.log
	//if node.server.Config.StaticNodes == nil {
	//	node.server.Config.StaticNodes = node.config.StaticNodes()
	//}
	//if node.server.Config.TrustedNodes == nil {
	//	node.server.Config.TrustedNodes = node.config.TrustedNodes()
	//}
	//if node.server.Config.NodeDatabase == "" {
	//	node.server.Config.NodeDatabase = node.config.NodeDB()
	//}

	// 检查HTTP/WS前缀是否有效。
	if err := validatePrefix("HTTP", conf.HTTPPathPrefix); err != nil {
		return nil, err
	}
	if err := validatePrefix("WebSocket", conf.WSPathPrefix); err != nil {
		return nil, err
	}

	// 配置RPC服务器。
	//node.http = newHTTPServer(node.log, conf.HTTPTimeouts)
	//node.httpAuth = newHTTPServer(node.log, conf.HTTPTimeouts)
	//node.ws = newHTTPServer(node.log, rpc.DefaultHTTPTimeouts)
	//node.wsAuth = newHTTPServer(node.log, rpc.DefaultHTTPTimeouts)
	//node.ipc = newIPCServer(node.log, conf.IPCEndpoint())

	return node, nil
}

// validatePrefix检查“path”是否是RPC前缀选项的有效配置值。
func validatePrefix(what, path string) error {
	if path == "" {
		return nil
	}
	if path[0] != '/' {
		return fmt.Errorf(`%s RPC path prefix %q does not contain leading "/"`, what, path)
	}
	if strings.ContainsAny(path, "?#") {
		// 这只是为了避免混淆。虽然这些将正确匹配（即，如果URL转义到路径中，它们将匹配），但用户在命令行上设置时不容易理解。
		return fmt.Errorf("%s RPC path prefix %q contains URL meta-characters", what, path)
	}
	return nil
}

// makeGenesis基于一些预定义的水龙头帐户创建自定义的八单元genesis块。
func makeGenesis(faucets []*ecdsa.PrivateKey) *genesis.Genesis {
	genesis := genesis.DefaultRopstenGenesisBlock()

	return genesis
}
