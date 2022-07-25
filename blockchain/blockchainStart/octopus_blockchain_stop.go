package blockchainStart

import "fmt"

type BlockChainStop struct {
}

func (bc *BlockChainStop) Stop() {
	//Stop()
	fmt.Println("BlockChain stop")
}
