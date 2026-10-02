package client

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"portbridge/internal/auth"
	"portbridge/internal/config"
	"portbridge/internal/eventhub"
	"portbridge/internal/loghub"
	"portbridge/internal/maintenance"
	"portbridge/internal/model"
	"portbridge/internal/protocol"
	"portbridge/internal/version"
)

// Client 原站端
type Client struct {
	cfg    *config.Config
	db     *gorm.DB
	events *eventhub.Hub
	rules  *ruleManager
	start  time.Time
	router *gin.Engine

	loginLimiter *auth.LoginLimiter
	clRPC        *connLogRPC

	mu         sync.RWMutex
	runID      string
	sessionKey string
	dataAddr   string
	connected  bool
	latencyMs  int
	lastErr    string
	clMode     string
	cc         *controlConn
}

// New 创建原站端实例
func New(cfg *config.Config, db *gorm.DB) *Client {
	auth.Init(cfg.JWTSecret)
	c := &Client{
		cfg:          cfg,
		db:           db,
		start:        time.Now(),
		runID:        newRunID(),
		events:       eventhub.New(),
		loginLimiter: auth.NewLoginLimiter(),
		clRPC:        newConnLogRPC(),
	}
	c.rules = newRuleManager(c)
	c.router = c.buildRouter()
	c.ensureAdmin()
	return c
}

// Run 启动原站端全部组件
func (c *Client) Run(ctx context.Context) {
	gin.SetMode(gin.ReleaseMode)
	c.restoreRules()

	go c.controlLoop(ctx)
	maintenance.Start(c.db, ctx.Done())

	srv := &http.Server{Addr: c.cfg.AdminAddr, Handler: c.router}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		_ = c.rules.CloseAll()
	}()

	scheme := "http"
	if c.cfg.TLSEnabled {
		scheme = "https"
	}
	msg := fmt.Sprintf("原站端 PortBridge %s 已启动，管理后台 %s://%s，中转端 %s",
		version.Version, scheme, c.cfg.AdminAddr, c.cfg.ServerAddr)
	log.Printf("[client] %s", msg)
	loghub.Default.Publish("info", msg)

	var err error
	if c.cfg.TLSEnabled && c.cfg.TLSCert != "" && c.cfg.TLSKey != "" {
		err = srv.ListenAndServeTLS(c.cfg.TLSCert, c.cfg.TLSKey)
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("[client] 服务退出: %v", err)
	}
	log.Println("[client] 已停止")
}

// restoreRules 重启后恢复本地规则
func (c *Client) restoreRules() {
	var proxies []model.Proxy
	if err := c.db.Where("enabled = ?", true).Find(&proxies).Error; err != nil {
		log.Printf("[client] 加载规则失败: %v", err)
		return
	}
	for i := range proxies {
		if err := c.rules.Apply(&proxies[i]); err != nil {
			log.Printf("[client] 恢复规则 %s 失败: %v", proxies[i].Name, err)
			loghub.Default.Publish("error", fmt.Sprintf("恢复规则 %s 失败: %v", proxies[i].Name, err))
		}
	}
	log.Printf("[client] 已恢复 %d 条规则", len(proxies))
}

// setConnected 更新连接状态并广播
func (c *Client) setConnected(ok bool, errMsg string) {
	c.mu.Lock()
	c.connected = ok
	if errMsg != "" {
		c.lastErr = errMsg
	}
	c.mu.Unlock()
	c.events.Broadcast("conn_status", map[string]any{"connected": ok, "message": errMsg})
}

// connState 读取连接态快照
func (c *Client) connState() (bool, int, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected, c.latencyMs, c.lastErr
}

// setSession 保存登录后得到的会话信息
func (c *Client) setSession(sessionKey, dataAddr string) {
	c.mu.Lock()
	c.sessionKey = sessionKey
	c.dataAddr = dataAddr
	c.mu.Unlock()
}

// sessionInfo 读取会话信息
func (c *Client) sessionInfo() (string, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionKey, c.dataAddr
}

// setLatency 更新到中转端的往返延迟
func (c *Client) setLatency(ms int) {
	c.mu.Lock()
	c.latencyMs = ms
	c.mu.Unlock()
}

// setControlConn 保存当前控制连接，断开时置空
func (c *Client) setControlConn(cc *controlConn) {
	c.mu.Lock()
	c.cc = cc
	c.mu.Unlock()
}

// send 通过当前控制连接发送消息；未连接时返回 false
func (c *Client) send(msg protocol.Message) bool {
	c.mu.RLock()
	cc := c.cc
	c.mu.RUnlock()
	if cc == nil {
		return false
	}
	return cc.send(msg)
}

// sendLog 通过当前控制连接最佳努力发送日志类消息（连接记录上报等），不阻塞、队列满时丢弃
func (c *Client) sendLog(msg protocol.Message) {
	c.mu.RLock()
	cc := c.cc
	c.mu.RUnlock()
	if cc == nil {
		return
	}
	cc.sendLog(msg)
}

// pushReport 立即上报本地规则与运行状态（本地规则变更后触发）
func (c *Client) pushReport() {
	if c.send(c.proxyReportMsg()) {
		c.send(c.statusReportMsg())
	}
}
