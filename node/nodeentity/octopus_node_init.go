package nodeentity

import (
	"github.com/radiation-octopus/octopus-blockchain/entity/genesis"
	"github.com/radiation-octopus/octopus/director"
)

func init() {
	//把启动注入
	director.Register(new(NodeStart))
	//把停止注入
	//director.Register(new(node.NodeStop))
	//node注入
	//director.Register(new(node.Node))
	//octNode注入
	director.Register(new(OctNode))
	//创世区块注入
	director.Register(new(genesis.Genesis))
	//数据库接口注入
	//director.Register(new(typedb.Database))
}
