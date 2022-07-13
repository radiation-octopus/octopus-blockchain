package octell

import (
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/log"
)

type OctellStart struct {
	ChainConfig *entity.ChainConfig `autoInjectLang:"entity.ChainConfig"`
	Octell      *Octell             `autoInjectLang:"octell.Octell"`
}

func (os OctellStart) Start() {
	log.Info("pow共识引擎启动中")
	os.Octell.octellStart(os.ChainConfig)
	log.Info("pow共识引擎启动完成")
}
