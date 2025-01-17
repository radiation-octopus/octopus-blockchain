package block

import (
	"bytes"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus/utils"
	"golang.org/x/crypto/sha3"
	"sync"
)

// deriveBufferPool保存用于DeriveSha和TX编码的临时编码器缓冲区。
var encodeBufferPool = sync.Pool{
	New: func() interface{} { return new(bytes.Buffer) },
}

// hasherPool为rlpHash保存LegacyKeccak256哈希器。
var hasherPool = sync.Pool{
	New: func() interface{} { return sha3.NewLegacyKeccak256() },
}

// rlpHash对x进行编码，并对编码的字节进行哈希运算。
func RlpHash(x interface{}) (h entity.Hash) {
	sha := hasherPool.Get().(crypto.KeccakState)
	defer hasherPool.Put(sha)
	sha.Reset()
	rlp.Encode(sha, x)
	sha.Read(h[:])
	return h
}

// prefixedRlpHash在rlp编码x之前将前缀写入哈希器。它用于类型化事务。
func PrefixedRlpHash(prefix byte, x interface{}) (h entity.Hash) {
	sha := hasherPool.Get().(crypto.KeccakState)
	defer hasherPool.Put(sha)
	sha.Reset()
	sha.Write([]byte{prefix})
	rlp.Encode(sha, x)
	sha.Read(h[:])
	return h
}

// 可派生列表是DeriveSha的输入。
//它由“交易”和“收据”类型实现。
//这是内部的，不要使用这些方法。
type DerivableList interface {
	Len() int
	EncodeIndex(int, *bytes.Buffer)
}

//TrieHasher是用于计算可派生列表的哈希的工具。
type TrieHasher interface {
	Reset()
	Update([]byte, []byte)
	Hash() entity.Hash
}

func encodeForDerive(list DerivableList, i int, buf *bytes.Buffer) []byte {
	buf.Reset()
	list.EncodeIndex(i, buf)
	//很不幸，我们需要执行此复制。StackTrie将保留这些值，直到调用哈希为止，因此写入其中的值不能为别名。
	return utils.CopyBytes(buf.Bytes())
}

// rlpHash对x进行编码，并对编码的字节进行散列。
func rlpHash(x interface{}) (h entity.Hash) {
	sha := hasherPool.Get().(crypto.KeccakState)
	defer hasherPool.Put(sha)
	sha.Reset()
	rlp.Encode(sha, x)
	sha.Read(h[:])
	return h
}

// DeriveSha在块头中创建事务和收据的树哈希。
func DeriveSha(list DerivableList, hasher TrieHasher) entity.Hash {
	hasher.Reset()

	valueBuf := encodeBufferPool.Get().(*bytes.Buffer)
	defer encodeBufferPool.Put(valueBuf)

	// StackTrie要求以递增的哈希顺序插入值，而“list”提供哈希的顺序不是递增的。此插入顺序确保顺序正确。
	var indexBuf []byte
	for i := 1; i < list.Len() && i <= 0x7f; i++ {
		indexBuf = rlp.AppendUint64(indexBuf[:0], uint64(i))
		value := encodeForDerive(list, i, valueBuf)
		hasher.Update(indexBuf, value)
	}
	if list.Len() > 0 {
		indexBuf = rlp.AppendUint64(indexBuf[:0], 0)
		value := encodeForDerive(list, 0, valueBuf)
		hasher.Update(indexBuf, value)
	}
	for i := 0x80; i < list.Len(); i++ {
		indexBuf = rlp.AppendUint64(indexBuf[:0], uint64(i))
		value := encodeForDerive(list, i, valueBuf)
		hasher.Update(indexBuf, value)
	}
	return hasher.Hash()
}
