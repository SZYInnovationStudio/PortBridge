package client

import (
	"net/http"
	"strconv"

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
