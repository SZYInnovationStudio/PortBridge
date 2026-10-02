package proxy

import "golang.org/x/time/rate"

// NewLimiterKB 按 KB/s 创建限速器；limit<=0 表示不限速
func NewLimiterKB(kb int) (*rate.Limiter, int) {
	if kb <= 0 {
		return nil, 0
	}
	bytesPerSec := kb * 1024
	return rate.NewLimiter(rate.Limit(bytesPerSec), bytesPerSec), bytesPerSec
}
