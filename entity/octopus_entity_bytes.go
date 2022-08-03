package entity

import (
	"encoding/hex"
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/entity/hexutil"
)

//FromHex返回由十六进制字符串s表示的字节。s的前缀可以是“0x”。
func FromHex(s string) []byte {
	if has0xPrefix(s) {
		s = s[2:]
	}
	if len(s)%2 == 1 {
		s = "0" + s
	}
	return Hex2Bytes(s)
}

// ParseHexOrString尝试对b进行hexdecode，但如果缺少前缀，则只返回原始字节
func ParseHexOrString(str string) ([]byte, error) {
	b, err := hexutil.Decode(str)
	if errors.Is(err, hexutil.ErrMissingPrefix) {
		return []byte(str), nil
	}
	return b, err
}

// Bytes2Hex返回d的十六进制编码。
func Bytes2Hex(d []byte) string {
	return hex.EncodeToString(d)
}

// has0xPrefix验证以“0x”或“0x”开头的str。
func has0xPrefix(str string) bool {
	return len(str) >= 2 && str[0] == '0' && (str[1] == 'x' || str[1] == 'X')
}

// Hex2Bytes返回由十六进制字符串str表示的字节。
func Hex2Bytes(str string) []byte {
	h, _ := hex.DecodeString(str)
	return h
}

// isHex验证每个字节是否为有效的十六进制字符串。
func isHex(str string) bool {
	if len(str)%2 != 0 {
		return false
	}
	for _, c := range []byte(str) {
		if !isHexCharacter(c) {
			return false
		}
	}
	return true
}

//isHexCharacter返回c的布尔值为有效的十六进制。
func isHexCharacter(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}
