package accounts

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"github.com/google/uuid"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	version = 3

	KeyStoreScheme = "keystore"
)

type Key struct {
	// 版本4“随机”表示未从密钥数据派生的唯一id
	Id uuid.UUID
	// 为了简化查找，我们还存储地址
	Address entity.Address
	// 我们只存储privkey，因为pubkey/address可以从中派生。此结构中的privkey始终为纯文本
	PrivateKey *ecdsa.PrivateKey
}

type cipherparamsJSON struct {
	IV string `json:"iv"`
}

type CryptoJSON struct {
	Cipher       string                 `json:"cipher"`
	CipherText   string                 `json:"ciphertext"`
	CipherParams cipherparamsJSON       `json:"cipherparams"`
	KDF          string                 `json:"kdf"`
	KDFParams    map[string]interface{} `json:"kdfparams"`
	MAC          string                 `json:"mac"`
}

type encryptedKeyJSONV1 struct {
	Address string     `json:"address"`
	Crypto  CryptoJSON `json:"crypto"`
	Id      string     `json:"id"`
	Version string     `json:"version"`
}

type encryptedKeyJSONV3 struct {
	Address string     `json:"address"`
	Crypto  CryptoJSON `json:"crypto"`
	Id      string     `json:"id"`
	Version int        `json:"version"`
}

func newKey(rand io.Reader) (*Key, error) {
	privateKeyECDSA, err := ecdsa.GenerateKey(crypto.S256(), rand)
	if err != nil {
		return nil, err
	}
	return newKeyFromECDSA(privateKeyECDSA), nil
}

func newKeyFromECDSA(privateKeyECDSA *ecdsa.PrivateKey) *Key {
	id, err := uuid.NewRandom()
	if err != nil {
		panic(fmt.Sprintf("Could not create random uuid: %v", err))
	}
	key := &Key{
		Id:         id,
		Address:    crypto.PubkeyToAddress(privateKeyECDSA.PublicKey),
		PrivateKey: privateKeyECDSA,
	}
	return key
}

func storeNewKey(ks keyStore, rand io.Reader, auth string) (*Key, Account, error) {
	key, err := newKey(rand)
	if err != nil {
		return nil, Account{}, err
	}
	a := Account{
		Address: key.Address,
		URL:     URL{Scheme: KeyStoreScheme, Path: ks.JoinPath(keyFileName(key.Address))},
	}
	if err := ks.StoreKey(a.URL.Path, key, auth); err != nil {
		zeroKey(key.PrivateKey)
		return nil, a, err
	}
	return key, a, err
}

type keyStore interface {
	//从磁盘加载并解密密钥。
	GetKey(addr entity.Address, filename string, auth string) (*Key, error)
	// 写入并加密密钥。
	StoreKey(filename string, k *Key, auth string) error
	// 将文件名与键目录连接，除非它已经是绝对目录。
	JoinPath(filename string) string
}

// keyFileName实现密钥文件的命名约定：OCT--<在OCT ISO8601创建的\u>-<地址十六进制>
func keyFileName(keyAddr entity.Address) string {
	ts := time.Now().UTC()
	return fmt.Sprintf("UTC--%s--%s", toISO8601(ts), hex.EncodeToString(keyAddr[:]))
}

func toISO8601(t time.Time) string {
	var tz string
	name, offset := t.Zone()
	if name == "UTC" {
		tz = "Z"
	} else {
		tz = fmt.Sprintf("%03d00", offset/3600)
	}
	return fmt.Sprintf("%04d-%02d-%02dT%02d-%02d-%02d.%09d%s",
		t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), tz)
}

func writeTemporaryKeyFile(file string, content []byte) (string, error) {
	//创建具有适当权限的密钥库目录，以防它还不存在。
	const dirPerm = 0700
	if err := os.MkdirAll(filepath.Dir(file), dirPerm); err != nil {
		return "", err
	}
	// 原子写入：首先创建一个临时隐藏文件，然后将其移动到位。TempFile分配模式0600。
	f, err := os.Create(filepath.Dir(file) + "." + filepath.Base(file) + ".tmp")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}
