package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"portbridge/internal/model"
	"portbridge/internal/store"
	"portbridge/internal/version"
)

// allSettings 读取全部键值设置
func (s *Server) allSettings() map[string]string {
	var rows []model.Setting
	s.db.Find(&rows)
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out
}

// handleGetSettings 读取系统设置
func (s *Server) handleGetSettings(c *gin.Context) {
	ok(c, gin.H{
		"role":           string(s.cfg.Role),
		"node_name":      s.cfg.NodeName,
		"admin_port":     s.cfg.AdminPort,
		"data_port":      s.cfg.DataPort,
		"heartbeat_sec":  s.cfg.HeartbeatSec,
		"proxy_port_min": s.cfg.ProxyPortMin,
		"proxy_port_max": s.cfg.ProxyPortMax,
		"tls_enabled":    s.cfg.TLSEnabled,
		"web_dist_path":  s.cfg.WebDistPath,
		"log_level":      s.cfg.LogLevel,
		"extra":          s.allSettings(),
	})
}

// handleUpdateSettings 更新可持久化设置
func (s *Server) handleUpdateSettings(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	for k, v := range req {
		if err := store.SetSetting(s.db, k, v); err != nil {
			fail(c, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.auditMe(c, "update_settings", "", "")
	ok(c, s.allSettings())
}

// exportPayload 配置导出结构
type exportPayload struct {
	Version    string            `json:"version"`
	ExportedAt time.Time         `json:"exported_at"`
	Settings   map[string]string `json:"settings"`
	Proxies    []model.Proxy     `json:"proxies"`
}

// handleExportConfig 导出规则与设置
func (s *Server) handleExportConfig(c *gin.Context) {
	var proxies []model.Proxy
	s.db.Find(&proxies)
	s.auditMe(c, "export_config", "", "")
	ok(c, exportPayload{
		Version: version.Version, ExportedAt: time.Now(),
		Settings: s.allSettings(), Proxies: proxies,
	})
}

// handleImportConfig 导入规则与设置
func (s *Server) handleImportConfig(c *gin.Context) {
	var payload exportPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		fail(c, http.StatusBadRequest, "导入文件格式错误")
		return
	}
	created, updated := 0, 0
	for i := range payload.Proxies {
		src := payload.Proxies[i]
		if src.Name == "" || src.RemotePort <= 0 || src.LocalPort <= 0 {
			continue
		}
		var p model.Proxy
		if err := s.db.Where("name = ?", src.Name).First(&p).Error; err != nil {
			np := src
			np.ID = 0
			np.CreatedAt = time.Time{}
			np.UpdatedAt = time.Time{}
			np.TrafficIn, np.TrafficOut, np.TotalConns = 0, 0, 0
			if err := s.db.Create(&np).Error; err != nil {
				continue
			}
			s.applyProxy(&np)
			created++
			continue
		}
		applySpecToProxy(proxyToSpec(&src), &p)
		if src.NodeName != "" {
			p.NodeName = src.NodeName
		}
		if err := s.db.Save(&p).Error; err != nil {
			continue
		}
		s.applyProxy(&p)
		updated++
	}
	for k, v := range payload.Settings {
		_ = store.SetSetting(s.db, k, v)
	}
	s.auditMe(c, "import_config", "", "")
	ok(c, gin.H{"created": created, "updated": updated})
}
