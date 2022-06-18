package octell

import (
	"bytes"
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"golang.org/x/crypto/sha3"
	"math/big"
	"runtime"
	"time"
)

var (
	errInvalidDifficulty = errors.New("non-positive difficulty")

	errInvalidMixDigest = errors.New("invalid mix digest")
	errInvalidPoW       = errors.New("invalid proof-of-work")
)

// SealHash返回块在被密封之前的哈希值。
func (octell *Octell) SealHash(header *block.Header) (hash entity.Hash) {
	hasher := sha3.NewLegacyKeccak256()

	enc := []interface{}{
		header.ParentHash,
		header.UncleHash,
		header.Coinbase,
		header.Root,
		header.TxHash,
		header.ReceiptHash,
		//header.Bloom,
		header.Difficulty,
		header.Number,
		header.GasLimit,
		header.GasUsed,
		header.Time,
		//header.Extra,
	}
	if header.BaseFee != nil {
		enc = append(enc, header.BaseFee)
	}
	rlp.Encode(hasher, enc)
	hasher.Sum(hash[:0])
	return hash
}

//verifySeal检查一个块是否满足PoW难度要求，要么使用常用的octell缓存，要么使用完整的DAG快速远程挖掘。
func (octell *Octell) verifySeal(chain consensus.ChainHeaderReader, header *block.Header, fulldag bool) error {
	// 如果我们使用的是假的战俘，请接受任何有效的印章
	if octell.config.PowMode == ModeFake || octell.config.PowMode == ModeFullFake {
		time.Sleep(octell.fakeDelay)
		if octell.fakeFail == header.Number.Uint64() {
			return errInvalidPoW
		}
		return nil
	}
	// 如果我们正在运行共享PoW，请将验证委托给它
	if octell.shared != nil {
		return octell.shared.verifySeal(chain, header, fulldag)
	}
	// 确保我们有一个有效的障碍
	if header.Difficulty.Sign() <= 0 {
		return errInvalidDifficulty
	}
	// 重新计算摘要和PoW值
	number := header.Number.Uint64()

	var (
		digest []byte
		result []byte
	)
	// 如果请求快速但繁重的PoW验证，请使用octell数据集
	if fulldag {
		dataset := octell.dataset(number, true)
		if dataset.generated() {
			digest, result = hashimotoFull(dataset.dataset, octell.SealHash(header).Bytes(), header.Nonce.Uint64())

			// 在终结器中未映射数据集。确保数据集在调用hashimotoFull之前保持活动状态，以便在使用时不会取消映射。
			runtime.KeepAlive(dataset)
		} else {
			// 尚未生成数据集，不要挂起，请使用缓存
			fulldag = false
		}
	}
	//如果请求缓慢但轻微的PoW验证（或DAG尚未就绪），请使用octell缓存
	if !fulldag {
		cache := octell.cache(number)

		size := datasetSize(number)
		if octell.config.PowMode == ModeTest {
			size = 32 * 1024
		}
		digest, result = hashimotoLight(size, cache.cache, octell.SealHash(header).Bytes(), header.Nonce.Uint64())

		// 缓存在终结器中未映射。确保缓存在调用hashimotoLight之前保持活动状态，以便在使用时不会取消映射。
		runtime.KeepAlive(cache)
	}
	// 对照标题中提供的值验证计算值
	if !bytes.Equal(header.MixDigest[:], digest) {
		return errInvalidMixDigest
	}
	target := new(big.Int).Div(two256, header.Difficulty)
	if new(big.Int).SetBytes(result).Cmp(target) > 0 {
		return errInvalidPoW
	}
	return nil
}

// dataset尝试检索指定块号的挖掘数据集，方法是首先检查内存中的数据集列表，然后检查存储在磁盘上的DAG，如果找不到，最后生成一个。
//如果指定了async，则不仅会在后台线程上生成未来DAG，还会生成当前DAG。
func (octell *Octell) dataset(block uint64, async bool) *dataset {
	// 检索请求的octell数据集
	epoch := block / epochLength
	currentI, futureI := octell.datasets.get(epoch)
	current := currentI.(*dataset)

	// 如果指定了async，则在后台线程中生成所有内容
	if async && !current.generated() {
		go func() {
			current.generate(octell.config.DatasetDir, octell.config.DatasetsOnDisk, octell.config.DatasetsLockMmap, octell.config.PowMode == ModeTest)

			if futureI != nil {
				future := futureI.(*dataset)
				future.generate(octell.config.DatasetDir, octell.config.DatasetsOnDisk, octell.config.DatasetsLockMmap, octell.config.PowMode == ModeTest)
			}
		}()
	} else {
		// 已请求阻塞生成，或已执行阻塞生成
		current.generate(octell.config.DatasetDir, octell.config.DatasetsOnDisk, octell.config.DatasetsLockMmap, octell.config.PowMode == ModeTest)

		if futureI != nil {
			future := futureI.(*dataset)
			go future.generate(octell.config.DatasetDir, octell.config.DatasetsOnDisk, octell.config.DatasetsLockMmap, octell.config.PowMode == ModeTest)
		}
	}
	return current
}
