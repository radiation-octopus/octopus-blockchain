package rawdb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus/log"
	"math/big"
)

// ReadHeaderRLP在其原始RLP数据库编码中检索块头。
func ReadHeaderRLP(db typedb.Reader, hash entity.Hash, number uint64) rlp.RawValue {
	var data []byte
	db.ReadAncients(func(reader typedb.AncientReaderOp) error {
		// 首先尝试在古代数据库中查找数据。由于古代数据库只维护规范数据，所以需要进行额外的哈希比较。
		data, _ = reader.Ancient(freezerHeaderTable, number)
		if len(data) > 0 && crypto.Keccak256Hash(data) == hash {
			return nil
		}
		// 如果没有，请尝试从leveldb读取
		data, _ = db.Get(headerKey(number, hash))
		return nil
	})
	return data
}

// isCanon是一种内部实用方法，用于检查给定的数字/哈希是否是古代（canon）集的一部分。
func isCanon(reader typedb.AncientReaderOp, number uint64, hash entity.Hash) bool {
	h, err := reader.Ancient(freezerHashTable, number)
	if err != nil {
		return false
	}
	return bytes.Equal(h, hash[:])
}

// ReadBodyRLP检索RLP编码中的块体（事务和未结项）。
func ReadBodyRLP(db typedb.Reader, hash entity.Hash, number uint64) rlp.RawValue {
	// 首先尝试在古代数据库中查找数据。由于古代数据库只维护规范数据，所以需要进行额外的哈希比较。
	var data []byte
	db.ReadAncients(func(reader typedb.AncientReaderOp) error {
		// 检查数据是否为旧的数据
		if isCanon(reader, number, hash) {
			data, _ = reader.Ancient(freezerBodiesTable, number)
			return nil
		}
		// 如果没有，请尝试从leveldb读取
		data, _ = db.Get(blockBodyKey(number, hash))
		return nil
	})
	return data
}

// ReadReceiptsRLP检索属于RLP编码中的块的所有事务收据。
func ReadReceiptsRLP(db typedb.Reader, hash entity.Hash, number uint64) rlp.RawValue {
	var data []byte
	db.ReadAncients(func(reader typedb.AncientReaderOp) error {
		// 检查数据是否为古代数据
		if isCanon(reader, number, hash) {
			data, _ = reader.Ancient(freezerReceiptTable, number)
			return nil
		}
		// 如果没有，请尝试从leveldb读取
		data, _ = db.Get(blockReceiptsKey(number, hash))
		return nil
	})
	return data
}

// ReadTdRLP检索与RLP编码中的哈希对应的块的总难度。
func ReadTdRLP(db typedb.Reader, hash entity.Hash, number uint64) rlp.RawValue {
	var data []byte
	db.ReadAncients(func(reader typedb.AncientReaderOp) error {
		// 检查数据是否为古代数据
		if isCanon(reader, number, hash) {
			data, _ = reader.Ancient(freezerDifficultyTable, number)
			return nil
		}
		// 如果没有，请尝试从leveldb读取
		data, _ = db.Get(headerTDKey(number, hash))
		return nil
	})
	return data
}

//检索与哈希对应的块头。
func ReadHeader(db typedb.Reader, hash entity.Hash, number uint64) *block2.Header {
	data := ReadHeaderRLP(db, hash, number)
	if len(data) == 0 {
		return nil
	}
	header := new(block2.Header)
	if err := rlp.Decode(bytes.NewReader(data), header); err != nil {
		log.Error("Invalid block header RLP", "hash", hash, "err", err)
		return nil
	}
	return header
}

// ReadHeaderNumber返回分配给哈希的标头编号。
func ReadHeaderNumber(db typedb.KeyValueReader, hash entity.Hash) *uint64 {
	data, _ := db.Get(headerNumberKey(hash))
	if len(data) != 8 {
		return nil
	}
	number := binary.BigEndian.Uint64(data)
	return &number
}

//检索与哈希对应的body
func ReadBody(db typedb.Reader, hash entity.Hash, number uint64) *block2.Body {
	data := ReadBodyRLP(db, hash, number)
	if len(data) == 0 {
		return nil
	}
	body := new(block2.Body)
	if err := rlp.Decode(bytes.NewReader(data), body); err != nil {
		log.Error("Invalid block body RLP", "hash", hash, "err", err)
		return nil
	}
	return body
}

// ReadBlock检索与哈希相对应的整个块，并将其从存储的标头和正文中组装回来。如果无法检索标头或正文，则返回nil。
//注意，由于头和块体的并发下载，头和规范哈希可以存储在数据库中，但主体数据（尚未）不能存储。
func ReadBlock(db typedb.Reader, hash entity.Hash, number uint64) *block2.Block {
	header := ReadHeader(db, hash, number)
	if header == nil {
		return nil
	}
	body := ReadBody(db, hash, number)
	if body == nil {
		return nil
	}
	return block2.NewBlockWithHeader(header).WithBody(body.Transactions, body.Uncles)
}

//ReadCanonicalHash检索分配给规范块号的哈希。
func ReadCanonicalHash(db typedb.Reader, number uint64) entity.Hash {
	var data []byte
	db.ReadAncients(func(reader typedb.AncientReaderOp) error {
		data, _ = reader.Ancient(freezerHashTable, number)
		if len(data) == 0 {
			// 从leveldb通过哈希获取
			data, _ = db.Get(headerHashKey(number))
		}
		return nil
	})
	return entity.BytesToHash(data)
}

//ReadHeadBlockHash检索当前规范头块的哈希。
func ReadHeadBlockHash(db typedb.KeyValueReader) entity.Hash {
	data, _ := db.Get(headBlockKey)
	if len(data) == 0 {
		return entity.Hash{}
	}
	return entity.BytesToHash(data)
}

// ReadHeadHeaderHash检索当前规范标头的哈希。
func ReadHeadHeaderHash(db typedb.KeyValueReader) entity.Hash {
	data, _ := db.Get(headHeaderKey)
	if len(data) == 0 {
		return entity.Hash{}
	}
	return entity.BytesToHash(data)
}

// ReadTd检索与哈希对应的块的总难度。
func ReadTd(db typedb.Reader, hash entity.Hash, number uint64) *big.Int {
	data := ReadTdRLP(db, hash, number)
	if len(data) == 0 {
		return nil
	}
	td := new(big.Int)
	if err := rlp.Decode(bytes.NewReader(data), td); err != nil {
		log.Error("Invalid block total difficulty RLP", "hash", hash, "err", err)
		return nil
	}
	return td
}

// ReadAllHashes检索指定给特定高度的块的所有哈希，包括规范叉和重新排序叉。
func ReadAllHashes(db typedb.Iteratee, number uint64) []entity.Hash {
	prefix := headerKeyPrefix(number)

	hashes := make([]entity.Hash, 0, 1)
	it := db.NewIterator(prefix, nil)
	defer it.Release()

	for it.Next() {
		if key := it.Key(); len(key) == len(prefix)+32 {
			hashes = append(hashes, entity.BytesToHash(key[len(key)-32:]))
		}
	}
	return hashes
}

// HasBody验证是否存在与哈希对应的块体。
func HasBody(db typedb.Reader, hash entity.Hash, number uint64) bool {
	if isCanon(db, number, hash) {
		return true
	}
	if has, err := db.IsHas(blockBodyKey(number, hash)); !has || err != nil {
		return false
	}
	return true
}

//td新增到数据库
func WriteTd(db typedb.KeyValueWriter, hash entity.Hash, number uint64, td *big.Int) {
	data, terr := rlp.EncodeToBytes(td)
	if terr != nil {
		log.Info("Failed to RLP encode block total difficulty", "terr", terr)
	}
	if err := db.Put(headerTDKey(number, hash), data); err != nil {
		errors.New("Failed to store block total difficulty")
	}
}
func WriteBlock(db typedb.KeyValueWriter, block *block2.Block) {
	WriteBody(db, block.Hash(), block.NumberU64(), block.Body())
	WriteHeader(db, block.Header())
}

// WriteBodyRLP将RLP编码的块体存储到数据库中。
func WriteBodyRLP(db typedb.KeyValueWriter, hash entity.Hash, number uint64, rlp rlp.RawValue) {
	if err := db.Put(blockBodyKey(number, hash), rlp); err != nil {
		log.Info("Failed to store block body", "err", err)
	}
}

//body新增到数据库
func WriteBody(db typedb.KeyValueWriter, hash entity.Hash, number uint64, body *block2.Body) {
	data, err := rlp.EncodeToBytes(body)
	if err != nil {
		log.Info("Failed to store block total difficulty", "err", err)
	}
	WriteBodyRLP(db, hash, number, data)
}

//header新增到数据库
func WriteHeader(db typedb.KeyValueWriter, header *block2.Header) {
	var (
		hash   = header.Hash()
		number = header.Number.Uint64()
	)
	// 写入哈希->数字映射
	WriteHeaderNumber(db, hash, number)
	// 写入编码的标头
	data, err := rlp.EncodeToBytes(header)
	if err != nil {
		log.Info("Failed to RLP encode header", "err", err)
	}
	key := headerKey(number, hash)
	if err := db.Put(key, data); err != nil {
		log.Info("Failed to store header", "err", err)
	}
}

// WriteHeaderNumber存储哈希->数字映射。
func WriteHeaderNumber(db typedb.KeyValueWriter, hash entity.Hash, number uint64) {
	key := headerNumberKey(hash)
	enc := encodeBlockNumber(number)
	if err := db.Put(key, enc); err != nil {
		log.Info("Failed to store hash to number mapping", "err", err)
	}
}

//收据新增到数据库
func WriteReceipts(db typedb.KeyValueWriter, hash entity.Hash, number uint64, receipts block2.Receipts) {
	// 将收据转换为其存储形式并序列化
	storageReceipts := make([]*block2.ReceiptForStorage, len(receipts))
	for i, receipt := range receipts {
		storageReceipts[i] = (*block2.ReceiptForStorage)(receipt)
	}
	bytes, err := rlp.EncodeToBytes(storageReceipts)
	if err != nil {
		log.Info("Failed to encode block receipts", "err", err)
	}
	// 存储扁平收据切片
	if err := db.Put(blockReceiptsKey(number, hash), bytes); err != nil {
		log.Info("Failed to store block receipts", "err", err)
	}
}

// WriteHeaderHash存储当前规范标头的哈希。
func WriteHeadHeaderHash(db typedb.KeyValueWriter, hash entity.Hash) {
	if err := db.Put(headHeaderKey, hash.Bytes()); err != nil {
		log.Info("Failed to store last header's hash", "err", err)
	}
}

// WriteCanonicalHash存储分配给规范块号的哈希。
func WriteCanonicalHash(db typedb.KeyValueWriter, hash entity.Hash, number uint64) {
	if err := db.Put(headerHashKey(number), hash.Bytes()); err != nil {
		log.Info("Failed to store number to hash mapping", "err", err)
	}
}

// WriteTxLookupEntriesByBlock为块中的每个事务存储位置元数据，支持基于哈希的事务和收据查找。
func WriteTxLookupEntriesByBlock(db typedb.KeyValueWriter, block *block2.Block) {
	numberBytes := block.Number().Bytes()
	for _, tx := range block.Transactions() {
		writeTxLookupEntry(db, tx.Hash(), numberBytes)
	}
}

// writeTxLookupEntry存储事务的位置元数据，支持基于哈希的事务和收据查找。
func writeTxLookupEntry(db typedb.KeyValueWriter, hash entity.Hash, numberBytes []byte) {
	if err := db.Put(txLookupKey(hash), numberBytes); err != nil {
		log.Info("Failed to store transaction lookup entry", "err", err)
	}
}

//WriteHeadBlockHash存储头块的哈希。
func WriteHeadBlockHash(db typedb.KeyValueWriter, hash entity.Hash) {
	if err := db.Put(headBlockKey, hash.Bytes()); err != nil {
		log.Info("Failed to store last block's hash", "err", err)
	}
}

// WriteCode写入提供的合同代码数据库。
func WriteCode(db typedb.KeyValueWriter, hash entity.Hash, code []byte) {
	if err := db.Put(codeKey(hash), code); err != nil {
		log.Info("Failed to store contract code", "err", err)
	}
}

//WritePreimages将提供的前映像集写入数据库。
//func WritePreimages(db KeyValueWriter, preimages map[entity.Hash][]byte) {
//	for hash, preimage := range preimages {
//		if err := db.Put(preimageKey(hash), preimage); err != nil {
//			log.Info("Failed to store trie preimage", "err", err)
//		}
//	}
//	//preimageCounter.Inc(int64(len(preimages)))
//	//preimageHitCounter.Inc(int64(len(preimages)))
//}

// WriteGenesisState将genesis状态写入磁盘。
func WriteGenesisState(db typedb.KeyValueWriter, hash entity.Hash, data []byte) {
	if err := db.Put(genesisKey(hash), data); err != nil {
		log.Info("Failed to store genesis state", "err", err)
	}
}

// DeleteReceipts删除与块哈希关联的所有收据数据。
func DeleteReceipts(db typedb.KeyValueWriter, hash entity.Hash, number uint64) {
	if err := db.Delete(blockReceiptsKey(number, hash)); err != nil {
		log.Info("Failed to delete block receipts", "err", err)
	}
}

// DeleteHeader删除与哈希关联的所有块头数据。
func DeleteHeader(db typedb.KeyValueWriter, hash entity.Hash, number uint64) {
	deleteHeaderWithoutNumber(db, hash, number)
	if err := db.Delete(headerNumberKey(hash)); err != nil {
		log.Info("Failed to delete hash to number mapping", "err", err)
	}
}

//deleteHeaderWithoutNumber只删除块头，但不删除哈希到数字的映射。
func deleteHeaderWithoutNumber(db typedb.KeyValueWriter, hash entity.Hash, number uint64) {
	if err := db.Delete(headerKey(number, hash)); err != nil {
		log.Info("Failed to delete header", "err", err)
	}
}

//DeleteBody删除与哈希关联的所有块体数据。
func DeleteBody(db typedb.KeyValueWriter, hash entity.Hash, number uint64) {
	if err := db.Delete(blockBodyKey(number, hash)); err != nil {
		log.Info("Failed to delete block body", "err", err)
	}
}

//DeleteTd删除与哈希关联的所有块总难度数据。
func DeleteTd(db typedb.KeyValueWriter, hash entity.Hash, number uint64) {
	if err := db.Delete(headerTDKey(number, hash)); err != nil {
		log.Info("Failed to delete block total difficulty", "err", err)
	}
}

// DeleteCanonicalHash删除数字到哈希规范映射。
func DeleteCanonicalHash(db typedb.KeyValueWriter, number uint64) {
	if err := db.Delete(headerHashKey(number)); err != nil {
		log.Info("Failed to delete number to hash mapping", "err", err)
	}
}
