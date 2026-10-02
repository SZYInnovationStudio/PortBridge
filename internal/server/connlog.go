package server

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"gorm.io/gorm"

	"portbridge/internal/connlog"
	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
	"portbridge/internal/proxy"
	"portbridge/internal/store"
)

// connLogModeKey 连接记录模式的配置键（存于 Setting 表）
const connLogModeKey = "conn_log_mode"

// connLogExcludedKey 排除 IP 列表的配置键（存于 Setting 表，值为 JSON 数组字符串）
const connLogExcludedKey = "conn_log_excluded_ips"

// connLogMode 当前连接记录模式（中转端统一控制，默认 mirror）
func (s *Server) connLogMode() string {
	if store.GetSetting(s.db, connLogModeKey, connlog.ModeMirror) == connlog.ModeRemote {
		return connlog.ModeRemote
	}
	return connlog.ModeMirror
}

// connLogExcluder 排除 IP 列表的内存缓存：避免每建一条连接都查库。
// 权威列表存于 Setting 表，启动时加载，变更时由中转端统一刷新。
type connLogExcluder struct {
	db  *gorm.DB
	mu  sync.RWMutex
	ips []string
	set map[string]struct{}
}

func newConnLogExcluder(db *gorm.DB) *connLogExcluder {
	e := &connLogExcluder{db: db, set: map[string]struct{}{}}
	e.reload()
	return e
}

// reload 从 Setting 表重新加载
func (e *connLogExcluder) reload() {
	e.replace(connlog.ParseExcluded(store.GetSetting(e.db, connLogExcludedKey, "")))
}

// replace 以规范化并去重后的列表整体替换缓存
func (e *connLogExcluder) replace(ips []string) {
	ips = connlog.NormalizeList(ips)
	set := make(map[string]struct{}, len(ips))
	for _, ip := range ips {
		set[ip] = struct{}{}
	}
	e.mu.Lock()
	e.ips = ips
	e.set = set
	e.mu.Unlock()
}

// list 返回排除 IP 列表的快照
func (e *connLogExcluder) list() []string {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, len(e.ips))
	copy(out, e.ips)
	return out
}

// contains 判断来源 IP 是否被排除（空 IP 不排除）
func (e *connLogExcluder) contains(ip string) bool {
	if e == nil || ip == "" {
		return false
	}
	n := connlog.NormalizeIP(ip)
	e.mu.RLock()
	_, hit := e.set[n]
	e.mu.RUnlock()
	return hit
}

// connLogExcludedList 当前排除 IP 列表快照
func (s *Server) connLogExcludedList() []string { return s.clEx.list() }

// setConnLogExcluded 覆盖设置排除 IP 列表：持久化 + 刷新缓存 + 广播到所有在线节点
func (s *Server) setConnLogExcluded(ips []string) error {
	ips = connlog.NormalizeList(ips)
	if err := store.SetSetting(s.db, connLogExcludedKey, connlog.MarshalExcluded(ips)); err != nil {
		return err
	}
	s.clEx.replace(ips)
	s.broadcastConnLogExcluded()
	return nil
}

// pushConnLogExcluded 向指定节点下发当前排除 IP 列表（登录时调用）
func (s *Server) pushConnLogExcluded(a *Agent) {
	payload, err := json.Marshal(protocol.ConnLogExcludePayload{OK: true, IPs: s.clEx.list()})
	if err != nil {
		return
	}
	a.send(protocol.Message{Type: protocol.MsgConnLogExclude, Ts: time.Now().UnixMilli(), Data: payload})
}

// broadcastConnLogExcluded 向所有在线节点下发最新排除 IP 列表并通知 Web UI
func (s *Server) broadcastConnLogExcluded() {
	ips := s.clEx.list()
	payload, err := json.Marshal(protocol.ConnLogExcludePayload{OK: true, IPs: ips})
	if err != nil {
		return
	}
	msg := protocol.Message{Type: protocol.MsgConnLogExclude, Ts: time.Now().UnixMilli(), Data: payload}
	for name := range s.hub.OnlineNodes() {
		_ = s.hub.Send(name, msg)
	}
	s.events.Broadcast("conn_log_excluded", map[string]any{"ips": ips})
}

// handleConnLogExcludeSet 处理原站端提交的排除 IP 列表设置
func (s *Server) handleConnLogExcludeSet(a *Agent, msg protocol.Message) {
	p, err := protocol.Decode[protocol.ConnLogExcludePayload](msg)
	if err != nil {
		return
	}
	if err := s.setConnLogExcluded(p.IPs); err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("节点 %s 更新排除 IP 列表失败: %v", a.NodeName, err))
		ack, _ := protocol.NewMessage(protocol.MsgConnLogExclude, protocol.ConnLogExcludePayload{OK: false, Msg: err.Error(), IPs: s.clEx.list()})
		a.send(ack)
		return
	}
	loghub.Default.Publish("info", fmt.Sprintf("节点 %s 更新排除 IP 列表（共 %d 条）", a.NodeName, len(s.clEx.list())))
}

// connRecord 一条进行中的连接记录句柄；所有方法对 nil 安全
type connRecord struct {
	srv    *Server
	l      *model.ConnLog
	mirror *proxy.Counter
}

// counter 返回该连接的会话级计数器（与规则计数器并行累加）
func (r *connRecord) counter() *proxy.Counter {
	if r == nil {
		return nil
	}
	return r.mirror
}

// connLogBegin 创建一条连接记录（status=active），返回句柄。失败时返回 nil（不影响转发）。
func (s *Server) connLogBegin(meta *proxyMeta, sessionID string, addr net.Addr) *connRecord {
	if meta == nil {
		return nil
	}
	srcIP, srcPort := "", 0
	if addr != nil {
		srcIP, srcPort = connlog.SplitHostPort(addr.String())
	}
	// 被排除的来源 IP 不记录（返回 nil 句柄，不影响转发；nil 句柄方法均安全）
	if s.clEx.contains(srcIP) {
		return nil
	}
	l := &model.ConnLog{
		SessionID:  sessionID,
		ProxyName:  meta.Name,
		NodeName:   meta.NodeName,
		Type:       meta.Type,
		Direction:  meta.Direction,
		SourceIP:   srcIP,
		SourcePort: srcPort,
		Target:     meta.Target,
		StartedAt:  time.Now(),
		Status:     connlog.StatusActive,
	}
	if err := connlog.Save(s.db, l); err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("写入连接记录失败: %v", err))
		return nil
	}
	s.mirrorConnLog(connlog.ToItem(l))
	s.events.Broadcast("conn_log_change", map[string]any{"item": connlog.ToItem(l)})
	return &connRecord{srv: s, l: l, mirror: &proxy.Counter{}}
}

// finish 结束记录：写入会话字节数与时长并落库，随后镜像下发
func (r *connRecord) finish() {
	if r == nil {
		return
	}
	in, out, _, _ := r.mirror.Snapshot()
	now := time.Now()
	r.l.BytesIn = in
	r.l.BytesOut = out
	r.l.EndedAt = now
	r.l.DurationMs = now.Sub(r.l.StartedAt).Milliseconds()
	r.l.Status = connlog.StatusClosed
	if err := connlog.Save(r.srv.db, r.l); err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("更新连接记录失败: %v", err))
		return
	}
	r.srv.mirrorConnLog(connlog.ToItem(r.l))
	r.srv.events.Broadcast("conn_log_change", map[string]any{"item": connlog.ToItem(r.l)})
}

// mirrorConnLog mirror 模式下把记录镜像下发到对应节点；remote 模式不发送
func (s *Server) mirrorConnLog(item protocol.ConnLogItem) {
	if s.connLogMode() != connlog.ModeMirror {
		return
	}
	payload, err := json.Marshal(protocol.ConnLogEventPayload{Item: item})
	if err != nil {
		return
	}
	s.hub.SendLog(item.NodeName, protocol.Message{
		Type: protocol.MsgConnLogEvent,
		Ts:   time.Now().UnixMilli(),
		Data: payload,
	})
}

// handleConnLogReport 处理原站端上报的 forward 方向连接记录
func (s *Server) handleConnLogReport(a *Agent, msg protocol.Message) {
	report, err := protocol.Decode[protocol.ConnLogReportPayload](msg)
	if err != nil {
		return
	}
	for _, it := range report.Items {
		if it.SessionID == "" {
			continue
		}
		// 被排除的来源 IP 不记录
		if s.clEx.contains(it.SourceIP) {
			continue
		}
		if it.NodeName == "" {
			it.NodeName = a.NodeName
		}
		it.ID = 0 // 原站端自增 ID 无意义，权威 ID 由中转端分配
		if err := connlog.Upsert(s.db, it); err != nil {
			continue
		}
		// 回读权威 ID 后镜像回原站端，保证两端 session_id 对应同一权威记录
		var l model.ConnLog
		if err := s.db.First(&l, "session_id = ?", it.SessionID).Error; err == nil {
			s.mirrorConnLog(connlog.ToItem(&l))
			s.events.Broadcast("conn_log_change", map[string]any{"item": connlog.ToItem(&l)})
		}
	}
}

// handleConnLogPull 处理原站端镜像对账请求：返回 since_id 之后的记录
func (s *Server) handleConnLogPull(a *Agent, msg protocol.Message) {
	req, err := protocol.Decode[protocol.ConnLogPullReq](msg)
	if err != nil {
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	var items []protocol.ConnLogItem
	var rows []model.ConnLog
	if err := s.db.Where("node_name = ? AND id > ?", a.NodeName, req.SinceID).
		Order("id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return
	}
	lastID := req.SinceID
	for i := range rows {
		items = append(items, connlog.ToItem(&rows[i]))
		lastID = rows[i].ID
	}

	var total int64
	s.db.Model(&model.ConnLog{}).Where("node_name = ?", a.NodeName).Count(&total)
	var minID uint
	_ = s.db.Model(&model.ConnLog{}).Where("node_name = ?", a.NodeName).
		Select("COALESCE(MIN(id),0)").Scan(&minID).Error

	payload, err := json.Marshal(protocol.ConnLogSyncPayload{
		Items:   items,
		LastID:  lastID,
		HasMore: len(items) == limit,
		MinID:   minID,
		Total:   total,
		Mode:    s.connLogMode(),
	})
	if err != nil {
		return
	}
	resp := protocol.Message{Type: protocol.MsgConnLogSync, Seq: msg.Seq, Ts: time.Now().UnixMilli(), Data: payload}
	a.send(resp)
}

// handleConnLogQuery 处理原站端的远程查询请求
func (s *Server) handleConnLogQuery(a *Agent, msg protocol.Message) {
	req, err := protocol.Decode[protocol.ConnLogQueryReq](msg)
	if err != nil {
		return
	}
	rows, total, err := connlog.Query(s.db, connlog.Filter{
		Keyword:   req.Keyword,
		ProxyName: req.ProxyName,
		NodeName:  a.NodeName,
		Status:    req.Status,
		Page:      req.Page,
		PageSize:  req.PageSize,
	})
	payload := protocol.ConnLogRespPayload{OK: true, Total: total, Mode: s.connLogMode()}
	if err != nil {
		payload.OK = false
		payload.Msg = err.Error()
	} else {
		for i := range rows {
			payload.Items = append(payload.Items, connlog.ToItem(&rows[i]))
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	resp := protocol.Message{Type: protocol.MsgConnLogResp, Seq: msg.Seq, Ts: time.Now().UnixMilli(), Data: data}
	a.send(resp)
}

// handleConnLogClear 清空记录：清空权威库并广播给所有在线节点
func (s *Server) handleConnLogClear(a *Agent, msg protocol.Message) {
	if err := connlog.Clear(s.db); err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("清空连接记录失败: %v", err))
		return
	}
	loghub.Default.Publish("info", "连接记录已清空")
	s.broadcastConnLogClear()
}

// broadcastConnLogClear 通知所有在线节点清空本地镜像
func (s *Server) broadcastConnLogClear() {
	data, _ := json.Marshal(protocol.ConnLogClearPayload{From: "server"})
	msg := protocol.Message{Type: protocol.MsgConnLogClear, Ts: time.Now().UnixMilli(), Data: data}
	for name := range s.hub.OnlineNodes() {
		_ = s.hub.Send(name, msg)
	}
	s.events.Broadcast("conn_log_change", map[string]any{"reset": true})
}

// connLogTrimLoop 周期性裁剪连接记录，防止无限增长撑爆磁盘
func (s *Server) connLogTrimLoop(ctxDone <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctxDone:
			return
		case <-ticker.C:
			if err := connlog.Trim(s.db, connlog.MaxRecords); err != nil {
				loghub.Default.Publish("warn", fmt.Sprintf("裁剪连接记录失败: %v", err))
			}
		}
	}
}
