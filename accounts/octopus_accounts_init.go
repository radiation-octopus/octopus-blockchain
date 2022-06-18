package accounts

import "github.com/radiation-octopus/octopus/director"

func init() {
	//密钥库注入
	director.Register(new(Manager))
}
