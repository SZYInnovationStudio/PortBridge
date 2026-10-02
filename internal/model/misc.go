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
