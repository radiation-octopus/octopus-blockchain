package operationdb

import (
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus/db"
	"github.com/radiation-octopus/octopus/log"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
)

//定义读取数据所需的方法
type Reader interface {
	KeyValueReader
	AncientReader
}

type KeyValueReader interface {
	//是否存在键
	IsHas(mark string, key string) (bool, error)

	//通过键检索值
	Query(mark string, key string) *block.Header
}

type KeyValueWriter interface {
	// Put inserts the given value into the key-value data store.
	Put(mark string, key string, value *big.Int) error

	// Delete removes the key from the key-value data store.
	Delete(mark string, key string)
}

func (op Database) IsHas(mark string, key string) (bool, error) {
	return op.IsHas(mark, key)
}
func (op Database) Query(mark string, key string) *block.Header {
	u := block.Header{}
	u = db.Query(mark, key, u).(block.Header)
	return &u
}

func (op Database) Put(mark string, key string, value *big.Int) error {
	return db.Insert(mark, key, value)
}

func (op Database) Delete(mark string, key string) {
	op.Delete(mark, key)
}

type AncientReader interface {
	//AncientReaderOp
	//
	//// ReadAncients runs the given read operation while ensuring that no writes take place
	//// on the underlying freezer.
	//ReadAncients(fn func(AncientReaderOp) error) (err error)
}

//检索与哈希对应的块头。
func ReadHeader(db Reader, hash entity.Hash, number uint64) *block.Header {
	return db.Query(headerMark, utils.GetInToStr(hash))
}

//检索与哈希对应的body
func ReadBody(hash entity.Hash, number uint64) *block.Body {
	b := block.Body{}
	b = db.Query(bodyMark, utils.GetInToStr(hash), b).(block.Body)
	return &b
}

// ReadBlock检索与哈希相对应的整个块，并将其从存储的标头和正文中组装回来。如果无法检索标头或正文，则返回nil。
//注意，由于头和块体的并发下载，头和规范哈希可以存储在数据库中，但主体数据（尚未）不能存储。
func ReadBlock(db Reader, hash entity.Hash, number uint64) *block.Block {
	header := ReadHeader(db, hash, number)
	if header == nil {
		return nil
	}
	body := ReadBody(hash, number)
	if body == nil {
		return nil
	}
	return block.NewBlockWithHeader(header).WithBody(body.Transactions, body.Uncles)
}

// HasBody verifies the existence of a block body corresponding to the hash.
func HasBody(db Reader, hash entity.Hash, number uint64) bool {
	//if isCanon(db, number, hash) {
	//	return true
	//}
	if has, err := db.IsHas(bodyMark, utils.GetInToStr(hash)); !has || err != nil {
		return false
	}
	return true
}

//td新增到数据库
func WriteTd(db KeyValueWriter, hash entity.Hash, number uint64, td *big.Int) {
	//data, err := rlp.EncodeToBytes(td)
	//if err != nil {
	//	log.Crit("Failed to RLP encode block total difficulty", "err", err)
	//}
	if err := db.Put(tdMark, utils.GetInToStr(hash), td); err != nil {
		errors.New("Failed to store block total difficulty")
	}
}
func WriteBlock(block *block.Block) {
	WriteBody(block.Hash(), block.Body())
	WriteHeader(block.Header())
}

//body新增到数据库
func WriteBody(hash entity.Hash, body *block.Body) {
	if err := db.Insert(tdMark, utils.GetInToStr(hash), body); err != nil {
		log.Info("Failed to store block total difficulty")
	}
}

//header新增到数据库
func WriteHeader(header *block.Header) {
	var (
		hash = header.Hash()
	)
	if err := db.Insert(headerMark, utils.GetInToStr(hash), header); err != nil {
		log.Info("Failed to store header")
	}
}

//收据新增到数据库
func WriteReceipts(hash entity.Hash, receipts block.Receipts) {
	if err := db.Insert(receiptsMark, utils.GetInToStr(hash), receipts); err != nil {
		log.Info("Failed to store header")
	}
}
