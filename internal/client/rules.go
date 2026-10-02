package client

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"gorm.io/gorm"

	"portbridge/internal/apiutil"
	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
	"portbridge/internal/proxy"
)

// clientMeta 规则的本地运行态元信息
type clientMeta struct {
	Name        string
	Type        string
	Direction   string
	ListenAddr  string
	Target      string
	RateLimitKB int
	counter     *proxy.Counter
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

// ruleManager 管理原站端规则：
//   - reverse 方向：监听在中转端，本地仅保留元信息等待数据连接
//   - forward 方向：原站端本地监听，每条用户连接拨号一条工作连接到中转端
type ruleManager struct {
	cli *Client

	mu      sync.RWMutex
	metas   map[string]*clientMeta
	entries map[string]*proxy.LocalListener
	errs    map[string]string
}

func newRuleManager(c *Client) *ruleManager {
	return &ruleManager{
		cli:     c,
		metas:   make(map[string]*clientMeta),
		entries: make(map[string]*proxy.LocalListener),
		errs:    make(map[string]string),
	}
}

// meta 返回规则元信息副本
func (m *ruleManager) meta(name string) *clientMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mm, ok := m.metas[name]
	if !ok {
		return nil
	}
	cp := *mm
	return &cp
}

// stats 汇总当前连接数与累计流量
func (m *ruleManager) stats() (conns, in, out int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mm := range m.metas {
		if mm.counter == nil {
			continue
		}
		i, o, c, _ := mm.counter.Snapshot()
		in += i
		out += o
		conns += c
	}
	return
}

// SetError 记录规则最近一次错误
func (m *ruleManager) SetError(name, msg string) {
	m.mu.Lock()
	if msg == "" {
		delete(m.errs, name)
	} else {
		m.errs[name] = msg
	}
	m.mu.Unlock()
}

// Apply 应用单条规则（必要时启动本地监听）
func (m *ruleManager) Apply(p *model.Proxy) error {
	m.Stop(p.Name)

	m.mu.Lock()
	ctr := (*proxy.Counter)(nil)
	if old, ok := m.metas[p.Name]; ok {
		ctr = old.counter
	}
	if ctr == nil {
		ctr = &proxy.Counter{}
	}
	meta := &clientMeta{
		Name: p.Name, Type: p.Type, Direction: p.Direction,
		ListenAddr: p.ListenAddr(), Target: p.TargetAddr(),
		RateLimitKB: p.RateLimitKB, counter: ctr,
	}
	m.metas[p.Name] = meta
	m.mu.Unlock()

	// reverse 方向由中转端监听；停用规则无需本地监听
	if !p.Enabled || p.Direction != model.DirectionForward {
		m.SetError(p.Name, "")
		return nil
	}

	dial := func(name, sessionID, typ string) (net.Conn, error) {
		return m.cli.dialWorkConn(name, sessionID, typ, model.DirectionForward)
	}
	onErr := func(msg string) { m.SetError(p.Name, msg) }
	// forward 方向：真实访客地址只在本地监听侧可见，登记会话记录回调并上报中转端
	hook := func(sessionID string, peer net.Addr) func(int64, int64) {
		return m.cli.forwardConnLog(meta, sessionID, peer)
	}

	ll, err := proxy.NewLocalListener(p.Name, p.Type, p.ListenAddr(), dial, ctr, p.RateLimitKB, onErr, hook)
	if err != nil {
		m.SetError(p.Name, err.Error())
		return err
	}
	m.mu.Lock()
	m.entries[p.Name] = ll
	m.mu.Unlock()
	m.SetError(p.Name, "")
	return nil
}

// ensure 仅在规则配置发生变化或监听状态不一致时重新应用，避免周期性同步导致监听抖动
func (m *ruleManager) ensure(p *model.Proxy) {
	m.mu.RLock()
	old := m.metas[p.Name]
	_, listening := m.entries[p.Name]
	m.mu.RUnlock()

	wantListen := p.Enabled && p.Direction == model.DirectionForward
	if old != nil && old.Type == p.Type && old.Direction == p.Direction &&
		old.ListenAddr == p.ListenAddr() && old.Target == p.TargetAddr() &&
		old.RateLimitKB == p.RateLimitKB && listening == wantListen {
		return
	}
	if err := m.Apply(p); err != nil {
		loghub.Default.Publish("error", fmt.Sprintf("应用规则 %s 失败: %v", p.Name, err))
	}
}

// Stop 停止某规则的本地监听
func (m *ruleManager) Stop(name string) {
	m.mu.Lock()
	ll, ok := m.entries[name]
	if ok {
		delete(m.entries, name)
	}
	m.mu.Unlock()
	if ok && ll != nil {
		ll.Close()
	}
}

// Remove 移除规则（含元信息）
func (m *ruleManager) Remove(name string) {
	m.Stop(name)
	m.mu.Lock()
	delete(m.metas, name)
	delete(m.errs, name)
	m.mu.Unlock()
}

// CloseAll 关闭全部本地监听
func (m *ruleManager) CloseAll() error {
	m.mu.Lock()
	entries := m.entries
	m.entries = make(map[string]*proxy.LocalListener)
	m.mu.Unlock()
	for _, ll := range entries {
		ll.Close()
	}
	return nil
}

// Snapshot 返回全部规则运行态
func (m *ruleManager) Snapshot() map[string]RuntimeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]RuntimeInfo, len(m.metas))
	for name, mm := range m.metas {
		info := RuntimeInfo{Name: name, ListenAddr: mm.ListenAddr, LastError: m.errs[name]}
		_, listening := m.entries[name]
		// reverse 方向监听在中转端，本地视为可服务
		info.Running = listening || mm.Direction == model.DirectionReverse
		if mm.counter != nil {
			_, _, c, total := mm.counter.Snapshot()
			info.ConnCount = c
			info.TotalConns = total
		}
		out[name] = info
	}
	return out
}

// applySync 用中转端下发的全量规则收敛本地状态（LWW）
func (c *Client) applySync(report protocol.ProxyReport) {
	seen := make(map[string]bool, len(report.Proxies))
	for i := range report.Proxies {
		spec := report.Proxies[i]
		if spec.Name == "" {
			continue
		}
		seen[spec.Name] = true
		c.applyProxySpec(spec)
	}

	// 中转端下发的规则若已不在同步列表中，删除本地副本
	var locals []model.Proxy
	if err := c.db.Where("origin = ?", model.OriginServer).Find(&locals).Error; err == nil {
		for i := range locals {
			if !seen[locals[i].Name] {
				c.rules.Remove(locals[i].Name)
				c.db.Delete(&locals[i])
				c.events.Broadcast("proxy_change", map[string]any{"name": locals[i].Name, "deleted": true})
			}
		}
	}
}

// applyProxySpec 应用中转端下发的一条规则到本地（LWW），不影响其它规则
func (c *Client) applyProxySpec(spec protocol.ProxySpec) {
	if spec.Name == "" {
		return
	}
	var p model.Proxy
	err := c.db.Where("name = ?", spec.Name).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		np := apiutil.SpecToProxy(spec, c.cfg.NodeName)
		if np.Origin == "" {
			np.Origin = model.OriginServer
		}
		if spec.UpdatedAt > 0 {
			np.UpdatedAt = time.UnixMilli(spec.UpdatedAt)
			np.CreatedAt = np.UpdatedAt
		}
		if err := c.db.Create(np).Error; err != nil {
			loghub.Default.Publish("warn", fmt.Sprintf("同步规则 %s 落库失败: %v", spec.Name, err))
			return
		}
		c.rules.ensure(np)
		return
	}
	if err != nil {
		return
	}
	// LWW：以配置版本号为准（UpdatedAt 会被 GORM 在每次保存时刷新，不能用于比较）
	if spec.Version > p.Version {
		apiutil.ApplySpecToProxy(spec, &p)
		if err := c.db.Save(&p).Error; err != nil {
			return
		}
	}
	c.rules.ensure(&p)
}

// removeLocalProxy 删除一条中转端下发的本地规则副本
func (c *Client) removeLocalProxy(name string) {
	if name == "" {
		return
	}
	res := c.db.Where("name = ? AND origin = ?", name, model.OriginServer).Delete(&model.Proxy{})
	if res.Error != nil || res.RowsAffected == 0 {
		return
	}
	c.rules.Remove(name)
	c.events.Broadcast("proxy_change", map[string]any{"name": name, "deleted": true})
}
