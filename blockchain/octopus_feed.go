package blockchain

import (
	"errors"
	"reflect"
	"sync"
)

var errBadChannel = errors.New("event: Subscribe argument does not have sendable channel type")

// 这是sendCases中第一个实际订阅通道的索引。sendCases[0]是removeSub通道的SelectRecv案例。
const firstSubSendCase = 1

//Feed实现了一对多订阅，其中事件的载体是一个频道
type Feed struct {
	once      sync.Once        //只能初始化一次
	sendLock  chan struct{}    //单元素缓冲区
	removeSub chan interface{} //中断发送
	sendCases caseList         //元素活动集
	mu        sync.Mutex
	inbox     caseList
	etype     reflect.Type
}

//Subscribe向提要添加频道。在取消订阅之前，将来的发送将在通道上传递。添加的所有通道必须具有相同的元素类型。
//通道应具有足够的缓冲空间，以避免阻塞其他订阅者。慢速订阅服务器不会被丢弃
func (f *Feed) Subscribe(channel interface{}) Subscription {
	f.once.Do(f.init)

	chanval := reflect.ValueOf(channel)
	chantyp := chanval.Type()
	if chantyp.Kind() != reflect.Chan || chantyp.ChanDir()&reflect.SendDir == 0 {
		panic(errBadChannel)
	}
	sub := &feedSub{feed: f, channel: chanval, err: make(chan error, 1)}

	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.typecheck(chantyp.Elem()) {
		panic(feedTypeError{op: "Subscribe", got: chantyp, want: reflect.ChanOf(reflect.SendDir, f.etype)})
	}
	//将select案例添加到收件箱。
	//下一次发送将把它添加到f.sendCases。
	cas := reflect.SelectCase{Dir: reflect.SelectSend, Chan: chanval}
	f.inbox = append(f.inbox, cas)
	return sub
}

func (f *Feed) init() {
	f.removeSub = make(chan interface{})
	f.sendLock = make(chan struct{}, 1)
	f.sendLock <- struct{}{}
	f.sendCases = caseList{{Chan: reflect.ValueOf(f.removeSub), Dir: reflect.SelectRecv}}
}
func (f *Feed) remove(sub *feedSub) {
	// 首先从收件箱中删除，其中包括尚未添加到f.sendCases的频道。
	ch := sub.channel.Interface()
	f.mu.Lock()
	index := f.inbox.find(ch)
	if index != -1 {
		f.inbox = f.inbox.delete(index)
		f.mu.Unlock()
		return
	}
	f.mu.Unlock()

	select {
	case f.removeSub <- ch:
		// Send将从f.sendCases中删除通道。
	case <-f.sendLock:
		// 没有发送正在进行，现在我们有了发送锁定，请删除频道。
		f.sendCases = f.sendCases.delete(f.sendCases.find(ch))
		f.sendLock <- struct{}{}
	}
}

// 注意：呼叫者必须持有f.mu
func (f *Feed) typecheck(typ reflect.Type) bool {
	if f.etype == nil {
		f.etype = typ
		return true
	}
	return f.etype == typ
}

//发送同时发送到所有订阅的频道。它返回向其发送值的订户数。
func (f *Feed) Send(value interface{}) (nsent int) {
	rvalue := reflect.ValueOf(value)

	f.once.Do(f.init)
	<-f.sendLock

	// 获取发送锁定后，从收件箱添加新案例。
	f.mu.Lock()
	f.sendCases = append(f.sendCases, f.inbox...)
	f.inbox = nil

	if !f.typecheck(rvalue.Type()) {
		f.sendLock <- struct{}{}
		f.mu.Unlock()
		panic(feedTypeError{op: "Send", got: rvalue.Type(), want: f.etype})
	}
	f.mu.Unlock()

	// 在所有通道上设置发送值。
	for i := firstSubSendCase; i < len(f.sendCases); i++ {
		f.sendCases[i].Send = rvalue
	}

	// 发送，直到选择了除removeSub之外的所有频道。”“案例”跟踪sendCases的前缀。当发送成功时，相应的case移动到“cases”的末尾，并收缩一个元素。
	cases := f.sendCases
	for {
		// 快速路径：在添加到选择集之前，尝试在不阻塞的情况下发送。如果订阅者速度足够快并且有可用的缓冲区空间，这通常会成功。
		for i := firstSubSendCase; i < len(cases); i++ {
			if cases[i].Chan.TrySend(rvalue) {
				nsent++
				cases = cases.deactivate(i)
				i--
			}
		}
		if len(cases) == firstSubSendCase {
			break
		}
		// 在所有接收器上选择，等待它们解除锁定。
		chosen, recv, _ := reflect.Select(cases)
		if chosen == 0 /* <-f.removeSub */ {
			index := f.sendCases.find(recv.Interface())
			f.sendCases = f.sendCases.delete(index)
			if index >= 0 && index < len(cases) {
				// 收缩“案例”，因为删除的案例仍处于活动状态。
				cases = f.sendCases[:len(cases)-1]
			}
		} else {
			cases = cases.deactivate(chosen)
			nsent++
		}
	}

	// 忘记发送值，并移交发送锁。
	for i := firstSubSendCase; i < len(f.sendCases); i++ {
		f.sendCases[i].Send = reflect.Value{}
	}
	f.sendLock <- struct{}{}
	return nsent
}

/*

 */
type caseList []reflect.SelectCase

//find返回包含给定通道的案例索引。
func (cs caseList) find(channel interface{}) int {
	for i, cas := range cs {
		if cas.Chan.Interface() == channel {
			return i
		}
	}
	return -1
}

//删除从cs中删除给定案例。
func (cs caseList) delete(index int) caseList {
	return append(cs[:index], cs[index+1:]...)
}

//停用将索引处的案例移动到cs切片的不可访问部分。
func (cs caseList) deactivate(index int) caseList {
	last := len(cs) - 1
	cs[index], cs[last] = cs[last], cs[index]
	return cs[:last]
}

/*

 */
type feedSub struct {
	feed    *Feed
	channel reflect.Value
	errOnce sync.Once
	err     chan error
}

func (sub *feedSub) Unsubscribe() {
	sub.errOnce.Do(func() {
		sub.feed.remove(sub)
		close(sub.err)
	})
}
func (sub *feedSub) Err() <-chan error {
	return sub.err
}

/*

 */
type feedTypeError struct {
	got, want reflect.Type
	op        string
}

func (e feedTypeError) Error() string {
	return "event: wrong type in " + e.op + " got " + e.got.String() + ", want " + e.want.String()
}
