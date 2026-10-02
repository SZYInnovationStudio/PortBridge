package server

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
)

// Agent 一条在线的原站端控制连接
type Agent struct {
	NodeName   string
	RunID      string
	SessionKey string
	Version    string
	OS         string
	Arch       string
	RemoteIP   string

	conn      *websocket.Conn
	sendCh    chan protocol.Message
	logCh     chan protocol.Message
	done      chan struct{}
	closeOnce sync.Once

	mu        sync.RWMutex
	lastSeen  time.Time
	latencyMs int
}

func (a *Agent) send(msg protocol.Message) bool {
	select {
	case a.sendCh <- msg:
		return true
	case <-a.done:
		return false
	default:
		// 发送队列满，视为连接异常
		return false
	}
}

// sendLog 最佳努力发送日志类消息（连接记录镜像等）：队列满时直接丢弃，
// 避免日志流量挤占 sendCh 中的控制消息（心跳、工作连接等）。
func (a *Agent) sendLog(msg protocol.Message) {
	select {
	case a.logCh <- msg:
	case <-a.done:
	default:
	}
}

func (a *Agent) touch() {
	a.mu.Lock()
	a.lastSeen = time.Now()
	a.mu.Unlock()
}

func (a *Agent) LastSeen() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastSeen
}

func (a *Agent) SetLatency(ms int) {
	a.mu.Lock()
	a.latencyMs = ms
	a.mu.Unlock()
}

func (a *Agent) Latency() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.latencyMs
}

func (a *Agent) close() {
	a.closeOnce.Do(func() {
		close(a.done)
		_ = a.conn.Close()
	})
}

// AgentHub 管理所有在线节点连接
type AgentHub struct {
	srv    *Server
	mu     sync.RWMutex
	agents map[string]*Agent // nodeName -> agent
}

func newAgentHub(s *Server) *AgentHub {
	return &AgentHub{srv: s, agents: make(map[string]*Agent)}
}

// Get 按节点名获取连接
func (h *AgentHub) Get(nodeName string) *Agent {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agents[nodeName]
}

// FindByRunID 按运行实例 ID 查找
func (h *AgentHub) FindByRunID(runID string) *Agent {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, a := range h.agents {
		if a.RunID == runID {
			return a
		}
	}
	return nil
}

func (h *AgentHub) register(a *Agent) {
	h.mu.Lock()
	if old, ok := h.agents[a.NodeName]; ok {
		old.close() // 同名节点重复登录，踢掉旧连接
	}
	h.agents[a.NodeName] = a
	h.mu.Unlock()
}

// unregister 注销连接，仅当 hub 中当前记录的就是该连接（指针一致）时才删除，返回是否真正删除
func (h *AgentHub) unregister(a *Agent) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur, ok := h.agents[a.NodeName]; ok && cur == a {
		delete(h.agents, a.NodeName)
		return true
	}
	return false
}

// OnlineNodes 返回在线节点名列表
func (h *AgentHub) OnlineNodes() map[string]*Agent {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]*Agent, len(h.agents))
	for k, v := range h.agents {
		out[k] = v
	}
	return out
}

// Send 向指定节点发送控制消息
func (h *AgentHub) Send(nodeName string, msg protocol.Message) error {
	a := h.Get(nodeName)
	if a == nil {
		return errNodeOffline
	}
	if !a.send(msg) {
		return errSendQueueFull
	}
	return nil
}

// SendLog 向指定节点发送日志类消息（最佳努力，节点离线或队列满时静默丢弃）
func (h *AgentHub) SendLog(nodeName string, msg protocol.Message) {
	a := h.Get(nodeName)
	if a == nil {
		return
	}
	a.sendLog(msg)
}

// SendWorkConn 请求某规则对应的节点建立数据连接
func (h *AgentHub) SendWorkConn(proxyName, sessionID, typ, mode string) error {
	meta := h.srv.pm.Meta(proxyName)
	if meta == nil {
		return errProxyNotFound
	}
	data, err := json.Marshal(protocol.WorkConnReq{
		ProxyName: proxyName, SessionID: sessionID, Type: typ, Mode: mode,
	})
	if err != nil {
		return err
	}
	return h.Send(meta.NodeName, protocol.Message{
		Type: protocol.MsgNewWorkConn,
		Ts:   time.Now().UnixMilli(),
		Data: data,
	})
}

// runReadLoop 处理来自原站端的消息
func (h *AgentHub) runReadLoop(a *Agent) {
	defer func() {
		a.close()
		// 仅当该连接仍是 hub 中的当前连接时才标记离线；
		// 若期间已被新连接顶替（重连/同名登录），则不应把新连接误判为离线。
		if h.unregister(a) {
			h.srv.onAgentOffline(a)
		}
	}()

	for {
		var msg protocol.Message
		if err := a.conn.ReadJSON(&msg); err != nil {
			log.Printf("[server] 节点 %s 连接断开: %v", a.NodeName, err)
			return
		}
		a.touch()
		h.dispatch(a, msg)
	}
}

func (h *AgentHub) dispatch(a *Agent, msg protocol.Message) {
	switch msg.Type {
	case protocol.MsgPing:
		var req protocol.PingReq
		_ = json.Unmarshal(msg.Data, &req)
		resp, _ := protocol.NewMessage(protocol.MsgPong, nil)
		resp.Seq = msg.Seq
		resp.Ts = msg.Ts // 回显时间戳用于计算 RTT
		a.send(resp)

	case protocol.MsgPong:
		if msg.Ts > 0 {
			a.SetLatency(int(time.Now().UnixMilli() - msg.Ts))
		}

	case protocol.MsgProxyReport:
		report, err := protocol.Decode[protocol.ProxyReport](msg)
		if err != nil {
			return
		}
		h.srv.handleProxyReport(a.NodeName, &report)

	case protocol.MsgProxyCreate, protocol.MsgProxyUpdate, protocol.MsgProxyDelete:
		spec, err := protocol.Decode[protocol.ProxySpec](msg)
		if err != nil {
			return
		}
		ok, errMsg := h.srv.handleProxyChange(a.NodeName, msg.Type, &spec)
		ack, _ := protocol.NewMessage(protocol.MsgProxyAck, protocol.ProxyAck{Name: spec.Name, OK: ok, Msg: errMsg})
		a.send(ack)

	case protocol.MsgStatusReport:
		// 运行态，仅记录日志
		if a.Version == "" {
			report, err := protocol.Decode[protocol.StatusReport](msg)
			if err == nil {
				a.Version = report.Version
			}
		}

	case protocol.MsgConnLogReport:
		go h.srv.handleConnLogReport(a, msg)

	case protocol.MsgConnLogPull:
		go h.srv.handleConnLogPull(a, msg)

	case protocol.MsgConnLogQuery:
		go h.srv.handleConnLogQuery(a, msg)

	case protocol.MsgConnLogClear:
		go h.srv.handleConnLogClear(a, msg)
	}
}

// offlineWatcher 心跳超时检测
func (h *AgentHub) offlineWatcher(ctxDone interface{ Done() <-chan struct{} }) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctxDone.Done():
			return
		case <-ticker.C:
			timeout := time.Duration(h.srv.cfg.HeartbeatSec*3) * time.Second
			now := time.Now()
			for name, a := range h.OnlineNodes() {
				if now.Sub(a.LastSeen()) > timeout {
					loghub.Default.Publish("warn", "节点心跳超时，标记离线: "+name)
					a.close()
				}
			}
		}
	}
}

// onAgentOffline 节点下线处理
func (s *Server) onAgentOffline(a *Agent) {
	s.db.Model(&model.Node{}).Where("name = ?", a.NodeName).Updates(map[string]any{
		"status":     model.NodeStatusOffline,
		"latency_ms": 0,
	})
	s.events.Broadcast("node_status", map[string]any{"name": a.NodeName, "online": false})
	loghub.Default.Publish("warn", "节点已离线: "+a.NodeName)
}
