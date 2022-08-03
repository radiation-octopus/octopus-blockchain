package oct

type OctopusStart struct {
	//Bc        *blockchain.BlockChainStart `autoRelyonLang:"blockchain.BlockChainStart"`
	//Octell    *octell.OctellStart         `autoRelyonLang:"octell.OctellStart"`
	//Oct       *Octopus                    `autoInjectLang:"oct.Octopus"`
	//OctConfig *octconfig.Config           `autoInjectLang:"octconfig.Config"`
}

func (bc *OctopusStart) Start() {
	//bc.Oct.start(bc.OctConfig)
	//bc.Bc.DB = bc.Oct.chainDb
}
