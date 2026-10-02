package loghub

import (
	"sync"
	"time"
)

// Entry 一条运行日志
type Entry struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`
}

// Hub 内存环形日志缓冲 + 订阅广播，用于 Web UI 实时日志
type Hub struct {
	mu   sync.RWMutex
	subs map[chan Entry]struct{}
	buf  []Entry
	size int
}

// New 创建日志中心
func New(size int) *Hub {
	if size <= 0 {
		size = 500
	}
	return &Hub{subs: make(map[chan Entry]struct{}), size: size}
}

// Default 全局默认日志中心
var Default = New(500)

// Publish 写入一条日志并广播
func (h *Hub) Publish(level, msg string) {
	e := Entry{Time: time.Now(), Level: level, Msg: msg}

	// 持锁期间完成写入与发送，避免与 unsubscribe/close 竞态导致 "send on closed channel"
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf = append(h.buf, e)
	if len(h.buf) > h.size {
		h.buf = h.buf[len(h.buf)-h.size:]
	}
	for ch := range h.subs {
		select {
		case ch <- e:
		default: // 订阅者消费慢则丢弃，避免阻塞
		}
	}
}

// Reset 清空内存中的日志缓冲（供每日清理调用）
func (h *Hub) Reset() {
	h.mu.Lock()
	h.buf = nil
	h.mu.Unlock()
}

// Subscribe 订阅日志，返回接收通道与取消函数
func (h *Hub) Subscribe() (<-chan Entry, func()) {
	ch := make(chan Entry, 128)
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

// Recent 返回最近的日志快照
func (h *Hub) Recent() []Entry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Entry, len(h.buf))
	copy(out, h.buf)
	return out
}
