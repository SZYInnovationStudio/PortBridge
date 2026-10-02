// Package connlog 提供连接记录的共享存储层：两端（中转端与原站端）共用同一套模型与读写逻辑。
// 中转端为权威存储；mirror 模式下记录会镜像下发到原站端本地库，remote 模式下原站端不落库、远程查询中转端。
package connlog

import (
	"net"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"portbridge/internal/model"
	"portbridge/internal/protocol"
)

const (
	// MaxRecords 记录条数上限（防止无限增长撑爆磁盘），超出时删除最旧的记录
	MaxRecords = 200000

	// ModeMirror 镜像模式：中转端把记录镜像下发到原站端本地库
	ModeMirror = "mirror"
	// ModeRemote 远程模式：仅中转端存储，原站端通过控制通道远程查询
	ModeRemote = "remote"

	// StatusActive 连接进行中
	StatusActive = "active"
	// StatusClosed 连接已结束
	StatusClosed = "closed"
)

// Filter 查询过滤条件
type Filter struct {
	Keyword   string
	ProxyName string
	NodeName  string
	Status    string
	Page      int
	PageSize  int
}

var upsertCols = []string{
	"src_id", "proxy_name", "node_name", "type", "direction",
	"source_ip", "source_port", "target", "started_at", "ended_at",
	"duration_ms", "bytes_in", "bytes_out", "status",
}

// ToItem 模型转传输对象；镜像记录的 ID 统一以权威端 ID（src_id）呈现，保证两端一致
func ToItem(l *model.ConnLog) protocol.ConnLogItem {
	var ended int64
	if !l.EndedAt.IsZero() {
		ended = l.EndedAt.UnixMilli()
	}
	id := l.ID
	if l.SrcID > 0 {
		id = l.SrcID
	}
	return protocol.ConnLogItem{
		ID:         id,
		SessionID:  l.SessionID,
		ProxyName:  l.ProxyName,
		NodeName:   l.NodeName,
		Type:       l.Type,
		Direction:  l.Direction,
		SourceIP:   l.SourceIP,
		SourcePort: l.SourcePort,
		Target:     l.Target,
		StartedAt:  l.StartedAt.UnixMilli(),
		EndedAt:    ended,
		DurationMs: l.DurationMs,
		BytesIn:    l.BytesIn,
		BytesOut:   l.BytesOut,
		Status:     l.Status,
	}
}

// FromItem 传输对象转模型；镜像端以 item.ID 作为 src_id 对账游标
func FromItem(it protocol.ConnLogItem) *model.ConnLog {
	return &model.ConnLog{
		SrcID:      it.ID,
		SessionID:  it.SessionID,
		ProxyName:  it.ProxyName,
		NodeName:   it.NodeName,
		Type:       it.Type,
		Direction:  it.Direction,
		SourceIP:   it.SourceIP,
		SourcePort: it.SourcePort,
		Target:     it.Target,
		StartedAt:  millisToTime(it.StartedAt),
		EndedAt:    millisToTime(it.EndedAt),
		DurationMs: it.DurationMs,
		BytesIn:    it.BytesIn,
		BytesOut:   it.BytesOut,
		Status:     it.Status,
	}
}

// Save 按 session_id upsert 落库（权威端本地记录）。若为新建，l.ID 会被回填。
func Save(db *gorm.DB, l *model.ConnLog) error {
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "session_id"}},
		DoUpdates: clause.AssignmentColumns(upsertCols),
	}).Create(l).Error; err != nil {
		return err
	}
	if l.ID == 0 {
		var got model.ConnLog
		if err := db.First(&got, "session_id = ?", l.SessionID).Error; err == nil {
			l.ID = got.ID
		}
	}
	return nil
}

// Upsert 按 session_id upsert 镜像记录（src_id = item.ID）
func Upsert(db *gorm.DB, it protocol.ConnLogItem) error {
	l := FromItem(it)
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "session_id"}},
		DoUpdates: clause.AssignmentColumns(upsertCols),
	}).Create(l).Error
}

func baseQuery(db *gorm.DB, f Filter) *gorm.DB {
	q := db.Model(&model.ConnLog{})
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where(
			"source_ip LIKE ? OR CAST(source_port AS TEXT) LIKE ? OR proxy_name LIKE ? OR node_name LIKE ? OR target LIKE ? OR session_id LIKE ?",
			kw, kw, kw, kw, kw, kw,
		)
	}
	if f.ProxyName != "" {
		q = q.Where("proxy_name = ?", f.ProxyName)
	}
	if f.NodeName != "" {
		q = q.Where("node_name = ?", f.NodeName)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	return q
}

// Query 分页查询，返回记录与总数（按开始时间倒序）
func Query(db *gorm.DB, f Filter) ([]model.ConnLog, int64, error) {
	var total int64
	if err := baseQuery(db, f).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, size := f.Page, f.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	var list []model.ConnLog
	err := baseQuery(db, f).
		Order("started_at DESC, id DESC").
		Offset((page - 1) * size).Limit(size).
		Find(&list).Error
	return list, total, err
}

// Clear 清空全部记录
func Clear(db *gorm.DB) error {
	return db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.ConnLog{}).Error
}

// Trim 超出 max 条时删除最旧记录
func Trim(db *gorm.DB, max int) error {
	var count int64
	if err := db.Model(&model.ConnLog{}).Count(&count).Error; err != nil {
		return err
	}
	if count <= int64(max) {
		return nil
	}
	var cutoff model.ConnLog
	if err := db.Order("id ASC").Offset(int(count) - max).Limit(1).Find(&cutoff).Error; err != nil {
		return err
	}
	if cutoff.ID == 0 {
		return nil
	}
	return db.Where("id < ?", cutoff.ID).Delete(&model.ConnLog{}).Error
}

// CloseActive 把遗留的进行中记录标记为已结束（进程重启后连接已不存在）
func CloseActive(db *gorm.DB) error {
	var rows []model.ConnLog
	if err := db.Where("status = ?", StatusActive).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	now := time.Now()
	for i := range rows {
		dur := now.Sub(rows[i].StartedAt).Milliseconds()
		if dur < 0 {
			dur = 0
		}
		if err := db.Model(&model.ConnLog{}).Where("id = ?", rows[i].ID).
			Updates(map[string]any{
				"status":      StatusClosed,
				"ended_at":    now,
				"duration_ms": dur,
			}).Error; err != nil {
			return err
		}
	}
	return nil
}

// MaxSrcID 返回镜像库中最大的权威端 ID（对账游标）
func MaxSrcID(db *gorm.DB) uint {
	var v uint
	_ = db.Model(&model.ConnLog{}).Select("COALESCE(MAX(src_id),0)").Scan(&v).Error
	return v
}

// PruneBelow 删除权威端已不存在（src_id 小于 min_id）的陈旧镜像记录
func PruneBelow(db *gorm.DB, minID uint) error {
	if minID == 0 {
		return nil
	}
	return db.Where("src_id > 0 AND src_id < ?", minID).Delete(&model.ConnLog{}).Error
}

// Stats 返回镜像库中记录总数与 src_id 范围（仅统计 src_id > 0 的镜像记录）
func Stats(db *gorm.DB) (minID, maxID uint, total int64) {
	db.Model(&model.ConnLog{}).Where("src_id > 0").Count(&total)
	_ = db.Model(&model.ConnLog{}).Where("src_id > 0").Select("COALESCE(MIN(src_id),0)").Scan(&minID).Error
	_ = db.Model(&model.ConnLog{}).Where("src_id > 0").Select("COALESCE(MAX(src_id),0)").Scan(&maxID).Error
	return
}

// SplitHostPort 拆分 "host:port"，解析失败时端口返回 0
func SplitHostPort(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}
	p, _ := strconv.Atoi(portStr)
	return host, p
}

func millisToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
