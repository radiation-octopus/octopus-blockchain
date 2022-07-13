package oct

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/entity/hexutil"
	"github.com/radiation-octopus/octopus-blockchain/entity/math"
	"github.com/radiation-octopus/octopus-blockchain/internal/ethapi"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/rpc"
	"math/big"
)

// TransactionArgs表示构造新事务或消息调用的参数。
type TransactionArgs struct {
	From                 *entity.Address `json:"from"`
	To                   *entity.Address `json:"to"`
	Gas                  *hexutil.Uint64 `json:"gas"`
	GasPrice             *hexutil.Big    `json:"gasPrice"`
	MaxFeePerGas         *hexutil.Big    `json:"maxFeePerGas"`
	MaxPriorityFeePerGas *hexutil.Big    `json:"maxPriorityFeePerGas"`
	Value                *hexutil.Big    `json:"value"`
	Nonce                *hexutil.Uint64 `json:"nonce"`

	// 出于向后兼容的原因，我们接受“数据”和“输入”。
	//“input”是一个较新的名称，应该是客户的首选。
	Data  *hexutil.Bytes `json:"data"`
	Input *hexutil.Bytes `json:"input"`

	// 由AccessListTxType事务引入。访问列表*类型。AccessList `json：“AccessList，省略empty”`
	ChainID *hexutil.Big `json:"chainId,omitempty"`
}

// 从检索事务发送方地址。
func (args *TransactionArgs) from() entity.Address {
	if args.From == nil {
		return entity.Address{}
	}
	return *args.From
}

// 数据检索事务调用数据。首选输入字段。
func (args *TransactionArgs) data() []byte {
	if args.Input != nil {
		return *args.Input
	}
	if args.Data != nil {
		return *args.Data
	}
	return nil
}

// setDefaults为未指定的tx字段填写默认值。
func (args *TransactionArgs) setDefaults(ctx context.Context, b ethapi.Backend) error {
	if args.GasPrice != nil && (args.MaxFeePerGas != nil || args.MaxPriorityFeePerGas != nil) {
		return errors.New("both gasPrice and (maxFeePerGas or maxPriorityFeePerGas) specified")
	}
	// 伦敦之后，除非设置gasPrice，否则默认为1559
	head := b.CurrentHeader()
	// 如果用户同时指定maxPriorityfee和maxFee，那么我们不需要咨询链以了解默认值。这绝对是一个伦敦德克萨斯州。
	if args.MaxPriorityFeePerGas == nil || args.MaxFeePerGas == nil {
		// In this clause, user left some fields unspecified.
		if b.ChainConfig().IsLondon(head.Number) && args.GasPrice == nil {
			if args.MaxPriorityFeePerGas == nil {
				tip, err := b.SuggestGasTipCap(ctx)
				if err != nil {
					return err
				}
				args.MaxPriorityFeePerGas = (*hexutil.Big)(tip)
			}
			if args.MaxFeePerGas == nil {
				gasFeeCap := new(big.Int).Add(
					(*big.Int)(args.MaxPriorityFeePerGas),
					new(big.Int).Mul(head.BaseFee, big.NewInt(2)),
				)
				args.MaxFeePerGas = (*hexutil.Big)(gasFeeCap)
			}
			if args.MaxFeePerGas.ToInt().Cmp(args.MaxPriorityFeePerGas.ToInt()) < 0 {
				return fmt.Errorf("maxFeePerGas (%v) < maxPriorityFeePerGas (%v)", args.MaxFeePerGas, args.MaxPriorityFeePerGas)
			}
		} else {
			if args.MaxFeePerGas != nil || args.MaxPriorityFeePerGas != nil {
				return errors.New("maxFeePerGas or maxPriorityFeePerGas specified but london is not active yet")
			}
			if args.GasPrice == nil {
				price, err := b.SuggestGasTipCap(ctx)
				if err != nil {
					return err
				}
				if b.ChainConfig().IsLondon(head.Number) {
					// 传统的德克萨斯州天然气价格建议不应增加2倍的基本费用，因为所有费用都已消耗，因此它将导致螺旋上升。
					price.Add(price, head.BaseFee)
				}
				args.GasPrice = (*hexutil.Big)(price)
			}
		}
	} else {
		// maxPriorityfee和maxFee均由调用方设置。理智检查他们的内在联系
		if args.MaxFeePerGas.ToInt().Cmp(args.MaxPriorityFeePerGas.ToInt()) < 0 {
			return fmt.Errorf("maxFeePerGas (%v) < maxPriorityFeePerGas (%v)", args.MaxFeePerGas, args.MaxPriorityFeePerGas)
		}
	}
	if args.Value == nil {
		args.Value = new(hexutil.Big)
	}
	if args.Nonce == nil {
		nonce, err := b.GetPoolNonce(ctx, args.from())
		if err != nil {
			return err
		}
		args.Nonce = (*hexutil.Uint64)(&nonce)
	}
	if args.Data != nil && args.Input != nil && !bytes.Equal(*args.Data, *args.Input) {
		return errors.New(`both "data" and "input" are set and not equal. Please use "input" to pass transaction call data`)
	}
	if args.To == nil && len(args.data()) == 0 {
		return errors.New(`contract creation without any data provided`)
	}
	// 如有必要，估计gas用量。
	if args.Gas == nil {
		// 这些字段在估计过程中是不可变的，可以安全地直接传递指针。
		data := args.data()
		callArgs := ethapi.TransactionArgs{
			From:                 args.From,
			To:                   args.To,
			GasPrice:             args.GasPrice,
			MaxFeePerGas:         args.MaxFeePerGas,
			MaxPriorityFeePerGas: args.MaxPriorityFeePerGas,
			Value:                args.Value,
			Data:                 (*hexutil.Bytes)(&data),
			//AccessList:           args.AccessList,
		}
		pendingBlockNr := rpc.BlockNumberOrHashWithNumber(rpc.PendingBlockNumber)
		estimated, err := ethapi.DoEstimateGas(ctx, b, callArgs, pendingBlockNr, b.RPCGasCap())
		if err != nil {
			return err
		}
		args.Gas = &estimated
		log.Trace("Estimate gas usage automatically", "gas", args.Gas)
	}
	if args.ChainID == nil {
		id := (*hexutil.Big)(b.ChainConfig().ChainID)
		args.ChainID = id
	}
	return nil
}

//ToMessage将事务参数转换为核心evm使用的消息类型。
//此方法用于不需要真实实时事务的调用和跟踪。
func (args *TransactionArgs) ToMessage(globalGasCap uint64, baseFee *big.Int) (block.Message, error) {
	// 拒绝1559年前后费用样式的无效组合
	if args.GasPrice != nil && (args.MaxFeePerGas != nil || args.MaxPriorityFeePerGas != nil) {
		return block.Message{}, errors.New("both gasPrice and (maxFeePerGas or maxPriorityFeePerGas) specified")
	}
	// 设置发件人地址，如果未指定，则使用零地址。
	addr := args.from()

	// 设置默认天然气价格&如果未设置天然气价格
	gas := globalGasCap
	if gas == 0 {
		gas = uint64(math.MaxUint64 / 2)
	}
	if args.Gas != nil {
		gas = uint64(*args.Gas)
	}
	if globalGasCap != 0 && globalGasCap < gas {
		log.Warn("Caller gas above allowance, capping", "requested", gas, "cap", globalGasCap)
		gas = globalGasCap
	}
	var (
		gasPrice  *big.Int
		gasFeeCap *big.Int
		gasTipCap *big.Int
	)
	if baseFee == nil {
		// 如果没有基准费，那么必须是非1559执行
		gasPrice = new(big.Int)
		if args.GasPrice != nil {
			gasPrice = args.GasPrice.ToInt()
		}
		gasFeeCap, gasTipCap = gasPrice, gasPrice
	} else {
		// 提供了基本费用，需要1559类型执行
		if args.GasPrice != nil {
			// 用户指定的传统气田，转换为1559天然气类型
			gasPrice = args.GasPrice.ToInt()
			gasFeeCap, gasTipCap = gasPrice, gasPrice
		} else {
			// 用户指定的1559个气体场（或无），使用这些
			gasFeeCap = new(big.Int)
			if args.MaxFeePerGas != nil {
				gasFeeCap = args.MaxFeePerGas.ToInt()
			}
			gasTipCap = new(big.Int)
			if args.MaxPriorityFeePerGas != nil {
				gasTipCap = args.MaxPriorityFeePerGas.ToInt()
			}
			// 为EVM执行回填遗留gasPrice，除非我们都是零
			gasPrice = new(big.Int)
			if gasFeeCap.BitLen() > 0 || gasTipCap.BitLen() > 0 {
				gasPrice = math.BigMin(new(big.Int).Add(gasTipCap, baseFee), gasFeeCap)
			}
		}
	}
	value := new(big.Int)
	if args.Value != nil {
		value = args.Value.ToInt()
	}
	data := args.data()
	//var accessList types.AccessList
	//if args.AccessList != nil {
	//	accessList = *args.AccessList
	//}
	msg := block.NewMessage(addr, args.To, 0, value, gas, gasPrice, gasFeeCap, gasTipCap, data, true)
	return msg, nil
}

// toTransaction将参数转换为事务。这假设已调用setDefaults。
func (args *TransactionArgs) toTransaction() *block.Transaction {
	var data block.TxData
	switch {
	case args.MaxFeePerGas != nil:
		//al := types.AccessList{}
		//if args.AccessList != nil {
		//	al = *args.AccessList
		//}
		data = &block.DynamicFeeTx{
			To:        args.To,
			ChainID:   (*big.Int)(args.ChainID),
			Nonce:     uint64(*args.Nonce),
			Gas:       uint64(*args.Gas),
			GasFeeCap: (*big.Int)(args.MaxFeePerGas),
			GasTipCap: (*big.Int)(args.MaxPriorityFeePerGas),
			Value:     (*big.Int)(args.Value),
			Data:      args.data(),
			//AccessList: al,
		}
	//case args.AccessList != nil:
	//	data = &types.AccessListTx{
	//		To:         args.To,
	//		ChainID:    (*big.Int)(args.ChainID),
	//		Nonce:      uint64(*args.Nonce),
	//		Gas:        uint64(*args.Gas),
	//		GasPrice:   (*big.Int)(args.GasPrice),
	//		Value:      (*big.Int)(args.Value),
	//		Data:       args.data(),
	//		AccessList: *args.AccessList,
	//	}
	default:
		data = &block.LegacyTx{
			To:       args.To,
			Nonce:    uint64(*args.Nonce),
			Gas:      uint64(*args.Gas),
			GasPrice: (*big.Int)(args.GasPrice),
			Value:    (*big.Int)(args.Value),
			Data:     args.data(),
		}
	}
	return block.NewTx(data)
}

// ToTransaction将参数转换为事务。这假设已调用setDefaults。
func (args *TransactionArgs) ToTransaction() *block.Transaction {
	return args.toTransaction()
}
