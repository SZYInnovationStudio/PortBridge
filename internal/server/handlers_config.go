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

// handleUpdateSettings 更新可持久化设置；值为 null 表示删除该键
func (s *Server) handleUpdateSettings(c *gin.Context) {
	var req map[string]*string
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	for k, v := range req {
		var err error
		if v == nil {
			err = store.DeleteSetting(s.db, k)
		} else {
			err = store.SetSetting(s.db, k, *v)
		}
		if err != nil {
			fail(c, http.StatusInternalServerError, err.Error())
			return
		}
	}
	// 批量更新可能旁路修改排除 IP 列表，需刷新内存缓存并同步节点
	if _, touched := req[connLogExcludedKey]; touched {
		s.reloadConnLogExcluded()
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
	// 预取本机已有节点名，导入时校验规则归属，避免指向不存在的节点
	nodeSet := make(map[string]bool)
	var nodes []model.Node
	s.db.Find(&nodes)
	for _, n := range nodes {
		nodeSet[n.Name] = true
	}

	created, updated, skipped := 0, 0, 0
	for i := range payload.Proxies {
		src := payload.Proxies[i]
		if src.Name == "" || src.RemotePort <= 0 || src.LocalPort <= 0 || src.RemotePort > 65535 || src.LocalPort > 65535 {
			skipped++
			continue
		}
		if s.checkProxyPort(src.RemotePort) != nil {
			skipped++
			continue
		}
		if src.NodeName == "" || !nodeSet[src.NodeName] {
			skipped++
			continue
		}
		var p model.Proxy
		if err := s.db.Where("name = ?", src.Name).First(&p).Error; err != nil {
			np := src
			np.ID = 0
			np.NodeID = 0
			np.CreatedAt = time.Time{}
			np.UpdatedAt = time.Time{}
			np.Version++ // 导入视为真实编辑
			np.TrafficIn, np.TrafficOut, np.TotalConns = 0, 0, 0
			if err := s.db.Create(&np).Error; err != nil {
				skipped++
				continue
			}
			s.applyProxy(&np)
			created++
			continue
		}
		applySpecToProxy(proxyToSpec(&src), &p)
		p.NodeName = src.NodeName
		p.Version++ // 导入视为真实编辑
		if err := s.db.Save(&p).Error; err != nil {
			skipped++
			continue
		}
		s.applyProxy(&p)
		updated++
	}
	for k, v := range payload.Settings {
		_ = store.SetSetting(s.db, k, v)
	}
	// 导入可能旁路修改排除 IP 列表，需刷新内存缓存并同步节点
	if _, touched := payload.Settings[connLogExcludedKey]; touched {
		s.reloadConnLogExcluded()
	}
	s.auditMe(c, "import_config", "", "")
	ok(c, gin.H{"created": created, "updated": updated, "skipped": skipped})
}
