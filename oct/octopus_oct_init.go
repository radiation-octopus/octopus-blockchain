package oct

import (
	"github.com/radiation-octopus/octopus/director"
)

func init() {
	//把启动注入
	director.Register(new(OctopusStart))
	//把停止注入
	director.Register(new(OctopusStop))
	//章鱼
	director.Register(new(Octopus))
}
