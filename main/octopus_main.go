package main

import (
	_ "github.com/radiation-octopus/octopus-blockchain/blockchain"
	_ "github.com/radiation-octopus/octopus-blockchain/miner"
	_ "github.com/radiation-octopus/octopus-blockchain/oct"
	_ "github.com/radiation-octopus/octopus/api"
	"github.com/radiation-octopus/octopus/core"
	_ "github.com/radiation-octopus/octopus/db"
	"github.com/radiation-octopus/octopus/director"
	"github.com/radiation-octopus/octopus/log"
	_ "github.com/radiation-octopus/octopus/log"
	_ "github.com/radiation-octopus/octopus/tcp"
	_ "github.com/radiation-octopus/octopus/udp"
)

func init() {
	mainDao := new(MainDao)
	mainService := new(MainService)
	director.Register(mainDao)
	director.Register(mainService)
}

func main() {
	director.Start()
	var testMainDao = core.GetLang("main.MainDao").(*MainDao)
	var testMainService = core.GetLang("main.MainService").(*MainService)
	log.Info(testMainDao, testMainService)
}
