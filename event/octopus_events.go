package event

import (
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus/log"
)

//订阅表示事件流。事件的载体通常是一个通道，但不是接口的一部分。
//订阅在建立时可能会失败。通过错误通道报告故障。如果订阅存在问题（例如，传递事件的网络连接已关闭），它将收到一个值。将只发送一个值。
//订阅成功结束时（即事件源关闭时），错误通道关闭。当调用Unsubscribe时，它也会关闭。
//Unsubscribe方法取消发送事件。在任何情况下，您都必须调用Unsubscribe，以确保释放与订阅相关的资源。它可以被调用任意次数。
type Subscription interface {
	Err() <-chan error //返回错误通道
	Unsubscribe()      //取消发送事件，关闭错误通道
}

//当一批事务进入事务池时，会过帐NewTxsEvent。
type NewTxsEvent struct{ Txs []*block2.Transaction }

// 导入块后，将发布NewMinedBlockEvent。
type NewMinedBlockEvent struct{ Block *block2.Block }

// RemovedLogseEvent在发生reorg时发布
type RemovedLogsEvent struct{ Logs []*log.OctopusLog }

type ChainEvent struct {
	Block *block2.Block
	Hash  entity.Hash
	Logs  []*log.OctopusLog
}

type ChainSideEvent struct {
	Block *block2.Block
}

type ChainHeadEvent struct{ Block *block2.Block }
