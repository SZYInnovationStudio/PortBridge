package server

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"portbridge/internal/connlog"
	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
	"portbridge/internal/proxy"
	"portbridge/internal/store"
)

// connLogModeKey 连接记录模式的配置键（存于 Setting 表）
const connLogModeKey = "conn_log_mode"

// connLogMode 当前连接记录模式（中转端统一控制，默认 mirror）
func (s *Server) connLogMode() string {
	if store.GetSetting(s.db, connLogModeKey, connlog.ModeMirror) == connlog.ModeRemote {
		return connlog.ModeRemote
	}
	return connlog.ModeMirror
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
