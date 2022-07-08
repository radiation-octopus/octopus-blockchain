package block

import (
	"bytes"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"math/big"
)

var (
	receiptStatusFailedRLP     = []byte{}
	receiptStatusSuccessfulRLP = []byte{0x01}
)

const (
	// ReceiptStatusFailed是执行失败时事务的状态代码。
	ReceiptStatusFailed = uint64(0)

	// ReceiptStatusSuccessful是执行成功时事务的状态代码。
	ReceiptStatusSuccessful = uint64(1)
)

// receiptRLP是收据的一致编码。
type receiptRLP struct {
	PostStateOrStatus []byte
	CumulativeGasUsed uint64
	//Bloom             Bloom
	//Logs              []*log.OctopusLog
}

//收据代表交易的返回结果
type Receipt struct {
	// 共识领域：这些领域由黄皮书定义
	Type              uint8  `json:"type,omitempty"`
	PostState         []byte `json:"root"`
	Status            uint64 `json:"status"`
	CumulativeGasUsed uint64 `json:"cumulativeGasUsed" gencodec:"required"`
	//Bloom             Bloom  `json:"logsBloom"         gencodec:"required"`
	//Logs              []*log.OctopusLog `json:"logs"              gencodec:"required"`

	// 实现字段：这些字段由geth在处理事务时添加。它们存储在链数据库中。
	TxHash          entity.Hash    `json:"transactionHash" gencodec:"required"`
	ContractAddress entity.Address `json:"contractAddress"`
	GasUsed         uint64         `json:"gasUsed" gencodec:"required"`

	// 包含信息：这些字段提供有关包含与此收据对应的交易的信息。
	BlockHash        entity.Hash `json:"blockHash,omitempty"`
	BlockNumber      *big.Int    `json:"blockNumber,omitempty"`
	TransactionIndex uint        `json:"transactionIndex"`
}

//收据列表
type Receipts []*Receipt

// Len返回此列表中的收据数。
func (rs Receipts) Len() int { return len(rs) }

// EncodeIndex将第i个收据编码为w。
func (rs Receipts) EncodeIndex(i int, w *bytes.Buffer) {
	r := rs[i]
	data := &receiptRLP{r.statusEncoding(), r.CumulativeGasUsed}
	switch r.Type {
	case LegacyTxType:
		rlp.Encode(w, data)
	case AccessListTxType:
		w.WriteByte(AccessListTxType)
		rlp.Encode(w, data)
	case DynamicFeeTxType:
		w.WriteByte(DynamicFeeTxType)
		rlp.Encode(w, data)
	default:
		// 对于不支持的类型，不写任何内容。因为这是针对DeriveSha的，所以terr会被捕捉到将派生哈希与块匹配。
	}
}

func (r *Receipt) statusEncoding() []byte {
	if len(r.PostState) == 0 {
		if r.Status == ReceiptStatusFailed {
			return receiptStatusFailedRLP
		}
		return receiptStatusSuccessfulRLP
	}
	return r.PostState
}

// ReceiptForStorage是使用RLP序列化的回执的包装器，它省略了Bloom字段，并进行了重新计算的反序列化。
type ReceiptForStorage Receipt
