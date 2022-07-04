package node

import "github.com/radiation-octopus/octopus/director"

func init() {
	//把启动注入
	director.Register(new(NodeStart))
	//把停止注入
	director.Register(new(NodeStop))
	//node注入
	director.Register(new(Node))
}
