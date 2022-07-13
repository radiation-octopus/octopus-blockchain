package rawdb

import (
	"bytes"
	"github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
)

// ReadSkeletonSyncStatus检索在关机时保存的序列化同步状态。
func ReadSkeletonSyncStatus(db typedb.KeyValueReader) []byte {
	data, _ := db.Get(skeletonSyncStatusKey)
	return data
}

// ReadSkeletonHeader从骨架同步存储中检索块标头，
func ReadSkeletonHeader(db typedb.KeyValueReader, number uint64) *block.Header {
	data, _ := db.Get(skeletonHeaderKey(number))
	if len(data) == 0 {
		return nil
	}
	header := new(block.Header)
	if err := rlp.Decode(bytes.NewReader(data), header); err != nil {
		log.Error("Invalid skeleton header RLP", "number", number, "err", err)
		return nil
	}
	return header
}

// WriteSkeletonSyncStatus存储要在关机时保存的序列化同步状态。
func WriteSkeletonSyncStatus(db typedb.KeyValueWriter, status []byte) {
	if err := db.Put(skeletonSyncStatusKey, status); err != nil {
		log.Crit("Failed to store skeleton sync status", "err", err)
	}
}

// WriteSkeletonHeader将块头存储到骨架同步存储中。
func WriteSkeletonHeader(db typedb.KeyValueWriter, header *block.Header) {
	data, err := rlp.EncodeToBytes(header)
	if err != nil {
		log.Crit("Failed to RLP encode header", "err", err)
	}
	key := skeletonHeaderKey(header.Number.Uint64())
	if err := db.Put(key, data); err != nil {
		log.Crit("Failed to store skeleton header", "err", err)
	}
}

// DeleteSkeletonHeader 删除与哈希相关的所有块头数据。
func DeleteSkeletonHeader(db typedb.KeyValueWriter, number uint64) {
	if err := db.Delete(skeletonHeaderKey(number)); err != nil {
		log.Crit("Failed to delete skeleton header", "err", err)
	}
}
