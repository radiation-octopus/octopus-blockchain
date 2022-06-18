package operationconsole

import (
	"github.com/radiation-octopus/octopus/console"
	"github.com/radiation-octopus/octopus/director"
)

func init() {
	director.Register(new(OctAPIBackend))

	mam := make(map[string]interface{})
	mam["pass"] = ""
	mam["from"] = ""
	mam["to"] = ""
	console.BindingConsole(
		new(TxConsole),
		"TxCmd",
		mam,
		"Execute transaction",
		"tx")
	accounts := make(map[string]interface{})
	accounts["pass"] = ""
	console.BindingConsole(
		new(NewAccountConsole),
		"NewAccountCmd",
		accounts,
		"Build account",
		"newAccount")
	addOct := make(map[string]interface{})
	addOct["oct"] = ""
	addOct["from"] = ""
	console.BindingConsole(
		new(AddBalance),
		"AddBalanceCmd",
		addOct,
		"add oct",
		"addBalance")
	map1 := make(map[string]interface{})
	map1["from"] = ""
	console.BindingConsole(
		new(GetBalance),
		"GetBalanceCmd",
		map1,
		"get oct",
		"getBalance")
	miner := make(map[string]interface{})
	console.BindingConsole(
		new(MinerStart),
		"MinerStartCmd",
		miner,
		"start miner",
		"minerStart")
}
