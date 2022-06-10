package operationconsole

import "github.com/radiation-octopus/octopus/console"

func init() {
	mam := make(map[string]interface{})
	mam["userid"] = ""
	console.BindingConsole(
		new(TxConsole),
		"TxCmd",
		mam,
		"Execute transaction",
		"tx")
}
