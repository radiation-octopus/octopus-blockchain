package event

import (
	"sync"
)

//订阅表示事件流。事件的载体通常是一个通道，但不是接口的一部分。
//订阅在建立时可能会失败。通过错误通道报告故障。如果订阅存在问题（例如，传递事件的网络连接已关闭），它将收到一个值。将只发送一个值。
//订阅成功结束时（即事件源关闭时），错误通道关闭。当调用Unsubscribe时，它也会关闭。
//Unsubscribe方法取消发送事件。在任何情况下，您都必须调用Unsubscribe，以确保释放与订阅相关的资源。它可以被调用任意次数。
type Subscription interface {
	Err() <-chan error //返回错误通道
	Unsubscribe()      //取消发送事件，关闭错误通道
}

// NewSubscription在新的goroutine中运行生产者函数作为订阅。
//当调用Unsubscribe时，提供给制作人的频道关闭。如果fn返回错误，则会在订阅的错误通道上发送。
func NewSubscription(producer func(<-chan struct{}) error) Subscription {
	s := &funcSub{unsub: make(chan struct{}), err: make(chan error, 1)}
	go func() {
		defer close(s.err)
		err := producer(s.unsub)
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.unsubscribed {
			if err != nil {
				s.err <- err
			}
			s.unsubscribed = true
		}
	}()
	return s
}

type funcSub struct {
	unsub        chan struct{}
	err          chan error
	mu           sync.Mutex
	unsubscribed bool
}

func (s *funcSub) Unsubscribe() {
	s.mu.Lock()
	if s.unsubscribed {
		s.mu.Unlock()
		return
	}
	s.unsubscribed = true
	close(s.unsub)
	s.mu.Unlock()
	// 等待生产商关闭。
	<-s.err
}

func (s *funcSub) Err() <-chan error {
	return s.err
}

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
	s  Subscription
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
func (sc *SubscriptionScope) Track(s Subscription) Subscription {
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
