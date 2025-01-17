package accounts

import (
	"crypto/ecdsa"
	crand "crypto/rand"
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/event"
	"math/big"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"time"
)

var (
	ErrLocked = NewAuthNeededError("password or unlock")

	ErrNoMatch = errors.New("no key for given address or file")

	ErrDecrypt = errors.New("could not decrypt key with given password")

	// 如果试图导入的帐户已存在于密钥库中，则返回ErrAccountAlreadyExists。
	ErrAccountAlreadyExists = errors.New("account already exists")
)

//KeyStoreType是密钥库后端的反射类型。
var KeyStoreType = reflect.TypeOf(&KeyStore{})

//钱包刷新之间的最长时间（如果文件系统通知不起作用）。
const walletRefreshCycle = 3 * time.Second

// 密钥库管理磁盘上的密钥存储目录。
type KeyStore struct {
	storage  keyStore                     // 存储后端，可能是明文或加密的
	cache    *accountCache                // 文件系统存储上的内存中帐户缓存
	changes  chan struct{}                // 从缓存接收更改通知的通道
	unlocked map[entity.Address]*unlocked // 当前解锁的帐户（解密的私钥）

	wallets     []Wallet                // 各个密钥文件周围的钱包包装
	updateFeed  event.Feed              // 通知钱包添加/删除的事件源
	updateScope event.SubscriptionScope // 订阅范围跟踪当前实时侦听器
	updating    bool                    // 事件通知循环是否正在运行

	mu       sync.RWMutex
	importMu sync.Mutex // 导入互斥锁锁定导入以防止两个插入发生冲突
}

//NewKeyStore为给定目录创建密钥库。
func NewKeyStore(keydir string, scryptN, scryptP int) *KeyStore {
	keydir, _ = filepath.Abs(keydir)
	ks := &KeyStore{storage: &keyStorePassphrase{keydir, scryptN, scryptP, false}}
	ks.init(keydir)
	return ks
}

// NewPlaintextKeyStore为给定目录创建密钥库。不推荐使用：使用NewKeyStore。
func NewPlaintextKeyStore(keydir string) *KeyStore {
	keydir, _ = filepath.Abs(keydir)
	ks := &KeyStore{storage: &keyStorePlain{keydir}}
	ks.init(keydir)
	return ks
}

func (ks *KeyStore) init(keydir string) {
	//锁定互斥锁，因为帐户缓存可能会通过事件回调
	ks.mu.Lock()
	defer ks.mu.Unlock()

	// 初始化解锁密钥集和帐户缓存
	ks.unlocked = make(map[entity.Address]*unlocked)
	ks.cache, ks.changes = newAccountCache(keydir)

	// 至ks。addressCache不保留引用，但解锁的密钥保留引用，因此在所有定时解锁过期之前，终结器不会触发。
	runtime.SetFinalizer(ks, func(m *KeyStore) {
		m.cache.close()
	})
	// 从缓存创建钱包的初始列表
	accs := ks.cache.accounts()
	ks.wallets = make([]Wallet, len(accs))
	for i := 0; i < len(accs); i++ {
		ks.wallets[i] = &keystoreWallet{account: accs[i], keystore: ks}
	}
}

// NewAccount生成一个新密钥并将其存储到密钥目录中，并使用密码短语对其进行加密。
func (ks *KeyStore) NewAccount(passphrase string) (Account, error) {
	_, account, err := storeNewKey(ks.storage, crand.Reader, passphrase)
	if err != nil {
		return Account{}, err
	}
	// 立即将帐户添加到缓存中，而不是等待文件系统通知将其提取。
	ks.cache.add(account)
	ks.refreshWallets()
	return account, nil
}

// Accounts返回目录中存在的所有密钥文件。
func (ks *KeyStore) Accounts() []Account {
	return ks.cache.accounts()
}

// Update更改现有帐户的密码短语。
func (ks *KeyStore) Update(a Account, passphrase, newPassphrase string) error {
	a, key, err := ks.getDecryptedKey(a, passphrase)
	if err != nil {
		return err
	}
	return ks.storage.StoreKey(a.URL.Path, key, newPassphrase)
}

// ImportPreSaleKey对给定的以太坊预售钱包进行解密，并将密钥文件存储在密钥目录中。
//密钥文件使用相同的密码短语加密。
func (ks *KeyStore) ImportPreSaleKey(keyJSON []byte, passphrase string) (Account, error) {
	a, _, err := importPreSaleKey(ks.storage, keyJSON, passphrase)
	if err != nil {
		return a, err
	}
	ks.cache.add(a)
	ks.refreshWallets()
	return a, nil
}

// Unlock无限期解锁给定帐户。
func (ks *KeyStore) Unlock(a Account, passphrase string) error {
	return ks.TimedUnlock(a, passphrase, 0)
}

// Import将给定的加密JSON密钥存储到密钥目录中。
func (ks *KeyStore) Import(keyJSON []byte, passphrase, newPassphrase string) (Account, error) {
	key, err := DecryptKey(keyJSON, passphrase)
	if key != nil && key.PrivateKey != nil {
		defer zeroKey(key.PrivateKey)
	}
	if err != nil {
		return Account{}, err
	}
	ks.importMu.Lock()
	defer ks.importMu.Unlock()

	if ks.cache.hasAddress(key.Address) {
		return Account{
			Address: key.Address,
		}, ErrAccountAlreadyExists
	}
	return ks.importKey(key, newPassphrase)
}

// ImportECDSA将给定的密钥存储到密钥目录中，并使用密码对其进行加密。
func (ks *KeyStore) ImportECDSA(priv *ecdsa.PrivateKey, passphrase string) (Account, error) {
	ks.importMu.Lock()
	defer ks.importMu.Unlock()

	key := newKeyFromECDSA(priv)
	if ks.cache.hasAddress(key.Address) {
		return Account{
			Address: key.Address,
		}, ErrAccountAlreadyExists
	}
	return ks.importKey(key, passphrase)
}

// 锁从内存中删除具有给定地址的私钥。
func (ks *KeyStore) Lock(addr entity.Address) error {
	ks.mu.Lock()
	if unl, found := ks.unlocked[addr]; found {
		ks.mu.Unlock()
		ks.expire(addr, unl, time.Duration(0)*time.Nanosecond)
	} else {
		ks.mu.Unlock()
	}
	return nil
}

// TimedUnlock使用密码短语解锁给定帐户。帐户在超时期间保持解锁。
//超时0将解锁帐户，直到程序退出。帐户必须与唯一密钥文件匹配。
//如果帐户地址已解锁一段时间，TimedUnlock会延长或缩短活动解锁超时。
//如果该地址之前已无限期解锁，则不会更改超时。
func (ks *KeyStore) TimedUnlock(a Account, passphrase string, timeout time.Duration) error {
	a, key, err := ks.getDecryptedKey(a, passphrase)
	if err != nil {
		return err
	}

	ks.mu.Lock()
	defer ks.mu.Unlock()
	u, found := ks.unlocked[a.Address]
	if found {
		if u.abort == nil {
			// 地址被无限期解锁，因此用超时来解锁它会让人困惑。
			zeroKey(key.PrivateKey)
			return nil
		}
		// 终止expire goroutine并在下面替换它。
		close(u.abort)
	}
	if timeout > 0 {
		u = &unlocked{Key: key, abort: make(chan struct{})}
		go ks.expire(a.Address, u, timeout)
	} else {
		u = &unlocked{Key: key}
	}
	ks.unlocked[a.Address] = u
	return nil
}

func (ks *KeyStore) expire(addr entity.Address, u *unlocked, timeout time.Duration) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-u.abort:
		// 退出就行了
	case <-t.C:
		ks.mu.Lock()
		// 只有当它仍然是发射dropLater时使用的同一个关键实例时，才可以使用dropLater。
		//我们可以使用指针相等来检查，因为每次解锁密钥时，映射都会存储一个新指针。
		if ks.unlocked[addr] == u {
			zeroKey(u.PrivateKey)
			delete(ks.unlocked, addr)
		}
		ks.mu.Unlock()
	}
}

func (ks *KeyStore) importKey(key *Key, passphrase string) (Account, error) {
	a := Account{Address: key.Address, URL: URL{Scheme: KeyStoreScheme, Path: ks.storage.JoinPath(keyFileName(key.Address))}}
	if err := ks.storage.StoreKey(a.URL.Path, key, passphrase); err != nil {
		return Account{}, err
	}
	ks.cache.add(a)
	ks.refreshWallets()
	return a, nil
}

//refreshWallets检索当前帐户列表，并在此基础上进行任何必要的钱包刷新。
func (ks *KeyStore) refreshWallets() {
	// 检索当前帐户列表
	ks.mu.Lock()
	accs := ks.cache.accounts()

	// 将当前钱包列表转换为新的钱包列表
	var (
		wallets = make([]Wallet, 0, len(accs))
		events  []WalletEvent
	)

	for _, account := range accs {
		// 当钱包在下一个帐户前面时，将其丢弃
		for len(ks.wallets) > 0 && ks.wallets[0].URL().Cmp(account.URL) < 0 {
			events = append(events, WalletEvent{Wallet: ks.wallets[0], Kind: WalletDropped})
			ks.wallets = ks.wallets[1:]
		}
		// 如果没有更多钱包或帐户在下一个之前，请包装新钱包
		if len(ks.wallets) == 0 || ks.wallets[0].URL().Cmp(account.URL) > 0 {
			wallet := &keystoreWallet{account: account, keystore: ks}

			events = append(events, WalletEvent{Wallet: wallet, Kind: WalletArrived})
			wallets = append(wallets, wallet)
			continue
		}
		// 如果帐户与第一个钱包相同，请保留它
		if ks.wallets[0].Accounts()[0] == account {
			wallets = append(wallets, ks.wallets[0])
			ks.wallets = ks.wallets[1:]
			continue
		}
	}
	//丢弃所有剩余钱包并设置新的批次
	for _, wallet := range ks.wallets {
		events = append(events, WalletEvent{Wallet: wallet, Kind: WalletDropped})
	}
	ks.wallets = wallets
	ks.mu.Unlock()

	//启动所有钱包活动并返回
	for _, event := range events {
		ks.updateFeed.Send(event)
	}
}

//SignHash为给定哈希计算ECDSA签名。生成的签名采用[R | | S | V]格式，其中V为0或1。
func (ks *KeyStore) SignHash(a Account, hash []byte) ([]byte, error) {
	// 查找要签名的密钥，如果找不到，则中止
	ks.mu.RLock()
	defer ks.mu.RUnlock()

	unlockedKey, found := ks.unlocked[a.Address]
	if !found {
		return nil, ErrLocked
	}
	// 使用普通ECDSA操作对哈希进行签名
	return crypto.Sign(hash, unlockedKey.PrivateKey)
}

// 如果与给定地址匹配的私钥可以用给定的密码短语解密，SignHashWithPassphrase将对哈希进行签名。生成的签名采用[R | | S | V]格式，其中V为0或1。
func (ks *KeyStore) SignHashWithPassphrase(a Account, passphrase string, hash []byte) (signature []byte, err error) {
	_, key, err := ks.getDecryptedKey(a, passphrase)
	if err != nil {
		return nil, err
	}
	defer zeroKey(key.PrivateKey)
	return crypto.Sign(hash, key.PrivateKey)
}

func (ks *KeyStore) getDecryptedKey(a Account, auth string) (Account, *Key, error) {
	a, err := ks.Find(a)
	if err != nil {
		return a, nil, err
	}
	key, err := ks.storage.GetKey(a.Address, a.URL.Path, auth)
	return a, key, err
}

//Find将给定帐户解析为密钥库中的唯一条目。
func (ks *KeyStore) Find(a Account) (Account, error) {
	ks.cache.maybeReload()
	ks.cache.mu.Lock()
	a, err := ks.cache.find(a)
	ks.cache.mu.Unlock()
	return a, err
}

// SignTx使用请求的帐户签署给定的交易。
func (ks *KeyStore) SignTx(a Account, tx *block2.Transaction, chainID *big.Int) (*block2.Transaction, error) {
	// 查找要签名的密钥，如果找不到，则中止
	ks.mu.RLock()
	defer ks.mu.RUnlock()

	unlockedKey, found := ks.unlocked[a.Address]
	if !found {
		return nil, ErrLocked
	}
	// 根据链ID的存在，使用2718或homestead签名
	signer := block2.LatestSignerForChainID(chainID)
	return block2.SignTx(tx, signer, unlockedKey.PrivateKey)
}

// 如果与给定地址匹配的私钥可以用给定的密码短语解密，SignTxWithPassphrase将对事务进行签名。
func (ks *KeyStore) SignTxWithPassphrase(a Account, passphrase string, tx *block2.Transaction, chainID *big.Int) (*block2.Transaction, error) {
	_, key, err := ks.getDecryptedKey(a, passphrase)
	if err != nil {
		return nil, err
	}
	defer zeroKey(key.PrivateKey)
	// 根据链ID的存在情况，使用或不使用重播保护进行签名。
	signer := block2.LatestSignerForChainID(chainID)
	return block2.SignTx(tx, signer, key.PrivateKey)
}

// 钱包实现帐户。后端，从密钥库目录返回所有单钥匙钱包。
func (ks *KeyStore) Wallets() []Wallet {
	// 确保钱包列表与帐户缓存同步
	ks.refreshWallets()

	ks.mu.RLock()
	defer ks.mu.RUnlock()

	cpy := make([]Wallet, len(ks.wallets))
	copy(cpy, ks.wallets)
	return cpy
}

//订阅实现帐户。后端，创建异步订阅以接收有关添加或删除密钥库钱包的通知。
func (ks *KeyStore) Subscribe(sink chan<- WalletEvent) event.Subscription {
	// 我们需要互斥体来可靠地启动/停止更新循环
	ks.mu.Lock()
	defer ks.mu.Unlock()

	// 订阅呼叫者并跟踪订阅者计数
	sub := ks.updateScope.Track(ks.updateFeed.Subscribe(sink))

	// 订阅服务器需要活动通知循环，请启动它
	if !ks.updating {
		ks.updating = true
		go ks.updater()
	}
	return sub
}

// 更新程序负责维护存储在密钥库中的钱包的最新列表，并启动钱包添加/删除事件。
//它侦听底层帐户缓存中的帐户更改事件，并定期强制手动刷新（仅对文件系统通知程序未运行的系统触发）。
func (ks *KeyStore) updater() {
	for {
		// 等待帐户更新或刷新超时
		select {
		case <-ks.changes:
		case <-time.After(walletRefreshCycle):
		}
		// 运行钱包刷新器
		ks.refreshWallets()

		// 如果所有订户都离开了，请停止更新程序
		ks.mu.Lock()
		if ks.updateScope.Count() == 0 {
			ks.updating = false
			ks.mu.Unlock()
			return
		}
		ks.mu.Unlock()
	}
}

type unlocked struct {
	*Key
	abort chan struct{}
}

//zeroKey将内存中的私钥归零。
func zeroKey(k *ecdsa.PrivateKey) {
	b := k.D.Bits()
	for i := range b {
		b[i] = 0
	}
}
