package miner

import "fmt"

type MinerStop struct {
}

func (bc *MinerStop) Stop() {
	Stop()
	fmt.Println("BlockChain stop")
}
