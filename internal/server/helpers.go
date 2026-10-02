package server

import (
	"github.com/gin-gonic/gin"

	"portbridge/internal/apiutil"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
)

// ok 统一成功响应
func ok(c *gin.Context, data any) { apiutil.OK(c, data) }

// fail 统一失败响应
func fail(c *gin.Context, status int, msg string) { apiutil.Fail(c, status, msg) }

// mustMsg 构造消息（忽略错误）
func mustMsg(t protocol.MsgType, data any) protocol.Message {
	m, _ := protocol.NewMessage(t, data)
	return m
}

// parseCIDRList 解析节点 IP 白名单（支持 JSON 数组或逗号分隔）
func parseCIDRList(s string) []string { return apiutil.ParseCIDRList(s) }

// cidrJSON 将白名单文本规范化为 JSON 数组字符串
func cidrJSON(s string) string { return apiutil.CIDRJSON(s) }

// proxyToSpec 规则 -> 同步摘要
func proxyToSpec(p *model.Proxy) protocol.ProxySpec { return apiutil.ProxyToSpec(p) }

// specToProxy 同步摘要 -> 规则
func specToProxy(spec protocol.ProxySpec, nodeName string) *model.Proxy {
	return apiutil.SpecToProxy(spec, nodeName)
}

// applySpecToProxy 将摘要字段写入既有规则
func applySpecToProxy(spec protocol.ProxySpec, p *model.Proxy) { apiutil.ApplySpecToProxy(spec, p) }
