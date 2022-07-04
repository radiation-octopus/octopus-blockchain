package block

import (
	"bytes"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus/log"
	"math/big"
)

//收据代表交易的返回结果
type Receipt struct {
	TxHash  entity.Hash
	GasUsed uint64
	Logs    []*log.OctopusLog

	BlockHash        entity.Hash
	BlockNumber      *big.Int
	TransactionIndex uint
}

//收据列表
type Receipts []*Receipt

// Len返回此列表中的收据数。
func (rs Receipts) Len() int { return len(rs) }

// EncodeIndex将第i个收据编码为w。
func (rs Receipts) EncodeIndex(i int, w *bytes.Buffer) {
	//r := rs[i]
	//data := &receiptRLP{r.statusEncoding(), r.CumulativeGasUsed, r.Bloom, r.Logs}
	//switch r.Type {
	//case LegacyTxType:
	//	rlp.Encode(w, data)
	//case AccessListTxType:
	//	w.WriteByte(AccessListTxType)
	//	rlp.Encode(w, data)
	//case DynamicFeeTxType:
	//	w.WriteByte(DynamicFeeTxType)
	//	rlp.Encode(w, data)
	//default:
	//	// For unsupported types, write nothing. Since this is for
	//	// DeriveSha, the terr will be caught matching the derived hash
	//	// to the block.
	//}
}

// ReceiptForStorage是使用RLP序列化的回执的包装器，它省略了Bloom字段，并进行了重新计算的反序列化。
type ReceiptForStorage Receipt
