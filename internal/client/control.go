package client

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"portbridge/internal/apiutil"
	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
	"portbridge/internal/util"
	"portbridge/internal/version"
)

// newRunID 生成本次运行的实例 ID
func newRunID() string { return util.NewID("run") }

// runtimeOS 返回操作系统标识
func runtimeOS() string { return runtime.GOOS }

// runtimeArch 返回 CPU 架构标识
func runtimeArch() string { return runtime.GOARCH }

// controlConn 一条到中转端的控制连接
type controlConn struct {
	cli       *Client
	conn      *websocket.Conn
	sendCh    chan protocol.Message
	logCh     chan protocol.Message
	done      chan struct{}
	closeOnce sync.Once
}

func (cc *controlConn) send(msg protocol.Message) bool {
	select {
	case cc.sendCh <- msg:
		return true
	case <-cc.done:
		return false
	default:
		return false
	}
}

// sendLog 最佳努力发送日志类消息（连接记录上报等）：队列满时直接丢弃，
// 避免日志流量挤占 sendCh 中的控制消息（心跳、对账等）。
func (cc *controlConn) sendLog(msg protocol.Message) {
	select {
	case cc.logCh <- msg:
	case <-cc.done:
	default:
	}
}

func (cc *controlConn) close() {
	cc.closeOnce.Do(func() {
		close(cc.done)
		_ = cc.conn.Close()
	})
}

// controlLoop 维护到中转端的控制连接，断线自动重连（指数退避）
func (c *Client) controlLoop(ctx context.Context) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := c.runControlOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			c.setConnected(false, err.Error())
			loghub.Default.Publish("warn", fmt.Sprintf("控制连接断开: %v，%s 后重连", err, backoff))
			log.Printf("[client] 控制连接断开: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// runControlOnce 建立一次控制连接并阻塞直到断开
func (c *Client) runControlOnce(ctx context.Context) error {
	url := c.cfg.AgentControlURL()
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(url, http.Header{})
	if err != nil {
		return fmt.Errorf("连接中转端 %s 失败: %w", url, err)
	}

	// 首帧：登录
	login, _ := protocol.NewMessage(protocol.MsgLogin, protocol.LoginReq{
		RunID:    c.runID,
		NodeName: c.cfg.NodeName,
		Version:  version.Version,
		OS:       runtimeOS(),
		Arch:     runtimeArch(),
		Ts:       time.Now().UnixMilli(),
		Token:    c.cfg.Token,
	})
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteJSON(login); err != nil {
		_ = conn.Close()
		return fmt.Errorf("发送登录请求失败: %w", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var first protocol.Message
	if err := conn.ReadJSON(&first); err != nil {
		_ = conn.Close()
		return fmt.Errorf("读取登录响应失败: %w", err)
	}
	if first.Type != protocol.MsgLoginResp {
		_ = conn.Close()
		return fmt.Errorf("登录响应类型异常: %s", first.Type)
	}
	resp, err := protocol.Decode[protocol.LoginResp](first)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("登录响应解析失败: %w", err)
	}
	if !resp.OK {
		_ = conn.Close()
		return fmt.Errorf("登录被拒绝: %s", resp.Msg)
	}
	_ = conn.SetReadDeadline(time.Time{})

	c.setSession(resp.SessionKey, c.resolveDataAddr(resp.DataPort))
	c.setConnected(true, "")
	msg := fmt.Sprintf("已连接中转端 %s（节点 %s），数据面 %s", c.cfg.ServerAddr, c.cfg.NodeName, c.dataAddrLocked())
	log.Printf("[client] %s", msg)
	loghub.Default.Publish("info", msg)

	cc := &controlConn{
		cli: c, conn: conn,
		sendCh: make(chan protocol.Message, 256),
		logCh:  make(chan protocol.Message, 512),
		done:   make(chan struct{}),
	}
	defer cc.close()
	c.setControlConn(cc)
	defer c.setControlConn(nil)

	hb := resp.HeartbeatSec
	if hb <= 0 {
		hb = c.cfg.HeartbeatSec
	}

	go c.controlWriter(cc)
	go c.controlHeartbeat(cc, hb)
	go c.controlReporter(cc, ctx)

	// 登录成功后立即全量上报一次本地规则
	cc.send(c.proxyReportMsg())
	cc.send(c.statusReportMsg())

	// 登录成功后做一次连接记录镜像对账（mirror 模式）
	c.pullConnLogs()

	return c.controlReadLoop(cc)
}

// resolveDataAddr 计算数据面地址：优先显式配置，否则用中转端返回的端口推导
func (c *Client) resolveDataAddr(port int) string {
	if c.cfg.ServerDataAddr != "" {
		return c.cfg.ServerDataAddr
	}
	host, _, err := net.SplitHostPort(c.cfg.ServerAddr)
	if err != nil {
		host = c.cfg.ServerAddr
	}
	if port <= 0 {
		port = c.cfg.DataPort
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func (c *Client) dataAddrLocked() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dataAddr
}

// controlWriter 单写协程，串行发送控制消息；优先发送 sendCh，空闲时才发送 logCh 中的日志消息
func (c *Client) controlWriter(cc *controlConn) {
	for {
		select {
		case <-cc.done:
			return
		case msg := <-cc.sendCh:
			if !c.writeControlMsg(cc, msg) {
				return
			}
		default:
		}
		select {
		case <-cc.done:
			return
		case msg := <-cc.sendCh:
			if !c.writeControlMsg(cc, msg) {
				return
			}
		case msg := <-cc.logCh:
			if !c.writeControlMsg(cc, msg) {
				return
			}
		}
	}
}

func (c *Client) writeControlMsg(cc *controlConn, msg protocol.Message) bool {
	_ = cc.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := cc.conn.WriteJSON(msg); err != nil {
		cc.close()
		return false
	}
	return true
}

// controlHeartbeat 周期性发送心跳
func (c *Client) controlHeartbeat(cc *controlConn, hbSec int) {
	if hbSec <= 0 {
		hbSec = 10
	}
	ticker := time.NewTicker(time.Duration(hbSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-cc.done:
			return
		case <-ticker.C:
			cc.send(mustMsg(protocol.MsgPing, protocol.PingReq{}))
		}
	}
}

// controlReporter 周期性上报规则与运行状态
func (c *Client) controlReporter(cc *controlConn, ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-cc.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			cc.send(c.proxyReportMsg())
			cc.send(c.statusReportMsg())
		}
	}
}

// controlReadLoop 读取并分发中转端消息
func (c *Client) controlReadLoop(cc *controlConn) error {
	for {
		var msg protocol.Message
		if err := cc.conn.ReadJSON(&msg); err != nil {
			return err
		}
		c.dispatch(cc, msg)
	}
}

// dispatch 处理单条控制消息
func (c *Client) dispatch(cc *controlConn, msg protocol.Message) {
	switch msg.Type {
	case protocol.MsgPing:
		resp, _ := protocol.NewMessage(protocol.MsgPong, nil)
		resp.Seq = msg.Seq
		resp.Ts = msg.Ts
		cc.send(resp)

	case protocol.MsgPong:
		if msg.Ts > 0 {
			c.setLatency(int(time.Now().UnixMilli() - msg.Ts))
		}

	case protocol.MsgProxySync:
		report, err := protocol.Decode[protocol.ProxyReport](msg)
		if err != nil {
			return
		}
		c.applySync(report)

	case protocol.MsgProxyCreate, protocol.MsgProxyUpdate:
		spec, err := protocol.Decode[protocol.ProxySpec](msg)
		if err != nil {
			return
		}
		c.applyProxySpec(spec)

	case protocol.MsgProxyDelete:
		spec, err := protocol.Decode[protocol.ProxySpec](msg)
		if err != nil {
			return
		}
		c.removeLocalProxy(spec.Name)

	case protocol.MsgProxyAck:
		ack, err := protocol.Decode[protocol.ProxyAck](msg)
		if err == nil && !ack.OK {
			loghub.Default.Publish("warn", "中转端拒绝规则 "+ack.Name+": "+ack.Msg)
		}

	case protocol.MsgNewWorkConn:
		req, err := protocol.Decode[protocol.WorkConnReq](msg)
		if err != nil {
			return
		}
		go c.serveReverse(req)

	case protocol.MsgConnLogEvent:
		go c.handleConnLogEvent(msg)

	case protocol.MsgConnLogSync:
		go c.handleConnLogSync(msg)

	case protocol.MsgConnLogClear:
		go c.handleConnLogClear(msg)

	case protocol.MsgConnLogResp:
		go c.handleConnLogResp(msg)

	case protocol.MsgError:
		em, err := protocol.Decode[protocol.ErrorMsg](msg)
		if err == nil {
			loghub.Default.Publish("error", "中转端错误: "+em.Message)
		}
	}
}

// proxyReportMsg 构造本地规则全量上报消息
func (c *Client) proxyReportMsg() protocol.Message {
	var proxies []model.Proxy
	c.db.Where("node_name = ? OR node_name = ''", c.cfg.NodeName).Find(&proxies)
	specs := make([]protocol.ProxySpec, 0, len(proxies))
	for i := range proxies {
		specs = append(specs, apiutil.ProxyToSpec(&proxies[i]))
	}
	msg, _ := protocol.NewMessage(protocol.MsgProxyReport, protocol.ProxyReport{Proxies: specs})
	return msg
}

// statusReportMsg 构造运行状态上报消息
func (c *Client) statusReportMsg() protocol.Message {
	conns, in, out := c.rules.stats()
	msg, _ := protocol.NewMessage(protocol.MsgStatusReport, protocol.StatusReport{
		Connections: int(conns),
		TrafficIn:   in,
		TrafficOut:  out,
		Uptime:      int64(time.Since(c.start).Seconds()),
		Version:     version.Version,
	})
	return msg
}

// mustMsg 构造消息（忽略错误）
func mustMsg(t protocol.MsgType, data any) protocol.Message {
	m, _ := protocol.NewMessage(t, data)
	return m
}
