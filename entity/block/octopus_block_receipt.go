package block

import (
	"bytes"
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
	"unsafe"
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
	//Logs              []*log.Logger
}

//收据代表交易的返回结果
type Receipt struct {
	// 共识领域：这些领域由黄皮书定义
	Type              uint8  `json:"type,omitempty"`
	PostState         []byte `json:"root"`
	Status            uint64 `json:"status"`
	CumulativeGasUsed uint64 `json:"cumulativeGasUsed" gencodec:"required"`
	//Bloom             Bloom  `json:"logsBloom"         gencodec:"required"`
	Logs []*Log `json:"logs"              gencodec:"required"`

	// 实现字段：这些字段由geth在处理事务时添加。它们存储在链数据库中。
	TxHash          entity.Hash    `json:"transactionHash" gencodec:"required"`
	ContractAddress entity.Address `json:"contractAddress"`
	GasUsed         uint64         `json:"gasUsed" gencodec:"required"`

	// 包含信息：这些字段提供有关包含与此收据对应的交易的信息。
	BlockHash        entity.Hash `json:"blockHash,omitempty"`
	BlockNumber      *big.Int    `json:"blockNumber,omitempty"`
	TransactionIndex uint        `json:"transactionIndex"`
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

// encodeTyped将已键入回执的规范编码写入w。
func (r *Receipt) encodeTyped(data *receiptRLP, w *bytes.Buffer) error {
	w.WriteByte(r.Type)
	return rlp.Encode(w, data)
}

// MarshalBinary返回回执的一致编码。
func (r *Receipt) MarshalBinary() ([]byte, error) {
	if r.Type == LegacyTxType {
		return rlp.EncodeToBytes(r)
	}
	data := &receiptRLP{r.statusEncoding(), r.CumulativeGasUsed}
	var buf bytes.Buffer
	err := r.encodeTyped(data, &buf)
	return buf.Bytes(), err
}

// 大小返回所有内部内容使用的近似内存。它用于近似和限制各种缓存的内存消耗。
func (r *Receipt) Size() utils.StorageSize {
	size := utils.StorageSize(unsafe.Sizeof(*r)) + utils.StorageSize(len(r.PostState))
	size += utils.StorageSize(len(r.Logs)) * utils.StorageSize(unsafe.Sizeof(Log{}))
	for _, log := range r.Logs {
		size += utils.StorageSize(len(log.Topics)*entity.HashLength + len(log.Data))
	}
	return size
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

// DeriveFields使用基于共识数据和上下文信息（如包含块和事务）的计算字段填充收据。
func (rs Receipts) DeriveFields(config *entity.ChainConfig, hash entity.Hash, number uint64, txs Transactions) error {
	signer := MakeSigner(new(big.Int).SetUint64(number))

	logIndex := uint(0)
	if len(txs) != len(rs) {
		return errors.New("transaction and receipt count mismatch")
	}
	for i := 0; i < len(rs); i++ {
		// The transaction type and hash can be retrieved from the transaction itself
		rs[i].Type = txs[i].Type()
		rs[i].TxHash = txs[i].Hash()

		// block location fields
		rs[i].BlockHash = hash
		rs[i].BlockNumber = new(big.Int).SetUint64(number)
		rs[i].TransactionIndex = uint(i)

		// The contract address can be derived from the transaction itself
		if txs[i].To() == nil {
			// Deriving the signer is expensive, only do if it's actually needed
			from, _ := Sender(signer, txs[i])
			rs[i].ContractAddress = crypto.CreateAddress(from, txs[i].Nonce())
		}
		// The used gas can be calculated based on previous r
		if i == 0 {
			rs[i].GasUsed = rs[i].CumulativeGasUsed
		} else {
			rs[i].GasUsed = rs[i].CumulativeGasUsed - rs[i-1].CumulativeGasUsed
		}
		// 可以简单地从块和事务中设置派生的日志字段
		for j := 0; j < len(rs[i].Logs); j++ {
			rs[i].Logs[j].BlockNumber = number
			rs[i].Logs[j].BlockHash = hash
			rs[i].Logs[j].TxHash = rs[i].TxHash
			rs[i].Logs[j].TxIndex = uint(i)
			rs[i].Logs[j].Index = logIndex
			logIndex++
		}
	}
	return nil
}

// ReceiptForStorage是使用RLP序列化的回执的包装器，它省略了Bloom字段，并进行了重新计算的反序列化。
type ReceiptForStorage Receipt
