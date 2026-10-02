package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// gin.Context 中的键
const (
	CtxUserID   = "pb_uid"
	CtxUsername = "pb_username"
	CtxRole     = "pb_role"
)

// wsTokenPrefix WebSocket 子协议中携带令牌的前缀
const wsTokenPrefix = "pb-token."

// TokenFromRequest 从 Header 或 WebSocket 子协议中提取令牌。
// 浏览器建立 WebSocket 时无法自定义 Header，因此改用子协议（Sec-WebSocket-Protocol）
// 传递令牌，避免把 token 放进 URL query 而被访问日志 / 反向代理记录。
func TokenFromRequest(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	for _, p := range strings.Split(c.GetHeader("Sec-WebSocket-Protocol"), ",") {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, wsTokenPrefix) {
			return strings.TrimPrefix(p, wsTokenPrefix)
		}
	}
	return ""
}

// AuthMiddleware JWT 鉴权中间件
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := ParseToken(TokenFromRequest(c))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "未认证或登录已过期"})
			return
		}
		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxUsername, claims.Username)
		c.Set(CtxRole, claims.Role)
		c.Next()
	}
}

// RequireAdmin 要求当前登录用户为管理员，需置于 AuthMiddleware 之后
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get(CtxRole)
		if r, _ := role.(string); r != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "需要管理员权限"})
			return
		}
		c.Next()
	}
}

// CurrentUser 读取当前登录用户
func CurrentUser(c *gin.Context) (uint, string) {
	var uid uint
	if v, ok := c.Get(CtxUserID); ok {
		uid, _ = v.(uint)
	}
	var name string
	if v, ok := c.Get(CtxUsername); ok {
		name, _ = v.(string)
	}
	return uid, name
}
