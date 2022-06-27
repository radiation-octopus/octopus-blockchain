package octell

import "github.com/radiation-octopus/octopus/director"

//初始化octopus_octell
func init() {
	//配置注入
	director.Register(new(Config))
	//把共识注入
	director.Register(new(Octell))
	//把共识启动项注入
	director.Register(new(OctellStart))
	//把停止注入
	//director.Register(new(Stop))

}
