package auth

import (
	"sync"
	"time"
)

// 登录失败限流参数：同一来源在窗口期内累计失败达到阈值后暂时锁定，防止暴力破解
const (
	loginMaxFailures = 5
	loginFailWindow  = 5 * time.Minute
	loginMaxKeys     = 10000 // 追踪的来源上限，超过后清理过期项，避免内存无界增长
)

type loginAttempt struct {
	failures int
	firstAt  time.Time
}

// LoginLimiter 按来源（如客户端 IP）统计登录失败次数
type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt
}

// NewLoginLimiter 创建登录失败限流器
func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{attempts: make(map[string]*loginAttempt)}
}

// Allow 判断来源当前是否允许尝试登录；被锁定返回 false 与剩余锁定时间
func (l *LoginLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.attempts[key]
	if a == nil {
		return true, 0
	}
	elapsed := time.Since(a.firstAt)
	if elapsed > loginFailWindow {
		delete(l.attempts, key)
		return true, 0
	}
	if a.failures >= loginMaxFailures {
		return false, loginFailWindow - elapsed
	}
	return true, 0
}

// Fail 记录一次登录失败
func (l *LoginLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.attempts[key]
	if a == nil || time.Since(a.firstAt) > loginFailWindow {
		l.attempts[key] = &loginAttempt{failures: 1, firstAt: time.Now()}
		return
	}
	a.failures++
	if len(l.attempts) > loginMaxKeys {
		l.pruneLocked()
	}
}

// Reset 登录成功后清除该来源的失败记录
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

// pruneLocked 清理已过期条目（调用方需持锁）
func (l *LoginLimiter) pruneLocked() {
	now := time.Now()
	for k, a := range l.attempts {
		if now.Sub(a.firstAt) > loginFailWindow {
			delete(l.attempts, k)
		}
	}
}
