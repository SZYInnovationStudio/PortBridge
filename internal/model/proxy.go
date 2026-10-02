package model

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Proxy 转发/映射规则
type Proxy struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	NodeID   uint   `gorm:"index" json:"node_id"`
	NodeName string `gorm:"size:64;index" json:"node_name"`
	Name     string `gorm:"uniqueIndex;size:64" json:"name"`
	Type     string `gorm:"size:8;default:tcp" json:"type"`           // tcp | udp
	Direction string `gorm:"size:16;default:reverse" json:"direction"` // reverse | forward

	// RemoteAddr/RemotePort = 监听侧地址（reverse 时为中转端公网监听；forward 时为原站端本地监听）
	RemoteAddr string `gorm:"size:64;default:0.0.0.0" json:"remote_addr"`
	RemotePort int    `json:"remote_port"`
	// LocalIP/LocalPort = 目标地址（reverse 时为原站端本地目标；forward 时为中转端侧目标）
	LocalIP   string `gorm:"size:64" json:"local_ip"`
	LocalPort int    `json:"local_port"`

	Enabled bool `gorm:"default:true" json:"enabled"`

	Origin       string `gorm:"size:16;default:server" json:"origin"`
	Version      int64  `json:"version"`        // 配置版本号，仅真实编辑时递增，用于两端 LWW 收敛（不能依赖 UpdatedAt）
	RateLimitKB  int    `json:"rate_limit_kb"`  // 0 表示不限速
	TrafficLimit int64  `json:"traffic_limit"`  // 月流量上限(字节)，0 表示不限
	TrafficIn    int64  `json:"traffic_in"`     // 累计入流量(字节)
	TrafficOut   int64  `json:"traffic_out"`    // 累计出流量(字节)
	TotalConns   int64  `json:"total_conns"`    // 累计连接数
	Remark       string `gorm:"size:255" json:"remark"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// 运行态（不入库）
	ConnCount  int    `gorm:"-" json:"conn_count"`
	RunStatus  string `gorm:"-" json:"run_status"`  // running | stopped | error
	LastError  string `gorm:"-" json:"last_error"`
}

// TargetAddr 目标地址
func (p *Proxy) TargetAddr() string {
	return fmt.Sprintf("%s:%d", p.LocalIP, p.LocalPort)
}

// ListenAddr 监听地址
func (p *Proxy) ListenAddr() string {
	addr := p.RemoteAddr
	if addr == "" {
		addr = "0.0.0.0"
	}
	return fmt.Sprintf("%s:%d", addr, p.RemotePort)
}
