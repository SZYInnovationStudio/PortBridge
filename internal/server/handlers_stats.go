package server

import (
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"portbridge/internal/loghub"
	"portbridge/internal/model"
)

// handleOverview 概览统计
func (s *Server) handleOverview(c *gin.Context) {
	var nodeTotal, proxyTotal, proxyEnabled int64
	s.db.Model(&model.Node{}).Count(&nodeTotal)
	s.db.Model(&model.Proxy{}).Count(&proxyTotal)
	s.db.Model(&model.Proxy{}).Where("enabled = ?", true).Count(&proxyEnabled)

	var agg struct {
		BytesIn  int64
		BytesOut int64
		Conns    int64
	}
	s.db.Model(&model.Proxy{}).
		Select("COALESCE(SUM(traffic_in),0) AS bytes_in, COALESCE(SUM(traffic_out),0) AS bytes_out, COALESCE(SUM(total_conns),0) AS conns").
		Scan(&agg)

	var currentConns int64
	for _, ri := range s.pm.Snapshot() {
		currentConns += ri.ConnCount
	}

	ok(c, gin.H{
		"node_total":     nodeTotal,
		"node_online":    len(s.hub.OnlineNodes()),
		"proxy_total":    proxyTotal,
		"proxy_enabled":  proxyEnabled,
		"traffic_in":     agg.BytesIn,
		"traffic_out":    agg.BytesOut,
		"total_conns":    agg.Conns,
		"current_conns":  currentConns,
		"uptime_sec":     int64(time.Since(s.start).Seconds()),
		"web_clients":    s.events.Subscribers(),
	})
}

type trafficBucket struct {
	Time     time.Time `json:"time"`
	BytesIn  int64     `json:"bytes_in"`
	BytesOut int64     `json:"bytes_out"`
	Conns    int64     `json:"conns"`
}

// handleTrafficSeries 按小时聚合流量曲线
func (s *Server) handleTrafficSeries(c *gin.Context) {
	hours := 24
	if v := c.Query("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 720 {
			hours = n
		}
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)

	var stats []model.TrafficStat
	s.db.Where("bucket_at >= ?", since).Find(&stats)

	idx := make(map[int64]*trafficBucket)
	for i := range stats {
		st := stats[i]
		key := st.BucketAt.Truncate(time.Hour).Unix()
		b, ok := idx[key]
		if !ok {
			b = &trafficBucket{Time: time.Unix(key, 0)}
			idx[key] = b
		}
		b.BytesIn += st.BytesIn
		b.BytesOut += st.BytesOut
		b.Conns += int64(st.Conns)
	}

	keys := make([]int64, 0, len(idx))
	for k := range idx {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	out := make([]*trafficBucket, 0, len(keys))
	for _, k := range keys {
		out = append(out, idx[k])
	}
	ok(c, out)
}

// handleLogs 返回最近运行日志
func (s *Server) handleLogs(c *gin.Context) {
	limit := 200
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	entries := loghub.Default.Recent()
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	ok(c, entries)
}
