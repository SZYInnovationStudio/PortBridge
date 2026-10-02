package model

import "time"

// AuditLog 审计日志
type AuditLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index" json:"user_id"`
	Username  string    `gorm:"size:64" json:"username"`
	Action    string    `gorm:"size:64" json:"action"`
	Target    string    `gorm:"size:128" json:"target"`
	Detail    string    `gorm:"type:text" json:"detail"`
	IP        string    `gorm:"size:64" json:"ip"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

// Setting 键值设置
type Setting struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TrafficStat 时序流量统计
type TrafficStat struct {
	ID       uint      `gorm:"primaryKey" json:"id"`
	ProxyID  uint      `gorm:"index" json:"proxy_id"`
	ProxyName string   `gorm:"size:64;index" json:"proxy_name"`
	BytesIn  int64     `json:"bytes_in"`
	BytesOut int64     `json:"bytes_out"`
	Conns    int       `json:"conns"`
	BucketAt time.Time `gorm:"index" json:"bucket_at"`
}

// ConnLog 连接记录：一次用户访问的完整信息。
// 中转端为权威存储；mirror 模式下中转端会把记录镜像下发到原站端本地库，
// remote 模式下原站端不落库、通过控制通道远程查询中转端。
type ConnLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	SrcID      uint      `gorm:"index" json:"src_id"` // 权威端记录 ID（镜像对账游标）
	SessionID  string    `gorm:"size:64;uniqueIndex" json:"session_id"`
	ProxyName  string    `gorm:"size:64;index" json:"proxy_name"`
	NodeName   string    `gorm:"size:64;index" json:"node_name"`
	Type       string    `gorm:"size:8" json:"type"`       // tcp | udp
	Direction  string    `gorm:"size:16" json:"direction"` // reverse | forward
	SourceIP   string    `gorm:"size:64;index" json:"source_ip"`
	SourcePort int       `json:"source_port"`
	Target     string    `gorm:"size:128" json:"target"`
	StartedAt  time.Time `gorm:"index" json:"started_at"`
	EndedAt    time.Time `json:"ended_at"`
	DurationMs int64     `json:"duration_ms"`
	BytesIn    int64     `json:"bytes_in"`
	BytesOut   int64     `json:"bytes_out"`
	Status     string    `gorm:"size:16;index" json:"status"` // active | closed
}
