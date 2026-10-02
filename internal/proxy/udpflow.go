package proxy

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// UDPFlow 表示一条 UDP 会话：本地 UDP 监听套接字与某个对端地址之间的数据通道。
// 它实现了 net.Conn 接口，使收发两侧都可以直接复用 RelayUDP / Bridge 等桥接逻辑。
//
// 读取侧来自 UDP 监听协程通过 Push 投递数据报，写入侧直接写回对端地址。
type UDPFlow struct {
	conn   *net.UDPConn
	addr   *net.UDPAddr
	in     chan []byte
	once   sync.Once
	mu     sync.RWMutex // 保护 closed 与 in 的关闭/发送，避免 send on closed channel
	closed bool
	last   atomic.Int64 // 最后活跃时间（UnixNano）
}

// NewUDPFlow 创建一个 UDP 会话
func NewUDPFlow(conn *net.UDPConn, addr *net.UDPAddr) *UDPFlow {
	f := &UDPFlow{conn: conn, addr: addr, in: make(chan []byte, 256)}
	f.last.Store(time.Now().UnixNano())
	return f
}

// Touch 刷新活跃时间
func (f *UDPFlow) Touch() { f.last.Store(time.Now().UnixNano()) }

// IdleFor 返回距最后活跃的时长
func (f *UDPFlow) IdleFor() time.Duration { return time.Since(time.Unix(0, f.last.Load())) }

// Peer 返回对端地址
func (f *UDPFlow) Peer() *net.UDPAddr { return f.addr }

func (f *UDPFlow) LocalAddr() net.Addr              { return f.conn.LocalAddr() }
func (f *UDPFlow) RemoteAddr() net.Addr             { return f.addr }
func (f *UDPFlow) SetDeadline(time.Time) error      { return nil }
func (f *UDPFlow) SetReadDeadline(time.Time) error  { return nil }
func (f *UDPFlow) SetWriteDeadline(time.Time) error { return nil }

// Push 将收到的数据报投递到读取侧，过载时丢弃，避免阻塞监听循环。
// 持读锁检查并发送，确保不会与 Close 的 close(in) 竞争。
func (f *UDPFlow) Push(b []byte) {
	cp := make([]byte, len(b))
	copy(cp, b)
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.closed {
		return
	}
	select {
	case f.in <- cp:
	default:
	}
}

func (f *UDPFlow) Read(p []byte) (int, error) {
	b, ok := <-f.in
	if !ok {
		return 0, io.EOF
	}
	return copy(p, b), nil
}

func (f *UDPFlow) Write(p []byte) (int, error) {
	f.mu.RLock()
	closed := f.closed
	f.mu.RUnlock()
	if closed {
		return 0, net.ErrClosed
	}
	return f.conn.WriteToUDP(p, f.addr)
}

// Close 关闭会话（幂等）
func (f *UDPFlow) Close() error {
	f.once.Do(func() {
		f.mu.Lock()
		f.closed = true
		close(f.in)
		f.mu.Unlock()
	})
	return nil
}
