package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"portbridge/internal/config"
	"portbridge/internal/loghub"
	"portbridge/internal/model"
	"portbridge/internal/version"
)

// Server 中转端
type Server struct {
	cfg    *config.Config
	db     *gorm.DB
	hub    *AgentHub
	pm     *ProxyManager
	events *UIHub
	start  time.Time
	router *gin.Engine
}

// New 创建中转端实例
func New(cfg *config.Config, db *gorm.DB) *Server {
	s := &Server{cfg: cfg, db: db, start: time.Now()}
	s.events = newUIHub()
	s.hub = newAgentHub(s)
	s.pm = newProxyManager(s)
	s.router = s.buildRouter()
	return s
}

// Run 启动中转端全部组件
func (s *Server) Run(ctx context.Context) {
	gin.SetMode(gin.ReleaseMode)
	s.restoreProxies()

	go func() {
		if err := s.runDataListener(ctx); err != nil {
			log.Printf("[server] 数据面监听退出: %v", err)
		}
	}()
	go s.hub.offlineWatcher(ctx)
	go s.trafficFlusher(ctx)

	srv := &http.Server{Addr: s.cfg.AdminAddr, Handler: s.router}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		_ = s.pm.CloseAll()
	}()

	scheme := "http"
	if s.cfg.TLSEnabled {
		scheme = "https"
	}
	msg := fmt.Sprintf("中转端 PortBridge %s 已启动，管理后台 %s://%s，数据面 %s",
		version.Version, scheme, s.cfg.AdminAddr, s.cfg.DataAddr)
	log.Printf("[server] %s", msg)
	loghub.Default.Publish("info", msg)

	var err error
	if s.cfg.TLSEnabled && s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
		err = srv.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("[server] 服务退出: %v", err)
	}
	log.Println("[server] 已停止")
}

// restoreProxies 服务重启后恢复已启用规则
func (s *Server) restoreProxies() {
	var proxies []model.Proxy
	if err := s.db.Where("enabled = ?", true).Find(&proxies).Error; err != nil {
		log.Printf("[server] 加载规则失败: %v", err)
		return
	}
	for i := range proxies {
		p := proxies[i]
		if err := s.pm.Apply(&p); err != nil {
			log.Printf("[server] 恢复规则 %s 失败: %v", p.Name, err)
			loghub.Default.Publish("error", fmt.Sprintf("恢复规则 %s 失败: %v", p.Name, err))
		}
	}
	log.Printf("[server] 已恢复 %d 条规则", len(proxies))
}

// flushState 上一次刷盘时的累计值
type flushState struct {
	in    int64
	out   int64
	total int64
}

// trafficFlusher 周期性将内存计数落库并做流量限额校验
func (s *Server) trafficFlusher(ctx context.Context) {
	last := make(map[string]flushState)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			for _, name := range s.pm.RunningNames() {
				c := s.pm.Counter(name)
				if c == nil {
					continue
				}
				in, out, conns, total := c.Snapshot()
				prev := last[name]
				dIn, dOut, dConns := in-prev.in, out-prev.out, total-prev.total
				last[name] = flushState{in: in, out: out, total: total}
				if dIn == 0 && dOut == 0 {
					continue
				}

				s.db.Model(&model.Proxy{}).Where("name = ?", name).Updates(map[string]any{
					"traffic_in":  gorm.Expr("traffic_in + ?", dIn),
					"traffic_out": gorm.Expr("traffic_out + ?", dOut),
					"total_conns": gorm.Expr("total_conns + ?", dConns),
				})

				var p model.Proxy
				if err := s.db.Where("name = ?", name).First(&p).Error; err == nil {
					s.db.Create(&model.TrafficStat{
						ProxyID: p.ID, ProxyName: name,
						BytesIn: dIn, BytesOut: dOut, Conns: int(dConns), BucketAt: now,
					})
					// 月流量限额
					if p.TrafficLimit > 0 && p.TrafficIn+p.TrafficOut >= p.TrafficLimit {
						loghub.Default.Publish("warn", fmt.Sprintf("规则 %s 已达流量上限，自动停用", name))
						s.disableProxy(&p, "traffic_limit")
					}
				}
				_ = conns
			}
		}
	}
}
