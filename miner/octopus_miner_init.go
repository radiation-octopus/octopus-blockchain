package miner

import "github.com/radiation-octopus/octopus/director"

//初始化miner
func init() {
	//把启动注入
	director.Register(new(MinerStart))
	//把停止注入
	director.Register(new(MinerStop))
}
