package client

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
	"portbridge/internal/proxy"
)

// dialWorkConn 连接中转端数据面并完成握手，返回承载用户流量的连接
func (c *Client) dialWorkConn(proxyName, sessionID, typ, mode string) (net.Conn, error) {
	sessionKey, dataAddr := c.sessionInfo()
	if sessionKey == "" || dataAddr == "" {
		return nil, fmt.Errorf("控制通道尚未就绪")
	}
	conn, err := net.DialTimeout("tcp", dataAddr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("连接数据面 %s 失败: %w", dataAddr, err)
	}
	hs := protocol.NewWorkHandshake(sessionKey, c.runID, proxyName, sessionID, typ, mode)
	line, err := json.Marshal(hs)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	line = append(line, '\n')
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(line); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("发送握手失败: %w", err)
	}
	_ = conn.SetWriteDeadline(time.Time{})
	return conn, nil
}

// serveReverse 处理中转端发来的数据连接请求（reverse 方向：用户 -> 中转端 -> 本地目标）
func (c *Client) serveReverse(req protocol.WorkConnReq) {
	meta := c.rules.meta(req.ProxyName)
	if meta == nil {
		loghub.Default.Publish("warn", "忽略数据连接请求：本地规则不存在 "+req.ProxyName)
		return
	}
	typ := req.Type
	if typ == "" {
		typ = meta.Type
	}
	wc, err := c.dialWorkConn(req.ProxyName, req.SessionID, typ, model.DirectionReverse)
	if err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("规则 %s 建立工作连接失败: %v", req.ProxyName, err))
		return
	}

	if typ == model.ProxyTypeUDP {
		c.serveReverseUDP(wc, meta)
		return
	}
	c.serveReverseTCP(wc, meta)
}

// serveReverseTCP 将工作连接与本地 TCP 目标桥接
func (c *Client) serveReverseTCP(wc net.Conn, meta *clientMeta) {
	tc, err := net.DialTimeout("tcp", meta.Target, 10*time.Second)
	if err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("规则 %s 连接本地目标 %s 失败: %v", meta.Name, meta.Target, err))
		_ = wc.Close()
		return
	}
	meta.counter.IncConn()
	proxy.Bridge(proxy.WrapConn(tc, meta.counter, meta.RateLimitKB), wc)
	meta.counter.DecConn()
}

// serveReverseUDP 将工作连接与本地 UDP 目标桥接
func (c *Client) serveReverseUDP(wc net.Conn, meta *clientMeta) {
	raddr, err := net.ResolveUDPAddr("udp", meta.Target)
	if err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("规则 %s 解析目标 %s 失败: %v", meta.Name, meta.Target, err))
		_ = wc.Close()
		return
	}
	tc, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("规则 %s 连接本地 UDP 目标 %s 失败: %v", meta.Name, meta.Target, err))
		_ = wc.Close()
		return
	}
	meta.counter.IncConn()
	proxy.RelayUDP(wc, tc, meta.counter, nil)
	meta.counter.DecConn()
}
