package main

import (
	_ "github.com/radiation-octopus/octopus-blockchain/blockchain"
	_ "github.com/radiation-octopus/octopus-blockchain/consensus/octell"
	_ "github.com/radiation-octopus/octopus-blockchain/node"
	_ "github.com/radiation-octopus/octopus-blockchain/oct"
	_ "github.com/radiation-octopus/octopus-blockchain/operationconsole"
	_ "github.com/radiation-octopus/octopus/api"
	"github.com/radiation-octopus/octopus/console"
	_ "github.com/radiation-octopus/octopus/db"
	"github.com/radiation-octopus/octopus/director"
	_ "github.com/radiation-octopus/octopus/log"
	_ "github.com/radiation-octopus/octopus/tcp"
	_ "github.com/radiation-octopus/octopus/udp"
)

func main() {
	director.Start()
	console.ExecuteConsole()
}
