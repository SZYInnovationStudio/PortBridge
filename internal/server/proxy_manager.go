package server

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
	"portbridge/internal/util"

	"portbridge/internal/proxy"
)

// proxyMeta 规则的运行态元信息
type proxyMeta struct {
	Name         string
	NodeName     string
	Type         string
	Direction    string
	ListenAddr   string
	Target       string
	RateLimitKB  int
	TrafficLimit int64
	counter      *proxy.Counter
}

// proxyEntry 一条正在监听的规则
type proxyEntry struct {
	meta      proxyMeta
	stopCh    chan struct{}
	closeOnce sync.Once

	tcpLn   net.Listener
	udpConn *net.UDPConn

	mu       sync.Mutex
	udpFlows map[string]*proxy.UDPFlow
}

func (e *proxyEntry) close() {
	e.closeOnce.Do(func() {
		close(e.stopCh)
		if e.tcpLn != nil {
			_ = e.tcpLn.Close()
		}
		if e.udpConn != nil {
			_ = e.udpConn.Close()
		}
		e.mu.Lock()
		for k, f := range e.udpFlows {
			_ = f.Close()
			delete(e.udpFlows, k)
		}
		e.mu.Unlock()
	})
}

// serveTCP 接受用户连接并配对数据连接
func (e *proxyEntry) serveTCP(pm *ProxyManager) {
	for {
		c, err := e.tcpLn.Accept()
		if err != nil {
			select {
			case <-e.stopCh:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return
		}
		go e.handleTCPConn(pm, c)
	}
}

func (e *proxyEntry) handleTCPConn(pm *ProxyManager, c net.Conn) {
	sessionID := util.NewID("s")
	ch := pm.PrepareSession(sessionID)
	defer pm.dropSession(sessionID)

	if err := pm.srv.hub.SendWorkConn(e.meta.Name, sessionID, model.ProxyTypeTCP, model.DirectionReverse); err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("规则 %s 请求数据连接失败: %v", e.meta.Name, err))
		_ = c.Close()
		return
	}

	select {
	case wc := <-ch:
		if wc == nil {
			_ = c.Close()
			return
		}
		e.meta.counter.IncConn()
		loghub.Default.Publish("debug", fmt.Sprintf("规则 %s 建立连接 <- %s", e.meta.Name, c.RemoteAddr()))
		proxy.Bridge(proxy.WrapConn(c, e.meta.counter, e.meta.RateLimitKB), wc)
		e.meta.counter.DecConn()
	case <-time.After(15 * time.Second):
		loghub.Default.Publish("warn", fmt.Sprintf("规则 %s 等待数据连接超时", e.meta.Name))
		_ = c.Close()
	case <-e.stopCh:
		_ = c.Close()
	}
}

// serveUDP 接受用户数据报并按源地址维护会话
func (e *proxyEntry) serveUDP(pm *ProxyManager) {
	buf := make([]byte, proxy.MaxPacket)
	for {
		_ = e.udpConn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, addr, err := e.udpConn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				e.reapFlows(60 * time.Second)
				continue
			}
			select {
			case <-e.stopCh:
				return
			default:
			}
			return
		}
		f := e.getOrCreateFlow(pm, addr.String(), addr)
		if f == nil {
			continue
		}
		f.Push(buf[:n])
	}
}

func (e *proxyEntry) getOrCreateFlow(pm *ProxyManager, key string, addr *net.UDPAddr) *proxy.UDPFlow {
	e.mu.Lock()
	if f, ok := e.udpFlows[key]; ok {
		f.Touch()
		e.mu.Unlock()
		return f
	}
	f := proxy.NewUDPFlow(e.udpConn, addr)
	e.udpFlows[key] = f
	e.mu.Unlock()

	go e.setupFlow(pm, key, f)
	return f
}

func (e *proxyEntry) setupFlow(pm *ProxyManager, key string, f *proxy.UDPFlow) {
	sessionID := util.NewID("s")
	ch := pm.PrepareSession(sessionID)
	defer pm.dropSession(sessionID)

	if err := pm.srv.hub.SendWorkConn(e.meta.Name, sessionID, model.ProxyTypeUDP, model.DirectionReverse); err != nil {
		e.removeFlow(key, f)
		return
	}
	select {
	case wc := <-ch:
		if wc == nil {
			e.removeFlow(key, f)
			return
		}
		e.meta.counter.IncConn()
		proxy.RelayUDP(wc, f, e.meta.counter)
		e.meta.counter.DecConn()
	case <-time.After(15 * time.Second):
	case <-e.stopCh:
	}
	e.removeFlow(key, f)
}

func (e *proxyEntry) removeFlow(key string, f *proxy.UDPFlow) {
	e.mu.Lock()
	if cur, ok := e.udpFlows[key]; ok && cur == f {
		delete(e.udpFlows, key)
	}
	e.mu.Unlock()
	_ = f.Close()
}

func (e *proxyEntry) reapFlows(idle time.Duration) {
	e.mu.Lock()
	var dead []*proxy.UDPFlow
	for k, f := range e.udpFlows {
		if f.IdleFor() > idle {
			delete(e.udpFlows, k)
			dead = append(dead, f)
		}
	}
	e.mu.Unlock()
	for _, f := range dead {
		_ = f.Close()
	}
}

// RuntimeInfo 规则运行态快照
type RuntimeInfo struct {
	Name       string `json:"name"`
	Running    bool   `json:"running"`
	ConnCount  int64  `json:"conn_count"`
	TotalConns int64  `json:"total_conns"`
	ListenAddr string `json:"listen_addr"`
	LastError  string `json:"last_error"`
}

// ProxyManager 管理中转端的所有监听与数据连接配对
type ProxyManager struct {
	srv *Server

	mu      sync.RWMutex
	metas   map[string]proxyMeta
	entries map[string]*proxyEntry
	pending map[string]chan net.Conn
	errs    map[string]string
}

func newProxyManager(s *Server) *ProxyManager {
	return &ProxyManager{
		srv:     s,
		metas:   make(map[string]proxyMeta),
		entries: make(map[string]*proxyEntry),
		pending: make(map[string]chan net.Conn),
		errs:    make(map[string]string),
	}
}

// Meta 返回规则元信息
func (pm *ProxyManager) Meta(name string) *proxyMeta {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	m, ok := pm.metas[name]
	if !ok {
		return nil
	}
	return &m
}

// Counter 返回规则计数器
func (pm *ProxyManager) Counter(name string) *proxy.Counter {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if m, ok := pm.metas[name]; ok {
		return m.counter
	}
	return nil
}

// SetLastError 记录规则最近一次错误
func (pm *ProxyManager) SetLastError(name, msg string) {
	pm.mu.Lock()
	if msg == "" {
		delete(pm.errs, name)
	} else {
		pm.errs[name] = msg
	}
	pm.mu.Unlock()
}

// RunningNames 返回所有已注册规则名
func (pm *ProxyManager) RunningNames() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	out := make([]string, 0, len(pm.metas))
	for name := range pm.metas {
		out = append(out, name)
	}
	return out
}

// Snapshot 返回全部规则运行态
func (pm *ProxyManager) Snapshot() map[string]RuntimeInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	out := make(map[string]RuntimeInfo, len(pm.metas))
	for name, m := range pm.metas {
		info := RuntimeInfo{Name: name, ListenAddr: m.ListenAddr, LastError: pm.errs[name]}
		if _, ok := pm.entries[name]; ok {
			info.Running = true
		}
		if m.counter != nil {
			_, _, conns, total := m.counter.Snapshot()
			info.ConnCount = conns
			info.TotalConns = total
		}
		out[name] = info
	}
	return out
}

// Apply 应用规则：启动/重启本地监听（reverse 方向）
func (pm *ProxyManager) Apply(p *model.Proxy) error {
	pm.stop(p.Name)

	pm.mu.Lock()
	old := pm.metas[p.Name]
	ctr := old.counter
	if ctr == nil {
		ctr = &proxy.Counter{}
	}
	meta := proxyMeta{
		Name: p.Name, NodeName: p.NodeName, Type: p.Type, Direction: p.Direction,
		ListenAddr: p.ListenAddr(), Target: p.TargetAddr(),
		RateLimitKB: p.RateLimitKB, TrafficLimit: p.TrafficLimit, counter: ctr,
	}
	pm.metas[p.Name] = meta
	pm.mu.Unlock()

	if !p.Enabled {
		return nil
	}
	// forward 方向由原站端监听，中转端仅保留元信息用于目标拨号
	if p.Direction == model.DirectionForward {
		return nil
	}

	e := &proxyEntry{meta: meta, stopCh: make(chan struct{}), udpFlows: make(map[string]*proxy.UDPFlow)}

	switch p.Type {
	case model.ProxyTypeUDP:
		addr, err := net.ResolveUDPAddr("udp", p.ListenAddr())
		if err != nil {
			return err
		}
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			return fmt.Errorf("%w: %v", errPortInUse, err)
		}
		e.udpConn = conn
		pm.mu.Lock()
		pm.entries[p.Name] = e
		pm.mu.Unlock()
		go e.serveUDP(pm)
	default:
		ln, err := net.Listen("tcp", p.ListenAddr())
		if err != nil {
			return fmt.Errorf("%w: %v", errPortInUse, err)
		}
		e.tcpLn = ln
		pm.mu.Lock()
		pm.entries[p.Name] = e
		pm.mu.Unlock()
		go e.serveTCP(pm)
	}

	msg := fmt.Sprintf("规则 %s 已监听 %s (%s/%s)", p.Name, p.ListenAddr(), p.Type, p.Direction)
	log.Println("[server]", msg)
	loghub.Default.Publish("info", msg)
	return nil
}

// stop 停止某规则的监听
func (pm *ProxyManager) stop(name string) {
	pm.mu.Lock()
	e, ok := pm.entries[name]
	if ok {
		delete(pm.entries, name)
	}
	pm.mu.Unlock()
	if ok && e != nil {
		e.close()
	}
}

// Remove 移除规则（含元信息）
func (pm *ProxyManager) Remove(name string) {
	pm.stop(name)
	pm.mu.Lock()
	delete(pm.metas, name)
	delete(pm.errs, name)
	pm.mu.Unlock()
}

// PrepareSession 注册一个待配对的数据会话
func (pm *ProxyManager) PrepareSession(sessionID string) chan net.Conn {
	ch := make(chan net.Conn, 1)
	pm.mu.Lock()
	pm.pending[sessionID] = ch
	pm.mu.Unlock()
	return ch
}

func (pm *ProxyManager) dropSession(sessionID string) {
	pm.mu.Lock()
	delete(pm.pending, sessionID)
	pm.mu.Unlock()
}

// DeliverWorkConn 将 reverse 方向的数据连接交付给等待方
func (pm *ProxyManager) DeliverWorkConn(conn net.Conn, hs protocol.WorkHandshake) error {
	pm.mu.Lock()
	ch := pm.pending[hs.SessionID]
	pm.mu.Unlock()
	if ch == nil {
		_ = conn.Close()
		return errNoSession
	}
	select {
	case ch <- conn:
		return nil
	default:
		_ = conn.Close()
		return errNoSession
	}
}

// ServeForwardConn 处理 forward 方向的数据连接：中转端主动拨号到本侧目标
func (pm *ProxyManager) ServeForwardConn(conn net.Conn, hs protocol.WorkHandshake) {
	meta := pm.Meta(hs.ProxyName)
	if meta == nil {
		_ = conn.Close()
		return
	}
	if hs.Type == model.ProxyTypeUDP {
		raddr, err := net.ResolveUDPAddr("udp", meta.Target)
		if err != nil {
			_ = conn.Close()
			return
		}
		tc, err := net.DialUDP("udp", nil, raddr)
		if err != nil {
			loghub.Default.Publish("warn", fmt.Sprintf("规则 %s 拨号目标失败: %v", meta.Name, err))
			_ = conn.Close()
			return
		}
		meta.counter.IncConn()
		proxy.RelayUDP(conn, tc, meta.counter)
		meta.counter.DecConn()
		return
	}

	tc, err := net.DialTimeout("tcp", meta.Target, 10*time.Second)
	if err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("规则 %s 拨号目标 %s 失败: %v", meta.Name, meta.Target, err))
		_ = conn.Close()
		return
	}
	meta.counter.IncConn()
	proxy.Bridge(proxy.WrapConn(tc, meta.counter, meta.RateLimitKB), conn)
	meta.counter.DecConn()
}

// CloseAll 关闭全部监听
func (pm *ProxyManager) CloseAll() error {
	pm.mu.Lock()
	entries := pm.entries
	pm.entries = make(map[string]*proxyEntry)
	pm.mu.Unlock()
	for _, e := range entries {
		e.close()
	}
	return nil
}
