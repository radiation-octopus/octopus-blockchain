package event

import (
	"github.com/ethereum/go-ethereum/event"
	"sync"
)

/*
SubscriptionScope提供了一种功能，可以一次取消订阅多个订阅。
对于处理多个订阅的代码，可以使用一个作用域通过单个调用方便地取消所有订阅。该示例演示了在大型程序中的典型用法。
零值已准备好使用。
*/
type SubscriptionScope struct {
	mu     sync.Mutex
	subs   map[*scopeSub]struct{}
	closed bool
}

//Close calls取消对所有跟踪订阅的订阅，并阻止进一步添加到跟踪集。关闭后跟踪的调用返回nil。
func (sc *SubscriptionScope) Close() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.closed {
		return
	}
	sc.closed = true
	for s := range sc.subs {
		s.s.Unsubscribe()
	}
	sc.subs = nil
}

// Count返回跟踪的订阅数。它用于调试。
func (sc *SubscriptionScope) Count() int {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return len(sc.subs)
}

type scopeSub struct {
	sc *SubscriptionScope
	s  event.Subscription
}

func (s *scopeSub) Err() <-chan error {
	return s.s.Err()
}

func (s *scopeSub) Unsubscribe() {
	s.s.Unsubscribe()
	s.sc.mu.Lock()
	defer s.sc.mu.Unlock()
	delete(s.sc.subs, s)
}

// Track开始跟踪订阅。如果作用域已关闭，Track将返回nil。返回的订阅是包装。取消订阅包装将其从范围中删除。
func (sc *SubscriptionScope) Track(s event.Subscription) event.Subscription {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.closed {
		return nil
	}
	if sc.subs == nil {
		sc.subs = make(map[*scopeSub]struct{})
	}
	ss := &scopeSub{sc, s}
	sc.subs[ss] = struct{}{}
	return ss
}
