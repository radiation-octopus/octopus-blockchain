package blockchain

import (
	"github.com/radiation-octopus/octopus-blockchain/node"
	"github.com/radiation-octopus/octopus-blockchain/oct/octconfig"
	"github.com/radiation-octopus/octopus/db"
	"github.com/radiation-octopus/octopus/log"
)

//区块链启动配置cfg结构体
type BlockChainStart struct {
	Db        *db.DbStart       `autoRelyonLang:"db.DbStart"`
	Bc        *BlockChain       `autoInjectLang:"blockchain.BlockChain"`
	NodeStart *node.NodeStart   `autoRelyonLang:"node.NodeStart"`
	OctConfig *octconfig.Config `autoInjectLang:"octconfig.Config"`
}

func (bc *BlockChainStart) Start() {
	log.Info("blockchain Starting")
	bc.Bc.start(bc.NodeStart.Node, bc.OctConfig)
	log.Info("blockchain 启动完成")
}
