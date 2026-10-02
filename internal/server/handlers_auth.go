package server

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"portbridge/internal/auth"
	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/util"
	"portbridge/internal/version"
)

// audit 写入审计日志
func (s *Server) audit(c *gin.Context, uid uint, username, action, target, detail string) {
	ip := util.RemoteIP(c.Request.RemoteAddr)
	_ = s.db.Create(&model.AuditLog{
		UserID: uid, Username: username, Action: action,
		Target: target, Detail: detail, IP: ip,
	}).Error
}

// auditMe 以当前登录用户身份写入审计日志
func (s *Server) auditMe(c *gin.Context, action, target, detail string) {
	uid, name := auth.CurrentUser(c)
	s.audit(c, uid, name, action, target, detail)
}

// ensureAdmin 首次启动时创建默认管理员
func (s *Server) ensureAdmin() {
	var count int64
	s.db.Model(&model.User{}).Count(&count)
	if count > 0 {
		return
	}
	hash, err := auth.HashPassword("admin123")
	if err != nil {
		log.Printf("[server] 创建默认管理员失败: %v", err)
		return
	}
	if err := s.db.Create(&model.User{
		Username: "admin", PasswordHash: hash, Nickname: "管理员", Role: "admin",
	}).Error; err != nil {
		log.Printf("[server] 创建默认管理员失败: %v", err)
		return
	}
	msg := "已创建默认管理员 admin / admin123，请登录后尽快修改密码"
	log.Println("[server]", msg)
	loghub.Default.Publish("warn", msg)
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// handleLogin 管理后台登录
func (s *Server) handleLogin(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "用户名与密码不能为空")
		return
	}
	var user model.User
	if err := s.db.Where("username = ?", req.Username).First(&user).Error; err != nil {
		fail(c, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		s.audit(c, user.ID, user.Username, "login_failed", "", "密码错误")
		fail(c, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	token, err := auth.GenerateToken(user.ID, user.Username, user.Role, 7*24*time.Hour)
	if err != nil {
		fail(c, http.StatusInternalServerError, "生成令牌失败")
		return
	}
	now := time.Now()
	ip := util.RemoteIP(c.Request.RemoteAddr)
	s.db.Model(&user).Updates(map[string]any{"last_login_at": now, "last_login_ip": ip})
	s.audit(c, user.ID, user.Username, "login", "", "登录成功")
	ok(c, gin.H{
		"token": token,
		"user": gin.H{
			"id": user.ID, "username": user.Username,
			"nickname": user.Nickname, "role": user.Role,
		},
	})
}

// handleMe 当前登录用户信息
func (s *Server) handleMe(c *gin.Context) {
	uid, _ := auth.CurrentUser(c)
	var user model.User
	if err := s.db.First(&user, uid).Error; err != nil {
		fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	ok(c, user)
}

type changePwdReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

// handleChangePassword 修改当前用户密码
func (s *Server) handleChangePassword(c *gin.Context) {
	var req changePwdReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "原密码与新密码不能为空")
		return
	}
	if len(req.NewPassword) < 6 {
		fail(c, http.StatusBadRequest, "新密码长度至少 6 位")
		return
	}
	uid, _ := auth.CurrentUser(c)
	var user model.User
	if err := s.db.First(&user, uid).Error; err != nil {
		fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.OldPassword) {
		fail(c, http.StatusBadRequest, "原密码错误")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		fail(c, http.StatusInternalServerError, "密码加密失败")
		return
	}
	s.db.Model(&user).Update("password_hash", hash)
	s.auditMe(c, "change_password", user.Username, "")
	ok(c, nil)
}

// handleSystemInfo 系统公开信息
func (s *Server) handleSystemInfo(c *gin.Context) {
	var nodeCount, proxyCount, userCount int64
	s.db.Model(&model.Node{}).Count(&nodeCount)
	s.db.Model(&model.Proxy{}).Count(&proxyCount)
	s.db.Model(&model.User{}).Count(&userCount)
	ok(c, gin.H{
		"version":     version.Version,
		"build_time":  version.BuildTime,
		"role":        string(s.cfg.Role),
		"node_name":   s.cfg.NodeName,
		"uptime_sec":  int64(time.Since(s.start).Seconds()),
		"node_count":  nodeCount,
		"proxy_count": proxyCount,
		"initialized": userCount > 0,
		"server_time": time.Now(),
	})
}
