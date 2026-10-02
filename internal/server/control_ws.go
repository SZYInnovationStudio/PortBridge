package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
	"portbridge/internal/util"
	"portbridge/internal/version"
)

var agentUpgrader = websocket.Upgrader{
	ReadBufferSize:  8192,
	WriteBufferSize: 8192,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// handleAgentControl 原站端控制通道（WebSocket + JSON）
func (s *Server) handleAgentControl(c *gin.Context) {
	conn, err := agentUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// 第一条消息必须是登录
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var msg protocol.Message
	if err := conn.ReadJSON(&msg); err != nil {
		return
	}
	if msg.Type != protocol.MsgLogin {
		_ = conn.WriteJSON(mustMsg(protocol.MsgLoginResp, protocol.LoginResp{OK: false, Msg: "首条消息必须为 login"}))
		return
	}
	req, err := protocol.Decode[protocol.LoginReq](msg)
	if err != nil {
		_ = conn.WriteJSON(mustMsg(protocol.MsgLoginResp, protocol.LoginResp{OK: false, Msg: "登录报文解析失败"}))
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	ip := util.RemoteIP(c.Request.RemoteAddr)
	node, err := s.authenticateNode(req, ip)
	if err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("节点登录失败 %s(%s): %v", req.NodeName, ip, err))
		_ = conn.WriteJSON(mustMsg(protocol.MsgLoginResp, protocol.LoginResp{OK: false, Msg: err.Error()}))
		return
	}

	sessionKey := util.NewToken()
	agent := &Agent{
		NodeName: req.NodeName, RunID: req.RunID, SessionKey: sessionKey,
		Version: req.Version, OS: req.OS, Arch: req.Arch, RemoteIP: ip,
		conn: conn, sendCh: make(chan protocol.Message, 256),
		logCh: make(chan protocol.Message, 512), done: make(chan struct{}),
	}
	agent.touch()
	s.hub.register(agent)

	resp, _ := protocol.NewMessage(protocol.MsgLoginResp, protocol.LoginResp{
		OK: true, RunID: req.RunID, ServerVersion: version.Version,
		HeartbeatSec: s.cfg.HeartbeatSec, DataPort: s.cfg.DataPort, SessionKey: sessionKey,
	})
	if err := conn.WriteJSON(resp); err != nil {
		agent.close()
		s.hub.unregister(agent)
		return
	}

	s.onAgentOnline(agent, node)

	go s.agentWriter(agent)
	go s.agentHeartbeat(agent)
	s.hub.runReadLoop(agent) // 阻塞直到断开
}

// authenticateNode 校验节点名与预共享密钥
func (s *Server) authenticateNode(req protocol.LoginReq, ip string) (*model.Node, error) {
	if req.NodeName == "" || req.Token == "" {
		return nil, fmt.Errorf("节点名与密钥不能为空")
	}
	var node model.Node
	if err := s.db.Where("name = ?", req.NodeName).First(&node).Error; err != nil {
		return nil, fmt.Errorf("节点未授权：请先在中转端后台创建该节点")
	}
	if !util.SHA256Equal(req.Token, node.TokenHash) {
		return nil, fmt.Errorf("节点密钥错误")
	}
	if !util.IPAllowed(ip, parseCIDRList(node.IPWhitelist)) {
		return nil, fmt.Errorf("来源 IP 不在白名单内")
	}
	return &node, nil
}

// onAgentOnline 节点上线处理
func (s *Server) onAgentOnline(a *Agent, node *model.Node) {
	now := time.Now()
	s.db.Model(&model.Node{}).Where("id = ?", node.ID).Updates(map[string]any{
		"status":    model.NodeStatusOnline,
		"last_seen": now,
		"version":   a.Version,
		"os":        a.OS,
		"arch":      a.Arch,
		"remote_ip": a.RemoteIP,
	})
	s.events.Broadcast("node_status", map[string]any{"name": a.NodeName, "online": true})
	loghub.Default.Publish("info", fmt.Sprintf("节点已上线: %s (%s)", a.NodeName, a.RemoteIP))

	// 下发该节点全部规则
	go s.pushNodeSync(a.NodeName)
	// 下发当前排除 IP 列表，保证两端列表一致
	go s.pushConnLogExcluded(a)
}

// agentWriter 单写协程，串行发送控制消息；优先发送 sendCh，空闲时才发送 logCh 中的日志消息
func (s *Server) agentWriter(a *Agent) {
	for {
		select {
		case <-a.done:
			return
		case msg := <-a.sendCh:
			if !s.writeAgentMsg(a, msg) {
				return
			}
		default:
		}
		select {
		case <-a.done:
			return
		case msg := <-a.sendCh:
			if !s.writeAgentMsg(a, msg) {
				return
			}
		case msg := <-a.logCh:
			if !s.writeAgentMsg(a, msg) {
				return
			}
		}
	}
}

func (s *Server) writeAgentMsg(a *Agent, msg protocol.Message) bool {
	_ = a.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := a.conn.WriteJSON(msg); err != nil {
		a.close()
		return false
	}
	return true
}

// agentHeartbeat 周期性发送心跳，维持连接并测量延迟
func (s *Server) agentHeartbeat(a *Agent) {
	interval := time.Duration(s.cfg.HeartbeatSec) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			if !a.send(mustMsg(protocol.MsgPing, protocol.PingReq{})) {
				a.close()
				return
			}
			// 同步在线信息
			s.db.Model(&model.Node{}).Where("name = ?", a.NodeName).Updates(map[string]any{
				"last_seen":  time.Now(),
				"latency_ms": a.Latency(),
			})
		}
	}
}
