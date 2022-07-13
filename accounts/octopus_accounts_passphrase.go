package accounts

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
	"io"
	"os"
	"path/filepath"
)

const (
	keyHeaderKDF = "scrypt"

	// StandardScryptN是Scrypt加密算法的N参数，使用256MB内存，在现代处理器上大约需要1s的CPU时间。
	StandardScryptN = 1 << 18

	// StandardScryptP是Scrypt加密算法的P参数，使用256MB内存，在现代处理器上大约需要1s的CPU时间。
	StandardScryptP = 1

	//LightScryptN是Scrypt加密算法的N参数，使用4MB内存，在现代处理器上占用大约100ms的CPU时间。
	LightScryptN = 1 << 12

	// LightScryptP是Scrypt加密算法的P参数，使用4MB内存，在现代处理器上需要大约100ms的CPU时间。
	LightScryptP = 6

	scryptR     = 8
	scryptDKLen = 32
)

type keyStorePassphrase struct {
	keysDirPath string
	scryptN     int
	scryptP     int
	//skipKeyFileVerification禁用安全功能，该功能会读取和解密任何新创建的密钥文件。在除测试之外的所有情况下，该值都应为“false”——不建议将其设置为“true”。
	skipKeyFileVerification bool
}

func (ks *keyStorePassphrase) GetKey(addr entity.Address, filename string, auth string) (*Key, error) {
	// 从密钥库加载密钥并解密其内容
	keyjson, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	key, err := DecryptKey(keyjson, auth)
	if err != nil {
		return nil, err
	}
	// 确保我们确实在对请求的密钥进行操作（无交换攻击）
	if key.Address != addr {
		return nil, fmt.Errorf("key content mismatch: have account %x, want %x", key.Address, addr)
	}
	return key, nil
}

func (ks *keyStorePassphrase) StoreKey(filename string, key *Key, auth string) error {
	keyjson, err := EncryptKey(key, auth, ks.scryptN, ks.scryptP)
	if err != nil {
		return err
	}
	//写入临时文件
	tmpName, err := writeTemporaryKeyFile(filename, keyjson)
	if err != nil {
		return err
	}
	if !ks.skipKeyFileVerification {
		//验证是否可以使用给定的密码解密文件。
		_, err = ks.GetKey(key.Address, tmpName, auth)
		if err != nil {
			msg := "An error was encountered when saving and verifying the keystore file. \n" +
				"This indicates that the keystore is corrupted. \n" +
				"The corrupted file is stored at \n%v\n" +
				"Please file a ticket at:\n\n" +
				"https://github.com/ethereum/go-ethereum/issues." +
				"The error was : %s"
			//lint：忽略ST1005这是给用户的消息
			return fmt.Errorf(msg, tmpName, err)
		}
	}
	//return os.Rename(tmpName, filename)
	return nil
}

func (ks keyStorePassphrase) JoinPath(filename string) string {
	if filepath.IsAbs(filename) {
		return filename
	}
	return filepath.Join(ks.keysDirPath, filename)
}

//DecryptKey对json blob中的密钥进行解密，并返回私钥本身。
func DecryptKey(keyjson []byte, auth string) (*Key, error) {
	// 将json解析为一个简单的映射以获取密钥版本
	m := make(map[string]interface{})
	if err := json.Unmarshal(keyjson, &m); err != nil {
		return nil, err
	}
	// 根据版本，尝试以某种方式进行分析
	var (
		keyBytes, keyId []byte
		err             error
	)
	if version, ok := m["version"].(string); ok && version == "1" {
		k := new(encryptedKeyJSONV1)
		if err := json.Unmarshal(keyjson, k); err != nil {
			return nil, err
		}
		keyBytes, keyId, err = decryptKeyV1(k, auth)
	} else {
		k := new(encryptedKeyJSONV3)
		if err := json.Unmarshal(keyjson, k); err != nil {
			return nil, err
		}
		keyBytes, keyId, err = decryptKeyV3(k, auth)
	}
	// 处理所有解密错误并返回密钥
	if err != nil {
		return nil, err
	}
	key := crypto.ToECDSAUnsafe(keyBytes)
	id, err := uuid.FromBytes(keyId)
	if err != nil {
		return nil, err
	}
	return &Key{
		Id:         id,
		Address:    crypto.PubkeyToAddress(key.PublicKey),
		PrivateKey: key,
	}, nil
}

func decryptKeyV1(keyProtected *encryptedKeyJSONV1, auth string) (keyBytes []byte, keyId []byte, err error) {
	keyUUID, err := uuid.Parse(keyProtected.Id)
	if err != nil {
		return nil, nil, err
	}
	keyId = keyUUID[:]
	mac, err := hex.DecodeString(keyProtected.Crypto.MAC)
	if err != nil {
		return nil, nil, err
	}

	iv, err := hex.DecodeString(keyProtected.Crypto.CipherParams.IV)
	if err != nil {
		return nil, nil, err
	}

	cipherText, err := hex.DecodeString(keyProtected.Crypto.CipherText)
	if err != nil {
		return nil, nil, err
	}

	derivedKey, err := getKDFKey(keyProtected.Crypto, auth)
	if err != nil {
		return nil, nil, err
	}

	calculatedMAC := crypto.Keccak256(derivedKey[16:32], cipherText)
	if !bytes.Equal(calculatedMAC, mac) {
		return nil, nil, ErrDecrypt
	}

	plainText, err := aesCBCDecrypt(crypto.Keccak256(derivedKey[:16])[:16], cipherText, iv)
	if err != nil {
		return nil, nil, err
	}
	return plainText, keyId, err
}

func decryptKeyV3(keyProtected *encryptedKeyJSONV3, auth string) (keyBytes []byte, keyId []byte, err error) {
	if keyProtected.Version != version {
		return nil, nil, fmt.Errorf("version not supported: %v", keyProtected.Version)
	}
	keyUUID, err := uuid.Parse(keyProtected.Id)
	if err != nil {
		return nil, nil, err
	}
	keyId = keyUUID[:]
	plainText, err := DecryptDataV3(keyProtected.Crypto, auth)
	if err != nil {
		return nil, nil, err
	}
	return plainText, keyId, err
}

func DecryptDataV3(cryptoJson CryptoJSON, auth string) ([]byte, error) {
	if cryptoJson.Cipher != "aes-128-ctr" {
		return nil, fmt.Errorf("cipher not supported: %v", cryptoJson.Cipher)
	}
	mac, err := hex.DecodeString(cryptoJson.MAC)
	if err != nil {
		return nil, err
	}

	iv, err := hex.DecodeString(cryptoJson.CipherParams.IV)
	if err != nil {
		return nil, err
	}

	cipherText, err := hex.DecodeString(cryptoJson.CipherText)
	if err != nil {
		return nil, err
	}

	derivedKey, err := getKDFKey(cryptoJson, auth)
	if err != nil {
		return nil, err
	}

	calculatedMAC := crypto.Keccak256(derivedKey[16:32], cipherText)
	if !bytes.Equal(calculatedMAC, mac) {
		return nil, ErrDecrypt
	}

	plainText, err := aesCTRXOR(derivedKey[:16], cipherText, iv)
	if err != nil {
		return nil, err
	}
	return plainText, err
}

// EncryptKey使用指定的scrypt参数将密钥加密到json blob中，稍后可以解密该blob。
func EncryptKey(key *Key, auth string, scryptN, scryptP int) ([]byte, error) {
	keyBytes := operationutils.PaddedBigBytes(key.PrivateKey.D, 32)
	cryptoStruct, err := EncryptDataV3(keyBytes, []byte(auth), scryptN, scryptP)
	if err != nil {
		return nil, err
	}
	encryptedKeyJSONV3 := encryptedKeyJSONV3{
		hex.EncodeToString(key.Address[:]),
		cryptoStruct,
		key.Id.String(),
		version,
	}
	return json.Marshal(encryptedKeyJSONV3)
}

//Encryptdata使用密码“auth”对作为“data”给定的数据进行加密。
func EncryptDataV3(data, auth []byte, scryptN, scryptP int) (CryptoJSON, error) {

	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		panic("reading from crypto/rand failed: " + err.Error())
	}
	derivedKey, err := scrypt.Key(auth, salt, scryptN, scryptR, scryptP, scryptDKLen)
	if err != nil {
		return CryptoJSON{}, err
	}
	encryptKey := derivedKey[:16]

	iv := make([]byte, aes.BlockSize) // 16
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		panic("reading from crypto/rand failed: " + err.Error())
	}
	cipherText, err := aesCTRXOR(encryptKey, data, iv)
	if err != nil {
		return CryptoJSON{}, err
	}
	mac := crypto.Keccak256(derivedKey[16:32], cipherText)

	scryptParamsJSON := make(map[string]interface{}, 5)
	scryptParamsJSON["n"] = scryptN
	scryptParamsJSON["r"] = scryptR
	scryptParamsJSON["p"] = scryptP
	scryptParamsJSON["dklen"] = scryptDKLen
	scryptParamsJSON["salt"] = hex.EncodeToString(salt)
	cipherParamsJSON := cipherparamsJSON{
		IV: hex.EncodeToString(iv),
	}

	cryptoStruct := CryptoJSON{
		Cipher:       "aes-128-ctr",
		CipherText:   hex.EncodeToString(cipherText),
		CipherParams: cipherParamsJSON,
		KDF:          keyHeaderKDF,
		KDFParams:    scryptParamsJSON,
		MAC:          hex.EncodeToString(mac),
	}
	return cryptoStruct, nil
}

//为什么KDF参数中的整数在解组后会变成float64而不是int？
func ensureInt(x interface{}) int {
	res, ok := x.(int)
	if !ok {
		res = int(x.(float64))
	}
	return res
}

func aesCTRXOR(key, inText, iv []byte) ([]byte, error) {
	//选择AES-128是因为encryptKey的大小。
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	stream := cipher.NewCTR(aesBlock, iv)
	outText := make([]byte, len(inText))
	stream.XORKeyStream(outText, inText)
	return outText, err
}

func aesCBCDecrypt(key, cipherText, iv []byte) ([]byte, error) {
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	decrypter := cipher.NewCBCDecrypter(aesBlock, iv)
	paddedPlaintext := make([]byte, len(cipherText))
	decrypter.CryptBlocks(paddedPlaintext, cipherText)
	plaintext := pkcs7Unpad(paddedPlaintext)
	if plaintext == nil {
		return nil, ErrDecrypt
	}
	return plaintext, err
}

func pkcs7Unpad(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}

	padding := in[len(in)-1]
	if int(padding) > len(in) || padding > aes.BlockSize {
		return nil
	} else if padding == 0 {
		return nil
	}

	for i := len(in) - 1; i > len(in)-int(padding)-1; i-- {
		if in[i] != padding {
			return nil
		}
	}
	return in[:len(in)-int(padding)]
}

func getKDFKey(cryptoJSON CryptoJSON, auth string) ([]byte, error) {
	authArray := []byte(auth)
	salt, err := hex.DecodeString(cryptoJSON.KDFParams["salt"].(string))
	if err != nil {
		return nil, err
	}
	dkLen := ensureInt(cryptoJSON.KDFParams["dklen"])

	if cryptoJSON.KDF == keyHeaderKDF {
		n := ensureInt(cryptoJSON.KDFParams["n"])
		r := ensureInt(cryptoJSON.KDFParams["r"])
		p := ensureInt(cryptoJSON.KDFParams["p"])
		return scrypt.Key(authArray, salt, n, r, p, dkLen)

	} else if cryptoJSON.KDF == "pbkdf2" {
		c := ensureInt(cryptoJSON.KDFParams["c"])
		prf := cryptoJSON.KDFParams["prf"].(string)
		if prf != "hmac-sha256" {
			return nil, fmt.Errorf("unsupported PBKDF2 PRF: %s", prf)
		}
		key := pbkdf2.Key(authArray, salt, c, dkLen, sha256.New)
		return key, nil
	}

	return nil, fmt.Errorf("unsupported KDF: %s", cryptoJSON.KDF)
}
