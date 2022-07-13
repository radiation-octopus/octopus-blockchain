package octell

import (
	"bytes"
	crand "crypto/rand"
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/consensus/misc"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus-blockchain/operationdb/trie"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"golang.org/x/crypto/sha3"
	"math"
	"math/big"
	"math/rand"
	"runtime"
	"sync"
	"time"
)

var (
	FrontierBlockReward           = big.NewInt(5e+18) // wei成功挖掘区块的区块奖励
	ByzantiumBlockReward          = big.NewInt(3e+18) // wei成功从拜占庭向上挖掘一个区块的区块奖励
	ConstantinopleBlockReward     = big.NewInt(2e+18) // wei成功从君士坦丁堡向上挖掘一个区块的区块奖励
	allowedFutureBlockTimeSeconds = int64(15)         // 在将块视为未来块之前，当前时间允许的最大秒数

	// calcDifficultyEip4345是EIP 4345规定的难度调整算法。它总共抵消了1070万块炸弹。
	calcDifficultyEip4345 = makeDifficultyCalculator(big.NewInt(10_700_000))

	// calcDifficultyEip3554是EIP 3554规定的难度调整算法。它总共抵消了970万块炸弹。
	calcDifficultyEip3554 = makeDifficultyCalculator(big.NewInt(9700000))

	// calcDifficultyEip2384是EIP 2384规定的难度调整算法。它将炸弹与君士坦丁堡相隔400万个街区，因此总共有900万个街区。
	calcDifficultyEip2384 = makeDifficultyCalculator(big.NewInt(9000000))

	// calcDifficultyConstantinople是君士坦丁堡的难度调整算法。它返回在给定父块的时间和难度的时间创建新块时应具有的难度。计算使用拜占庭规则，但炸弹偏移量为5M。
	calcDifficultyConstantinople = makeDifficultyCalculator(big.NewInt(5000000))

	// calcDifficultyByzantium是难度调整算法。它返回在给定父块的时间和难度的时间创建新块时应具有的难度。计算使用拜占庭规则。
	calcDifficultyByzantium = makeDifficultyCalculator(big.NewInt(3000000))
)

var (
	errOlderBlockTime    = errors.New("timestamp older than parent")
	errInvalidDifficulty = errors.New("non-positive difficulty")

	errInvalidMixDigest = errors.New("invalid mix digest")
	errInvalidPoW       = errors.New("invalid proof-of-work")
)

// SealHash返回块在被密封之前的哈希值。
func (octell *Octell) SealHash(header *block2.Header) (hash entity.Hash) {
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
func (octell *Octell) verifySeal(chain consensus.ChainHeaderReader, header *block2.Header, fulldag bool) error {
	// 如果我们使用的是假的战俘，请接受任何有效的印章
	if octell.Config.PowMode == ModeFake || octell.Config.PowMode == ModeFullFake {
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
		if octell.Config.PowMode == ModeTest {
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

// Prepare implements consensus.Engine, initializing the difficulty field of a
// header to conform to the ethash protocol. The changes are done inline.
func (octell *Octell) Prepare(chain consensus.ChainHeaderReader, header *block2.Header) error {
	parent := chain.GetHeader(header.ParentHash, header.Number.Uint64()-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	header.Difficulty = octell.CalcDifficulty(chain, header.Time, parent)
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
			current.generate(octell.Config.DatasetDir, octell.Config.DatasetsOnDisk, octell.Config.DatasetsLockMmap, octell.Config.PowMode == ModeTest)

			if futureI != nil {
				future := futureI.(*dataset)
				future.generate(octell.Config.DatasetDir, octell.Config.DatasetsOnDisk, octell.Config.DatasetsLockMmap, octell.Config.PowMode == ModeTest)
			}
		}()
	} else {
		// 已请求阻塞生成，或已执行阻塞生成
		current.generate(octell.Config.DatasetDir, octell.Config.DatasetsOnDisk, octell.Config.DatasetsLockMmap, octell.Config.PowMode == ModeTest)

		if futureI != nil {
			future := futureI.(*dataset)
			go future.generate(octell.Config.DatasetDir, octell.Config.DatasetsOnDisk, octell.Config.DatasetsLockMmap, octell.Config.PowMode == ModeTest)
		}
	}
	return current
}

//作者实现共识。引擎，返回标题的coinbase作为工作证明，以验证块的作者。
func (o *Octell) Author(header *block2.Header) (entity.Address, error) {
	return header.Coinbase, nil
}

//verifyHeader检查标头是否符合股票以太坊octell引擎的一致规则。
func (octell *Octell) verifyHeader(chain consensus.ChainHeaderReader, header, parent *block2.Header, uncle bool, seal bool, unixNow int64) error {
	// 确保标头的额外数据节的大小合理
	//if uint64(len(header.Extra)) > params.MaximumExtraDataSize {
	//	return fmt.Errorf("extra-data too long: %d > %d", len(header.Extra), params.MaximumExtraDataSize)
	//}
	// 验证标头的时间戳
	if !uncle {
		if header.Time > uint64(unixNow+allowedFutureBlockTimeSeconds) {
			return consensus.ErrFutureBlock
		}
	}
	if header.Time <= parent.Time {
		return errOlderBlockTime
	}
	// 根据块的时间戳和父块的难度验证块的难度
	expected := octell.CalcDifficulty(chain, header.Time, parent)

	if expected.Cmp(header.Difficulty) != 0 {
		return fmt.Errorf("invalid difficulty: have %v, want %v", header.Difficulty, expected)
	}
	// 验证gas极限<=2^63-1
	if header.GasLimit > entity.MaxGasLimit {
		return fmt.Errorf("invalid gasLimit: have %v, max %v", header.GasLimit, entity.MaxGasLimit)
	}
	// 确认使用的gas<=gas极限
	if header.GasUsed > header.GasLimit {
		return fmt.Errorf("invalid gasUsed: have %d, gasLimit %d", header.GasUsed, header.GasLimit)
	}
	// 核实区块的gas使用情况，并（如适用）核实基本费用。
	if !chain.Config().IsLondon(header.Number) {
		// 验证在EIP-1559 fork之前不存在BaseFee。
		if header.BaseFee != nil {
			return fmt.Errorf("invalid baseFee before fork: have %d, expected 'nil'", header.BaseFee)
		}
		if err := misc.VerifyGaslimit(parent.GasLimit, header.GasLimit); err != nil {
			return err
		}
	} else if err := misc.VerifyEip1559Header(chain.Config(), parent, header); err != nil {
		// 验证标头的EIP-1559属性。
		return err
	}
	// 验证块编号是否为父级的+1
	if diff := new(big.Int).Sub(header.Number, parent.Number); diff.Cmp(big.NewInt(1)) != 0 {
		return consensus.ErrInvalidNumber
	}
	// 验证固定缸体的发动机专用密封件
	if seal {
		if err := octell.verifySeal(chain, header, false); err != nil {
			return err
		}
	}
	// 如果所有检查都通过，请验证硬叉的任何特殊字段
	if err := misc.VerifyDAOHeaderExtraData(chain.Config(), header); err != nil {
		return err
	}
	//if err := misc.VerifyForkHashes(chain.Config(), header, uncle); err != nil {
	//	return err
	//}
	return nil
}

// CalcDifficulty是难度调整算法。它返回在给定父块的时间和难度的时间创建新块时应具有的难度。
func (octell *Octell) CalcDifficulty(chain consensus.ChainHeaderReader, time uint64, parent *block2.Header) *big.Int {
	return CalcDifficulty(chain.Config(), time, parent)
}

// CalcDifficulty是难度调整算法。它返回在给定父块的时间和难度的时间创建新块时应具有的难度。
func CalcDifficulty(config *entity.ChainConfig, time uint64, parent *block2.Header) *big.Int {
	next := new(big.Int).Add(parent.Number, operationutils.Big1)
	switch {
	case config.IsArrowGlacier(next):
		return calcDifficultyEip4345(time, parent)
	case config.IsLondon(next):
		return calcDifficultyEip3554(time, parent)
	case config.IsMuirGlacier(next):
		return calcDifficultyEip2384(time, parent)
	case config.IsConstantinople(next):
		return calcDifficultyConstantinople(time, parent)
	case config.IsByzantium(next):
		return calcDifficultyByzantium(time, parent)
	case config.IsHomestead(next):
		return calcDifficultyHomestead(time, parent)
	default:
		return calcDifficultyFrontier(time, parent)
	}
}

// makedifficullycalculator创建具有给定炸弹延迟的difficullycalculator。难度是用拜占庭规则计算的，这与宅地不同，叔叔如何影响计算
func makeDifficultyCalculator(bombDelay *big.Int) func(time uint64, parent *block2.Header) *big.Int {
	// 注意，下面的计算将查看父编号，即块编号下方的1。因此，我们从给出的延迟中删除一个
	bombDelayFromParent := new(big.Int).Sub(bombDelay, operationutils.Big1)
	return func(time uint64, parent *block2.Header) *big.Int {
		bigTime := new(big.Int).SetUint64(time)
		bigParentTime := new(big.Int).SetUint64(parent.Time)

		// 保留中间值，使算法更易于阅读和审核
		x := new(big.Int)
		y := new(big.Int)

		x.Sub(bigTime, bigParentTime)
		x.Div(x, operationutils.Big9)
		if parent.UncleHash == block2.EmptyUncleHash {
			x.Sub(operationutils.Big1, x)
		} else {
			x.Sub(operationutils.Big2, x)
		}
		// max((2 if len(parent_uncles) else 1) - (block_timestamp - parent_timestamp) // 9, -99)
		if x.Cmp(operationutils.BigMinus99) < 0 {
			x.Set(operationutils.BigMinus99)
		}
		// parent_diff + (parent_diff / 2048 * max((2 if len(parent.uncles) else 1) - ((timestamp - parent.timestamp) // 9), -99))
		y.Div(parent.Difficulty, entity.DifficultyBoundDivisor)
		x.Mul(y, x)
		x.Add(parent.Difficulty, x)

		// 最小难度可能是（指数因子之前）
		if x.Cmp(entity.MinimumDifficulty) < 0 {
			x.Set(entity.MinimumDifficulty)
		}
		// 计算冰河期延迟的假块数
		fakeBlockNumber := new(big.Int)
		if parent.Number.Cmp(bombDelayFromParent) >= 0 {
			fakeBlockNumber = fakeBlockNumber.Sub(parent.Number, bombDelayFromParent)
		}
		// 对于指数因子
		periodCount := fakeBlockNumber
		periodCount.Div(periodCount, operationutils.ExpDiffPeriod)

		// 指数因子，通常被称为“炸弹”
		// diff = diff + 2^(periodCount - 2)
		if periodCount.Cmp(operationutils.Big1) > 0 {
			y.Sub(periodCount, operationutils.Big2)
			y.Exp(operationutils.Big2, y, nil)
			x.Add(x, y)
		}
		return x
	}
}

// calcDifficultyHomestead是难度调整算法。它返回在给定父块的时间和难度的时间创建新块时应具有的难度。计算使用宅地规则。
func calcDifficultyHomestead(time uint64, parent *block2.Header) *big.Int {
	// algorithm:
	// diff = (parent_diff +
	//         (parent_diff / 2048 * max(1 - (block_timestamp - parent_timestamp) // 10, -99))
	//        ) + 2^(periodCount - 2)

	bigTime := new(big.Int).SetUint64(time)
	bigParentTime := new(big.Int).SetUint64(parent.Time)

	// 保留中间值，使算法更易于阅读和审核
	x := new(big.Int)
	y := new(big.Int)

	// 1 - (block_timestamp - parent_timestamp) // 10
	x.Sub(bigTime, bigParentTime)
	x.Div(x, operationutils.Big10)
	x.Sub(operationutils.Big1, x)

	// max(1 - (block_timestamp - parent_timestamp) // 10, -99)
	if x.Cmp(operationutils.BigMinus99) < 0 {
		x.Set(operationutils.BigMinus99)
	}
	// (parent_diff + parent_diff // 2048 * max(1 - (block_timestamp - parent_timestamp) // 10, -99))
	y.Div(parent.Difficulty, entity.DifficultyBoundDivisor)
	x.Mul(y, x)
	x.Add(parent.Difficulty, x)

	// 最小难度可能是（指数因子之前）
	if x.Cmp(entity.MinimumDifficulty) < 0 {
		x.Set(entity.MinimumDifficulty)
	}
	// 对于指数因子
	periodCount := new(big.Int).Add(parent.Number, operationutils.Big1)
	periodCount.Div(periodCount, operationutils.ExpDiffPeriod)

	// 指数因子，通常被称为“炸弹”
	// diff = diff + 2^(periodCount - 2)
	if periodCount.Cmp(operationutils.Big1) > 0 {
		y.Sub(periodCount, operationutils.Big2)
		y.Exp(operationutils.Big2, y, nil)
		x.Add(x, y)
	}
	return x
}

// calcDifficultyFrontier是难度调整算法。它返回在给定父块的时间和难度的时间创建新块时应具有的难度。计算使用边界规则。
func calcDifficultyFrontier(time uint64, parent *block2.Header) *big.Int {
	diff := new(big.Int)
	adjust := new(big.Int).Div(parent.Difficulty, entity.DifficultyBoundDivisor)
	bigTime := new(big.Int)
	bigParentTime := new(big.Int)

	bigTime.SetUint64(time)
	bigParentTime.SetUint64(parent.Time)

	if bigTime.Sub(bigTime, bigParentTime).Cmp(entity.DurationLimit) < 0 {
		diff.Add(parent.Difficulty, adjust)
	} else {
		diff.Sub(parent.Difficulty, adjust)
	}
	if diff.Cmp(entity.MinimumDifficulty) < 0 {
		diff.Set(entity.MinimumDifficulty)
	}

	periodCount := new(big.Int).Add(parent.Number, operationutils.Big1)
	periodCount.Div(periodCount, operationutils.ExpDiffPeriod)
	if periodCount.Cmp(operationutils.Big1) > 0 {
		// diff = diff + 2^(periodCount - 2)
		expDiff := periodCount.Sub(periodCount, operationutils.Big2)
		expDiff.Exp(operationutils.Big2, expDiff, nil)
		diff.Add(diff, expDiff)
		diff = operationutils.BigMax(diff, entity.MinimumDifficulty)
	}
	return diff
}

// VerifyHeader检查标头是否符合股票以太坊ethash引擎的共识规则。
func (o *Octell) VerifyHeader(chain consensus.ChainHeaderReader, header *block2.Header, seal bool) error {
	// 如果我们运行的是全引擎模拟，则接受任何有效输入
	if o.Config.PowMode == ModeFullFake {
		return nil
	}
	// 如果标头已知或其父项未知，则短路
	number := header.Number.Uint64()
	if chain.GetHeader(header.Hash(), number) != nil {
		return nil
	}
	parent := chain.GetHeader(header.ParentHash, number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	// 通过健康检查，进行适当验证
	return o.verifyHeader(chain, header, parent, false, seal, time.Now().Unix())
}

//VerifyHeaders与VerifyHeader类似，但同时验证一批标头。该方法返回退出通道以中止操作，返回结果通道以检索异步验证。
func (o *Octell) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*block2.Header, seals []bool) (chan<- struct{}, <-chan error) {
	//如果我们运行的是全引擎模拟，请接受任何有效输入
	if o.Config.PowMode == ModeFullFake || len(headers) == 0 {
		abort, results := make(chan struct{}), make(chan error, len(headers))
		for i := 0; i < len(headers); i++ {
			results <- nil
		}
		return abort, results
	}

	// 生成尽可能多的工作线程
	workers := runtime.GOMAXPROCS(0)
	if len(headers) < workers {
		workers = len(headers)
	}

	// 创建任务通道并生成验证器
	var (
		inputs  = make(chan int)
		done    = make(chan int, workers)
		errors  = make([]error, len(headers))
		abort   = make(chan struct{})
		unixNow = time.Now().Unix()
	)
	for i := 0; i < workers; i++ {
		go func() {
			for index := range inputs {
				errors[index] = o.verifyHeaderWorker(chain, headers, seals, index, unixNow)
				done <- index
			}
		}()
	}

	errorsOut := make(chan error, len(headers))
	go func() {
		defer close(inputs)
		var (
			in, out = 0, 0
			checked = make([]bool, len(headers))
			inputs  = inputs
		)
		for {
			select {
			case inputs <- in:
				if in++; in == len(headers) {
					// 到达页眉末尾。停止发送给工人。
					inputs = nil
				}
			case index := <-done:
				for checked[index] = true; checked[out]; out++ {
					errorsOut <- errors[out]
					if out == len(headers)-1 {
						return
					}
				}
			case <-abort:
				return
			}
		}
	}()
	return abort, errorsOut
}

func (octell *Octell) verifyHeaderWorker(chain consensus.ChainHeaderReader, headers []*block2.Header, seals []bool, index int, unixNow int64) error {
	var parent *block2.Header
	if index == 0 {
		parent = chain.GetHeader(headers[0].ParentHash, headers[0].Number.Uint64()-1)
	} else if headers[index-1].Hash() == headers[index].ParentHash {
		parent = headers[index-1]
	}
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	return octell.verifyHeader(chain, headers[index], parent, false, seals[index], unixNow)
}

//FinalizeAndAssemble 实现共识。引擎，累积积木和叔叔奖励，设置最终状态并组装积木。
func (o *Octell) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *block2.Header, ob *operationdb.OperationDB, txs []*block2.Transaction, uncles []*block2.Header, receipts []*block2.Receipt) (*block2.Block, error) {
	// 最终确定块
	o.Finalize(chain, header, ob, txs, uncles)

	// 收割台似乎已完成，组装成块并返回
	return block2.NewBlock(header, txs, receipts, trie.NewStackTrie(nil)), nil
}

// Finalize实现共识。引擎，累积积木和叔叔奖励，设置标题的最终状态
func (octell *Octell) Finalize(chain consensus.ChainHeaderReader, header *block2.Header, ob *operationdb.OperationDB, txs []*block2.Transaction, uncles []*block2.Header) {
	// 累积任何方块和叔叔奖励并提交最终状态根
	accumulateRewards(chain.Config(), ob, header, uncles)
	header.Root = ob.IntermediateRoot(chain.Config().IsEIP158(header.Number))
}

//Seal实现共识。引擎，试图找到满足模块难度要求的nonce。
func (o *Octell) Seal(chain consensus.ChainHeaderReader, b *block2.Block, results chan<- *block2.Block, stop <-chan struct{}) error {
	// 如果我们运行的是假的PoW，只需立即返回一个0 nonce
	if o.Config.PowMode == ModeFake || o.Config.PowMode == ModeFullFake {
		header := b.Header()
		header.Nonce, header.MixDigest = block2.BlockNonce{}, entity.Hash{}
		select {
		case results <- b.WithSeal(header):
		default:
			log.Warn("Sealing result is not read by miner", "mode", "fake", "sealhash", o.SealHash(b.Header()))
		}
		return nil
	}
	// 如果我们正在运行共享PoW，请将密封委派给它
	if o.shared != nil {
		return o.shared.Seal(chain, b, results, stop)
	}
	// 创建一个运行程序及其所指向的多个搜索线程
	abort := make(chan struct{})

	o.lock.Lock()
	threads := o.threads
	if o.rand == nil {
		seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
		if err != nil {
			o.lock.Unlock()
			return err
		}
		o.rand = rand.New(rand.NewSource(seed.Int64()))
	}
	o.lock.Unlock()
	if threads == 0 {
		threads = runtime.NumCPU()
	}
	if threads < 0 {
		threads = 0 // 允许禁用本地工作，而无需本地/远程的额外逻辑
	}
	// 将新工作推至远程封口机
	if o.remote != nil {
		o.remote.workCh <- &sealTask{block: b, results: results}
	}
	var (
		pend   sync.WaitGroup
		locals = make(chan *block2.Block)
	)
	for i := 0; i < threads; i++ {
		pend.Add(1)
		go func(id int, nonce uint64) {
			defer pend.Done()
			o.mine(b, id, nonce, abort, locals)
		}(i, uint64(o.rand.Int63()))
	}
	// 等待，直到终止密封或找到nonce
	go func() {
		var result *block2.Block
		select {
		case <-stop:
			// 外部中止，停止所有工作线程
			close(abort)
		case result = <-locals:
			// 其中一个线程发现一个块，中止所有其他线程
			select {
			case results <- result:
			default:
				log.Warn("Sealing result is not read by miner", "mode", "local", "sealhash", o.SealHash(b.Header()))
			}
			close(abort)
		case <-o.update:
			// 线程计数已根据用户请求更改，请重新启动
			close(abort)
			if err := o.Seal(chain, b, results, stop); err != nil {
				log.Error("Failed to restart sealing after update", "err", err)
			}
		}
		// 等待所有工作者终止并归还区块
		pend.Wait()
	}()
	return nil
}

//累计向给定区块的币库授予采矿奖励。总奖励包括静态块奖励和包含叔叔的奖励。每个叔叔街区的铸币库也会得到奖励。
func accumulateRewards(config *entity.ChainConfig, ob *operationdb.OperationDB, header *block2.Header, uncles []*block2.Header) {
	// 根据链进度选择正确的区块奖励
	blockReward := FrontierBlockReward
	if config.IsByzantium(header.Number) {
		blockReward = ByzantiumBlockReward
	}
	if config.IsConstantinople(header.Number) {
		blockReward = ConstantinopleBlockReward
	}
	// 为矿工和其他叔叔累积奖励
	reward := new(big.Int).Set(blockReward)
	r := new(big.Int)
	for _, uncle := range uncles {
		r.Add(uncle.Number, operationutils.Big8)
		r.Sub(r, header.Number)
		r.Mul(r, blockReward)
		r.Div(r, operationutils.Big8)
		ob.AddBalance(uncle.Coinbase, r)

		r.Div(blockReward, operationutils.Big32)
		reward.Add(reward, r)
	}
	ob.AddBalance(header.Coinbase, reward)
}
