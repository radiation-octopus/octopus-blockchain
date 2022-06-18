package accounts

import (
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"golang.org/x/crypto/sha3"
	"math/big"
)

const (
	// 当通过USB或密钥库中的文件系统事件检测到新钱包时，将触发WalletArrived。
	WalletArrived WalletEventType = iota

	//WalletOpened在钱包成功打开时触发，目的是启动任何后台进程，如自动密钥导出。
	WalletOpened

	// WalletDropped
	WalletDropped
)

// Wallet表示可能包含一个或多个帐户（源自同一种子）的软件或硬件钱包。
type Wallet interface {
	// URL检索可访问此钱包的规范路径。上层使用它定义从多个后端到所有钱包的排序顺序。
	URL() URL

	// Status返回文本状态，以帮助用户了解钱包的当前状态。它还返回一个错误，指示钱包可能遇到的任何故障。
	Status() (string, error)

	// Open初始化对wallet实例的访问。它并不意味着解锁或解密帐户密钥，而只是建立与硬件钱包的连接和/或访问派生种子。
	//特定钱包实例的实现可能使用或不使用passphrase参数。没有无密码开放方法的原因是为了实现统一的钱包处理，而不考虑不同的后端提供商。
	//请注意，如果您打开钱包，必须将其关闭以释放任何分配的资源（使用硬件钱包时尤其重要）。
	Open(passphrase string) error

	// Close释放打开的wallet实例所持有的所有资源。
	Close() error

	// Accounts检索钱包当前知道的签名帐户列表。
	//对于分层确定性钱包，该列表不会详尽无遗，而只包含在帐户派生过程中显式固定的帐户。
	Accounts() []Account

	// 包含返回帐户是否属于此特定钱包的一部分。
	Contains(account Account) bool

	// 派生尝试在指定的派生路径上显式派生层次确定性帐户。
	//如果需要，派生帐户将添加到钱包的跟踪帐户列表中。
	//Derive(path DerivationPath, pin bool) (Account, error)

	// SelfDerive设置基本帐户派生路径，钱包尝试从中发现非零帐户，并自动将其添加到跟踪帐户列表中。
	//注意，自派生将递增指定路径的最后一个组件，而不是递减到子路径中，以允许发现从非零组件开始的帐户。
	//一些硬件钱包在进化过程中切换了派生路径，因此这种方法也支持提供多个基础来发现旧用户帐户。只有最后一个基数将用于派生下一个空帐户。
	//您可以通过使用零链状态读取器调用SelfDerive来禁用自动帐户发现。
	//SelfDerive(bases []DerivationPath, chain ethereum.ChainStateReader)

	// SignData请求钱包对给定数据的哈希进行签名，它可以仅通过包含在其中的地址来查找指定的帐户，也可以选择借助嵌入URL字段中的任何位置元数据来查找指定的帐户。
	//如果钱包需要额外的身份验证来签署请求（例如，解密帐户的密码或验证交易的PIN码），将返回AuthNeededError实例，其中包含用户需要哪些字段或操作的信息。
	//用户可以通过SignDataWithPassphrase或其他方式（例如解锁密钥库中的帐户）提供所需的详细信息来重试。
	SignData(account Account, mimeType string, data []byte) ([]byte, error)

	// SignDataWithPassphrase与SignData相同，但也接受密码
	//注意：错误的调用可能会将两个字符串弄错，并在mimetype字段中提供密码，反之亦然。
	//因此，实现永远不应回显mimetype或在错误响应中返回mimetype
	SignDataWithPassphrase(account Account, passphrase, mimeType string, data []byte) ([]byte, error)

	// SignText请求钱包对给定数据段的哈希进行签名，该数据段以以太坊前缀方案为前缀，钱包可以仅通过其包含的地址查找指定的帐户，也可以借助嵌入URL字段中的任何位置元数据进行查找。
	//如果钱包需要额外的身份验证来签署请求（例如，解密帐户的密码或验证交易的PIN码），将返回AuthNeededError实例，其中包含用户需要哪些字段或操作的信息。
	//用户可以通过SignTextWithPassphrase或其他方式（例如解锁密钥库中的帐户）提供所需的详细信息来重试。
	//此方法应以“规范”格式返回签名，v为0或1。
	SignText(account Account, text []byte) ([]byte, error)

	// SignTextWithPassphrase与Signtext相同，但也接受密码
	SignTextWithPassphrase(account Account, passphrase string, hash []byte) ([]byte, error)

	// SignTx请求钱包签署给定交易。
	//它可以单独通过其中包含的地址来查找指定的帐户，也可以选择借助嵌入URL字段中的任何位置元数据来查找指定的帐户。
	//如果钱包需要额外的身份验证来签署请求（例如，解密帐户的密码或验证交易的PIN码），将返回AuthNeededError实例，其中包含用户需要哪些字段或操作的信息。
	//用户可以通过SignTxWithPassphrase或其他方block锁密钥库中的帐户）提供所需的详细信息来重试。
	SignTx(account Account, tx *block.Transaction, chainID *big.Int) (*block.Transaction, error)

	// SignTxWithPassphrase与SignTx相同，但也接受密码
	SignTxWithPassphrase(account Account, passphrase string, tx *block.Transaction, chainID *big.Int) (*block.Transaction, error)
}

//后端是一个“钱包提供商”，其中可能包含一批他们可以签署交易的账户，并可根据请求签署交易。
type Backend interface {
	// Wallet检索后端当前知道的钱包列表。
	//默认情况下，不会打开返回的钱包。
	//对于软件高清钱包，这意味着没有解密任何基本种子，而对于硬件钱包，则没有建立任何实际连接。
	//生成的钱包列表将根据后端分配的内部URL按字母顺序排序。
	//由于钱包（尤其是硬件）可能来去不定，因此在后续检索过程中，同一个钱包可能会出现在列表中的不同位置。
	Wallets() []Wallet

	// Subscribe创建异步订阅，以便在后端检测到钱包到达或离开时接收通知。
	Subscribe(sink chan<- WalletEvent) blockchain.Subscription
}

// Account表示位于可选URL字段定义的特定位置的octopus帐户。
type Account struct {
	Address entity.Address `json:"address"` //从密钥派生的octopus帐户地址
	URL     URL            `json:"url"`     // 后端中的可选资源定位器
}

//WalletEventType表示钱包订阅子系统可以触发的不同事件类型。
type WalletEventType int

// WalletEvent是在检测到钱包到达或离开时由帐户后端触发的事件。
type WalletEvent struct {
	Wallet Wallet          // 钱包实例到达或离开
	Kind   WalletEventType // 系统中发生的事件类型
}

// TextHash是一个帮助函数，用于计算给定消息的哈希值，该哈希值可安全用于计算签名。
//哈希计算为keccak256（“\x19Octopus签名消息：\n“${Message length}${Message}”）。
//这为已签名的消息提供了上下文，并阻止了事务的签名。
func TextHash(data []byte) []byte {
	hash, _ := TextAndHash(data)
	return hash
}

//TextAndHash是一个帮助函数，用于计算给定消息的哈希，该哈希可安全用于计算签名。
//哈希计算为keccak256（“\x19Octopus签名消息：\n“${Message length}${Message}”）。
//这为已签名的消息提供了上下文，并阻止了事务的签名。
func TextAndHash(data []byte) ([]byte, string) {
	msg := fmt.Sprintf("\x19Octopus Signed Message:\n%d%s", len(data), string(data))
	hasher := sha3.NewLegacyKeccak256()
	hasher.Write([]byte(msg))
	return hasher.Sum(nil), msg
}
