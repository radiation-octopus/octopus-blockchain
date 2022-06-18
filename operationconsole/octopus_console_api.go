package operationconsole

import (
	"context"
	"github.com/radiation-octopus/octopus-blockchain/accounts"
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/blockchain"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/oct"
	"github.com/radiation-octopus/octopus-blockchain/operationdb"
	"github.com/radiation-octopus/octopus-blockchain/vm"
	"github.com/radiation-octopus/octopus/log"
	"math/big"
	"time"
)

// 后端接口为公共API服务（由完整客户端和轻型客户端提供）提供对必要功能的访问。
type Backend interface {
	// 通用辐射章鱼API
	//SyncProgress() oct.SyncProgress

	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	//FeeHistory(ctx context.Context, blockCount int, lastBlock rpc.BlockNumber, rewardPercentiles []float64) (*big.Int, [][]*big.Int, []*big.Int, []float64, error)
	ChainDb() operationdb.Database
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
	CurrentHeader() *block.Header
	CurrentBlock() *block.Block
	//BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*block.Block, error)
	//BlockByHash(ctx context.Context, hash entity.Hash) (*block.Block, error)
	//BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*block.Block, error)
	//StateAndHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*operationdb.OperationDB, *block.Header, error)
	//StateAndHeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*operationdb.OperationDB, *block.Header, error)
	PendingBlockAndReceipts() (*block.Block, block.Receipts)
	GetReceipts(ctx context.Context, hash entity.Hash) (block.Receipts, error)
	GetTd(ctx context.Context, hash entity.Hash) *big.Int
	GetEVM(ctx context.Context, msg block.Message, state *operationdb.OperationDB, header *block.Header, vmConfig *vm.Config) (*vm.OVM, func() error, error)
	SubscribeChainEvent(ch chan<- blockchain.ChainEvent) blockchain.Subscription
	SubscribeChainHeadEvent(ch chan<- blockchain.ChainHeadEvent) blockchain.Subscription
	SubscribeChainSideEvent(ch chan<- blockchain.ChainSideEvent) blockchain.Subscription

	// 事务池API
	SendTx(signedTx *block.Transaction) error
	GetTransaction(ctx context.Context, txHash entity.Hash) (*block.Transaction, entity.Hash, uint64, uint64, error)
	GetPoolTransactions() (block.Transactions, error)
	GetPoolTransaction(txHash entity.Hash) *block.Transaction
	GetPoolNonce(ctx context.Context, addr entity.Address) (uint64, error)
	Stats() (pending int, queued int)
	TxPoolContent() (map[entity.Address]block.Transactions, map[entity.Address]block.Transactions)
	TxPoolContentFrom(addr entity.Address) (block.Transactions, block.Transactions)
	SubscribeNewTxsEvent(chan<- blockchain.NewTxsEvent) blockchain.Subscription

	// 过滤器API
	BloomStatus() (uint64, uint64)
	GetLogs(ctx context.Context, blockHash entity.Hash) ([][]*log.OctopusLog, error)
	//ServiceFilter(ctx context.Context, session *bloombits.MatcherSession)
	SubscribeLogsEvent(ch chan<- []*log.OctopusLog) blockchain.Subscription
	SubscribePendingLogsEvent(ch chan<- []*log.OctopusLog) blockchain.Subscription
	SubscribeRemovedLogsEvent(ch chan<- blockchain.RemovedLogsEvent) blockchain.Subscription

	ChainConfig() *blockchain.ChainConfig
	Engine() consensus.Engine
}

// OctAPIBackend实现octapi。完整节点的后端
type OctAPIBackend struct {
	extRPCEnabled       bool
	allowUnprotectedTxs bool
	oct                 *oct.Octopus
	//gpo                 *gasprice.Oracle
}

func (o *OctAPIBackend) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	panic("implement me")
}

func (o *OctAPIBackend) ChainDb() operationdb.Database {
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

func (o *OctAPIBackend) CurrentHeader() *block.Header {
	return o.oct.Blockchain.CurrentHeader()
}

func (o *OctAPIBackend) CurrentBlock() *block.Block {
	return o.oct.Blockchain.CurrentBlock()
}

func (o *OctAPIBackend) PendingBlockAndReceipts() (*block.Block, block.Receipts) {
	panic("implement me")
}

func (o *OctAPIBackend) GetReceipts(ctx context.Context, hash entity.Hash) (block.Receipts, error) {
	panic("implement me")
}

func (o *OctAPIBackend) GetTd(ctx context.Context, hash entity.Hash) *big.Int {
	panic("implement me")
}

func (o *OctAPIBackend) GetEVM(ctx context.Context, msg block.Message, state *operationdb.OperationDB, header *block.Header, vmConfig *vm.Config) (*vm.OVM, func() error, error) {
	panic("implement me")
}

func (o *OctAPIBackend) SubscribeChainEvent(ch chan<- blockchain.ChainEvent) blockchain.Subscription {
	panic("implement me")
}

func (o *OctAPIBackend) SubscribeChainHeadEvent(ch chan<- blockchain.ChainHeadEvent) blockchain.Subscription {
	panic("implement me")
}

func (o *OctAPIBackend) SubscribeChainSideEvent(ch chan<- blockchain.ChainSideEvent) blockchain.Subscription {
	panic("implement me")
}

func (o *OctAPIBackend) SendTx(signedTx *block.Transaction) error {
	return o.oct.TxPool().AddLocal(signedTx)
}

func (o *OctAPIBackend) GetTransaction(ctx context.Context, txHash entity.Hash) (*block.Transaction, entity.Hash, uint64, uint64, error) {
	panic("implement me")
}

func (o *OctAPIBackend) GetPoolTransactions() (block.Transactions, error) {
	panic("implement me")
}

func (o *OctAPIBackend) GetPoolTransaction(txHash entity.Hash) *block.Transaction {
	panic("implement me")
}

func (o *OctAPIBackend) GetPoolNonce(ctx context.Context, addr entity.Address) (uint64, error) {
	return o.oct.TxPool().Nonce(addr), nil
}

func (o *OctAPIBackend) Stats() (pending int, queued int) {
	panic("implement me")
}

func (o *OctAPIBackend) TxPoolContent() (map[entity.Address]block.Transactions, map[entity.Address]block.Transactions) {
	panic("implement me")
}

func (o *OctAPIBackend) TxPoolContentFrom(addr entity.Address) (block.Transactions, block.Transactions) {
	panic("implement me")
}

func (o *OctAPIBackend) SubscribeNewTxsEvent(events chan<- blockchain.NewTxsEvent) blockchain.Subscription {
	panic("implement me")
}

func (o *OctAPIBackend) BloomStatus() (uint64, uint64) {
	panic("implement me")
}

func (o *OctAPIBackend) GetLogs(ctx context.Context, blockHash entity.Hash) ([][]*log.OctopusLog, error) {
	panic("implement me")
}

func (o *OctAPIBackend) SubscribeLogsEvent(ch chan<- []*log.OctopusLog) blockchain.Subscription {
	panic("implement me")
}

func (o *OctAPIBackend) SubscribePendingLogsEvent(ch chan<- []*log.OctopusLog) blockchain.Subscription {
	panic("implement me")
}

func (o *OctAPIBackend) SubscribeRemovedLogsEvent(ch chan<- blockchain.RemovedLogsEvent) blockchain.Subscription {
	panic("implement me")
}

func (o *OctAPIBackend) ChainConfig() *blockchain.ChainConfig {
	return o.oct.BlockChain().Config()
}

func (o *OctAPIBackend) Engine() consensus.Engine {
	panic("implement me")
}
