package oct

import (
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/consensus/octell"
	"github.com/radiation-octopus/octopus-blockchain/oct/octconfig"
)

type OctopusStart struct {
	Bc        *blockchain.BlockChainStart `autoRelyonLang:"blockchain.BlockChainStart"`
	Octell    *octell.OctellStart         `autoRelyonLang:"octell.OctellStart"`
	Oct       *Octopus                    `autoInjectLang:"oct.Octopus"`
	OctConfig *octconfig.Config           `autoInjectLang:"octconfig.Config"`
}

func (bc *OctopusStart) Start() {
	bc.Oct.start(bc.OctConfig)
}
