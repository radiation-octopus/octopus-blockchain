package blockchain

import (
	"github.com/radiation-octopus/octopus/db"
	"github.com/radiation-octopus/octopus/log"
)

//区块链启动配置cfg结构体
type BlockChainStart struct {
	Db *db.DbStart `autoRelyonLang:"db.DbStart"`
	Bc *BlockChain `autoInjectLang:"blockchain.BlockChain"`
}

func (bc *BlockChainStart) Start() {
	log.Info("blockchain Starting")
	bc.Bc.start()
	log.Info("blockchain 启动完成")
}
