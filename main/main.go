package main

import (
	_ "github.com/radiation-octopus/octopus-blockchain/blockchain"
	_ "github.com/radiation-octopus/octopus-blockchain/cmd/geth"
	_ "github.com/radiation-octopus/octopus-blockchain/consensus/octell"
	_ "github.com/radiation-octopus/octopus-blockchain/log"
	_ "github.com/radiation-octopus/octopus-blockchain/node/nodeentity"
	_ "github.com/radiation-octopus/octopus-blockchain/oct"
	_ "github.com/radiation-octopus/octopus-blockchain/p2p"
	_ "github.com/radiation-octopus/octopus/api"
	"github.com/radiation-octopus/octopus/console"
	_ "github.com/radiation-octopus/octopus/db"
	"github.com/radiation-octopus/octopus/director"
	_ "github.com/radiation-octopus/octopus/tcp"
	_ "github.com/radiation-octopus/octopus/udp"
)

func main() {
	director.Start()
	console.ExecuteConsole()
}
