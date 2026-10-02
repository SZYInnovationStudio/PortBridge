package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"portbridge/internal/auth"
)

// buildRouter 构建中转端 HTTP 路由
func (s *Server) buildRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	// 原站端控制通道
	r.GET("/api/v1/agent/control", s.handleAgentControl)

	api := r.Group("/api/v1")
	api.POST("/login", s.handleLogin)
	api.GET("/system/info", s.handleSystemInfo)

	a := api.Group("")
	a.Use(auth.AuthMiddleware())
	{
		a.GET("/me", s.handleMe)
		a.PUT("/me/password", s.handleChangePassword)
		a.GET("/ws/events", s.handleWSEvents)

		a.GET("/nodes", s.handleListNodes)
		a.POST("/nodes", s.handleCreateNode)
		a.PUT("/nodes/:id", s.handleUpdateNode)
		a.DELETE("/nodes/:id", s.handleDeleteNode)
		a.POST("/nodes/:id/token", s.handleResetNodeToken)
		a.POST("/nodes/:id/kick", s.handleKickNode)

		a.GET("/proxies", s.handleListProxies)
		a.POST("/proxies", s.handleCreateProxy)
		a.PUT("/proxies/:id", s.handleUpdateProxy)
		a.DELETE("/proxies/:id", s.handleDeleteProxy)
		a.POST("/proxies/:id/toggle", s.handleToggleProxy)
		a.GET("/ports/check", s.handleCheckPort)

		a.GET("/stats/overview", s.handleOverview)
		a.GET("/stats/traffic", s.handleTrafficSeries)
		a.GET("/logs", s.handleLogs)

		a.GET("/settings", s.handleGetSettings)
		a.PUT("/settings", s.handleUpdateSettings)
		a.GET("/config/export", s.handleExportConfig)
		a.POST("/config/import", s.handleImportConfig)
	}

	s.mountStatic(r)
	return r
}

// mountStatic 挂载前端静态资源（SPA 回退）
func (s *Server) mountStatic(r *gin.Engine) {
	dist := s.cfg.WebDistPath
	if dist == "" {
		return
	}
	index := filepath.Join(dist, "index.html")
	if _, err := os.Stat(index); err != nil {
		return
	}
	r.Static("/assets", filepath.Join(dist, "assets"))
	if _, err := os.Stat(filepath.Join(dist, "favicon.ico")); err == nil {
		r.StaticFile("/favicon.ico", filepath.Join(dist, "favicon.ico"))
	}
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "接口不存在"})
			return
		}
		c.File(index)
	})
}
