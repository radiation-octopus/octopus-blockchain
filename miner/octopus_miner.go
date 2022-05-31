package miner

import (
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"math/big"
	"sync"
	"time"
)

//后端包含所有的处理区块的方法
type Backend interface {
	BlockChain() *blockchain.BlockChain
	TxPool() *blockchain.TxPool
	StateAtBlock(block *block.Block, reexec uint64, base *operationdb.OperationDB, checkLive bool, preferDisk bool) (statedb *operationdb.OperationDB, err error)
}

//链启动类，配置参数启动
func (m *Miner) start() {
	//初始化工作区
	//New(m.oct,m.worker.config,m.engine)
}

func (m *Miner) close() {

}

// 工作的配置参数
type Config struct {
	Etherbase  entity.Address `toml:",omitempty"` // 区块开采奖励的公共广播
	Notify     []string       `toml:",omitempty"` // 要通知新工作包http url列表
	NotifyFull bool           `toml:",omitempty"` // 使用挂起的块标题
	ExtraData  entity.Bytes   `toml:",omitempty"` // 阻止工作者的额外数据
	GasFloor   uint64         // 工作区块的目标gas底线
	GasCeil    uint64         // 工作区块的目标gas上限
	GasPrice   *big.Int       // 工作交易的最低gas价格
	Recommit   time.Duration  // 工作者重新工作的时间间隔
	Noverify   bool           // 禁止远程工作
}

type Miner struct {
	//mux      *event.TypeMux			//向注册者发送事件
	worker   *worker             //工作流程
	coinbase entity.Address      //工作者地址
	oct      Backend             //接口
	engine   consensus.Engine    //引擎
	exitCh   chan struct{}       //退出开关
	startCh  chan entity.Address //开启开关
	stopCh   chan struct{}       //停止开关

	wg sync.WaitGroup //同步属性
}

func New(oct Backend, config *Config, engine consensus.Engine) *Miner {
	miner := &Miner{
		oct: oct,
		//mux:     mux,
		engine:  engine,
		exitCh:  make(chan struct{}),
		startCh: make(chan entity.Address),
		stopCh:  make(chan struct{}),
		worker:  newWorker(config, engine, oct, true),
	}
	miner.wg.Add(1)
	go miner.update()
	return miner
}

//工作更新事件
func (miner *Miner) update() {
	defer miner.wg.Done()

	//监听downLoader事件，控制工作的启动与关闭
	//events := miner.mux.Subscribe(downloader.StartEvent{}, downloader.DoneEvent{}, downloader.FailedEvent{})
	//defer func() {
	//	if !events.Closed() {
	//		events.Unsubscribe()
	//	}
	//}()

	shouldStart := false
	if shouldStart {

	}
	canStart := true
	//dlEventCh := events.Chan()
	for {
		select {
		//case ev := <-dlEventCh:
		//	if ev == nil {
		//		// Unsubscription done, stop listening
		//		dlEventCh = nil
		//		continue
		//	}
		//	switch ev.Data.(type) {
		//	case downloader.StartEvent:
		//		wasMining := miner.Mining()
		//		miner.worker.stop()
		//		canStart = false
		//		if wasMining {
		//			// Resume mining after sync was finished
		//			shouldStart = true
		//			log.Info("Mining aborted due to sync")
		//		}
		//	case downloader.FailedEvent:
		//		canStart = true
		//		if shouldStart {
		//			miner.SetEtherbase(miner.coinbase)
		//			miner.worker.start()
		//		}
		//	case downloader.DoneEvent:
		//		canStart = true
		//		if shouldStart {
		//			miner.SetEtherbase(miner.coinbase)
		//			miner.worker.start()
		//		}
		//		// Stop reacting to downloader events
		//		events.Unsubscribe()
		//	}
		case addr := <-miner.startCh:
			miner.SetEtherbase(addr)
			if canStart {
				miner.worker.start()
			}
			shouldStart = true
		case <-miner.stopCh:
			shouldStart = false
			miner.worker.stop()
		case <-miner.exitCh:
			miner.worker.close()
			return
		}
	}
}

func (miner *Miner) Start(coinbase entity.Address) {
	miner.startCh <- coinbase
}

func (miner *Miner) Stop() {
	miner.stopCh <- struct{}{}
}

func (miner *Miner) Close() {
	close(miner.exitCh)
	miner.wg.Wait()
}

func (miner *Miner) Mining() bool {
	return miner.worker.isRunning()
}

func (miner *Miner) SetEtherbase(addr entity.Address) {
	miner.coinbase = addr
	miner.worker.setEtherbase(addr)
}
