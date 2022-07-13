package p2p

import (
	"container/heap"
	"github.com/radiation-octopus/octopus-blockchain/entity/mclock"
)

// expHeap跟踪字符串及其到期时间。
type expHeap []expItem

//expItem是addrHistory中的一个条目。
type expItem struct {
	item string
	exp  mclock.AbsTime
}

// nextExpiry返回下一个到期时间。
func (h *expHeap) nextExpiry() mclock.AbsTime {
	return (*h)[0].exp
}

//添加添加项目并设置其到期时间。
func (h *expHeap) add(item string, exp mclock.AbsTime) {
	heap.Push(h, expItem{item, exp})
}

// 包含检查项目是否存在。
func (h expHeap) contains(item string) bool {
	for _, v := range h {
		if v.item == item {
			return true
		}
	}
	return false
}

// expire删除过期时间在“now”之前的项目。
func (h *expHeap) expire(now mclock.AbsTime, onExp func(string)) {
	for h.Len() > 0 && h.nextExpiry() < now {
		item := heap.Pop(h)
		if onExp != nil {
			onExp(item.(expItem).item)
		}
	}
}

// 堆接口样板
func (h expHeap) Len() int            { return len(h) }
func (h expHeap) Less(i, j int) bool  { return h[i].exp < h[j].exp }
func (h expHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *expHeap) Push(x interface{}) { *h = append(*h, x.(expItem)) }
func (h *expHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}
