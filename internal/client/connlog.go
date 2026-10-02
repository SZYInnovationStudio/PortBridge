package client

import (
	"fmt"
	"net"
	"sync"
	"time"

	"portbridge/internal/connlog"
	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
)

// connLogRPC 管理远程查询请求与响应的配对（原站端 -> 中转端）。
type connLogRPC struct {
	mu      sync.Mutex
	seq     int64
	waiters map[int64]chan protocol.ConnLogRespPayload
}

func newConnLogRPC() *connLogRPC {
	return &connLogRPC{waiters: make(map[int64]chan protocol.ConnLogRespPayload)}
}

// register 分配一个新的请求序号与等待通道
func (r *connLogRPC) register() (int64, chan protocol.ConnLogRespPayload) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	seq := r.seq
	ch := make(chan protocol.ConnLogRespPayload, 1)
	r.waiters[seq] = ch
	return seq, ch
}

// deliver 交付响应；无匹配等待者时丢弃
func (r *connLogRPC) deliver(seq int64, p protocol.ConnLogRespPayload) {
	r.mu.Lock()
	ch, ok := r.waiters[seq]
	if ok {
		delete(r.waiters, seq)
	}
	r.mu.Unlock()
	if ok {
		select {
		case ch <- p:
		default:
		}
	}
}

// forget 放弃等待（超时或被取消）
func (r *connLogRPC) forget(seq int64) {
	r.mu.Lock()
	delete(r.waiters, seq)
	r.mu.Unlock()
}

// notifyChange 广播连接记录变更，供 Web UI 实时刷新
func (c *Client) connLogNotify(data map[string]any) {
	c.events.Broadcast("conn_log_change", data)
}

// setConnLogMode 缓存中转端的连接记录模式（用于 UI 展示）
func (c *Client) setConnLogMode(mode string) {
	if mode == "" {
		return
	}
	c.mu.Lock()
	c.clMode = mode
	c.mu.Unlock()
}

// connLogModeCached 读取最近一次已知的连接记录模式
func (c *Client) connLogModeCached() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.clMode
}

// handleConnLogEvent 处理中转端镜像下发的单条记录（mirror 模式）
func (c *Client) handleConnLogEvent(msg protocol.Message) {
	p, err := protocol.Decode[protocol.ConnLogEventPayload](msg)
	if err != nil || p.Item.SessionID == "" {
		return
	}
	if err := connlog.Upsert(c.db, p.Item); err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("镜像连接记录失败: %v", err))
		return
	}
	c.connLogNotify(map[string]any{"session_id": p.Item.SessionID})
}

// handleConnLogSync 处理对账数据：清空/裁剪后增量补齐本地镜像库
func (c *Client) handleConnLogSync(msg protocol.Message) {
	p, err := protocol.Decode[protocol.ConnLogSyncPayload](msg)
	if err != nil {
		return
	}
	c.setConnLogMode(p.Mode)
	if p.Total == 0 {
		if err := connlog.Clear(c.db); err != nil {
			loghub.Default.Publish("warn", fmt.Sprintf("清空本地连接记录失败: %v", err))
		}
		c.connLogNotify(map[string]any{"reset": true})
		return
	}
	if p.MinID > 0 {
		if err := connlog.PruneBelow(c.db, p.MinID); err != nil {
			loghub.Default.Publish("warn", fmt.Sprintf("裁剪本地连接记录失败: %v", err))
		}
	}
	for i := range p.Items {
		if err := connlog.Upsert(c.db, p.Items[i]); err != nil {
			loghub.Default.Publish("warn", fmt.Sprintf("同步连接记录失败: %v", err))
		}
	}
	c.connLogNotify(map[string]any{"reset": true})
	if p.HasMore {
		c.pullConnLogsFrom(p.LastID)
	}
}

// handleConnLogClear 处理清空指令：清空本地镜像库
func (c *Client) handleConnLogClear(msg protocol.Message) {
	if _, err := protocol.Decode[protocol.ConnLogClearPayload](msg); err != nil {
		return
	}
	if err := connlog.Clear(c.db); err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("清空本地连接记录失败: %v", err))
		return
	}
	c.connLogNotify(map[string]any{"reset": true})
}

// handleConnLogResp 处理远程查询结果，交付给等待的调用方
func (c *Client) handleConnLogResp(msg protocol.Message) {
	p, err := protocol.Decode[protocol.ConnLogRespPayload](msg)
	if err != nil {
		return
	}
	c.setConnLogMode(p.Mode)
	c.clRPC.deliver(msg.Seq, p)
}

// handleConnLogExclude 接收中转端下发的排除 IP 列表（登录时与变更时）
func (c *Client) handleConnLogExclude(msg protocol.Message) {
	p, err := protocol.Decode[protocol.ConnLogExcludePayload](msg)
	if err != nil {
		return
	}
	ips := connlog.NormalizeList(p.IPs)
	c.mu.Lock()
	c.clExcluded = ips
	c.mu.Unlock()
	c.events.Broadcast("conn_log_excluded", map[string]any{"ips": ips})
}

// connLogExcluded 读取缓存的排除 IP 列表
func (c *Client) connLogExcluded() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.clExcluded))
	copy(out, c.clExcluded)
	return out
}

// setConnLogExcludedLocal 本地缓存排除 IP 列表（提交给中转端后即时更新，随后以中转端下发为准）
func (c *Client) setConnLogExcludedLocal(ips []string) {
	ips = connlog.NormalizeList(ips)
	c.mu.Lock()
	c.clExcluded = ips
	c.mu.Unlock()
	c.events.Broadcast("conn_log_excluded", map[string]any{"ips": ips})
}

// pullConnLogs 登录后以本地镜像游标为起点做增量对账
func (c *Client) pullConnLogs() {
	c.pullConnLogsFrom(connlog.MaxSrcID(c.db))
}

// pullConnLogsFrom 从指定权威 ID 之后拉取镜像记录
func (c *Client) pullConnLogsFrom(sinceID uint) {
	if !c.send(mustMsg(protocol.MsgConnLogPull, protocol.ConnLogPullReq{SinceID: sinceID, Limit: 500})) {
		loghub.Default.Publish("warn", "连接记录对账失败：控制通道未就绪")
	}
}

// reportConnLog 上报一条 forward 连接记录到中转端（权威端）；最佳努力，不阻塞控制消息
func (c *Client) reportConnLog(item protocol.ConnLogItem) {
	c.sendLog(mustMsg(protocol.MsgConnLogReport, protocol.ConnLogReportPayload{Items: []protocol.ConnLogItem{item}}))
}

// forwardConnLog 生成 forward 方向连接记录回调：会话开始时上报 active，结束时上报 closed。
// 真实访客地址仅在本地监听侧可见，因此由原站端负责采集并上报。
func (c *Client) forwardConnLog(meta *clientMeta, sessionID string, peer net.Addr) func(bytesIn, bytesOut int64) {
	if meta == nil {
		return nil
	}
	srcIP, srcPort := "", 0
	if peer != nil {
		srcIP, srcPort = connlog.SplitHostPort(peer.String())
	}
	base := protocol.ConnLogItem{
		SessionID:  sessionID,
		ProxyName:  meta.Name,
		NodeName:   c.cfg.NodeName,
		Type:       meta.Type,
		Direction:  model.DirectionForward,
		SourceIP:   srcIP,
		SourcePort: srcPort,
		Target:     meta.Target,
		StartedAt:  time.Now().UnixMilli(),
		Status:     connlog.StatusActive,
	}
	c.reportConnLog(base)

	started := time.Now()
	return func(in, out int64) {
		item := base
		item.BytesIn = in
		item.BytesOut = out
		item.EndedAt = time.Now().UnixMilli()
		item.DurationMs = time.Since(started).Milliseconds()
		item.Status = connlog.StatusClosed
		c.reportConnLog(item)
	}
}

// connLogQueryRemote 通过控制通道远程查询中转端（权威端）；超时或未连接时返回 false
func (c *Client) connLogQueryRemote(req protocol.ConnLogQueryReq) (protocol.ConnLogRespPayload, bool) {
	if connected, _, _ := c.connState(); !connected {
		return protocol.ConnLogRespPayload{}, false
	}
	seq, ch := c.clRPC.register()
	defer c.clRPC.forget(seq)

	msg := mustMsg(protocol.MsgConnLogQuery, req)
	msg.Seq = seq
	if !c.send(msg) {
		return protocol.ConnLogRespPayload{}, false
	}
	select {
	case p := <-ch:
		return p, true
	case <-time.After(5 * time.Second):
		return protocol.ConnLogRespPayload{}, false
	}
}
