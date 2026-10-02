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

	"portbridge/internal/auth"
	"portbridge/internal/config"
	"portbridge/internal/connlog"
	"portbridge/internal/eventhub"
	"portbridge/internal/loghub"
	"portbridge/internal/maintenance"
	"portbridge/internal/model"
	"portbridge/internal/version"
)

// Server 中转端
type Server struct {
	cfg          *config.Config
	db           *gorm.DB
	hub          *AgentHub
	pm           *ProxyManager
	events       *eventhub.Hub
	loginLimiter *auth.LoginLimiter
	clEx         *connLogExcluder
	start        time.Time
	router       *gin.Engine
}

// New 创建中转端实例
func New(cfg *config.Config, db *gorm.DB) *Server {
	auth.Init(cfg.JWTSecret)
	s := &Server{cfg: cfg, db: db, start: time.Now(), loginLimiter: auth.NewLoginLimiter()}
	s.events = eventhub.New()
	s.hub = newAgentHub(s)
	s.pm = newProxyManager(s)
	s.clEx = newConnLogExcluder(db)
	s.router = s.buildRouter()
	s.ensureAdmin()
	return s
}

// Run 启动中转端全部组件
func (s *Server) Run(ctx context.Context) {
	gin.SetMode(gin.ReleaseMode)
	s.restoreProxies()
	if err := connlog.CloseActive(s.db); err != nil {
		log.Printf("[server] 清理遗留连接记录失败: %v", err)
	}

	go func() {
		if err := s.runDataListener(ctx); err != nil {
			log.Printf("[server] 数据面监听退出: %v", err)
		}
	}()
	go s.hub.offlineWatcher(ctx)
	go s.trafficFlusher(ctx)
	go s.connLogTrimLoop(ctx.Done())
	maintenance.Start(s.db, ctx.Done())

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

// monthKey 返回时间所在的自然月（UTC+8），用于月流量限额的跨月判定
func monthKey(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(time.FixedZone("CST", 8*3600)).Format("2006-01")
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
					// 规则已停用/移除，遗忘旧计数快照，避免重新启用后产生负增量
					delete(last, name)
					continue
				}
				in, out, _, total := c.Snapshot()
				prev := last[name]
				dIn, dOut, dConns := in-prev.in, out-prev.out, total-prev.total
				// 计数器在规则重建（如自动停用后重新启用）时会归零，按重置处理
				if dIn < 0 || dOut < 0 || dConns < 0 {
					dIn, dOut, dConns = in, out, total
				}
				last[name] = flushState{in: in, out: out, total: total}
				if dIn == 0 && dOut == 0 && dConns == 0 {
					continue
				}

				var p model.Proxy
				if err := s.db.Where("name = ?", name).First(&p).Error; err != nil {
					continue
				}

				// 月流量限额：跨月后计数归零重新统计
				if monthKey(p.TrafficResetAt) != monthKey(now) {
					s.db.Model(&model.Proxy{}).Where("id = ?", p.ID).Updates(map[string]any{
						"traffic_in": 0, "traffic_out": 0, "total_conns": 0, "traffic_reset_at": now,
					})
					p.TrafficIn, p.TrafficOut, p.TotalConns = 0, 0, 0
				}

				s.db.Model(&model.Proxy{}).Where("id = ?", p.ID).Updates(map[string]any{
					"traffic_in":  gorm.Expr("traffic_in + ?", dIn),
					"traffic_out": gorm.Expr("traffic_out + ?", dOut),
					"total_conns": gorm.Expr("total_conns + ?", dConns),
				})

				s.db.Create(&model.TrafficStat{
					ProxyID: p.ID, ProxyName: name,
					BytesIn: dIn, BytesOut: dOut, Conns: int(dConns), BucketAt: now,
				})
				// 月流量限额（按本月累计判定）
				if p.TrafficLimit > 0 && p.TrafficIn+dIn+p.TrafficOut+dOut >= p.TrafficLimit {
					loghub.Default.Publish("warn", fmt.Sprintf("规则 %s 已达流量上限，自动停用", name))
					s.disableProxy(&p, "traffic_limit")
				}
			}
		}
	}
}
