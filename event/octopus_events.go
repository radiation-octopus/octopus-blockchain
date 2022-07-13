package event

import (
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	block2 "github.com/radiation-octopus/octopus-blockchain/entity/block"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"reflect"
	"sync"
	"time"
)

// TypeMuxEvent是推送到订阅者的带有时间标签的通知。
type TypeMuxEvent struct {
	Time time.Time
	Data interface{}
}

//TypeMux将事件发送给注册的接收器。
//可以注册接收器来处理特定类型的事件。
//停止多路复用器后调用的任何操作都将返回ErrMuxClosed。
//零值已准备好使用。不推荐：使用提要
type TypeMux struct {
	mutex   sync.RWMutex
	subm    map[reflect.Type][]*TypeMuxSubscription
	stopped bool
}

//在关闭的TypeMux上发布时返回ErrMuxClosed。
var ErrMuxClosed = errors.New("event: mux closed")

// Post向为给定类型注册的所有接收器发送事件。如果多路复用器已停止，则返回ErrMuxClosed。
func (mux *TypeMux) Post(ev interface{}) error {
	event := &TypeMuxEvent{
		Time: time.Now(),
		Data: ev,
	}
	rtyp := reflect.TypeOf(ev)
	mux.mutex.RLock()
	if mux.stopped {
		mux.mutex.RUnlock()
		return ErrMuxClosed
	}
	subs := mux.subm[rtyp]
	mux.mutex.RUnlock()
	for _, sub := range subs {
		sub.deliver(event)
	}
	return nil
}

// Subscribe为给定类型的事件创建订阅。订阅的频道在取消订阅或多路复用器关闭时关闭。
func (mux *TypeMux) Subscribe(types ...interface{}) *TypeMuxSubscription {
	sub := newsub(mux)
	mux.mutex.Lock()
	defer mux.mutex.Unlock()
	if mux.stopped {
		// 将状态设置为closed（已关闭），以便在此呼叫后呼叫Unsubscribe将短路。
		sub.closed = true
		close(sub.postC)
	} else {
		if mux.subm == nil {
			mux.subm = make(map[reflect.Type][]*TypeMuxSubscription)
		}
		for _, t := range types {
			rtyp := reflect.TypeOf(t)
			oldsubs := mux.subm[rtyp]
			if find(oldsubs, sub) != -1 {
				panic(fmt.Sprintf("event: duplicate type %s in Subscribe", rtyp))
			}
			subs := make([]*TypeMuxSubscription, len(oldsubs)+1)
			copy(subs, oldsubs)
			subs[len(oldsubs)] = sub
			mux.subm[rtyp] = subs
		}
	}
	return sub
}

func (mux *TypeMux) del(s *TypeMuxSubscription) {
	mux.mutex.Lock()
	defer mux.mutex.Unlock()
	for typ, subs := range mux.subm {
		if pos := find(subs, s); pos >= 0 {
			if len(subs) == 1 {
				delete(mux.subm, typ)
			} else {
				mux.subm[typ] = posdelete(subs, pos)
			}
		}
	}
}

//TypeMux订阅是通过TypeMux建立的订阅。
type TypeMuxSubscription struct {
	mux     *TypeMux
	created time.Time
	closeMu sync.Mutex
	closing chan struct{}
	closed  bool

	// 这两个通道相同。它们单独存储，因此可以将POST设置为零，而不影响Chan的返回值。
	postMu sync.RWMutex
	readC  <-chan *TypeMuxEvent
	postC  chan<- *TypeMuxEvent
}

func newsub(mux *TypeMux) *TypeMuxSubscription {
	c := make(chan *TypeMuxEvent)
	return &TypeMuxSubscription{
		mux:     mux,
		created: time.Now(),
		readC:   c,
		postC:   c,
		closing: make(chan struct{}),
	}
}

func (s *TypeMuxSubscription) deliver(event *TypeMuxEvent) {
	// 陈旧事件时短路交付
	if s.created.After(event.Time) {
		return
	}
	// 以其他方式交付活动
	s.postMu.RLock()
	defer s.postMu.RUnlock()

	select {
	case s.postC <- event:
	case <-s.closing:
	}
}

func (s *TypeMuxSubscription) Unsubscribe() {
	s.mux.del(s)
	s.closewait()
}

func (s *TypeMuxSubscription) Chan() <-chan *TypeMuxEvent {
	return s.readC
}

func (s *TypeMuxSubscription) closewait() {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return
	}
	close(s.closing)
	s.closed = true

	s.postMu.Lock()
	defer s.postMu.Unlock()
	close(s.postC)
	s.postC = nil
}

//当一批事务进入事务池时，会过帐NewTxsEvent。
type NewTxsEvent struct{ Txs []*block2.Transaction }

// 导入块后，将发布NewMinedBlockEvent。
type NewMinedBlockEvent struct{ Block *block2.Block }

// RemovedLogseEvent在发生reorg时发布
type RemovedLogsEvent struct{ Logs []*block2.Log }

type ChainEvent struct {
	Block *block2.Block
	Hash  entity.Hash
	Logs  []*log.Logger
}

type ChainSideEvent struct {
	Block *block2.Block
}

type ChainHeadEvent struct{ Block *block2.Block }

func find(slice []*TypeMuxSubscription, item *TypeMuxSubscription) int {
	for i, v := range slice {
		if v == item {
			return i
		}
	}
	return -1
}

func posdelete(slice []*TypeMuxSubscription, pos int) []*TypeMuxSubscription {
	news := make([]*TypeMuxSubscription, len(slice)-1)
	copy(news[:pos], slice[:pos])
	copy(news[pos:], slice[pos+1:])
	return news
}
