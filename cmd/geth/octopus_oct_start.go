package geth

import (
	"fmt"
	"os"
)

type gethStart struct {
	//Bc        *blockchain.BlockChainStart `autoRelyonLang:"blockchain.BlockChainStart"`
	//Octell    *octell.OctellStart         `autoRelyonLang:"octell.OctellStart"`
	//Oct       *Octopus                    `autoInjectLang:"oct.Octopus"`
	//OctConfig *octconfig.Config           `autoInjectLang:"octconfig.Config"`
}

func (bc *gethStart) Start() {
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
