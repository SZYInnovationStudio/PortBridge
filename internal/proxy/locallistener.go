package proxy

import (
	"fmt"
	"net"
	"sync"
	"time"

	"portbridge/internal/loghub"
	"portbridge/internal/util"
)

// ConnDialer 建立一条用于承载用户流量的工作连接（已完成后端配对）
type ConnDialer func(proxyName, sessionID, typ string) (net.Conn, error)

// LocalListener 本地监听端口，并将每条到达的用户连接/会话桥接到一条新建的工作连接。
// 适用于 forward 方向（原站端本地监听 -> 中转端侧目标）以及 UDP 会话场景。
type LocalListener struct {
	name       string
	typ        string
	listenAddr string
	dial       ConnDialer
	counter    *Counter
	rateKB     int
	onError    func(string)

	stopCh chan struct{}
	once   sync.Once

	tcpLn   net.Listener
	udpConn *net.UDPConn

	mu    sync.Mutex
	flows map[string]*UDPFlow
}

// NewLocalListener 创建并启动本地监听；监听失败时返回错误
func NewLocalListener(name, typ, listenAddr string, dial ConnDialer, counter *Counter, rateKB int, onError func(string)) (*LocalListener, error) {
	if counter == nil {
		counter = &Counter{}
	}
	l := &LocalListener{
		name: name, typ: typ, listenAddr: listenAddr, dial: dial,
		counter: counter, rateKB: rateKB, onError: onError,
		stopCh: make(chan struct{}),
		flows:  make(map[string]*UDPFlow),
	}

	if typ == "udp" {
		addr, err := net.ResolveUDPAddr("udp", listenAddr)
		if err != nil {
			return nil, err
		}
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			return nil, err
		}
		l.udpConn = conn
		go l.serveUDP()
	} else {
		ln, err := net.Listen("tcp", listenAddr)
		if err != nil {
			return nil, err
		}
		l.tcpLn = ln
		go l.serveTCP()
	}

	loghub.Default.Publish("info", fmt.Sprintf("规则 %s 已监听 %s (%s)", name, listenAddr, typ))
	return l, nil
}

// Name 返回规则名
func (l *LocalListener) Name() string { return l.name }

// Running 是否处于监听状态
func (l *LocalListener) Running() bool {
	select {
	case <-l.stopCh:
		return false
	default:
		return true
	}
}

// Close 关闭监听与所有会话
func (l *LocalListener) Close() {
	l.once.Do(func() {
		close(l.stopCh)
		if l.tcpLn != nil {
			_ = l.tcpLn.Close()
		}
		if l.udpConn != nil {
			_ = l.udpConn.Close()
		}
		l.mu.Lock()
		for k, f := range l.flows {
			_ = f.Close()
			delete(l.flows, k)
		}
		l.mu.Unlock()
	})
}

func (l *LocalListener) report(msg string) {
	if l.onError != nil {
		l.onError(msg)
	}
	loghub.Default.Publish("warn", msg)
}

// serveTCP 接受用户 TCP 连接并桥接到工作连接
func (l *LocalListener) serveTCP() {
	for {
		c, err := l.tcpLn.Accept()
		if err != nil {
			select {
			case <-l.stopCh:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return
		}
		go l.handleTCPConn(c)
	}
}

func (l *LocalListener) handleTCPConn(c net.Conn) {
	wc, err := l.dial(l.name, util.NewID("s"), l.typ)
	if err != nil {
		l.report(fmt.Sprintf("规则 %s 建立工作连接失败: %v", l.name, err))
		_ = c.Close()
		return
	}
	l.counter.IncConn()
	Bridge(WrapConn(c, l.counter, l.rateKB), wc)
	l.counter.DecConn()
}

// serveUDP 接收用户数据报并按源地址维护会话
func (l *LocalListener) serveUDP() {
	buf := make([]byte, MaxPacket)
	for {
		_ = l.udpConn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, addr, err := l.udpConn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				l.reapFlows(60 * time.Second)
				continue
			}
			select {
			case <-l.stopCh:
				return
			default:
			}
			return
		}
		f := l.getOrCreateFlow(addr)
		f.Push(buf[:n])
	}
}

func (l *LocalListener) getOrCreateFlow(addr *net.UDPAddr) *UDPFlow {
	key := addr.String()
	l.mu.Lock()
	if f, ok := l.flows[key]; ok {
		f.Touch()
		l.mu.Unlock()
		return f
	}
	f := NewUDPFlow(l.udpConn, addr)
	l.flows[key] = f
	l.mu.Unlock()

	go l.setupFlow(key, f)
	return f
}

func (l *LocalListener) setupFlow(key string, f *UDPFlow) {
	wc, err := l.dial(l.name, util.NewID("s"), "udp")
	if err != nil {
		l.report(fmt.Sprintf("规则 %s 建立 UDP 会话失败: %v", l.name, err))
		l.removeFlow(key, f)
		return
	}
	l.counter.IncConn()
	RelayUDP(wc, f, l.counter)
	l.counter.DecConn()
	l.removeFlow(key, f)
}

func (l *LocalListener) removeFlow(key string, f *UDPFlow) {
	l.mu.Lock()
	if cur, ok := l.flows[key]; ok && cur == f {
		delete(l.flows, key)
	}
	l.mu.Unlock()
	_ = f.Close()
}

func (l *LocalListener) reapFlows(idle time.Duration) {
	l.mu.Lock()
	var dead []*UDPFlow
	for k, f := range l.flows {
		if f.IdleFor() > idle {
			delete(l.flows, k)
			dead = append(dead, f)
		}
	}
	l.mu.Unlock()
	for _, f := range dead {
		_ = f.Close()
	}
}
