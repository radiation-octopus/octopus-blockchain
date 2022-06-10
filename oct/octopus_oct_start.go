package oct

import "github.com/radiation-octopus/octopus-blockchain/blockchain"

type OctopusStart struct {
	Bc  *blockchain.BlockChainStart `autoRelyonLang:"blockchain.BlockChainStart"`
	Oct *Octopus                    `autoInjectLang:"oct.Octopus"`
}

func (bc *OctopusStart) Start() {
	bc.Oct.start()
}
