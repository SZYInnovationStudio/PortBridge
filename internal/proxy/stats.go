package proxy

import "sync/atomic"

// Counter 规则/系统级运行态计数器（无锁原子）
type Counter struct {
	bytesIn  int64
	bytesOut int64
	conns    int64 // 当前连接数
	total    int64 // 累计连接数
}

// AddIn 累加入流量
func (c *Counter) AddIn(n int64) { atomic.AddInt64(&c.bytesIn, n) }

// AddOut 累加出流量
func (c *Counter) AddOut(n int64) { atomic.AddInt64(&c.bytesOut, n) }

// IncConn 当前连接数 +1，返回累计连接数
func (c *Counter) IncConn() int64 {
	atomic.AddInt64(&c.conns, 1)
	return atomic.AddInt64(&c.total, 1)
}

// DecConn 当前连接数 -1
func (c *Counter) DecConn() { atomic.AddInt64(&c.conns, -1) }

// Snapshot 读取统计快照：in, out, conns, total
func (c *Counter) Snapshot() (int64, int64, int64, int64) {
	return atomic.LoadInt64(&c.bytesIn), atomic.LoadInt64(&c.bytesOut),
		atomic.LoadInt64(&c.conns), atomic.LoadInt64(&c.total)
}
