package miner

type MinerStart struct {
}

func (bc *MinerStart) Start() {
	Start()
}
