package proxy

import (
	"io"
	"net"
)

// BufferedConn 在已读取过握手帧后继续使用底层 bufio.Reader，避免丢失缓冲数据
type BufferedConn struct {
	net.Conn
	r io.Reader
}

// NewBufferedConn 包装连接
func NewBufferedConn(c net.Conn, r io.Reader) *BufferedConn {
	return &BufferedConn{Conn: c, r: r}
}

func (b *BufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

// CloseWrite 透传半关闭
func (b *BufferedConn) CloseWrite() error {
	if cw, ok := b.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
