package oct

import (
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/consensus/octell"
)

type OctopusStart struct {
	Bc     *blockchain.BlockChainStart `autoRelyonLang:"blockchain.BlockChainStart"`
	Octell *octell.OctellStart         `autoRelyonLang:"octell.OctellStart"`
	Oct    *Octopus                    `autoInjectLang:"oct.Octopus"`
}

func (bc *OctopusStart) Start() {
	bc.Oct.start()
}
