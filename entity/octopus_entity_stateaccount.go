package entity

import "math/big"

//账户结构体
type StateAccount struct {
	Nonce    uint64
	Balance  *big.Int
	Root     Hash // 存储trie的merkle根
	CodeHash []byte
}
