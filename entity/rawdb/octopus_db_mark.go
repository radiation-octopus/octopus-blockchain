package rawdb

import (
	"encoding/binary"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
)

//blockheader标识
var (

	// databaseVersionKey tracks the current database version.
	databaseVersionKey = []byte("DatabaseVersion")

	// headHeaderKey tracks the latest known header's hash.
	headHeaderKey = []byte("LastHeader")

	// headBlockKey tracks the latest known full block's hash.
	headBlockKey = []byte("LastBlock")

	// headFastBlockKey tracks the latest known incomplete block's hash during fast sync.
	headFastBlockKey = []byte("LastFast")

	// headFinalizedBlockKey tracks the latest known finalized block hash.
	headFinalizedBlockKey = []byte("LastFinalized")

	// lastPivotKey tracks the last pivot block used by fast sync (to reenable on sethead).
	lastPivotKey = []byte("LastPivot")

	// fastTrieProgressKey tracks the number of trie entries imported during fast sync.
	fastTrieProgressKey = []byte("TrieSync")

	// snapshotDisabledKey flags that the snapshot should not be maintained due to initial sync.
	snapshotDisabledKey = []byte("SnapshotDisabled")

	// SnapshotRootKey tracks the hash of the last snapshot.
	SnapshotRootKey = []byte("SnapshotRoot")

	// snapshotJournalKey tracks the in-memory diff layers across restarts.
	snapshotJournalKey = []byte("SnapshotJournal")

	// snapshotGeneratorKey tracks the snapshot generation marker across restarts.
	snapshotGeneratorKey = []byte("SnapshotGenerator")

	// snapshotRecoveryKey tracks the snapshot recovery marker across restarts.
	snapshotRecoveryKey = []byte("SnapshotRecovery")

	// snapshotSyncStatusKey tracks the snapshot sync status across restarts.
	snapshotSyncStatusKey = []byte("SnapshotSyncStatus")

	// skeletonSyncStatusKey tracks the skeleton sync status across restarts.
	skeletonSyncStatusKey = []byte("SkeletonSyncStatus")

	// txIndexTailKey tracks the oldest block whose transactions have been indexed.
	txIndexTailKey = []byte("TransactionIndexTail")

	// fastTxLookupLimitKey tracks the transaction lookup limit during fast sync.
	fastTxLookupLimitKey = []byte("FastTransactionLookupLimit")

	// badBlockKey tracks the list of bad blocks seen by local
	badBlockKey = []byte("InvalidBlock")

	// uncleanShutdownKey tracks the list of local crashes
	uncleanShutdownKey = []byte("unclean-shutdown") // config prefix for the db

	// transitionStatusKey tracks the octopus transition status.
	transitionStatusKey = []byte("octopus-transition")

	PreimagePrefix = []byte("secure-key-")     // PreimagePrefix + hash -> preimage
	configPrefix   = []byte("octopus-config-") // config prefix for the db

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

	skeletonHeaderPrefix = []byte("0111") // skeletonHeaderPrefix + num (uint64 big endian) -> header

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

// configKey = configPrefix + hash
func configKey(hash entity.Hash) []byte {
	return append(configPrefix, hash.Bytes()...)
}

// skeletonHeaderKey = skeletonHeaderPrefix + num (uint64 big endian)
func skeletonHeaderKey(number uint64) []byte {
	return append(skeletonHeaderPrefix, encodeBlockNumber(number)...)
}
