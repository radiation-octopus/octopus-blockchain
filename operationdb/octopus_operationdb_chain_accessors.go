package operationdb

import (
	"errors"
	lru "github.com/hashicorp/golang-lru"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus/db"
	"github.com/radiation-octopus/octopus/log"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
)

var (
	// headHeaderKey跟踪最新已知标头的哈希。
	headHeaderKey = "LastHeader"
	//headBlockKey跟踪最新的已知完整块哈希。
	headBlockKey = "LastBlock"
)

//检索与哈希对应的块头。
func ReadHeader(hash entity.Hash, number uint64) *block.Header {
	u := block.Header{}
	er := db.Query(headerMark, utils.GetInToStr(hash), &u).(*block.Header)
	return er
}

//检索与哈希对应的body
func ReadBody(hash entity.Hash, number uint64) *block.Body {
	b := block.Body{}
	b = db.Query(bodyMark, utils.GetInToStr(hash), b).(block.Body)
	return &b
}

// ReadBlock检索与哈希相对应的整个块，并将其从存储的标头和正文中组装回来。如果无法检索标头或正文，则返回nil。
//注意，由于头和块体的并发下载，头和规范哈希可以存储在数据库中，但主体数据（尚未）不能存储。
func ReadBlock(dbdb Reader, hash entity.Hash, number uint64) *block.Block {
	header := ReadHeader(hash, number)
	if header == nil {
		return nil
	}
	body := ReadBody(hash, number)
	if body == nil {
		return nil
	}
	return block.NewBlockWithHeader(header).WithBody(body.Transactions, body.Uncles)
}

//ReadCanonicalHash检索分配给规范块号的哈希。
func ReadCanonicalHash(number uint64) entity.Hash {
	hashStruct := &entity.HashStruct{}
	// 从leveldb通过哈希获取
	hashS := db.Query(headHeaderMark, utils.GetInToStr(number), hashStruct).(*entity.HashStruct)
	return hashS.Hash
}

//ReadHeadBlockHash检索当前规范头块的哈希。
func ReadHeadBlockHash() entity.Hash {
	ha := entity.Hash{}
	data := db.Query(headerMark, headBlockKey, ha).(entity.Hash)
	if len(data) == 0 {
		return entity.Hash{}
	}
	return data
}

// ReadHeadHeaderHash检索当前规范标头的哈希。
func ReadHeadHeaderHash(dbdb KeyValueReader) entity.Hash {
	ha := entity.Hash{}
	data, _ := db.Query(headHeaderMark, headHeaderKey, ha).(entity.Hash)
	if len(data) == 0 {
		return entity.Hash{}
	}
	return data
}

// HasBody验证是否存在与哈希对应的块体。
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
func WriteTd(hash entity.Hash, number uint64, td *big.Int) {
	//data, terr := rlp.EncodeToBytes(td)
	//if terr != nil {
	//	log.Crit("Failed to RLP encode block total difficulty", "terr", terr)
	//}
	if err := db.Insert(tdMark, utils.GetInToStr(hash), td); err != nil {
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

// WriteHeaderHash存储当前规范标头的哈希。
func WriteHeadHeaderHash(hash entity.Hash) {
	if err := db.Insert(headHeaderMark, headHeaderKey, hash); err != nil {
		log.Info("Failed to store last header's hash", "err", err)
	}
}

// WriteCanonicalHash存储分配给规范块号的哈希。
func WriteCanonicalHash(hash entity.Hash, number uint64) {
	hashStruct := &entity.HashStruct{
		Hash: hash,
	}
	if err := db.Insert(headHeaderMark, utils.GetInToStr(number), hashStruct); err != nil {
		log.Info("Failed to store number to hash mapping", "err", err)
	}
}

// WriteTxLookupEntriesByBlock为块中的每个事务存储位置元数据，支持基于哈希的事务和收据查找。
func WriteTxLookupEntriesByBlock(block *block.Block) {
	numberBytes := block.Number().Bytes()
	for _, tx := range block.Transactions() {
		writeTxLookupEntry(tx.Hash(), numberBytes)
	}
}

// writeTxLookupEntry存储事务的位置元数据，支持基于哈希的事务和收据查找。
func writeTxLookupEntry(hash entity.Hash, numberBytes []byte) {
	if err := db.Insert(txMark, utils.GetInToStr(hash), numberBytes); err != nil {
		log.Info("Failed to store transaction lookup entry", "err", err)
	}
}

//WriteHeadBlockHash存储头块的哈希。
func WriteHeadBlockHash(hash entity.Hash) {
	if err := db.Insert(headBlockMark, headBlockKey, hash); err != nil {
		log.Info("Failed to store last block's hash", "err", err)
	}
}

// NewDatabase为状态创建备份存储。返回的数据库可以安全地并发使用，但不会在内存中保留任何最近的trie节点。
//要在内存中保留一些历史状态，请使用NewDatabaseWithConfig构造函数。
func NewDatabase(db Database) DatabaseI {
	return NewDatabaseWithConfig(db, nil)
}

// NewDatabaseWithConfig为状态创建备份存储。
//返回的数据库可以安全地并发使用，并在大型内存缓存中保留大量折叠的RLP trie节点。
func NewDatabaseWithConfig(db Database, config *Config) DatabaseI {
	csc, _ := lru.New(codeSizeCacheSize)
	return &cachingDB{
		db:            trieNewDatabaseWithConfig(db, config),
		codeSizeCache: csc,
		//codeCache:     fastcache.New(codeCacheSize),
	}
}
