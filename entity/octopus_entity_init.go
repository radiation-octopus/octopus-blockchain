package entity

import "github.com/radiation-octopus/octopus/director"

func init() {
	//链配置注入
	director.Register(new(ChainConfig))
}
