package server

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"portbridge/internal/model"
)

type proxyReq struct {
	NodeName     string `json:"node_name"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Direction    string `json:"direction"`
	RemoteAddr   string `json:"remote_addr"`
	RemotePort   int    `json:"remote_port"`
	LocalIP      string `json:"local_ip"`
	LocalPort    int    `json:"local_port"`
	Enabled      *bool  `json:"enabled"`
	RateLimitKB  int    `json:"rate_limit_kb"`
	TrafficLimit int64  `json:"traffic_limit"`
	Remark       string `json:"remark"`
}

func normType(t string) string {
	if strings.EqualFold(t, model.ProxyTypeUDP) {
		return model.ProxyTypeUDP
	}
	return model.ProxyTypeTCP
}

func normDirection(d string) string {
	if strings.EqualFold(d, model.DirectionForward) {
		return model.DirectionForward
	}
	return model.DirectionReverse
}

func defaultAddr(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "0.0.0.0"
	}
	return s
}

// handleListProxies 规则列表（合并运行态）
func (s *Server) handleListProxies(c *gin.Context) {
	q := s.db.Order("id asc")
	if node := c.Query("node"); node != "" {
		q = q.Where("node_name = ?", node)
	}
	if typ := c.Query("type"); typ != "" {
		q = q.Where("type = ?", typ)
	}
	var proxies []model.Proxy
	if err := q.Find(&proxies).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	snap := s.pm.Snapshot()
	for i := range proxies {
		p := &proxies[i]
		if ri, ok := snap[p.Name]; ok {
			p.ConnCount = int(ri.ConnCount)
			p.LastError = ri.LastError
			if ri.Running {
				p.RunStatus = "running"
			} else {
				p.RunStatus = "stopped"
			}
		} else {
			p.RunStatus = "stopped"
		}
	}
	ok(c, proxies)
}

// handleCreateProxy 创建规则
func (s *Server) handleCreateProxy(c *gin.Context) {
	var req proxyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.RemotePort <= 0 || req.LocalPort <= 0 {
		fail(c, http.StatusBadRequest, "规则名、监听端口、目标端口均不能为空")
		return
	}
	if req.NodeName == "" {
		fail(c, http.StatusBadRequest, "必须指定所属节点")
		return
	}
	var cnt int64
	s.db.Model(&model.Node{}).Where("name = ?", req.NodeName).Count(&cnt)
	if cnt == 0 {
		fail(c, http.StatusBadRequest, "所属节点不存在")
		return
	}
	var exist model.Proxy
	if err := s.db.Where("name = ?", req.Name).First(&exist).Error; err == nil {
		fail(c, http.StatusConflict, "规则名已存在")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	p := &model.Proxy{
		NodeName: req.NodeName, Name: req.Name,
		Type: normType(req.Type), Direction: normDirection(req.Direction),
		RemoteAddr: defaultAddr(req.RemoteAddr), RemotePort: req.RemotePort,
		LocalIP: req.LocalIP, LocalPort: req.LocalPort,
		Enabled: enabled, Origin: model.OriginServer,
		Version:     1,
		RateLimitKB: req.RateLimitKB, TrafficLimit: req.TrafficLimit, Remark: req.Remark,
	}
	if err := s.db.Create(p).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	s.applyProxy(p)
	s.broadcastProxyChange(p.Name)
	s.auditMe(c, "create_proxy", p.Name, p.ListenAddr()+" -> "+p.TargetAddr())
	ok(c, p)
}

// handleUpdateProxy 更新规则
func (s *Server) handleUpdateProxy(c *gin.Context) {
	var p model.Proxy
	if err := s.db.First(&p, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "规则不存在")
		return
	}
	var req proxyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Name != "" && req.Name != p.Name {
		var exist model.Proxy
		if err := s.db.Where("name = ?", req.Name).First(&exist).Error; err == nil {
			fail(c, http.StatusConflict, "规则名已存在")
			return
		}
		s.pm.Remove(p.Name)
		p.Name = req.Name
	}
	if req.NodeName != "" {
		p.NodeName = req.NodeName
	}
	if req.Type != "" {
		p.Type = normType(req.Type)
	}
	if req.Direction != "" {
		p.Direction = normDirection(req.Direction)
	}
	if req.RemoteAddr != "" {
		p.RemoteAddr = req.RemoteAddr
	}
	if req.RemotePort > 0 {
		p.RemotePort = req.RemotePort
	}
	if req.LocalIP != "" {
		p.LocalIP = req.LocalIP
	}
	if req.LocalPort > 0 {
		p.LocalPort = req.LocalPort
	}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	p.RateLimitKB = req.RateLimitKB
	p.TrafficLimit = req.TrafficLimit
	p.Remark = req.Remark
	p.Version++ // 真实编辑，递增版本号以同步到对端

	if err := s.db.Save(&p).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	s.applyProxy(&p)
	s.broadcastProxyChange(p.Name)
	s.auditMe(c, "update_proxy", p.Name, p.ListenAddr()+" -> "+p.TargetAddr())
	ok(c, &p)
}

// handleDeleteProxy 删除规则
func (s *Server) handleDeleteProxy(c *gin.Context) {
	var p model.Proxy
	if err := s.db.First(&p, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "规则不存在")
		return
	}
	s.pm.Remove(p.Name)
	s.db.Delete(&p)
	s.events.Broadcast("proxy_change", map[string]any{"name": p.Name, "deleted": true})
	if p.NodeName != "" {
		s.pushNodeSync(p.NodeName)
	}
	s.auditMe(c, "delete_proxy", p.Name, "")
	ok(c, nil)
}

// handleToggleProxy 启用/停用规则
func (s *Server) handleToggleProxy(c *gin.Context) {
	var p model.Proxy
	if err := s.db.First(&p, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "规则不存在")
		return
	}
	p.Enabled = !p.Enabled
	p.Version++ // 启停属于真实编辑，递增版本号以同步到对端
	if err := s.db.Model(&p).Updates(map[string]any{"enabled": p.Enabled, "version": p.Version}).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	s.applyProxy(&p)
	s.broadcastProxyChange(p.Name)
	state := "停用"
	if p.Enabled {
		state = "启用"
	}
	s.auditMe(c, "toggle_proxy", p.Name, state)
	ok(c, gin.H{"enabled": p.Enabled})
}

// handleCheckPort 检测端口占用
func (s *Server) handleCheckPort(c *gin.Context) {
	port, err := strconv.Atoi(c.Query("port"))
	if err != nil || port <= 0 || port > 65535 {
		fail(c, http.StatusBadRequest, "端口非法")
		return
	}
	typ := c.DefaultQuery("type", model.ProxyTypeTCP)
	addr := c.DefaultQuery("addr", "0.0.0.0")
	target := fmt.Sprintf("%s:%d", addr, port)

	available := true
	var msg string
	if typ == model.ProxyTypeUDP {
		ua, err := net.ResolveUDPAddr("udp", target)
		if err != nil {
			available, msg = false, err.Error()
		} else if uc, err := net.ListenUDP("udp", ua); err != nil {
			available, msg = false, err.Error()
		} else {
			_ = uc.Close()
		}
	} else {
		if ln, err := net.Listen("tcp", target); err != nil {
			available, msg = false, err.Error()
		} else {
			_ = ln.Close()
		}
	}
	ok(c, gin.H{"port": port, "type": typ, "addr": addr, "available": available, "message": msg})
}
