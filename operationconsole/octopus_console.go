package operationconsole

import (
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/accounts"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/oct"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus/log"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
	"runtime"
	"strconv"
)

//交易
type TxConsole struct {
	Oct           *oct.Octopus   `autoInjectLang:"oct.Octopus"`
	OctAPIBackend *OctAPIBackend `autoInjectLang:"operationconsole.OctAPIBackend"`
}

func (c *TxConsole) TxCmd(inMap map[string]interface{}) interface{} {
	fmt.Println(inMap)
	c.OctAPIBackend.oct = c.Oct
	c.OctAPIBackend.allowUnprotectedTxs = true
	fr := entity.HexToAddress(utils.GetInToStr(inMap["from"]))
	to := entity.HexToAddress(utils.GetInToStr(inMap["to"]))
	args := &block.TransactionArgs{
		From: &fr,
		To:   &to,
	}
	signtx, err := c.signTransaction(args, utils.GetInToStr(inMap["pass"]))
	if err != nil {
		return err
	}
	ha, err := SubmitTransaction(c.OctAPIBackend, signtx)
	if err != nil {
		return err
	}
	return ha
	//循环执行交易
	//key, _ := crypto.GenerateKey()
	//bu := utils.Hex2Bytes("0xa973FAc53c768B388e0EB8d002ABf0aCE92E26f4")
	//add := entity.BytesToAddress(bu)
	//tx, _ := block.SignTx(block.NewTransaction(0, add, big.NewInt(100), 100, big.NewInt(1), nil), block.HomesteadSigner{}, key)

	//if err := c.Oct.TxPool().AddLocal(tx); err != nil {
	//	log.Error(err)
	//}

}

// signTransaction设置默认值并对给定事务进行签名
//注意：调用方需要确保保留非锁定（如果适用），并在事务提交到发送池后释放它
func (s *TxConsole) signTransaction(args *block.TransactionArgs, passwd string) (*block.Transaction, error) {

	//查找包含请求签名者的钱包
	account := accounts.Account{Address: args.FromAddr()}
	wallet, err := s.Oct.AccountManager().Find(account)
	if err != nil {
		return nil, err
	}
	// 设置一些健全默认值并在失败时终止
	//if err := args.setDefaults(ctx, s.b); err != nil {
	//	return nil, err
	//}
	//初始化交易默认值
	no := operationutils.Uint64(0)
	args.Nonce = &no
	gas := operationutils.Uint64(22000)
	args.Gas = &gas
	gp := big.NewInt(1)
	args.GasPrice = (*operationutils.Big)(gp)
	v := big.NewInt(3)
	args.Value = (*operationutils.Big)(v)
	mpfpg := big.NewInt(123)
	args.MaxPriorityFeePerGas = (*operationutils.Big)(mpfpg)
	mfpg := big.NewInt(123)
	args.MaxFeePerGas = (*operationutils.Big)(mfpg)

	// 组装交易并使用钱包签名
	tx := args.ToTransaction()

	return wallet.SignTxWithPassphrase(account, passwd, tx, s.Oct.Blockchain.Config().ChainID)
}

// SubmitTransaction是一个助手函数，它将tx提交给txPool并记录消息。
func SubmitTransaction(b Backend, tx *block.Transaction) (entity.Hash, error) {
	// 如果已指定交易费用上限，请确保给定交易的费用合理。
	if err := checkTxFee(tx.GasPrice(), tx.Gas(), b.RPCTxFeeCap()); err != nil {
		return entity.Hash{}, err
	}
	if !b.UnprotectedAllowed() && !tx.Protected() {
		// 如果设置了EIP155Required，请确保仅提交eip155签名的事务。
		return entity.Hash{}, errors.New("only replay-protected (EIP-155) transactions allowed over RPC")
	}
	if err := b.SendTx(tx); err != nil {
		return entity.Hash{}, err
	}
	// 打印包含完整tx详细信息的日志，用于手动调查和干预
	signer := block.MakeSigner(b.CurrentBlock().Number())
	from, err := block.Sender(signer, tx)
	if err != nil {
		return entity.Hash{}, err
	}

	if tx.To() == nil {
		//addr := crypto.CreateAddress(from, tx.Nonce())
		log.Info("Submitted contract creation", "hash", tx.Hash().Hex(), "from", from, "nonce", tx.Nonce(), "value", tx.Value())
	} else {
		log.Info("Submitted transaction", "hash", tx.Hash().Hex(), "from", from, "nonce", tx.Nonce(), "recipient", tx.To(), "value", tx.Value())
	}
	return tx.Hash(), nil
}

// checkTxFee是一个内部函数，用于检查给定交易的费用是否合理（在上限下）。
func checkTxFee(gasPrice *big.Int, gas uint64, cap float64) error {
	// 如果交易费没有上限，则短路。
	if cap == 0 {
		return nil
	}
	feeEth := new(big.Float).Quo(new(big.Float).SetInt(new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gas))), new(big.Float).SetInt(big.NewInt(entity.Octcao)))
	feeFloat, _ := feeEth.Float64()
	if feeFloat > cap {
		return fmt.Errorf("tx fee (%.2f ether) exceeds the configured cap (%.2f ether)", feeFloat, cap)
	}
	return nil
}

//创建账户
type NewAccountConsole struct {
	Oct *oct.Octopus `autoInjectLang:"oct.Octopus"`
}

func (a *NewAccountConsole) NewAccountCmd(inMap map[string]interface{}) interface{} {
	fmt.Println(inMap)
	ks, err := fetchKeystore(a.Oct.AccountManager())
	if err != nil {
		log.Error(err)
		return err
	}
	account, _ := ks.NewAccount(utils.GetInToStr(inMap["pass"]))
	return account.Address.String()
}

// fetchKeystore从帐户管理器检索加密的密钥库。
func fetchKeystore(am *accounts.Manager) (*accounts.KeyStore, error) {
	if ks := am.Backends(accounts.KeyStoreType); len(ks) > 0 {
		return ks[0].(*accounts.KeyStore), nil
	}
	return nil, errors.New("local keystore not used")
}

//测试交易，添加余额
type AddBalance struct {
	Oct *oct.Octopus `autoInjectLang:"oct.Octopus"`
}

func (a *AddBalance) AddBalanceCmd(inMap map[string]interface{}) interface{} {
	oct := inMap["oct"].(string)
	octI, _ := strconv.ParseInt(oct, 10, 64)
	//blockchain.SetNonce(a.Oct.TxPool(), entity.HexToAddress(utils.GetInToStr(inMap["from"])), 1)

	blockchain.AddBalance(a.Oct.TxPool(), entity.HexToAddress(utils.GetInToStr(inMap["from"])), big.NewInt(octI))
	log.Info(inMap["from"], "add:", oct)
	return true
}

//查询余额
type GetBalance struct {
	Oct *oct.Octopus `autoInjectLang:"oct.Octopus"`
}

func (a *GetBalance) GetBalanceCmd(inMap map[string]interface{}) interface{} {
	from := inMap["from"]
	ba := a.Oct.TxPool().GetBalance(entity.HexToAddress(utils.GetInToStr(from)))
	log.Info(inMap["from"], "余额：", ba)
	return true
}

type MinerStart struct {
	Oct *oct.Octopus `autoInjectLang:"oct.Octopus"`
}

func (a MinerStart) MinerStartCmd(inMap map[string]interface{}) interface{} {
	//from := inMap["from"]
	a.Oct.StartMining(runtime.NumCPU())
	return true
}
