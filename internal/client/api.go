package client

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"portbridge/internal/apiutil"
	"portbridge/internal/auth"
	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
	"portbridge/internal/store"
	"portbridge/internal/util"
	"portbridge/internal/version"
)

var uiUpgrader = websocket.Upgrader{
	ReadBufferSize:  8192,
	WriteBufferSize: 8192,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// ok 统一成功响应
func ok(c *gin.Context, data any) { apiutil.OK(c, data) }

// fail 统一失败响应
func fail(c *gin.Context, status int, msg string) { apiutil.Fail(c, status, msg) }

// buildRouter 构建原站端 HTTP 路由
func (c *Client) buildRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(ctx *gin.Context) { ctx.JSON(http.StatusOK, gin.H{"ok": true}) })

	api := r.Group("/api/v1")
	api.POST("/login", c.handleLogin)
	api.GET("/system/info", c.handleSystemInfo)

	a := api.Group("")
	a.Use(auth.AuthMiddleware())
	{
		a.GET("/me", c.handleMe)
		a.PUT("/me/password", c.handleChangePassword)
		a.GET("/ws/events", c.handleWSEvents)

		a.GET("/proxies", c.handleListProxies)
		a.GET("/ports/check", c.handleCheckPort)

		a.GET("/logs", c.handleLogs)

		a.GET("/conn-logs", c.handleListConnLogs)
		a.GET("/conn-logs/excluded-ips", c.handleGetConnLogExcluded)

		a.GET("/settings", c.handleGetSettings)
		a.GET("/config/export", c.handleExportConfig)

		// 管理性写操作仅限管理员
		adm := a.Group("")
		adm.Use(auth.RequireAdmin())
		{
			adm.POST("/proxies", c.handleCreateProxy)
			adm.PUT("/proxies/:id", c.handleUpdateProxy)
			adm.DELETE("/proxies/:id", c.handleDeleteProxy)
			adm.POST("/proxies/:id/toggle", c.handleToggleProxy)

			adm.DELETE("/conn-logs", c.handleClearConnLogs)
			adm.PUT("/conn-logs/excluded-ips", c.handleSetConnLogExcluded)

			adm.PUT("/settings", c.handleUpdateSettings)
			adm.POST("/config/import", c.handleImportConfig)
		}
	}

	c.mountStatic(r)
	return r
}

// mountStatic 挂载前端静态资源（SPA 回退）
func (c *Client) mountStatic(r *gin.Engine) {
	dist := c.cfg.WebDistPath
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
	r.NoRoute(func(ctx *gin.Context) {
		p := ctx.Request.URL.Path
		if strings.HasPrefix(p, "/api/") {
			ctx.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "接口不存在"})
			return
		}
		ctx.File(index)
	})
}

// audit 写入审计日志
func (c *Client) audit(ctx *gin.Context, uid uint, username, action, target, detail string) {
	ip := util.RemoteIP(ctx.Request.RemoteAddr)
	_ = c.db.Create(&model.AuditLog{
		UserID: uid, Username: username, Action: action,
		Target: target, Detail: detail, IP: ip,
	}).Error
}

// auditMe 以当前登录用户身份写入审计日志
func (c *Client) auditMe(ctx *gin.Context, action, target, detail string) {
	uid, name := auth.CurrentUser(ctx)
	c.audit(ctx, uid, name, action, target, detail)
}

// ensureAdmin 首次启动时创建默认管理员
func (c *Client) ensureAdmin() {
	var count int64
	c.db.Model(&model.User{}).Count(&count)
	if count > 0 {
		return
	}
	hash, err := auth.HashPassword("admin123")
	if err != nil {
		log.Printf("[client] 创建默认管理员失败: %v", err)
		return
	}
	if err := c.db.Create(&model.User{
		Username: "admin", PasswordHash: hash, Nickname: "管理员", Role: "admin",
	}).Error; err != nil {
		log.Printf("[client] 创建默认管理员失败: %v", err)
		return
	}
	msg := "已创建默认管理员 admin / admin123，请登录后尽快修改密码"
	log.Println("[client]", msg)
	loghub.Default.Publish("warn", msg)
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// handleLogin 管理后台登录
func (c *Client) handleLogin(ctx *gin.Context) {
	var req loginReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		fail(ctx, http.StatusBadRequest, "用户名与密码不能为空")
		return
	}
	ip := util.RemoteIP(ctx.Request.RemoteAddr)
	if ok, wait := c.loginLimiter.Allow(ip); !ok {
		fail(ctx, http.StatusTooManyRequests, fmt.Sprintf("登录失败次数过多，请 %d 秒后重试", int(wait.Seconds())+1))
		return
	}
	var user model.User
	if err := c.db.Where("username = ?", req.Username).First(&user).Error; err != nil {
		c.loginLimiter.Fail(ip)
		fail(ctx, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		c.loginLimiter.Fail(ip)
		c.audit(ctx, user.ID, user.Username, "login_failed", "", "密码错误")
		fail(ctx, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	token, err := auth.GenerateToken(user.ID, user.Username, user.Role, 7*24*time.Hour)
	if err != nil {
		fail(ctx, http.StatusInternalServerError, "生成令牌失败")
		return
	}
	c.loginLimiter.Reset(ip)
	now := time.Now()
	c.db.Model(&user).Updates(map[string]any{"last_login_at": now, "last_login_ip": ip})
	c.audit(ctx, user.ID, user.Username, "login", "", "登录成功")
	ok(ctx, gin.H{
		"token": token,
		"user": gin.H{
			"id": user.ID, "username": user.Username,
			"nickname": user.Nickname, "role": user.Role,
		},
	})
}

// handleMe 当前登录用户信息
func (c *Client) handleMe(ctx *gin.Context) {
	uid, _ := auth.CurrentUser(ctx)
	var user model.User
	if err := c.db.First(&user, uid).Error; err != nil {
		fail(ctx, http.StatusNotFound, "用户不存在")
		return
	}
	ok(ctx, user)
}

type changePwdReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

// handleChangePassword 修改当前用户密码
func (c *Client) handleChangePassword(ctx *gin.Context) {
	var req changePwdReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		fail(ctx, http.StatusBadRequest, "原密码与新密码不能为空")
		return
	}
	if len(req.NewPassword) < 6 {
		fail(ctx, http.StatusBadRequest, "新密码长度至少 6 位")
		return
	}
	uid, _ := auth.CurrentUser(ctx)
	var user model.User
	if err := c.db.First(&user, uid).Error; err != nil {
		fail(ctx, http.StatusNotFound, "用户不存在")
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.OldPassword) {
		fail(ctx, http.StatusBadRequest, "原密码错误")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		fail(ctx, http.StatusInternalServerError, "密码加密失败")
		return
	}
	c.db.Model(&user).Update("password_hash", hash)
	c.auditMe(ctx, "change_password", user.Username, "")
	ok(ctx, nil)
}

// handleSystemInfo 系统公开信息
func (c *Client) handleSystemInfo(ctx *gin.Context) {
	var proxyCount, userCount int64
	c.db.Model(&model.Proxy{}).Count(&proxyCount)
	c.db.Model(&model.User{}).Count(&userCount)
	connected, latency, lastErr := c.connState()
	ok(ctx, gin.H{
		"version":     version.Version,
		"build_time":  version.BuildTime,
		"role":        string(c.cfg.Role),
		"node_name":   c.cfg.NodeName,
		"server_addr": c.cfg.ServerAddr,
		"connected":   connected,
		"latency_ms":  latency,
		"last_error":  lastErr,
		"uptime_sec":  int64(time.Since(c.start).Seconds()),
		"proxy_count": proxyCount,
		"initialized": userCount > 0,
		"server_time": time.Now(),
	})
}

// handleWSEvents Web UI 实时事件与日志推送
func (c *Client) handleWSEvents(ctx *gin.Context) {
	conn, err := uiUpgrader.Upgrade(ctx.Writer, ctx.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	evCh, cancelEv := c.events.Subscribe()
	defer cancelEv()
	lgCh, cancelLg := loghub.Default.Subscribe()
	defer cancelLg()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	writeJSON := func(v any) bool {
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(v) == nil
	}

	for _, e := range loghub.Default.Recent() {
		if !writeJSON(gin.H{"type": "log", "data": e}) {
			return
		}
	}
	connected, latency, lastErr := c.connState()
	if !writeJSON(gin.H{"type": "conn_status", "data": gin.H{"connected": connected, "latency_ms": latency, "message": lastErr}}) {
		return
	}

	for {
		select {
		case <-done:
			return
		case e, ok := <-evCh:
			if !ok || !writeJSON(gin.H{"type": e.Type, "data": e.Data, "ts": e.Ts}) {
				return
			}
		case l, ok := <-lgCh:
			if !ok || !writeJSON(gin.H{"type": "log", "data": l}) {
				return
			}
		}
	}
}

type proxyReq struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Direction    string `json:"direction"`
	RemoteAddr   string `json:"remote_addr"`
	RemotePort   int    `json:"remote_port"`
	LocalIP      string `json:"local_ip"`
	LocalPort    int    `json:"local_port"`
	Enabled      *bool  `json:"enabled"`
	RateLimitKB  *int   `json:"rate_limit_kb"`
	TrafficLimit *int64 `json:"traffic_limit"`
	Remark       *string `json:"remark"`
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
func (c *Client) handleListProxies(ctx *gin.Context) {
	q := c.db.Order("id asc")
	if typ := ctx.Query("type"); typ != "" {
		q = q.Where("type = ?", typ)
	}
	var proxies []model.Proxy
	if err := q.Find(&proxies).Error; err != nil {
		fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	snap := c.rules.Snapshot()
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
	ok(ctx, proxies)
}

// handleCreateProxy 创建规则
func (c *Client) handleCreateProxy(ctx *gin.Context) {
	var req proxyReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		fail(ctx, http.StatusBadRequest, "参数错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.RemotePort <= 0 || req.LocalPort <= 0 {
		fail(ctx, http.StatusBadRequest, "规则名、监听端口、目标端口均不能为空")
		return
	}
	if req.RemotePort > 65535 || req.LocalPort > 65535 {
		fail(ctx, http.StatusBadRequest, "端口必须在 1-65535 之间")
		return
	}
	var exist model.Proxy
	if err := c.db.Where("name = ?", req.Name).First(&exist).Error; err == nil {
		fail(ctx, http.StatusConflict, "规则名已存在")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	p := &model.Proxy{
		NodeName: c.cfg.NodeName, Name: req.Name,
		Type: normType(req.Type), Direction: normDirection(req.Direction),
		RemoteAddr: defaultAddr(req.RemoteAddr), RemotePort: req.RemotePort,
		LocalIP: req.LocalIP, LocalPort: req.LocalPort,
		Enabled: enabled, Origin: model.OriginClient,
		Version: 1,
	}
	if req.RateLimitKB != nil {
		p.RateLimitKB = *req.RateLimitKB
	}
	if req.TrafficLimit != nil {
		p.TrafficLimit = *req.TrafficLimit
	}
	if req.Remark != nil {
		p.Remark = *req.Remark
	}
	if err := c.db.Create(p).Error; err != nil {
		fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	c.rules.ensure(p)
	c.pushReport()
	c.auditMe(ctx, "create_proxy", p.Name, p.ListenAddr()+" -> "+p.TargetAddr())
	ok(ctx, p)
}

// handleUpdateProxy 更新规则
func (c *Client) handleUpdateProxy(ctx *gin.Context) {
	var p model.Proxy
	if err := c.db.First(&p, ctx.Param("id")).Error; err != nil {
		fail(ctx, http.StatusNotFound, "规则不存在")
		return
	}
	var req proxyReq
	if err := ctx.ShouldBindJSON(&req); err != nil {
		fail(ctx, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Name != "" && req.Name != p.Name {
		var exist model.Proxy
		if err := c.db.Where("name = ?", req.Name).First(&exist).Error; err == nil {
			fail(ctx, http.StatusConflict, "规则名已存在")
			return
		}
		c.rules.Remove(p.Name)
		p.Name = req.Name
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
		if req.RemotePort > 65535 {
			fail(ctx, http.StatusBadRequest, "端口必须在 1-65535 之间")
			return
		}
		p.RemotePort = req.RemotePort
	}
	if req.LocalIP != "" {
		p.LocalIP = req.LocalIP
	}
	if req.LocalPort > 0 {
		if req.LocalPort > 65535 {
			fail(ctx, http.StatusBadRequest, "端口必须在 1-65535 之间")
			return
		}
		p.LocalPort = req.LocalPort
	}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	if req.RateLimitKB != nil {
		p.RateLimitKB = *req.RateLimitKB
	}
	if req.TrafficLimit != nil {
		p.TrafficLimit = *req.TrafficLimit
	}
	if req.Remark != nil {
		p.Remark = *req.Remark
	}
	if p.Origin == "" {
		p.Origin = model.OriginClient
	}
	p.Version++ // 真实编辑，递增版本号以同步到中转端

	if err := c.db.Save(&p).Error; err != nil {
		fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	c.rules.ensure(&p)
	c.pushReport()
	c.auditMe(ctx, "update_proxy", p.Name, p.ListenAddr()+" -> "+p.TargetAddr())
	ok(ctx, &p)
}

// handleDeleteProxy 删除规则
func (c *Client) handleDeleteProxy(ctx *gin.Context) {
	var p model.Proxy
	if err := c.db.First(&p, ctx.Param("id")).Error; err != nil {
		fail(ctx, http.StatusNotFound, "规则不存在")
		return
	}
	c.rules.Remove(p.Name)
	c.db.Delete(&p)
	// 显式通知中转端删除，兼容 server 来源的规则
	spec := apiutil.ProxyToSpec(&p)
	if msg, err := protocol.NewMessage(protocol.MsgProxyDelete, spec); err == nil {
		c.send(msg)
	}
	c.pushReport()
	c.events.Broadcast("proxy_change", map[string]any{"name": p.Name, "deleted": true})
	c.auditMe(ctx, "delete_proxy", p.Name, "")
	ok(ctx, nil)
}

// handleToggleProxy 启用/停用规则
func (c *Client) handleToggleProxy(ctx *gin.Context) {
	var p model.Proxy
	if err := c.db.First(&p, ctx.Param("id")).Error; err != nil {
		fail(ctx, http.StatusNotFound, "规则不存在")
		return
	}
	p.Enabled = !p.Enabled
	p.Version++ // 启停属于真实编辑，递增版本号以同步到中转端
	if err := c.db.Model(&p).Updates(map[string]any{"enabled": p.Enabled, "version": p.Version}).Error; err != nil {
		fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	c.rules.ensure(&p)
	c.pushReport()
	state := "停用"
	if p.Enabled {
		state = "启用"
	}
	c.auditMe(ctx, "toggle_proxy", p.Name, state)
	ok(ctx, gin.H{"enabled": p.Enabled})
}

// handleCheckPort 检测本地端口占用
func (c *Client) handleCheckPort(ctx *gin.Context) {
	port, err := strconv.Atoi(ctx.Query("port"))
	if err != nil || port <= 0 || port > 65535 {
		fail(ctx, http.StatusBadRequest, "端口非法")
		return
	}
	typ := ctx.DefaultQuery("type", model.ProxyTypeTCP)
	addr := ctx.DefaultQuery("addr", "0.0.0.0")
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
	ok(ctx, gin.H{"port": port, "type": typ, "addr": addr, "available": available, "message": msg})
}

// handleLogs 返回最近运行日志
func (c *Client) handleLogs(ctx *gin.Context) {
	limit := 200
	if v := ctx.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	entries := loghub.Default.Recent()
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	ok(ctx, entries)
}

// allSettings 读取全部键值设置
func (c *Client) allSettings() map[string]string {
	var rows []model.Setting
	c.db.Find(&rows)
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out
}

// handleGetSettings 读取系统设置
func (c *Client) handleGetSettings(ctx *gin.Context) {
	connected, latency, lastErr := c.connState()
	ok(ctx, gin.H{
		"role":           string(c.cfg.Role),
		"node_name":      c.cfg.NodeName,
		"server_addr":    c.cfg.ServerAddr,
		"server_data_addr": c.cfg.ServerDataAddr,
		"admin_port":     c.cfg.AdminPort,
		"heartbeat_sec":  c.cfg.HeartbeatSec,
		"tls_enabled":    c.cfg.TLSEnabled,
		"web_dist_path":  c.cfg.WebDistPath,
		"log_level":      c.cfg.LogLevel,
		"connected":      connected,
		"latency_ms":     latency,
		"last_error":     lastErr,
		"extra":          c.allSettings(),
	})
}

// handleUpdateSettings 更新可持久化设置；值为 null 表示删除该键
func (c *Client) handleUpdateSettings(ctx *gin.Context) {
	var req map[string]*string
	if err := ctx.ShouldBindJSON(&req); err != nil {
		fail(ctx, http.StatusBadRequest, "参数错误")
		return
	}
	for k, v := range req {
		var err error
		if v == nil {
			err = store.DeleteSetting(c.db, k)
		} else {
			err = store.SetSetting(c.db, k, *v)
		}
		if err != nil {
			fail(ctx, http.StatusInternalServerError, err.Error())
			return
		}
	}
	c.auditMe(ctx, "update_settings", "", "")
	ok(ctx, c.allSettings())
}

// exportPayload 配置导出结构
type exportPayload struct {
	Version    string            `json:"version"`
	ExportedAt time.Time         `json:"exported_at"`
	Settings   map[string]string `json:"settings"`
	Proxies    []model.Proxy     `json:"proxies"`
}

// handleExportConfig 导出规则与设置
func (c *Client) handleExportConfig(ctx *gin.Context) {
	var proxies []model.Proxy
	c.db.Find(&proxies)
	c.auditMe(ctx, "export_config", "", "")
	ok(ctx, exportPayload{
		Version: version.Version, ExportedAt: time.Now(),
		Settings: c.allSettings(), Proxies: proxies,
	})
}

// handleImportConfig 导入规则与设置
func (c *Client) handleImportConfig(ctx *gin.Context) {
	var payload exportPayload
	if err := ctx.ShouldBindJSON(&payload); err != nil {
		fail(ctx, http.StatusBadRequest, "导入文件格式错误")
		return
	}
	created, updated := 0, 0
	for i := range payload.Proxies {
		src := payload.Proxies[i]
		if src.Name == "" || src.RemotePort <= 0 || src.LocalPort <= 0 {
			continue
		}
		var p model.Proxy
		if err := c.db.Where("name = ?", src.Name).First(&p).Error; err != nil {
			np := src
			np.ID = 0
			np.NodeID = 0
			np.CreatedAt = time.Time{}
			np.UpdatedAt = time.Time{}
			np.Version++ // 导入视为真实编辑
			np.NodeName = c.cfg.NodeName
			np.TrafficIn, np.TrafficOut, np.TotalConns = 0, 0, 0
			if np.Origin == "" {
				np.Origin = model.OriginClient
			}
			if err := c.db.Create(&np).Error; err != nil {
				continue
			}
			c.rules.ensure(&np)
			created++
			continue
		}
		apiutil.ApplySpecToProxy(apiutil.ProxyToSpec(&src), &p)
		if p.Origin == "" {
			p.Origin = model.OriginClient
		}
		p.Version++ // 导入视为真实编辑
		if err := c.db.Save(&p).Error; err != nil {
			continue
		}
		c.rules.ensure(&p)
		updated++
	}
	for k, v := range payload.Settings {
		_ = store.SetSetting(c.db, k, v)
	}
	c.pushReport()
	c.auditMe(ctx, "import_config", "", "")
	ok(ctx, gin.H{"created": created, "updated": updated})
}
