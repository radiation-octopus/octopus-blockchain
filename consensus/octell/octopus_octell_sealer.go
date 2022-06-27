package octell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus/log"
	"math/big"
	"net/http"
	"runtime"
	"sync"
	"time"
)

const (
	// staleThreshold是可接受的陈旧但有效的octell解决方案的最大深度。
	staleThreshold = 7
)

var (
	errNoMiningWork      = errors.New("no mining work available yet")
	errInvalidSealResult = errors.New("invalid or stale proof-of-work solution")
)

// 这是HTTP请求通知外部工作者的超时时间。
const remoteSealerTimeout = 1 * time.Second

//mine是一个实际的工作证明工作者，它从种子开始搜索一个nonce，从而获得正确的最终块难度。
func (octell *Octell) mine(b *block.Block, id int, seed uint64, abort chan struct{}, found chan *block.Block) {
	//从标题中提取一些数据
	var (
		header  = b.Header()
		hash    = octell.SealHash(header).Bytes()
		target  = new(big.Int).Div(two256, header.Difficulty)
		number  = header.Number.Uint64()
		dataset = octell.dataset(number, false)
	)
	// 开始生成随机nonce，直到我们中止或找到一个好的nonce
	var (
		attempts  = int64(0)
		nonce     = seed
		powBuffer = new(big.Int)
	)
	//logger := octell.Config.Log.New("miner", id)
	log.Info("Started Octell search for new nonces", "seed", seed)
search:
	for {
		select {
		case <-abort:
			// 工作已终止，更新统计信息并中止
			log.Info("Octell nonce search aborted", "attempts", nonce-seed)
			//octell.hashrate.Mark(attempts)
			break search

		default:
			// 我们不必在每个nonce上更新哈希率，所以在2^X nonce之后更新
			attempts++
			if (attempts % (1 << 15)) == 0 {
				//octell.hashrate.Mark(attempts)
				attempts = 0
			}
			// 计算此nonce的PoW值
			digest, result := hashimotoFull(dataset.dataset, hash, nonce)
			if powBuffer.SetBytes(result).Cmp(target) <= 0 {
				// 找到正确的nonce，使用它创建新的标头
				header = block.CopyHeader(header)
				header.Nonce = block.EncodeNonce(nonce)
				header.MixDigest = entity.BytesToHash(digest)

				// 密封并返回块（如果仍然需要）
				select {
				case found <- b.WithSeal(header):
					log.Info("Octell nonce found and reported", "attempts", nonce-seed, "nonce", nonce)
				case <-abort:
					log.Info("Octell nonce found but discarded", "attempts", nonce-seed, "nonce", nonce)
				}
				break search
			}
			nonce++
		}
	}
	// 在终结器中未映射数据集。确保数据集在密封期间保持活动状态，以便在读取时不会取消映射。
	runtime.KeepAlive(dataset)
}

type remoteSealer struct {
	works        map[entity.Hash]*block.Block
	rates        map[entity.Hash]hashrate
	currentBlock *block.Block
	currentWork  [4]string
	notifyCtx    context.Context
	cancelNotify context.CancelFunc // 取消所有通知请求。
	reqWG        sync.WaitGroup     // 跟踪通知请求goroutines。

	octell       *Octell
	noverify     bool
	notifyURLs   []string
	results      chan<- *block.Block
	workCh       chan *sealTask   // 将新工作推送至远程封口机的通知通道和相关结果通道。
	fetchWorkCh  chan *sealWork   // 用于远程封口机的通道，用于提取采矿作业。
	submitWorkCh chan *mineResult // 用于远程封口机提交采矿结果的通道。
	fetchRateCh  chan chan uint64 // 用于收集本地或远程密封程序提交的哈希率的通道。
	submitRateCh chan *hashrate   // 用于远程封口机提交其挖掘哈希率的通道。
	requestExit  chan struct{}
	exitCh       chan struct{}
}

func startRemoteSealer(octell *Octell, urls []string, noverify bool) *remoteSealer {
	ctx, cancel := context.WithCancel(context.Background())
	s := &remoteSealer{
		octell:       octell,
		noverify:     noverify,
		notifyURLs:   urls,
		notifyCtx:    ctx,
		cancelNotify: cancel,
		works:        make(map[entity.Hash]*block.Block),
		rates:        make(map[entity.Hash]hashrate),
		workCh:       make(chan *sealTask),
		fetchWorkCh:  make(chan *sealWork),
		submitWorkCh: make(chan *mineResult),
		fetchRateCh:  make(chan chan uint64),
		submitRateCh: make(chan *hashrate),
		requestExit:  make(chan struct{}),
		exitCh:       make(chan struct{}),
	}
	go s.loop()
	return s
}

func (s *remoteSealer) loop() {
	defer func() {
		log.Info("Octell remote sealer is exiting")
		s.cancelNotify()
		s.reqWG.Wait()
		close(s.exitCh)
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case work := <-s.workCh:
			//使用新接收的块更新当前工作。注：更改CPU线程时，可能会发生两次相同的工作。
			s.results = work.results
			s.makeWork(work.block)
			s.notifyWork()

		case work := <-s.fetchWorkCh:
			// 将当前作业返回给远程工作者。
			if s.currentBlock == nil {
				work.errc <- errNoMiningWork
			} else {
				work.res <- s.currentWork
			}

		case result := <-s.submitWorkCh:
			// 根据维护的工作区块验证提交的PoW解决方案。
			if s.submitWork(result.nonce, result.mixDigest, result.hash) {
				result.errc <- nil
			} else {
				result.errc <- errInvalidSealResult
			}

		case result := <-s.submitRateCh:
			// 按提交的值跟踪远程密封程序的哈希率。
			s.rates[result.id] = hashrate{rate: result.rate, ping: time.Now()}
			close(result.done)

		case req := <-s.fetchRateCh:
			// 收集远程密封器提交的所有哈希率。
			var total uint64
			for _, rate := range s.rates {
				// 这可能会溢出
				total += rate.rate
			}
			req <- total

		case <-ticker.C:
			// 清除过时提交的哈希率。
			for id, rate := range s.rates {
				if time.Since(rate.ping) > 10*time.Second {
					delete(s.rates, id)
				}
			}
			// 清除过时的挂起块
			if s.currentBlock != nil {
				for hash, block := range s.works {
					if block.NumberU64()+staleThreshold <= s.currentBlock.NumberU64() {
						delete(s.works, hash)
					}
				}
			}

		case <-s.requestExit:
			return
		}
	}
}

// makeWork为外部矿工创建工作包。工作包由3个字符串组成：
//结果[0]，32字节十六进制编码的当前块头pow哈希
//结果[1]，用于DAG的32字节十六进制编码种子哈希
//结果[2]，32字节十六进制编码边界条件（“目标”），2^256/难度
//结果[3]，十六进制编码块编号
func (s *remoteSealer) makeWork(block *block.Block) {
	hash := s.octell.SealHash(block.Header())
	s.currentWork[0] = hash.Hex()
	s.currentWork[1] = entity.BytesToHash(SeedHash(block.NumberU64())).Hex()
	s.currentWork[2] = entity.BytesToHash(new(big.Int).Div(two256, block.Difficulty()).Bytes()).Hex()
	s.currentWork[3] = operationutils.EncodeBig(block.Number())

	//追踪远程封口机获取的封口工作。
	s.currentBlock = block
	s.works[hash] = block
}

// notifyWork通知所有指定的挖掘端点要处理的新工作的可用性。
func (s *remoteSealer) notifyWork() {
	work := s.currentWork

	// 对通知的JSON负载进行编码。设置NotifyFull时，这是完整的块头，否则它是一个JSON数组。
	var blob []byte
	if s.octell.Config.NotifyFull {
		blob, _ = json.Marshal(s.currentBlock.Header())
	} else {
		blob, _ = json.Marshal(work)
	}

	s.reqWG.Add(len(s.notifyURLs))
	for _, url := range s.notifyURLs {
		go s.sendNotification(s.notifyCtx, url, blob, work)
	}
}

func (s *remoteSealer) sendNotification(ctx context.Context, url string, json []byte, work [4]string) {
	defer s.reqWG.Done()

	req, err := http.NewRequest("POST", url, bytes.NewReader(json))
	if err != nil {
		log.Warn("Can't create remote miner notification", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, remoteSealerTimeout)
	defer cancel()
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Warn("Failed to notify remote miner", "err", err)
	} else {
		log.Info("Notified remote miner", "miner", url, "hash", work[0], "target", work[2])
		resp.Body.Close()
	}
}

// submitWork验证提交的pow解决方案，返回该解决方案是否被接受（不可能既是错误的pow，也可能是任何其他错误，例如没有挂起的工作或陈旧的挖掘结果）。
func (s *remoteSealer) submitWork(nonce block.BlockNonce, mixDigest entity.Hash, sealhash entity.Hash) bool {
	if s.currentBlock == nil {
		log.Error("Pending work without block", "sealhash", sealhash)
		return false
	}
	// 确保提交的工作存在
	block := s.works[sealhash]
	if block == nil {
		log.Warn("Work submitted but none pending", "sealhash", sealhash, "curnumber", s.currentBlock.NumberU64())
		return false
	}
	// 验证提交结果的正确性。
	header := block.Header()
	header.Nonce = nonce
	header.MixDigest = mixDigest

	start := time.Now()
	if !s.noverify {
		if err := s.octell.verifySeal(nil, header, true); err != nil {
			log.Warn("Invalid proof-of-work submitted", "sealhash", sealhash, "elapsed", entity.PrettyDuration(time.Since(start)), "err", err)
			return false
		}
	}
	//确保已分配结果通道。
	if s.results == nil {
		log.Warn("Octell result channel is empty, submitted mining result is rejected")
		return false
	}
	log.Info("Verified correct proof-of-work", "sealhash", sealhash, "elapsed", entity.PrettyDuration(time.Since(start)))

	// 解决方案似乎有效，返回给工作者并通知验收。
	solution := block.WithSeal(header)

	// 提交的解决方案在验收范围内。
	if solution.NumberU64()+staleThreshold > s.currentBlock.NumberU64() {
		select {
		case s.results <- solution:
			log.Debug("Work submitted is acceptable", "number", solution.NumberU64(), "sealhash", sealhash, "hash", solution.Hash())
			return true
		default:
			log.Warn("Sealing result is not read by miner", "mode", "remote", "sealhash", sealhash)
			return false
		}
	}
	// 提交的块太旧，无法接受，请删除它。
	log.Warn("Work submitted is too old", "number", solution.NumberU64(), "sealhash", sealhash, "hash", solution.Hash())
	return false
}

// sealTask使用远程密封剂螺纹的相对结果通道包裹密封块。
type sealTask struct {
	block   *block.Block
	results chan<- *block.Block
}

//mineResult包装指定块的pow解决方案参数。
type mineResult struct {
	nonce     block.BlockNonce
	mixDigest entity.Hash
	hash      entity.Hash

	errc chan error
}

// hashrate包装远程密封器提交的哈希速率。
type hashrate struct {
	id   entity.Hash
	ping time.Time
	rate uint64

	done chan struct{}
}

//sealWork为远程封口机包装密封工作包。
type sealWork struct {
	errc chan error
	res  chan [4]string
}
