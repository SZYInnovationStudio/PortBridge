package model

import "time"

// Node 节点（原站端注册信息，仅中转端维护）
type Node struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	Name        string     `gorm:"uniqueIndex;size:64" json:"name"`
	TokenHash   string     `gorm:"size:128" json:"-"`
	Role        string     `gorm:"size:16;default:client" json:"role"`
	Status      string     `gorm:"size:16;default:offline" json:"status"`
	LastSeen    *time.Time `json:"last_seen"`
	LatencyMs   int        `json:"latency_ms"`
	Version     string     `gorm:"size:32" json:"version"`
	OS          string     `gorm:"size:32" json:"os"`
	Arch        string     `gorm:"size:32" json:"arch"`
	RemoteIP    string     `gorm:"size:64" json:"remote_ip"`
	IPWhitelist string     `gorm:"type:text" json:"-"` // JSON 数组 CIDR
	Remark      string     `gorm:"size:255" json:"remark"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	// 运行态字段（不入库）
	Online bool `gorm:"-" json:"online"`
}
