package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"portbridge/internal/model"
	"portbridge/internal/util"
)

// nodeView 节点列表视图（附带运行态）
type nodeView struct {
	model.Node
	IPWhitelist string `json:"ip_whitelist"`
	ProxyCount  int64  `json:"proxy_count"`
}

type nodeReq struct {
	Name        string `json:"name"`
	Remark      string `json:"remark"`
	Role        string `json:"role"`
	IPWhitelist string `json:"ip_whitelist"`
}

// handleListNodes 节点列表
func (s *Server) handleListNodes(c *gin.Context) {
	var nodes []model.Node
	if err := s.db.Order("id asc").Find(&nodes).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	type row struct {
		NodeName string
		N        int64
	}
	var rows []row
	s.db.Model(&model.Proxy{}).Select("node_name, count(*) as n").Group("node_name").Scan(&rows)
	counts := make(map[string]int64, len(rows))
	for _, r := range rows {
		counts[r.NodeName] = r.N
	}

	online := s.hub.OnlineNodes()
	views := make([]nodeView, 0, len(nodes))
	for i := range nodes {
		n := nodes[i]
		if a := online[n.Name]; a != nil {
			n.Online = true
			n.Status = model.NodeStatusOnline
			n.LatencyMs = a.Latency()
			n.Version = a.Version
			n.OS = a.OS
			n.Arch = a.Arch
			n.RemoteIP = a.RemoteIP
		} else {
			n.Online = false
			n.Status = model.NodeStatusOffline
		}
		views = append(views, nodeView{Node: n, IPWhitelist: n.IPWhitelist, ProxyCount: counts[n.Name]})
	}
	ok(c, views)
}

// handleCreateNode 创建节点并返回一次性明文密钥
func (s *Server) handleCreateNode(c *gin.Context) {
	var req nodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		fail(c, http.StatusBadRequest, "节点名不能为空")
		return
	}
	var exist model.Node
	if err := s.db.Where("name = ?", req.Name).First(&exist).Error; err == nil {
		fail(c, http.StatusConflict, "节点名已存在")
		return
	}
	token := util.NewToken()
	role := req.Role
	if role == "" {
		role = model.RoleClient
	}
	node := model.Node{
		Name: req.Name, TokenHash: util.SHA256Hex(token),
		Role: role, Status: model.NodeStatusOffline,
		Remark: req.Remark, IPWhitelist: cidrJSON(req.IPWhitelist),
	}
	if err := s.db.Create(&node).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditMe(c, "create_node", node.Name, "")
	ok(c, gin.H{"node": node, "token": token})
}

// handleUpdateNode 更新节点备注、角色与白名单
func (s *Server) handleUpdateNode(c *gin.Context) {
	var node model.Node
	if err := s.db.First(&node, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "节点不存在")
		return
	}
	var req nodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	node.Remark = req.Remark
	node.IPWhitelist = cidrJSON(req.IPWhitelist)
	if req.Role != "" {
		node.Role = req.Role
	}
	if err := s.db.Save(&node).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditMe(c, "update_node", node.Name, "")
	ok(c, node)
}

// handleDeleteNode 删除节点及其全部规则
func (s *Server) handleDeleteNode(c *gin.Context) {
	var node model.Node
	if err := s.db.First(&node, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "节点不存在")
		return
	}
	var proxies []model.Proxy
	s.db.Where("node_name = ?", node.Name).Find(&proxies)
	for i := range proxies {
		s.pm.Remove(proxies[i].Name)
	}
	s.db.Where("node_name = ?", node.Name).Delete(&model.Proxy{})
	if a := s.hub.Get(node.Name); a != nil {
		a.close()
	}
	s.db.Delete(&node)
	s.events.Broadcast("node_deleted", map[string]any{"name": node.Name})
	s.auditMe(c, "delete_node", node.Name, "")
	ok(c, nil)
}

// handleResetNodeToken 重置节点密钥并强制其重新登录
func (s *Server) handleResetNodeToken(c *gin.Context) {
	var node model.Node
	if err := s.db.First(&node, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "节点不存在")
		return
	}
	token := util.NewToken()
	if err := s.db.Model(&node).Update("token_hash", util.SHA256Hex(token)).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if a := s.hub.Get(node.Name); a != nil {
		a.close()
	}
	s.auditMe(c, "reset_node_token", node.Name, "")
	ok(c, gin.H{"token": token})
}

// handleKickNode 强制节点下线
func (s *Server) handleKickNode(c *gin.Context) {
	var node model.Node
	if err := s.db.First(&node, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "节点不存在")
		return
	}
	a := s.hub.Get(node.Name)
	if a == nil {
		fail(c, http.StatusBadRequest, "节点当前不在线")
		return
	}
	a.close()
	s.auditMe(c, "kick_node", node.Name, "")
	ok(c, nil)
}
