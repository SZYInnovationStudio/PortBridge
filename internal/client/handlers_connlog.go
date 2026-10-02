package client

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"portbridge/internal/connlog"
	"portbridge/internal/protocol"
)

// handleListConnLogs 查询连接记录：
// 在线时经控制通道远程查询中转端（权威库），离线时回退查询本地镜像库。
func (c *Client) handleListConnLogs(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	keyword := ctx.Query("keyword")
	proxyName := ctx.Query("proxy_name")
	status := ctx.Query("status")

	if p, okd := c.connLogQueryRemote(protocol.ConnLogQueryReq{
		Keyword: keyword, ProxyName: proxyName, Status: status,
		Page: page, PageSize: size,
	}); okd {
		if !p.OK {
			fail(ctx, http.StatusInternalServerError, p.Msg)
			return
		}
		ok(ctx, gin.H{
			"items":     p.Items,
			"total":     p.Total,
			"page":      page,
			"page_size": size,
			"mode":      c.connLogModeCached(),
			"backend":   "server",
		})
		return
	}

	rows, total, err := connlog.Query(c.db, connlog.Filter{
		Keyword: keyword, ProxyName: proxyName, Status: status,
		Page: page, PageSize: size,
	})
	if err != nil {
		fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]protocol.ConnLogItem, 0, len(rows))
	for i := range rows {
		items = append(items, connlog.ToItem(&rows[i]))
	}
	ok(ctx, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": size,
		"mode":      c.connLogModeCached(),
		"backend":   "local",
	})
}

// handleClearConnLogs 清空本地镜像库，并请求中转端清空权威库
func (c *Client) handleClearConnLogs(ctx *gin.Context) {
	if err := connlog.Clear(c.db); err != nil {
		fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	c.send(mustMsg(protocol.MsgConnLogClear, protocol.ConnLogClearPayload{From: "client"}))
	c.connLogNotify(map[string]any{"reset": true})
	c.auditMe(ctx, "clear_conn_logs", "", "")
	ok(ctx, nil)
}

// handleGetConnLogExcluded 返回缓存的排除 IP 列表（由中转端统一维护）
func (c *Client) handleGetConnLogExcluded(ctx *gin.Context) {
	ok(ctx, gin.H{"ips": c.connLogExcluded()})
}

// handleSetConnLogExcluded 更新排除 IP 列表：需在线，经控制通道提交给中转端统一生效
func (c *Client) handleSetConnLogExcluded(ctx *gin.Context) {
	var req struct {
		IPs []string `json:"ips"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		fail(ctx, http.StatusBadRequest, "参数错误")
		return
	}
	ips := connlog.NormalizeList(req.IPs)
	if !c.send(mustMsg(protocol.MsgConnLogExcludeSet, protocol.ConnLogExcludePayload{IPs: ips})) {
		fail(ctx, http.StatusServiceUnavailable, "未连接中转端，无法修改排除 IP 列表")
		return
	}
	c.setConnLogExcludedLocal(ips)
	c.auditMe(ctx, "set_conn_log_excluded", strings.Join(ips, ","), "")
	ok(ctx, gin.H{"ips": ips})
}
