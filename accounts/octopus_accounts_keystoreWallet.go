package accounts

import (
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"math/big"
)

// keystoreWallet实现这些帐户。原始密钥库的钱包接口。
type keystoreWallet struct {
	account  Account   // 此钱包中包含的单个帐户
	keystore *KeyStore // 帐户来源的密钥库
}

//URL实现帐户。钱包，返回内帐户的URL。
func (w *keystoreWallet) URL() URL {
	return w.account.URL
}

// 状态实现帐户。Wallet，返回keystore Wallet持有的帐户是否已解锁。
func (w *keystoreWallet) Status() (string, error) {
	w.keystore.mu.RLock()
	defer w.keystore.mu.RUnlock()

	if _, ok := w.keystore.unlocked[w.account.Address]; ok {
		return "Unlocked", nil
	}
	return "Locked", nil
}

// 打开机具帐户。Wallet，但它是普通钱包的noop，因为访问帐户列表不需要连接或解密步骤。
func (w *keystoreWallet) Open(passphrase string) error { return nil }

// 关闭机具帐户。钱包，但由于没有有意义的开放式操作，它是普通钱包的不二之选。
func (w *keystoreWallet) Close() error { return nil }

// Accounts实现Accounts。Wallet，返回由普通keystore Wallet包含的单个帐户组成的帐户列表。
func (w *keystoreWallet) Accounts() []Account {
	return []Account{w.account}
}

// 包含implements帐户。Wallet，返回特定帐户是否被此Wallet实例包装。
func (w *keystoreWallet) Contains(account Account) bool {
	return account.Address == w.account.Address && (account.URL == (URL{}) || account.URL == w.account.URL)
}

// 衍生工具帐户。Wallet，但它是普通钱包的noop，因为普通密钥库帐户没有层次帐户派生的概念。
//func (w *keystoreWallet) Derive(path DerivationPath, pin bool) (Account, error) {
//	return Account{}, ErrNotSupported
//}

// SelfDerive实现帐户。Wallet，但它是普通钱包的noop，因为普通密钥库帐户没有层次帐户派生的概念。
//func (w *keystoreWallet) SelfDerive(bases []DerivationPath, chain ChainStateReader) {
//}

//signHash尝试使用给定帐户对给定哈希进行签名。
//如果钱包没有包装此特定帐户，将返回一个错误以避免帐户泄漏（即使理论上我们可以通过共享密钥库后端进行签名）。
func (w *keystoreWallet) signHash(account Account, hash []byte) ([]byte, error) {
	// 确保请求的帐户包含在
	if !w.Contains(account) {
		return nil, ErrUnknownAccount
	}
	// 帐户似乎有效，请请求密钥库签名
	return w.keystore.SignHash(account, hash)
}

// SignData标志keccak256（数据）。mimetype参数描述要签名的数据类型。
func (w *keystoreWallet) SignData(account Account, mimeType string, data []byte) ([]byte, error) {
	return w.signHash(account, crypto.Keccak256(data))
}

// SignDataWithPassphrase符号keccak256（数据）。mimetype参数描述要签名的数据类型。
func (w *keystoreWallet) SignDataWithPassphrase(account Account, passphrase, mimeType string, data []byte) ([]byte, error) {
	// 确保请求的帐户包含在
	if !w.Contains(account) {
		return nil, ErrUnknownAccount
	}
	// 帐户似乎有效，请请求密钥库签名
	return w.keystore.SignHashWithPassphrase(account, passphrase, crypto.Keccak256(data))
}

// SignText实现帐户。钱包，试图用给定帐户对给定文本的哈希进行签名。
func (w *keystoreWallet) SignText(account Account, text []byte) ([]byte, error) {
	return w.signHash(account, TextHash(text))
}

// SignTextWithPassphrase实现帐户。钱包，尝试使用密码短语作为额外身份验证，使用给定帐户对给定文本的哈希进行签名。
func (w *keystoreWallet) SignTextWithPassphrase(account Account, passphrase string, text []byte) ([]byte, error) {
	// 确保请求的帐户包含在
	if !w.Contains(account) {
		return nil, ErrUnknownAccount
	}
	// 帐户似乎有效，请请求密钥库签名
	return w.keystore.SignHashWithPassphrase(account, passphrase, TextHash(text))
}

// SignTx实现帐户。钱包，尝试使用给定帐户签署给定交易。
//如果钱包没有包装此特定帐户，将返回一个错误以避免帐户泄漏（即使理论上我们可以通过共享密钥库后端进行签名）。
func (w *keystoreWallet) SignTx(account Account, tx *block2.Transaction, chainID *big.Int) (*block2.Transaction, error) {
	// 确保请求的帐户包含在
	if !w.Contains(account) {
		return nil, ErrUnknownAccount
	}
	// 帐户似乎有效，请请求密钥库签名
	return w.keystore.SignTx(account, tx, chainID)
}

// SignTxWithPassphrase实现帐户。钱包，尝试使用密码短语作为额外身份验证，使用给定帐户签署给定交易。
func (w *keystoreWallet) SignTxWithPassphrase(account Account, passphrase string, tx *block2.Transaction, chainID *big.Int) (*block2.Transaction, error) {
	// 确保请求的帐户包含在
	if !w.Contains(account) {
		return nil, ErrUnknownAccount
	}
	// 帐户似乎有效，请请求密钥库签名
	return w.keystore.SignTxWithPassphrase(account, passphrase, tx, chainID)
}
