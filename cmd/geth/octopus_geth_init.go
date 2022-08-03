package geth

import "github.com/radiation-octopus/octopus/director"

func init() {
	//把启动注入
	director.Register(new(gethStart))
}
