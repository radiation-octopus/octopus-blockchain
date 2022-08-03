package oct

import (
	"context"
	"errors"
	a "github.com/radiation-octopus/octopus-blockchain"
	ethereum "github.com/radiation-octopus/octopus-blockchain"
	"github.com/radiation-octopus/octopus-blockchain/accounts"
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/event"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus-blockchain/rpc"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus-blockchain/vm"
	"math/big"
	"time"
)

// 后端接口为公共API服务（由完整客户端和轻型客户端提供）提供对必要功能的访问。
type Backend interface {
	// 通用辐射章鱼API
	SyncProgress() a.SyncProgress

	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	//FeeHistory(ctx context.Context, blockCount int, lastBlock rpc.BlockNumber, rewardPercentiles []float64) (*big.Int, [][]*big.Int, []*big.Int, []float64, error)
	ChainDb() typedb.Database
	AccountManager() *accounts.Manager
	ExtRPCEnabled() bool
	RPCGasCap() uint64            // rpc上eth\U调用的全局gas cap:DoS保护
	RPCEVMTimeout() time.Duration // rpc上eth\u调用的全局超时：DoS保护
	RPCTxFeeCap() float64         // 所有交易相关API的全球发送费用上限
	UnprotectedAllowed() bool     // 仅允许EIP155事务。

	// 区块链API
	SetHead(number uint64)
	//Headeblockmber(ctx context.Context, number rpc.BlockNumber) (*block.Header, error)
	//HeaderByHash(ctx context.Context, hash entity.Hash) (*block.Header, error)
	//HeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*block.Header, error)
	CurrentHeader() *block2.Header
	CurrentBlock() *block2.Block
	//BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*block.Block, error)
	//BlockByHash(ctx context.Context, hash entity.Hash) (*block.Block, error)
	//BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*block.Block, error)
	//StateAndHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*operationdb.OperationDB, *block.Header, error)
	//StateAndHeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*operationdb.OperationDB, *block.Header, error)
	PendingBlockAndReceipts() (*block2.Block, block2.Receipts)
	GetReceipts(ctx context.Context, hash entity.Hash) (block2.Receipts, error)
	GetTd(ctx context.Context, hash entity.Hash) *big.Int
	GetEVM(ctx context.Context, msg block2.Message, state *operationdb.OperationDB, header *block2.Header, vmConfig *vm.Config) (*vm.OVM, func() error, error)
	SubscribeChainEvent(ch chan<- event.ChainEvent) event.Subscription
	SubscribeChainHeadEvent(ch chan<- event.ChainHeadEvent) event.Subscription
	SubscribeChainSideEvent(ch chan<- event.ChainSideEvent) event.Subscription

	// 事务池API
	SendTx(signedTx *block2.Transaction) error
	GetTransaction(ctx context.Context, txHash entity.Hash) (*block2.Transaction, entity.Hash, uint64, uint64, error)
	GetPoolTransactions() (block2.Transactions, error)
	GetPoolTransaction(txHash entity.Hash) *block2.Transaction
	GetPoolNonce(ctx context.Context, addr entity.Address) (uint64, error)
	Stats() (pending int, queued int)
	TxPoolContent() (map[entity.Address]block2.Transactions, map[entity.Address]block2.Transactions)
	TxPoolContentFrom(addr entity.Address) (block2.Transactions, block2.Transactions)
	SubscribeNewTxsEvent(chan<- event.NewTxsEvent) event.Subscription

	// 过滤器API
	BloomStatus() (uint64, uint64)
	GetLogs(ctx context.Context, blockHash entity.Hash) ([][]*log.Logger, error)
	//ServiceFilter(ctx context.Context, session *bloombits.MatcherSession)
	SubscribeLogsEvent(ch chan<- []*log.Logger) event.Subscription
	SubscribePendingLogsEvent(ch chan<- []*log.Logger) event.Subscription
	SubscribeRemovedLogsEvent(ch chan<- event.RemovedLogsEvent) event.Subscription

	ChainConfig() *entity.ChainConfig
	Engine() consensus.Engine
}

// OctAPIBackend实现octapi。完整节点的后端
type OctAPIBackend struct {
	extRPCEnabled       bool
	allowUnprotectedTxs bool
	oct                 *Octopus
	//gpo                 *gasprice.Oracle
}

func (o *OctAPIBackend) FeeHistory(ctx context.Context, blockCount int, lastBlock rpc.BlockNumber, rewardPercentiles []float64) (*big.Int, [][]*big.Int, []*big.Int, []float64, error) {
	panic("implement me")
}

func (o *OctAPIBackend) SyncProgress() ethereum.SyncProgress {
	panic("implement me")
}

func (o *OctAPIBackend) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	panic("implement me")
}

func (o *OctAPIBackend) ChainDb() typedb.Database {
	return o.oct.ChainDb()
}

func (o *OctAPIBackend) AccountManager() *accounts.Manager {
	return o.oct.AccountManager()
}

func (o *OctAPIBackend) ExtRPCEnabled() bool {
	return o.extRPCEnabled
}

func (o *OctAPIBackend) RPCGasCap() uint64 {
	panic("implement me")
}

func (o *OctAPIBackend) RPCEVMTimeout() time.Duration {
	panic("implement me")
}

func (o *OctAPIBackend) RPCTxFeeCap() float64 {
	return o.oct.GetCfg().RPCTxFeeCap
}

func (o *OctAPIBackend) UnprotectedAllowed() bool {
	return o.allowUnprotectedTxs
}

func (o *OctAPIBackend) SetHead(number uint64) {
	panic("implement me")
}

func (o *OctAPIBackend) HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*block2.Header, error) {
	// 待处理区块仅由矿工知道
	if number == rpc.PendingBlockNumber {
		block := o.oct.miner.PendingBlock()
		return block.Header(), nil
	}
	// Otherwise resolve and return the block
	if number == rpc.LatestBlockNumber {
		return o.oct.Blockchain.CurrentBlock().Header(), nil
	}
	if number == rpc.FinalizedBlockNumber {
		return o.oct.Blockchain.CurrentFinalizedBlock().Header(), nil
	}
	return o.oct.Blockchain.GetHeaderByNumber(uint64(number)), nil
}

func (o *OctAPIBackend) HeaderByHash(ctx context.Context, hash entity.Hash) (*block2.Header, error) {
	return o.oct.Blockchain.GetHeaderByHash(hash), nil
}

func (o *OctAPIBackend) HeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*block2.Header, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return o.HeaderByNumber(ctx, blockNr)
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		header := o.oct.Blockchain.GetHeaderByHash(hash)
		if header == nil {
			return nil, errors.New("header for hash not found")
		}
		if blockNrOrHash.RequireCanonical && o.oct.Blockchain.GetCanonicalHash(header.Number.Uint64()) != hash {
			return nil, errors.New("hash is not currently canonical")
		}
		return header, nil
	}
	return nil, errors.New("invalid arguments; neither block nor hash specified")
}

func (o *OctAPIBackend) CurrentHeader() *block2.Header {
	return o.oct.Blockchain.CurrentHeader()
}

func (o *OctAPIBackend) CurrentBlock() *block2.Block {
	return o.oct.Blockchain.CurrentBlock()
}

func (o *OctAPIBackend) BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*block2.Block, error) {
	// Pending block is only known by the miner
	if number == rpc.PendingBlockNumber {
		block := o.oct.miner.PendingBlock()
		return block, nil
	}
	// Otherwise resolve and return the block
	if number == rpc.LatestBlockNumber {
		return o.oct.Blockchain.CurrentBlock(), nil
	}
	if number == rpc.FinalizedBlockNumber {
		return o.oct.Blockchain.CurrentFinalizedBlock(), nil
	}
	return o.oct.Blockchain.GetBlockByNumber(uint64(number)), nil
}

func (o *OctAPIBackend) BlockByHash(ctx context.Context, hash entity.Hash) (*block2.Block, error) {
	panic("imp")
}

func (o *OctAPIBackend) BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*block2.Block, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return o.BlockByNumber(ctx, blockNr)
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		header := o.oct.Blockchain.GetHeaderByHash(hash)
		if header == nil {
			return nil, errors.New("header for hash not found")
		}
		if blockNrOrHash.RequireCanonical && o.oct.Blockchain.GetCanonicalHash(header.Number.Uint64()) != hash {
			return nil, errors.New("hash is not currently canonical")
		}
		block := o.oct.Blockchain.GetBlock(hash, header.Number.Uint64())
		if block == nil {
			return nil, errors.New("header found, but block body is missing")
		}
		return block, nil
	}
	return nil, errors.New("invalid arguments; neither block nor hash specified")
}

func (o *OctAPIBackend) StateAndHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*operationdb.OperationDB, *block2.Header, error) {
	// Pending state is only known by the miner
	//if number == rpc.PendingBlockNumber {
	//	block, state := o.oct.miner.Pending()
	//	return state, block.Header(), nil
	//}
	// Otherwise resolve the block number and return its state
	header, err := o.HeaderByNumber(ctx, number)
	if err != nil {
		return nil, nil, err
	}
	if header == nil {
		return nil, nil, errors.New("header not found")
	}
	stateDb, err := o.oct.BlockChain().StateAt(header.Root)
	return stateDb, header, err
}

func (o *OctAPIBackend) StateAndHeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*operationdb.OperationDB, *block2.Header, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return o.StateAndHeaderByNumber(ctx, blockNr)
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		header, err := o.HeaderByHash(ctx, hash)
		if err != nil {
			return nil, nil, err
		}
		if header == nil {
			return nil, nil, errors.New("header for hash not found")
		}
		if blockNrOrHash.RequireCanonical && o.oct.Blockchain.GetCanonicalHash(header.Number.Uint64()) != hash {
			return nil, nil, errors.New("hash is not currently canonical")
		}
		stateDb, err := o.oct.BlockChain().StateAt(header.Root)
		return stateDb, header, err
	}
	return nil, nil, errors.New("invalid arguments; neither block nor hash specified")
}

func (o *OctAPIBackend) PendingBlockAndReceipts() (*block2.Block, block2.Receipts) {
	panic("implement me")
}

func (o *OctAPIBackend) GetReceipts(ctx context.Context, hash entity.Hash) (block2.Receipts, error) {
	panic("implement me")
}

func (o *OctAPIBackend) GetTd(ctx context.Context, hash entity.Hash) *big.Int {
	panic("implement me")
}

func (o *OctAPIBackend) GetEVM(ctx context.Context, msg block2.Message, state *operationdb.OperationDB, header *block2.Header, vmConfig *vm.Config) (*vm.OVM, func() error, error) {
	panic("implement me")
}

func (o *OctAPIBackend) SubscribeChainEvent(ch chan<- event.ChainEvent) event.Subscription {
	return o.oct.BlockChain().SubscribeChainEvent(ch)
}

func (o *OctAPIBackend) SubscribeChainHeadEvent(ch chan<- event.ChainHeadEvent) event.Subscription {
	panic("implement me")
}

func (o *OctAPIBackend) SubscribeChainSideEvent(ch chan<- event.ChainSideEvent) event.Subscription {
	panic("implement me")
}

func (o *OctAPIBackend) SendTx(signedTx *block2.Transaction) error {
	return o.oct.TxPool().AddLocal(signedTx)
}

func (o *OctAPIBackend) GetTransaction(ctx context.Context, txHash entity.Hash) (*block2.Transaction, entity.Hash, uint64, uint64, error) {
	panic("implement me")
}

func (o *OctAPIBackend) GetPoolTransactions() (block2.Transactions, error) {
	panic("implement me")
}

func (o *OctAPIBackend) GetPoolTransaction(txHash entity.Hash) *block2.Transaction {
	panic("implement me")
}

func (o *OctAPIBackend) GetPoolNonce(ctx context.Context, addr entity.Address) (uint64, error) {
	return o.oct.TxPool().Nonce(addr), nil
}

func (o *OctAPIBackend) Stats() (pending int, queued int) {
	panic("implement me")
}

func (o *OctAPIBackend) TxPoolContent() (map[entity.Address]block2.Transactions, map[entity.Address]block2.Transactions) {
	panic("implement me")
}

func (o *OctAPIBackend) TxPoolContentFrom(addr entity.Address) (block2.Transactions, block2.Transactions) {
	panic("implement me")
}

func (b *OctAPIBackend) TxPool() *blockchain.TxPool {
	return b.oct.TxPool()
}

func (o *OctAPIBackend) SubscribeNewTxsEvent(events chan<- event.NewTxsEvent) event.Subscription {
	return o.oct.TxPool().SubscribeNewTxsEvent(events)
}

func (o *OctAPIBackend) BloomStatus() (uint64, uint64) {
	panic("implement me")
}

func (o *OctAPIBackend) GetLogs(ctx context.Context, blockHash entity.Hash) ([][]*block2.Log, error) {
	panic("implement me")
}

func (o *OctAPIBackend) SubscribeLogsEvent(ch chan<- []*block2.Log) event.Subscription {
	return o.oct.BlockChain().SubscribeLogsEvent(ch)
}

func (o *OctAPIBackend) SubscribePendingLogsEvent(ch chan<- []*block2.Log) event.Subscription {
	return o.oct.miner.SubscribePendingLogs(ch)
}

func (o *OctAPIBackend) SubscribeRemovedLogsEvent(ch chan<- event.RemovedLogsEvent) event.Subscription {
	return o.oct.BlockChain().SubscribeRemovedLogsEvent(ch)
}

func (b *OctAPIBackend) StartMining(threads int) error {
	return b.oct.StartMining(threads)
}

func (o *OctAPIBackend) ChainConfig() *entity.ChainConfig {
	return o.oct.BlockChain().Config()
}

func (o *OctAPIBackend) Engine() consensus.Engine {
	panic("implement me")
}
