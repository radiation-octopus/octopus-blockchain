package node

import "github.com/radiation-octopus/octopus-blockchain/blockchain"

type NodeStart struct {
	*blockchain.BlockChainStart `autoRelyonLang:"blockchain.BlockChainStart"`
}

func (ns *NodeStart) Start() {
	Start()
}
