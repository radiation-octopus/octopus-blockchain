package entity

import (
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/entity/hexutil"
)

// ParseHexOrString尝试对b进行hexdecode，但如果缺少前缀，则只返回原始字节
func ParseHexOrString(str string) ([]byte, error) {
	b, err := hexutil.Decode(str)
	if errors.Is(err, hexutil.ErrMissingPrefix) {
		return []byte(str), nil
	}
	return b, err
}
