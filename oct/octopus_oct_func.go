package oct

import "sync"

var octopus *Octopus
var once sync.Once

//单例模式
func getInstance() *Octopus {
	once.Do(func() {
		octopus = new(Octopus)
	})
	return octopus
}

func Start() {
	getInstance().start()
}

func Stop() {
	getInstance().close()
}
