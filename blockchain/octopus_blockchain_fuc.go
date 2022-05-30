package blockchain

import "sync"

var blockChain *BlockChain

var once sync.Once

//单例模式
func getInstance() *BlockChain {
	once.Do(func() {
		blockChain = new(BlockChain)
	})
	return blockChain
}

func Start() {
	getInstance().start()
}

func Stop() {
	getInstance().close()
}
