package operationconsole

import (
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/oct"
	"math/big"
)

type TxConsole struct {
	Oct *oct.Octopus `autoInjectLang:"oct.Octopus"`
}

func (c *TxConsole) TxCmd(inMap map[string]interface{}) interface{} {
	fmt.Println(inMap)
	//循环执行交易
	key, _ := crypto.GenerateKey()
	tx, _ := block.SignTx(block.NewTransaction(0, entity.Address{}, big.NewInt(100), 100, big.NewInt(1), nil), block.HomesteadSigner{}, key)

	if err := c.Oct.TxPool().AddLocal(tx); err != nil {
		errors.New("tx fails")
	}
	return true
}
