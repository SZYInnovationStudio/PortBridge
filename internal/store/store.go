package store

import (
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"portbridge/internal/config"
	"portbridge/internal/model"
)

// Open 打开数据库并执行自动迁移
func Open(cfg *config.Config) *gorm.DB {
	gormCfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)}

	var (
		db  *gorm.DB
		err error
	)
	switch cfg.DBDriver {
	case "postgres":
		db, err = gorm.Open(postgres.Open(cfg.DSN), gormCfg)
	default:
		if dir := filepath.Dir(cfg.DSN); dir != "" && dir != "." {
			_ = os.MkdirAll(dir, 0o755)
		}
		db, err = gorm.Open(sqlite.Open(cfg.DSN), gormCfg)
	}
	if err != nil {
		panic("打开数据库失败: " + err.Error())
	}
	if err := AutoMigrate(db); err != nil {
		panic("数据库迁移失败: " + err.Error())
	}
	return db
}

// AutoMigrate 建表
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.User{},
		&model.Node{},
		&model.Proxy{},
		&model.AuditLog{},
		&model.Setting{},
		&model.TrafficStat{},
		&model.ConnLog{},
	)
}

// GetSetting 读取配置项，不存在时返回默认值
func GetSetting(db *gorm.DB, key, def string) string {
	var s model.Setting
	if err := db.First(&s, "key = ?", key).Error; err != nil {
		return def
	}
	if s.Value == "" {
		return def
	}
	return s.Value
}

// SetSetting 写入配置项
func SetSetting(db *gorm.DB, key, value string) error {
	return db.Save(&model.Setting{Key: key, Value: value}).Error
}

// DeleteSetting 删除配置项
func DeleteSetting(db *gorm.DB, key string) error {
	return db.Delete(&model.Setting{}, "key = ?", key).Error
}
