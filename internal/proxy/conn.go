package proxy

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"

	"golang.org/x/time/rate"
)

// MaxPacket UDP 单包最大长度
const MaxPacket = 65535

// WriteFrame 写入一帧：2 字节大端长度 + 载荷（用于 UDP over TCP 分帧）
func WriteFrame(w io.Writer, b []byte) error {
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

// ReadFrame 读取一帧到 buf，返回载荷长度
func ReadFrame(r io.Reader, buf []byte) (int, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n > len(buf) {
		return 0, io.ErrShortBuffer
	}
	if _, err := io.ReadFull(r, buf[:n]); err != nil {
		return 0, err
	}
	return n, nil
}

// countingConn 包装 net.Conn，统计流量并可选限速。
// mirror 非空时，同一份字节数会同时计入 mirror（用于单条连接的连接记录统计）。
type countingConn struct {
	net.Conn
	counter *Counter
	mirror  *Counter
	limiter *rate.Limiter
	burst   int
}

// WrapConn 包装连接
func WrapConn(c net.Conn, counter *Counter, kbLimit int) net.Conn {
	return WrapConnDual(c, counter, nil, kbLimit)
}

// WrapConnDual 包装连接，同时把字节数计入 counter 与 mirror
func WrapConnDual(c net.Conn, counter, mirror *Counter, kbLimit int) net.Conn {
	limiter, burst := NewLimiterKB(kbLimit)
	if counter == nil {
		counter = &Counter{}
	}
	return &countingConn{Conn: c, counter: counter, mirror: mirror, limiter: limiter, burst: burst}
}

func (c *countingConn) addIn(n int64) {
	c.counter.AddIn(n)
	if c.mirror != nil {
		c.mirror.AddIn(n)
	}
}

func (c *countingConn) addOut(n int64) {
	c.counter.AddOut(n)
	if c.mirror != nil {
		c.mirror.AddOut(n)
	}
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.addIn(int64(n))
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	if c.limiter == nil {
		n, err := c.Conn.Write(p)
		if n > 0 {
			c.addOut(int64(n))
		}
		return n, err
	}
	total := 0
	for len(p) > 0 {
		chunk := len(p)
		if chunk > c.burst {
			chunk = c.burst
		}
		if err := c.limiter.WaitN(context.Background(), chunk); err != nil {
			return total, err
		}
		n, err := c.Conn.Write(p[:chunk])
		total += n
		c.addOut(int64(n))
		if err != nil {
			return total, err
		}
		p = p[chunk:]
	}
	return total, nil
}

// CloseWrite 尽可能半关闭写方向，保证 TCP FIN 语义
func (c *countingConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// Bridge 双向桥接两个连接，任一方结束后关闭双方
func Bridge(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	pipe := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}

	go pipe(a, b)
	go pipe(b, a)
	wg.Wait()

	_ = a.Close()
	_ = b.Close()
}
