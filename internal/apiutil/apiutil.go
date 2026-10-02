// Package apiutil 提供管理后台 API 的通用响应封装与规则数据转换，
// 供中转端与原站端的管理接口共用。
package apiutil

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"portbridge/internal/model"
	"portbridge/internal/protocol"
)

// OK 统一成功响应
func OK(c *gin.Context, data any) {
	c.JSON(200, gin.H{"code": 0, "message": "ok", "data": data})
}

// Fail 统一失败响应
func Fail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"code": status, "message": msg})
}

// ParseCIDRList 解析 IP 白名单，支持 JSON 数组或逗号分隔两种写法
func ParseCIDRList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "[") {
		var out []string
		if err := json.Unmarshal([]byte(s), &out); err == nil {
			return out
		}
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// CIDRJSON 将白名单文本规范化为 JSON 数组字符串存储
func CIDRJSON(s string) string {
	list := ParseCIDRList(s)
	if len(list) == 0 {
		return ""
	}
	b, _ := json.Marshal(list)
	return string(b)
}

// ProxyToSpec 规则 -> 同步摘要
func ProxyToSpec(p *model.Proxy) protocol.ProxySpec {
	return protocol.ProxySpec{
		Name:         p.Name,
		Type:         p.Type,
		Direction:    p.Direction,
		RemoteAddr:   p.RemoteAddr,
		RemotePort:   p.RemotePort,
		LocalIP:      p.LocalIP,
		LocalPort:    p.LocalPort,
		Enabled:      p.Enabled,
		Origin:       p.Origin,
		Version:      p.Version,
		RateLimitKB:  p.RateLimitKB,
		TrafficLimit: p.TrafficLimit,
		UpdatedAt:    p.UpdatedAt.UnixMilli(),
	}
}

// SpecToProxy 同步摘要 -> 规则
func SpecToProxy(spec protocol.ProxySpec, nodeName string) *model.Proxy {
	return &model.Proxy{
		NodeName:     nodeName,
		Name:         spec.Name,
		Type:         spec.Type,
		Direction:    spec.Direction,
		RemoteAddr:   spec.RemoteAddr,
		RemotePort:   spec.RemotePort,
		LocalIP:      spec.LocalIP,
		LocalPort:    spec.LocalPort,
		Enabled:      spec.Enabled,
		Origin:       spec.Origin,
		Version:      spec.Version,
		RateLimitKB:  spec.RateLimitKB,
		TrafficLimit: spec.TrafficLimit,
	}
}

// ApplySpecToProxy 将同步摘要字段写入既有规则
func ApplySpecToProxy(spec protocol.ProxySpec, p *model.Proxy) {
	p.Type = spec.Type
	p.Direction = spec.Direction
	p.RemoteAddr = spec.RemoteAddr
	p.RemotePort = spec.RemotePort
	p.LocalIP = spec.LocalIP
	p.LocalPort = spec.LocalPort
	p.Enabled = spec.Enabled
	if spec.Origin != "" {
		p.Origin = spec.Origin
	}
	p.Version = spec.Version
	p.RateLimitKB = spec.RateLimitKB
	p.TrafficLimit = spec.TrafficLimit
}
