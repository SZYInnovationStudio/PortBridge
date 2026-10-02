package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
	"portbridge/internal/proxy"
	"portbridge/internal/util"
)

const dataHandshakeTimeout = 10 * time.Second

// runDataListener 启动数据面监听（裸 TCP）
func (s *Server) runDataListener(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.DataAddr)
	if err != nil {
		return fmt.Errorf("监听数据面 %s 失败: %w", s.cfg.DataAddr, err)
	}
	msg := "数据面监听已就绪: " + s.cfg.DataAddr
	log.Println("[server]", msg)
	loghub.Default.Publish("info", msg)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return err
		}
		go s.handleDataConn(conn)
	}
}

// handleDataConn 处理一条数据连接：IP 白名单 -> 握手校验 -> 交付/拨号
func (s *Server) handleDataConn(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[server] 数据连接处理异常: %v", r)
			_ = conn.Close()
		}
	}()

	ip := util.RemoteIP(conn.RemoteAddr().String())
	if !util.IPAllowed(ip, s.cfg.IPWhitelist) {
		loghub.Default.Publish("warn", "数据面拒绝非白名单来源: "+ip)
		_ = conn.Close()
		return
	}

	// 读取一行 JSON 握手；无有效握手则静默关闭（防端口扫描）
	_ = conn.SetReadDeadline(time.Now().Add(dataHandshakeTimeout))
	br := bufio.NewReader(conn)
	line, err := br.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	var hs protocol.WorkHandshake
	if err := json.Unmarshal(line, &hs); err != nil {
		_ = conn.Close()
		return
	}

	agent := s.hub.FindByRunID(hs.RunID)
	if agent == nil {
		_ = conn.Close()
		return
	}
	if err := hs.Verify(agent.SessionKey); err != nil {
		loghub.Default.Publish("warn", fmt.Sprintf("数据面握手校验失败(%s): %v", ip, err))
		_ = conn.Close()
		return
	}

	// 保留 bufio 缓冲，避免丢失握手后紧随的业务数据
	bc := proxy.NewBufferedConn(conn, br)

	if hs.Mode == model.DirectionForward {
		go s.pm.ServeForwardConn(bc, hs)
		return
	}
	if err := s.pm.DeliverWorkConn(bc, hs); err != nil {
		loghub.Default.Publish("debug", fmt.Sprintf("数据连接无配对会话(规则 %s): %v", hs.ProxyName, err))
	}
}
