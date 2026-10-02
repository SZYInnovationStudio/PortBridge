package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"portbridge/internal/connlog"
	"portbridge/internal/protocol"
	"portbridge/internal/store"
)

// handleListConnLogs 分页查询连接记录（中转端权威库）
func (s *Server) handleListConnLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	rows, total, err := connlog.Query(s.db, connlog.Filter{
		Keyword:   c.Query("keyword"),
		ProxyName: c.Query("proxy_name"),
		Status:    c.Query("status"),
		Page:      page,
		PageSize:  size,
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]protocol.ConnLogItem, 0, len(rows))
	for i := range rows {
		items = append(items, connlog.ToItem(&rows[i]))
	}
	ok(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": size,
		"mode":      s.connLogMode(),
		"backend":   "server",
	})
}

// handleClearConnLogs 清空全部连接记录并通知所有在线节点清空本地镜像
func (s *Server) handleClearConnLogs(c *gin.Context) {
	if err := connlog.Clear(s.db); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	s.broadcastConnLogClear()
	s.auditMe(c, "clear_conn_logs", "", "")
	ok(c, nil)
}

// handleSetConnLogMode 切换连接记录存储模式（mirror / remote）
func (s *Server) handleSetConnLogMode(c *gin.Context) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	mode := connlog.ModeMirror
	if req.Mode == connlog.ModeRemote {
		mode = connlog.ModeRemote
	}
	if err := store.SetSetting(s.db, connLogModeKey, mode); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditMe(c, "set_conn_log_mode", mode, "")
	ok(c, gin.H{"mode": mode})
}

// handleGetConnLogExcluded 返回当前排除 IP 列表
func (s *Server) handleGetConnLogExcluded(c *gin.Context) {
	ok(c, gin.H{"ips": s.connLogExcludedList()})
}

// handleSetConnLogExcluded 覆盖设置排除 IP 列表：命中的来源 IP 不再写入连接记录
func (s *Server) handleSetConnLogExcluded(c *gin.Context) {
	var req struct {
		IPs []string `json:"ips"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if err := s.setConnLogExcluded(req.IPs); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ips := s.connLogExcludedList()
	s.auditMe(c, "set_conn_log_excluded", strings.Join(ips, ","), "")
	ok(c, gin.H{"ips": ips})
}
