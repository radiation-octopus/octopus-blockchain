package node

import "sync"

var node *Node
var once sync.Once

//单例模式
func getInstance() *Node {
	once.Do(func() {
		node = new(Node)
	})
	return node
}

func Start() {
	getInstance().start()
}

func Stop() {
	getInstance().close()
}
