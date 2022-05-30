package miner

import "sync"

var miner *Miner
var once sync.Once

//单例模式
func getInstance() *Miner {
	once.Do(func() {
		miner = new(Miner)
	})
	return miner
}

func Start() {
	getInstance().start()
}

func Stop() {
	getInstance().close()
}
