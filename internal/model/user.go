package model

import "time"

// User 管理后台用户
type User struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	Username     string     `gorm:"uniqueIndex;size:64" json:"username"`
	PasswordHash string     `gorm:"size:255" json:"-"`
	Nickname     string     `gorm:"size:64" json:"nickname"`
	Role         string     `gorm:"size:32;default:admin" json:"role"` // admin | viewer
	LastLoginAt  *time.Time `json:"last_login_at"`
	LastLoginIP  string     `gorm:"size:64" json:"last_login_ip"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
