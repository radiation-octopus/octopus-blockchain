package node

import (
	"crypto/ecdsa"
	crand "crypto/rand"
	"errors"
	"fmt"
	"github.com/prometheus/tsdb/fileutil"
	"github.com/radiation-octopus/octopus-blockchain/accounts"
	"github.com/radiation-octopus/octopus-blockchain/entity/genesis"
	"github.com/radiation-octopus/octopus-blockchain/entity/hexutil"
	"github.com/radiation-octopus/octopus-blockchain/entity/rawdb"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus-blockchain/p2p"
	"github.com/radiation-octopus/octopus-blockchain/params"
	"github.com/radiation-octopus/octopus-blockchain/rpc"
	"github.com/radiation-octopus/octopus-blockchain/terr"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"hash/crc32"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	initializingState = iota
	runningState
	closedState
)

// 节点是可以在其上注册服务的容器。
type Node struct {
	//eventmux      *event.TypeMux
	config        *Config
	accman        *accounts.Manager
	log           log.Logger
	keyDir        string            // 密钥存储目录
	keyDirTemp    bool              // 如果为true，则Stop将删除密钥目录
	dirLock       fileutil.Releaser // 防止并发使用实例目录
	stop          chan struct{}     // 等待终止通知的通道
	server        *p2p.Server       // 当前正在运行P2P网络层
	startStopLock sync.Mutex        // 启动/停止由附加锁保护
	state         int               // 跟踪节点生命周期的状态

	lock          sync.Mutex
	lifecycles    []Lifecycle // 具有生命周期的所有已注册后端、服务和辅助服务
	rpcAPIs       []rpc.API   // 节点当前提供的API列表
	http          *httpServer //
	ws            *httpServer //
	httpAuth      *httpServer //
	wsAuth        *httpServer //
	ipc           *ipcServer  // 存储有关ipc http服务器的信息
	inprocHandler *rpc.Server // 用于处理API请求的进程内RPC请求处理程序

	databases map[*closeTrackingDB]struct{} // 所有打开的数据库
}

// New创建一个新的P2P节点，为协议注册做好准备。
func New(conf *Config) (*Node, error) {
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
	if conf.Logger == nil {
		conf.Logger = log.New()
	}

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

	node := &Node{
		config:        conf,
		inprocHandler: rpc.NewServer(),
		//eventmux:      new(event.TypeMux),
		log:       conf.Logger,
		stop:      make(chan struct{}),
		server:    &p2p.Server{Config: conf.P2P},
		databases: make(map[*closeTrackingDB]struct{}),
	}

	// 注册内置API。
	node.rpcAPIs = append(node.rpcAPIs, node.apis()...)

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
	// 创建没有后端的空AccountManager。调用方（例如cmd/geth）需要稍后添加后端。
	node.accman = accounts.NewManager(&accounts.Config{InsecureUnlockAllowed: conf.InsecureUnlockAllowed})

	// 初始化p2p服务器。这将创建节点密钥和发现数据库。
	node.server.Config.PrivateKey = node.config.NodeKey()
	node.server.Config.Name = node.config.NodeName()
	node.server.Config.Logger = node.log
	if node.server.Config.StaticNodes == nil {
		node.server.Config.StaticNodes = node.config.StaticNodes()
	}
	if node.server.Config.TrustedNodes == nil {
		node.server.Config.TrustedNodes = node.config.TrustedNodes()
	}
	if node.server.Config.NodeDatabase == "" {
		node.server.Config.NodeDatabase = node.config.NodeDB()
	}

	// 检查HTTP/WS前缀是否有效。
	if err := validatePrefix("HTTP", conf.HTTPPathPrefix); err != nil {
		return nil, err
	}
	if err := validatePrefix("WebSocket", conf.WSPathPrefix); err != nil {
		return nil, err
	}

	// 配置RPC服务器。
	node.http = newHTTPServer(node.log, conf.HTTPTimeouts)
	node.httpAuth = newHTTPServer(node.log, conf.HTTPTimeouts)
	node.ws = newHTTPServer(node.log, rpc.DefaultHTTPTimeouts)
	node.wsAuth = newHTTPServer(node.log, rpc.DefaultHTTPTimeouts)
	node.ipc = newIPCServer(node.log, conf.IPCEndpoint())

	return node, nil
}

// 服务器检索当前运行的P2P网络层。
//此方法仅用于检查当前运行的服务器的字段。呼叫者不应启动或停止返回的服务器。
func (n *Node) Server() *p2p.Server {
	n.lock.Lock()
	defer n.lock.Unlock()

	return n.server
}

// Start启动所有注册的生命周期、RPC服务和p2p网络。节点只能启动一次。
func (n *Node) Start() error {
	n.startStopLock.Lock()
	defer n.startStopLock.Unlock()

	n.lock.Lock()
	switch n.state {
	case runningState:
		n.lock.Unlock()
		return ErrNodeRunning
	case closedState:
		n.lock.Unlock()
		return ErrNodeStopped
	}
	n.state = runningState
	// 开放网络和RPC端点
	err := n.openEndpoints()
	lifecycles := make([]Lifecycle, len(n.lifecycles))
	copy(lifecycles, n.lifecycles)
	n.lock.Unlock()

	// 检查端点启动是否失败。
	if err != nil {
		n.doClose(nil)
		return err
	}
	// 启动所有注册的生命周期。
	var started []Lifecycle
	for _, lifecycle := range lifecycles {
		if err = lifecycle.Start(); err != nil {
			break
		}
		started = append(started, lifecycle)
	}
	// 检查是否有任何生命周期未能启动。
	if err != nil {
		n.stopServices(started)
		n.doClose(nil)
	}
	return err
}

// 等待块，直到节点关闭。
func (n *Node) Wait() {
	<-n.stop
}

// Close停止节点并释放在Node constructor New中获取的资源。
func (n *Node) Close() error {
	n.startStopLock.Lock()
	defer n.startStopLock.Unlock()

	n.lock.Lock()
	state := n.state
	n.lock.Unlock()
	switch state {
	case initializingState:
		// 该节点从未启动。
		return n.doClose(nil)
	case runningState:
		// 节点已启动，释放Start（）获取的资源。
		var errs []error
		if err := n.stopServices(n.lifecycles); err != nil {
			errs = append(errs, err)
		}
		return n.doClose(errs)
	case closedState:
		return ErrNodeStopped
	default:
		panic(fmt.Sprintf("node is in unknown state %d", state))
	}
}

// doClose释放New（）获取的资源，收集错误。
func (n *Node) doClose(errs []error) error {
	// 关闭数据库。这需要锁，因为它需要与OpenDatabase*同步。
	n.lock.Lock()
	n.state = closedState
	errs = append(errs, n.closeDatabases()...)
	n.lock.Unlock()

	if err := n.accman.Close(); err != nil {
		errs = append(errs, err)
	}
	if n.keyDirTemp {
		if err := os.RemoveAll(n.keyDir); err != nil {
			errs = append(errs, err)
		}
	}

	// 释放实例目录锁。
	n.closeDataDir()

	// 解锁n.Wait。
	close(n.stop)

	// 报告可能发生的任何错误。
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return fmt.Errorf("%v", errs)
	}
}

// Attach创建连接到进程内API处理程序的RPC客户端。
func (n *Node) Attach() (*rpc.Client, error) {
	return rpc.DialInProc(n.inprocHandler), nil
}

// RPCHandler返回进程内RPC请求处理程序。
func (n *Node) RPCHandler() (*rpc.Server, error) {
	n.lock.Lock()
	defer n.lock.Unlock()

	if n.state == closedState {
		return nil, ErrNodeStopped
	}
	return n.inprocHandler, nil
}

//AccountManager检索协议堆栈使用的帐户管理器。
func (n *Node) AccountManager() *accounts.Manager {
	return n.accman
}

// openEndpoints启动所有网络和RPC端点。
func (n *Node) openEndpoints() error {
	// 启动网络端点
	n.log.Info("Starting peer-to-peer node", "instance", n.server.Name)
	if err := n.server.Start(); err != nil {
		return convertFileLockError(err)
	}
	// 启动RPC终结点
	err := n.startRPC()
	if err != nil {
		n.stopRPC()
		n.server.Stop()
	}
	return err
}

// startRPC是一种辅助方法，用于在节点启动期间配置所有各种RPC端点。
//由于它对节点的状态进行了某些假设，因此不打算在之后的任何时候调用它。
func (n *Node) startRPC() error {
	if err := n.startInProc(); err != nil {
		return err
	}

	// 配置IPC。
	if n.ipc.endpoint != "" {
		if err := n.ipc.start(n.rpcAPIs); err != nil {
			return err
		}
	}
	var (
		servers   []*httpServer
		open, all = n.GetAPIs()
	)

	initHttp := func(server *httpServer, apis []rpc.API, port int) error {
		if err := server.setListenAddr(n.config.HTTPHost, port); err != nil {
			return err
		}
		if err := server.enableRPC(apis, httpConfig{
			CorsAllowedOrigins: n.config.HTTPCors,
			Vhosts:             n.config.HTTPVirtualHosts,
			Modules:            n.config.HTTPModules,
			prefix:             n.config.HTTPPathPrefix,
		}); err != nil {
			return err
		}
		servers = append(servers, server)
		return nil
	}

	initWS := func(apis []rpc.API, port int) error {
		server := n.wsServerForPort(port, false)
		if err := server.setListenAddr(n.config.WSHost, port); err != nil {
			return err
		}
		if err := server.enableWS(n.rpcAPIs, wsConfig{
			Modules: n.config.WSModules,
			Origins: n.config.WSOrigins,
			prefix:  n.config.WSPathPrefix,
		}); err != nil {
			return err
		}
		servers = append(servers, server)
		return nil
	}

	initAuth := func(apis []rpc.API, port int, secret []byte) error {
		// Enable auth via HTTP
		server := n.httpAuth
		if err := server.setListenAddr(n.config.AuthAddr, port); err != nil {
			return err
		}
		if err := server.enableRPC(apis, httpConfig{
			CorsAllowedOrigins: DefaultAuthCors,
			Vhosts:             n.config.AuthVirtualHosts,
			Modules:            DefaultAuthModules,
			prefix:             DefaultAuthPrefix,
			jwtSecret:          secret,
		}); err != nil {
			return err
		}
		servers = append(servers, server)
		// 通过WS启用身份验证
		server = n.wsServerForPort(port, true)
		if err := server.setListenAddr(n.config.AuthAddr, port); err != nil {
			return err
		}
		if err := server.enableWS(apis, wsConfig{
			Modules:   DefaultAuthModules,
			Origins:   DefaultAuthOrigins,
			prefix:    DefaultAuthPrefix,
			jwtSecret: secret,
		}); err != nil {
			return err
		}
		servers = append(servers, server)
		return nil
	}

	//设置HTTP。
	if n.config.HTTPHost != "" {
		// 配置旧版未经身份验证的HTTP。
		if err := initHttp(n.http, open, n.config.HTTPPort); err != nil {
			return err
		}
	}
	// 配置WebSocket。
	if n.config.WSHost != "" {
		// 遗留未经验证
		if err := initWS(open, n.config.WSPort); err != nil {
			return err
		}
	}
	// 配置经过身份验证的API
	if len(open) != len(all) {
		jwtSecret, err := n.obtainJWTSecret(n.config.JWTSecret)
		if err != nil {
			return err
		}
		if err := initAuth(all, n.config.AuthPort, jwtSecret); err != nil {
			return err
		}
	}
	// 启动服务器
	for _, server := range servers {
		if err := server.start(); err != nil {
			return err
		}
	}
	return nil
}

//获取JWTSecret从提供的配置或默认位置加载jwt机密。
//如果两者都不存在，它会生成一个新的秘密并存储到默认位置。
func (n *Node) obtainJWTSecret(cliParam string) ([]byte, error) {
	fileName := cliParam
	if len(fileName) == 0 {
		// 未提供路径，请使用默认路径
		fileName = n.ResolvePath(datadirJWTKey)
	}
	// 尝试从文件读取
	if data, err := os.ReadFile(fileName); err == nil {
		jwtSecret := operationutils.FromHex(strings.TrimSpace(string(data)))
		if len(jwtSecret) == 32 {
			log.Info("Loaded JWT secret file", "path", fileName, "crc32", fmt.Sprintf("%#x", crc32.ChecksumIEEE(jwtSecret)))
			return jwtSecret, nil
		}
		log.Error("Invalid JWT secret", "path", fileName, "length", len(jwtSecret))
		return nil, errors.New("invalid JWT secret")
	}
	//需要生成一个
	jwtSecret := make([]byte, 32)
	crand.Read(jwtSecret)
	// 如果我们处于开发模式，不需要保存，只需显示它
	if fileName == "" {
		log.Info("Generated ephemeral JWT secret", "secret", hexutil.Encode(jwtSecret))
		return jwtSecret, nil
	}
	if err := os.WriteFile(fileName, []byte(hexutil.Encode(jwtSecret)), 0600); err != nil {
		return nil, err
	}
	log.Info("Generated JWT secret", "path", fileName)
	return jwtSecret, nil
}

//startInProc在inproc服务器上注册所有RPC API。
func (n *Node) startInProc() error {
	for _, api := range n.rpcAPIs {
		if err := n.inprocHandler.RegisterName(api.Namespace, api.Service); err != nil {
			return err
		}
	}
	return nil
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

// RegisterProtocols将后端协议添加到节点的p2p服务器。
func (n *Node) RegisterProtocols(protocols []p2p.Protocol) {
	n.lock.Lock()
	defer n.lock.Unlock()

	if n.state != initializingState {
		panic("can't register protocols on running/stopped node")
	}
	n.server.Protocols = append(n.server.Protocols, protocols...)
}

func (n *Node) wsServerForPort(port int, authenticated bool) *httpServer {
	httpServer, wsServer := n.http, n.ws
	if authenticated {
		httpServer, wsServer = n.httpAuth, n.wsAuth
	}
	if n.config.HTTPHost == "" || httpServer.port == port {
		return httpServer
	}
	return wsServer
}

// RegisterAPI注册服务在节点上提供的API。
func (n *Node) RegisterAPIs(apis []rpc.API) {
	n.lock.Lock()
	defer n.lock.Unlock()

	if n.state != initializingState {
		panic("can't register APIs on running/stopped node")
	}
	n.rpcAPIs = append(n.rpcAPIs, apis...)
}

// GetAPI返回两组API，一组不需要身份验证，另一组完整
func (n *Node) GetAPIs() (unauthenticated, all []rpc.API) {
	for _, api := range n.rpcAPIs {
		if !api.Authenticated {
			unauthenticated = append(unauthenticated, api)
		}
	}
	return unauthenticated, n.rpcAPIs
}

//DataDir检索协议堆栈使用的当前DataDir。
//已弃用：此目录中不应存储任何文件，请改用InstanceDir。
func (n *Node) DataDir() string {
	return n.config.DataDir
}

// WSEndpoint返回当前的JSON-RPC over WebSocket端点。
func (n *Node) WSEndpoint() string {
	if n.http.wsAllowed() {
		return "ws://" + n.http.listenAddr() + n.http.wsConfig.prefix
	}
	return "ws://" + n.ws.listenAddr() + n.ws.wsConfig.prefix
}

// stopInProc终止进程内RPC终结点。
func (n *Node) stopInProc() {
	n.inprocHandler.Stop()
}

func (n *Node) stopRPC() {
	n.http.stop()
	n.ws.stop()
	n.httpAuth.stop()
	n.wsAuth.stop()
	n.ipc.stop()
	n.stopInProc()
}

//stopServices终止正在运行的服务、RPC和p2p网络。它与Start相反。
func (n *Node) stopServices(running []Lifecycle) error {
	n.stopRPC()

	// 停止按相反顺序运行生命周期。
	failure := &StopError{Services: make(map[reflect.Type]error)}
	for i := len(running) - 1; i >= 0; i-- {
		if err := running[i].Stop(); err != nil {
			failure.Services[reflect.TypeOf(running[i])] = err
		}
	}

	// 停止p2p网络。
	n.server.Stop()

	if len(failure.Services) > 0 {
		return failure
	}
	return nil
}

// closeDatabases关闭所有打开的数据库。
func (n *Node) closeDatabases() (errors []error) {
	for db := range n.databases {
		delete(n.databases, db)
		if err := db.Database.Close(); err != nil {
			errors = append(errors, err)
		}
	}
	return errors
}

func (n *Node) closeDataDir() {
	// 释放实例目录锁。
	if n.dirLock != nil {
		if err := n.dirLock.Release(); err != nil {
			n.log.Error("Can't release datadir lock", "err", err)
		}
		n.dirLock = nil
	}
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
	if conf.Logger == nil {
		conf.Logger = log.New()
	}

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
	node.server = &p2p.Server{
		Config: conf.P2P,
	}
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
	node.server.Config.PrivateKey = node.config.NodeKey()
	node.server.Config.Name = node.config.NodeName()
	node.server.Config.Logger = node.log
	if node.server.Config.StaticNodes == nil {
		node.server.Config.StaticNodes = node.config.StaticNodes()
	}
	if node.server.Config.TrustedNodes == nil {
		node.server.Config.TrustedNodes = node.config.TrustedNodes()
	}
	if node.server.Config.NodeDatabase == "" {
		node.server.Config.NodeDatabase = node.config.NodeDB()
	}

	// 检查HTTP/WS前缀是否有效。
	if err := validatePrefix("HTTP", conf.HTTPPathPrefix); err != nil {
		return nil, err
	}
	if err := validatePrefix("WebSocket", conf.WSPathPrefix); err != nil {
		return nil, err
	}

	// 配置RPC服务器。
	node.http = newHTTPServer(node.log, conf.HTTPTimeouts)
	node.httpAuth = newHTTPServer(node.log, conf.HTTPTimeouts)
	node.ws = newHTTPServer(node.log, rpc.DefaultHTTPTimeouts)
	node.wsAuth = newHTTPServer(node.log, rpc.DefaultHTTPTimeouts)
	node.ipc = newIPCServer(node.log, conf.IPCEndpoint())

	return node, nil
}

// makeGenesis基于一些预定义的水龙头帐户创建自定义的八单元genesis块。
func makeGenesis(faucets []*ecdsa.PrivateKey) *genesis.Genesis {
	genesis := genesis.DefaultRopstenGenesisBlock()

	return genesis
}
