package oct

import (
	"github.com/radiation-octopus/octopus-blockchain/blockchain/blockchainStart"
	"github.com/radiation-octopus/octopus-blockchain/consensus/octell"
	"github.com/radiation-octopus/octopus-blockchain/oct/octconfig"
)

type OctopusStart struct {
	Bc        *blockchainStart.BlockChainStart `autoRelyonLang:"blockchainStart.BlockChainStart"`
	Octell    *octell.OctellStart              `autoRelyonLang:"octell.OctellStart"`
	Oct       *Octopus                         `autoInjectLang:"oct.Octopus"`
	OctConfig *octconfig.Config                `autoInjectLang:"octconfig.Config"`
}

func (bc *OctopusStart) Start() {
	bc.Oct.start(bc.OctConfig)
}
