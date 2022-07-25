package blockchainStart

import (
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/oct/octconfig"
	"github.com/radiation-octopus/octopus/director"
)

//初始化octopus_blockchain
func init() {
	//把启动注入
	director.Register(new(BlockChainStart))
	//把停止注入
	director.Register(new(BlockChainStop))
	director.Register(new(blockchain.BlockChain))
	//配置注入
	director.Register(new(octconfig.Config))
}
