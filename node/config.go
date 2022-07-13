// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package node

import (
	"crypto/ecdsa"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/p2p"
	"github.com/radiation-octopus/octopus-blockchain/p2p/enode"
	"github.com/radiation-octopus/octopus-blockchain/rpc"
)

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
	P2P p2p.Config

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
	HTTPTimeouts rpc.HTTPTimeouts

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
	Logger log.Logger `toml:",omitempty"`

	staticNodesWarning     bool
	trustedNodesWarning    bool
	oldGethResourceWarning bool

	// AllowUnprotectedTxs允许通过RPC发送非EIP-155保护的事务。
	AllowUnprotectedTxs bool `toml:",omitempty"`

	// JWTSecret是十六进制编码的jwt密钥。
	JWTSecret string `toml:",omitempty"`
}

// IPCEndpoint resolves an IPC endpoint based on a configured value, taking into
// account the set data folders as well as the designated platform we're currently
// running on.
func (c *Config) IPCEndpoint() string {
	// Short circuit if IPC has not been enabled
	if c.IPCPath == "" {
		return ""
	}
	// On windows we can only use plain top-level pipes
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(c.IPCPath, `\\.\pipe\`) {
			return c.IPCPath
		}
		return `\\.\pipe\` + c.IPCPath
	}
	// Resolve names into the data directory full paths otherwise
	if filepath.Base(c.IPCPath) == c.IPCPath {
		if c.DataDir == "" {
			return filepath.Join(os.TempDir(), c.IPCPath)
		}
		return filepath.Join(c.DataDir, c.IPCPath)
	}
	return c.IPCPath
}

// NodeDB returns the path to the discovery node database.
func (c *Config) NodeDB() string {
	if c.DataDir == "" {
		return "" // ephemeral
	}
	return c.ResolvePath(datadirNodeDatabase)
}

// DefaultIPCEndpoint returns the IPC path used by default.
func DefaultIPCEndpoint(clientIdentifier string) string {
	if clientIdentifier == "" {
		clientIdentifier = strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
		if clientIdentifier == "" {
			panic("empty executable name")
		}
	}
	config := &Config{DataDir: DefaultDataDir(), IPCPath: clientIdentifier + ".ipc"}
	return config.IPCEndpoint()
}

// HTTPEndpoint resolves an HTTP endpoint based on the configured host interface
// and port parameters.
func (c *Config) HTTPEndpoint() string {
	if c.HTTPHost == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", c.HTTPHost, c.HTTPPort)
}

// DefaultHTTPEndpoint returns the HTTP endpoint used by default.
func DefaultHTTPEndpoint() string {
	config := &Config{HTTPHost: DefaultHTTPHost, HTTPPort: DefaultHTTPPort, AuthPort: DefaultAuthPort}
	return config.HTTPEndpoint()
}

// WSEndpoint resolves a websocket endpoint based on the configured host interface
// and port parameters.
func (c *Config) WSEndpoint() string {
	if c.WSHost == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", c.WSHost, c.WSPort)
}

// DefaultWSEndpoint returns the websocket endpoint used by default.
func DefaultWSEndpoint() string {
	config := &Config{WSHost: DefaultWSHost, WSPort: DefaultWSPort}
	return config.WSEndpoint()
}

// ExtRPCEnabled returns the indicator whether node enables the external
// RPC(http, ws or graphql).
func (c *Config) ExtRPCEnabled() bool {
	return c.HTTPHost != "" || c.WSHost != ""
}

// NodeName returns the devp2p node identifier.
func (c *Config) NodeName() string {
	name := c.name()
	// Backwards compatibility: previous versions used title-cased "Geth", keep that.
	if name == "geth" || name == "geth-testnet" {
		name = "Geth"
	}
	if c.UserIdent != "" {
		name += "/" + c.UserIdent
	}
	if c.Version != "" {
		name += "/v" + c.Version
	}
	name += "/" + runtime.GOOS + "-" + runtime.GOARCH
	name += "/" + runtime.Version()
	return name
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

//对于“geth”实例，这些资源的解析方式不同。
var isOldGethResource = map[string]bool{
	"chaindata":          true,
	"nodes":              true,
	"nodekey":            true,
	"static-nodes.json":  false, //没有警告，因为他们有
	"trusted-nodes.json": false, // 拥有单独的警告。
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

func (c *Config) instanceDir() string {
	if c.DataDir == "" {
		return ""
	}
	return filepath.Join(c.DataDir, c.name())
}

// NodeKey retrieves the currently configured private key of the node, checking
// first any manually set key, falling back to the one found in the configured
// data folder. If no key can be found, a new one is generated.
func (c *Config) NodeKey() *ecdsa.PrivateKey {
	// Use any specifically configured key.
	if c.P2P.PrivateKey != nil {
		return c.P2P.PrivateKey
	}
	// Generate ephemeral key if no datadir is being used.
	if c.DataDir == "" {
		key, err := crypto.GenerateKey()
		if err != nil {
			log.Crit(fmt.Sprintf("Failed to generate ephemeral node key: %v", err))
		}
		return key
	}

	keyfile := c.ResolvePath(datadirPrivateKey)
	if key, err := crypto.LoadECDSA(keyfile); err == nil {
		return key
	}
	// No persistent key found, generate and store a new one.
	key, err := crypto.GenerateKey()
	if err != nil {
		log.Crit(fmt.Sprintf("Failed to generate node key: %v", err))
	}
	instanceDir := filepath.Join(c.DataDir, c.name())
	if err := os.MkdirAll(instanceDir, 0700); err != nil {
		log.Error(fmt.Sprintf("Failed to persist node key: %v", err))
		return key
	}
	keyfile = filepath.Join(instanceDir, datadirPrivateKey)
	if err := crypto.SaveECDSA(keyfile, key); err != nil {
		log.Error(fmt.Sprintf("Failed to persist node key: %v", err))
	}
	return key
}

// StaticNodes returns a list of node enode URLs configured as static nodes.
func (c *Config) StaticNodes() []*enode.Node {
	return c.parsePersistentNodes(&c.staticNodesWarning, c.ResolvePath(datadirStaticNodes))
}

// TrustedNodes returns a list of node enode URLs configured as trusted nodes.
func (c *Config) TrustedNodes() []*enode.Node {
	return c.parsePersistentNodes(&c.trustedNodesWarning, c.ResolvePath(datadirTrustedNodes))
}

// parsePersistentNodes parses a list of discovery node URLs loaded from a .json
// file from within the data directory.
func (c *Config) parsePersistentNodes(w *bool, path string) []*enode.Node {
	// Short circuit if no node config is present
	if c.DataDir == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	c.warnOnce(w, "Found deprecated node list file %s, please use the TOML config file instead.", path)

	// Load the nodes from the config file.
	var nodelist []string
	if err := entity.LoadJSON(path, &nodelist); err != nil {
		log.Error(fmt.Sprintf("Can't load node list file: %v", err))
		return nil
	}
	// Interpret the list as a discovery node array
	var nodes []*enode.Node
	for _, url := range nodelist {
		if url == "" {
			continue
		}
		node, err := enode.Parse(enode.ValidSchemes, url)
		if err != nil {
			log.Error(fmt.Sprintf("Node URL %s: %v\n", url, err))
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes
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

// getKeyStoreDir检索密钥目录，并在必要时创建临时目录。
func getKeyStoreDir(conf *Config) (string, bool, error) {
	keydir, err := conf.KeyDirConfig()
	if err != nil {
		return "", false, err
	}
	isEphemeral := false
	if keydir == "" {
		// 没有datadir。
		keydir, err = os.MkdirTemp("", "keystore")
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

var warnLock sync.Mutex

func (c *Config) warnOnce(w *bool, format string, args ...interface{}) {
	warnLock.Lock()
	defer warnLock.Unlock()

	if *w {
		return
	}
	l := c.Logger
	if l == nil {
		l = log.Root()
	}
	l.Warn(fmt.Sprintf(format, args...))
	*w = true
}
