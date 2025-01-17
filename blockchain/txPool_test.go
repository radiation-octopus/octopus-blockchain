package blockchain

import (
	"crypto/ecdsa"
	"errors"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/entity/rawdb"
	"github.com/radiation-octopus/octopus-blockchain/event"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus-blockchain/terr"
	"math/big"
	"sync/atomic"
	"testing"
)

var (
	// testTxPoolConfig是一种事务池配置，在测试期间没有使用有状态磁盘副作用。
	testTxPoolConfig TxPoolConfig

	TestChainConfig = &entity.ChainConfig{big.NewInt(1)}
)

func TestInvalidTransactions(t *testing.T) {
	t.Parallel()

	//复制数据库副本，构建区块链测试链，新建交易池；用于处理交易；最后会关闭释放交易池
	pool, key := setupTxPool()
	defer pool.Stop()

	//新建交易，并且给交易签名
	tx := transaction(0, 100, key)
	//通过交易参数获取发件人地址
	from, _ := deriveSender(tx)

	//给测试用户添加金额
	testAddBalance(pool, from, big.NewInt(1))
	if err := pool.AddRemote(tx); !errors.Is(err, terr.ErrInsufficientFunds) {
		t.Error("expected", terr.ErrInsufficientFunds)
	}

	balance := new(big.Int).Add(tx.Value(), new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas()), tx.GasPrice()))
	testAddBalance(pool, from, balance)
	if err := pool.AddRemote(tx); !errors.Is(err, terr.ErrIntrinsicGas) {
		t.Error("expected", terr.ErrIntrinsicGas, "got", err)
	}

	testSetNonce(pool, from, 1)
	testAddBalance(pool, from, big.NewInt(500000))
	tx = transaction(0, 100000, key)
	//if err := pool.AddRemote(tx); !errors.Is(err, terr.ErrNonceTooLow) {
	//	t.Error("expected", terr.ErrNonceTooLow)
	//}

	tx = transaction(1, 100000, key)
	pool.gasPrice = big.NewInt(1000)
	if err := pool.AddRemote(tx); err != ErrUnderpriced {
		t.Error("expected", ErrUnderpriced, "got", err)
	}

	froml, _ := deriveSender(tx)
	testAddBalance(pool, froml, big.NewInt(500000))
	if err := pool.AddLocal(tx); err != nil {
		t.Error("expected", nil, "got", err)
	}
}

func setupTxPool() (*TxPool, *ecdsa.PrivateKey) {
	return setupTxPoolWithConfig(TestChainConfig)
}

func setupTxPoolWithConfig(config *entity.ChainConfig) (*TxPool, *ecdsa.PrivateKey) {
	statedb, _ := operationdb.NewOperationDb(entity.Hash{}, rawdb.NewDatabase(rawdb.NewMemoryDatabase()))
	blockcha := &testBlockChain{10000000, statedb, new(event.Feed)}

	key, _ := crypto.GenerateKey()
	pool := NewTxPool(testTxPoolConfig, blockcha)

	// 等待池初始化
	<-pool.InitDoneCh
	return pool, key
}

type testBlockChain struct {
	gasLimit      uint64 // 必须是64位对齐（原子访问）的第一个字段
	statedb       *operationdb.OperationDB
	chainHeadFeed *event.Feed
}

func (bc *testBlockChain) CurrentBlock() *block2.Block {
	return block2.NewBlock(&block2.Header{
		GasLimit: atomic.LoadUint64(&bc.gasLimit),
	}, nil, nil)
}

func (bc *testBlockChain) GetBlock(hash entity.Hash, number uint64) *block2.Block {
	return bc.CurrentBlock()
}

func (bc *testBlockChain) StateAt(entity.Hash) (*operationdb.OperationDB, error) {
	return bc.statedb, nil
}

func (bc *testBlockChain) SubscribeChainHeadEvent(ch chan<- event.ChainHeadEvent) event.Subscription {
	return bc.chainHeadFeed.Subscribe(ch)
}

func transaction(nonce uint64, gaslimit uint64, key *ecdsa.PrivateKey) *block2.Transaction {
	return pricedTransaction(nonce, gaslimit, big.NewInt(1), key)
}

func pricedTransaction(nonce uint64, gaslimit uint64, gasprice *big.Int, key *ecdsa.PrivateKey) *block2.Transaction {
	tx, _ := block2.SignTx(block2.NewTransaction(nonce, entity.Address{}, big.NewInt(100), gaslimit, gasprice, nil), block2.HomesteadSigner{}, key)
	return tx
}

func deriveSender(tx *block2.Transaction) (entity.Address, error) {
	return block2.Sender(block2.HomesteadSigner{}, tx)
}

func testAddBalance(pool *TxPool, addr entity.Address, amount *big.Int) {
	pool.mu.Lock()
	pool.currentState.AddBalance(addr, amount)
	pool.mu.Unlock()
}

func testSetNonce(pool *TxPool, addr entity.Address, nonce uint64) {
	pool.mu.Lock()
	pool.currentState.SetNonce(addr, nonce)
	pool.mu.Unlock()
}
