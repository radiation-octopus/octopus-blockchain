package p2p

import "github.com/radiation-octopus/octopus/director"

func init() {
	//把启动注入
	director.Register(new(P2PStart))
	director.Register(new(Server))
	//把停止注入
	//director.Register(new(OctopusStop))
}
