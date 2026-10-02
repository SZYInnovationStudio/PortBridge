package eventhub

import (
	"sync"
	"time"
)

// Event Web UI 实时事件
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
	Ts   int64  `json:"ts"`
}

// Hub 简易的发布订阅中心，用于 Web UI 的 WebSocket 实时推送
type Hub struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

// New 创建事件中心
func New() *Hub {
	return &Hub{subs: make(map[chan Event]struct{})}
}

// Broadcast 广播一个事件
func (h *Hub) Broadcast(typ string, data any) {
	e := Event{Type: typ, Data: data, Ts: time.Now().UnixMilli()}

	// 持锁期间完成发送，避免与 unsubscribe/close 竞态导致 "send on closed channel"
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default: // 订阅者消费慢则丢弃，避免阻塞
		}
	}
}

// Subscribe 订阅事件，返回接收通道与取消函数
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 128)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

// Subscribers 当前订阅者数量
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}
