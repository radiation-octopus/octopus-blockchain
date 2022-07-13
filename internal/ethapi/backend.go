// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package ethapi implements the general Ethereum API functions.
package ethapi

import (
	"context"
	a "github.com/radiation-octopus/octopus-blockchain"
	"github.com/radiation-octopus/octopus-blockchain/accounts"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/event"
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
	FeeHistory(ctx context.Context, blockCount int, lastBlock rpc.BlockNumber, rewardPercentiles []float64) (*big.Int, [][]*big.Int, []*big.Int, []float64, error)
	ChainDb() typedb.Database
	AccountManager() *accounts.Manager
	ExtRPCEnabled() bool
	RPCGasCap() uint64            // rpc上eth\U调用的全局gas cap:DoS保护
	RPCEVMTimeout() time.Duration // rpc上eth\u调用的全局超时：DoS保护
	RPCTxFeeCap() float64         // 所有交易相关API的全球发送费用上限
	UnprotectedAllowed() bool     // 仅允许EIP155事务。

	// 区块链API
	SetHead(number uint64)
	HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*block2.Header, error)
	HeaderByHash(ctx context.Context, hash entity.Hash) (*block2.Header, error)
	HeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*block2.Header, error)
	CurrentHeader() *block2.Header
	CurrentBlock() *block2.Block
	BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*block2.Block, error)
	BlockByHash(ctx context.Context, hash entity.Hash) (*block2.Block, error)
	BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*block2.Block, error)
	StateAndHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*operationdb.OperationDB, *block2.Header, error)
	StateAndHeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*operationdb.OperationDB, *block2.Header, error)
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
	GetLogs(ctx context.Context, blockHash entity.Hash) ([][]*block2.Log, error)
	//ServiceFilter(ctx context.Context, session *bloombits.MatcherSession)
	SubscribeLogsEvent(ch chan<- []*block2.Log) event.Subscription
	SubscribePendingLogsEvent(ch chan<- []*block2.Log) event.Subscription
	SubscribeRemovedLogsEvent(ch chan<- event.RemovedLogsEvent) event.Subscription

	ChainConfig() *entity.ChainConfig
	Engine() consensus.Engine
}

func GetAPIs(apiBackend Backend) []rpc.API {
	nonceLock := new(AddrLocker)
	return []rpc.API{
		{
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewEthereumAPI(apiBackend),
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewBlockChainAPI(apiBackend),
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewTransactionAPI(apiBackend, nonceLock),
		}, {
			Namespace: "txpool",
			Version:   "1.0",
			Service:   NewTxPoolAPI(apiBackend),
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewDebugAPI(apiBackend),
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewEthereumAccountAPI(apiBackend.AccountManager()),
		}, {
			Namespace: "personal",
			Version:   "1.0",
			Service:   NewPersonalAccountAPI(apiBackend, nonceLock),
		},
	}
}
