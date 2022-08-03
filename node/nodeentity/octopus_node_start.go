package nodeentity

import (
	"github.com/radiation-octopus/octopus-blockchain/entity/genesis"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/oct"
)

type NodeStart struct {
	//Node 		*node.Node `autoInjectLang:"node.Node"`
	Oct     *oct.Octopus `autoInjectLang:"oct.Octopus"`
	OctNode *OctNode     `autoInjectLang:"nodeentity.OctNode"`
	//Octconfig 	*octconfig.Config
	Genesis *genesis.Genesis
}

func (ns *NodeStart) Start() {
	log.Info("node Starting")
	ns.OctNode, ns.Genesis = MainNode()
	ns.Oct = ns.OctNode.OctBackend
	//ns.DB = ns.OctNode.OctBackend.ChainDb()
	log.Info("node 启动完成")
}
