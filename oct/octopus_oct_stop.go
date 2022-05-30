package oct

import "fmt"

type OctopusStop struct {
}

func (bc *OctopusStop) Stop() {
	Stop()
	fmt.Println("BlockChain stop")
}
