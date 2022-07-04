package rawdb

import (
	"encoding/binary"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
)

//blockheader标识
var (
	// headHeaderKey跟踪最新已知标头的哈希。
	headHeaderKey = []byte("LastHeader")
	//headBlockKey跟踪最新的已知完整块哈希。
	headBlockKey = []byte("LastBlock")

	headerMark       = "0101" //区块header前缀
	headerHashSuffix = "0109" //区块headerhash后缀
	headerNumberMark = "0110" //区块header数前缀
	headerTDMark     = "0105" //当前规范标头的哈希

	headBlockMark  = "0102" //区块body前缀
	headHeaderMark = "0103" //td前缀

	receiptsMark = "0104" //收据前缀
	bodyMark     = "0107" //当前规范区块的哈希

	txLookupPrefix = "0106" //交易编
	CodePrefix     = "0108" //code前缀

	genesisPrefix = "octopus-genesis-" // db的genesis状态前缀
)

const (
	//freezerHeaderTable表示冷冻柜标题表的名称。
	freezerHeaderTable = "headers"

	// freezerHashTable表示冷冻库规范哈希表的名称。
	freezerHashTable = "hashes"

	// freezerBodiesTable表示冷冻块主体表的名称。
	freezerBodiesTable = "bodies"

	// freezerReceiptTable表示冷冻柜收据表的名称。
	freezerReceiptTable = "receipts"

	// freezerDifficultyTable表示冷冻柜总难度表的名称。
	freezerDifficultyTable = "diffs"
)

// 冻结应用程序配置是否为旧表禁用压缩。哈希和困难不能很好地压缩。
var FreezerNoSnappy = map[string]bool{
	freezerHeaderTable:     false,
	freezerHashTable:       true,
	freezerBodiesTable:     false,
	freezerReceiptTable:    false,
	freezerDifficultyTable: true,
}

// encodeBlockNumber将块编号编码为big-endian uint64
func encodeBlockNumber(number uint64) []byte {
	enc := make([]byte, 8)
	binary.BigEndian.PutUint64(enc, number)
	return enc
}

// headerKeyPrefix = headerPrefix + num (uint64 big endian)
func headerKeyPrefix(number uint64) []byte {
	return append(operationutils.FromHex(headerMark), encodeBlockNumber(number)...)
}

// headerKey = headerMark + num (uint64 big endian) + hash
func headerKey(number uint64, hash entity.Hash) []byte {
	return append(append(operationutils.FromHex(headerMark), encodeBlockNumber(number)...), hash.Bytes()...)
}

// headerHashKey = headerMark + num (uint64 big endian) + headerHashSuffix
func headerHashKey(number uint64) []byte {
	return append(append(operationutils.FromHex(headerMark), encodeBlockNumber(number)...), operationutils.FromHex(headerHashSuffix)...)
}

// headerNumberKey = headerMark + hash
func headerNumberKey(hash entity.Hash) []byte {
	return append(operationutils.FromHex(headerNumberMark), hash.Bytes()...)
}

// blockBodyKey = bodyMark + num (uint64 big endian) + hash
func blockBodyKey(number uint64, hash entity.Hash) []byte {
	return append(append(operationutils.FromHex(bodyMark), encodeBlockNumber(number)...), hash.Bytes()...)
}

// blockReceiptsKey = receiptsMark + num (uint64 big endian) + hash
func blockReceiptsKey(number uint64, hash entity.Hash) []byte {
	return append(append(operationutils.FromHex(receiptsMark), encodeBlockNumber(number)...), hash.Bytes()...)
}

// headerTDKey = headerMark + num (uint64 big endian) + hash + headerTDMark
func headerTDKey(number uint64, hash entity.Hash) []byte {
	return append(headerKey(number, hash), headerTDMark...)
}

// codeKey = CodePrefix + hash
func codeKey(hash entity.Hash) []byte {
	return append(operationutils.FromHex(CodePrefix), hash.Bytes()...)
}

// genesisKey = genesisPrefix + hash
func genesisKey(hash entity.Hash) []byte {
	return append(operationutils.FromHex(genesisPrefix), hash.Bytes()...)
}

// txLookupKey = txLookupPrefix + hash
func txLookupKey(hash entity.Hash) []byte {
	return append(operationutils.FromHex(txLookupPrefix), hash.Bytes()...)
}
