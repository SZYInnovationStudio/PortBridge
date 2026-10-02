// Package maintenance 提供每日定时清理，防止日志类数据无限增长把服务器磁盘写满。
package maintenance

import (
	"log"
	"time"

	"gorm.io/gorm"

	"portbridge/internal/connlog"
	"portbridge/internal/loghub"
	"portbridge/internal/model"
)

// 保留期：审计日志保留 7 天，流量明细保留 30 天（超出部分每天凌晨清理一次）
const (
	auditRetentionDays   = 7
	trafficRetentionDays = 30
)

// cst 东八区（UTC+8）
var cst = time.FixedZone("CST", 8*3600)

// Start 启动每日维护协程：每天北京时间 00:00 执行一次清理，ctx 结束时退出。
func Start(db *gorm.DB, done <-chan struct{}) {
	go func() {
		for {
			next := nextMidnight()
			timer := time.NewTimer(time.Until(next))
			select {
			case <-done:
				timer.Stop()
				return
			case <-timer.C:
				RunCleanup(db)
			}
		}
	}()
}

// nextMidnight 返回下一个北京时间 00:00
func nextMidnight() time.Time {
	now := time.Now().In(cst)
	y, m, d := now.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, cst)
}

// RunCleanup 执行一次清理：清空内存日志缓冲 + 删除过期的审计日志与流量明细。
func RunCleanup(db *gorm.DB) {
	loghub.Default.Reset()

	auditCutoff := time.Now().AddDate(0, 0, -auditRetentionDays)
	if err := db.Where("created_at < ?", auditCutoff).Delete(&model.AuditLog{}).Error; err != nil {
		log.Printf("[maintenance] 清理审计日志失败: %v", err)
	}

	trafficCutoff := time.Now().AddDate(0, 0, -trafficRetentionDays)
	if err := db.Where("bucket_at < ?", trafficCutoff).Delete(&model.TrafficStat{}).Error; err != nil {
		log.Printf("[maintenance] 清理流量明细失败: %v", err)
	}

	// 连接记录不做按天清理（由管理员手动清除），仅按条数上限裁剪，防止磁盘被撑爆
	if err := connlog.Trim(db, connlog.MaxRecords); err != nil {
		log.Printf("[maintenance] 裁剪连接记录失败: %v", err)
	}

	log.Printf("[maintenance] 每日清理完成：内存日志缓冲已重置，过期审计日志(%d 天)与流量明细(%d 天)已删除",
		auditRetentionDays, trafficRetentionDays)
	loghub.Default.Publish("info", "每日日志清理完成")
}
