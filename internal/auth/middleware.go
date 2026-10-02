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

// TokenFromRequest 从 Header 或 query 中提取令牌（WS 场景使用 query）
func TokenFromRequest(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return c.Query("token")
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
