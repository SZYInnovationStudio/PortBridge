package util

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"strings"
)

// SHA256Hex 计算字符串的 SHA-256 十六进制摘要（用于节点密钥校验）
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SHA256Equal 以恒定时间比较明文与已存储的 SHA-256 十六进制摘要，避免时序侧信道
func SHA256Equal(token, hashHex string) bool {
	want, err := hex.DecodeString(hashHex)
	if err != nil {
		return false
	}
	got := sha256.Sum256([]byte(token))
	return hmac.Equal(got[:], want)
}

// IPAllowed 判断 IP 是否命中白名单；白名单为空时放行
func IPAllowed(ip string, cidrs []string) bool {
	if len(cidrs) == 0 {
		return true
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !strings.Contains(c, "/") {
			if net.ParseIP(c) != nil && net.ParseIP(c).Equal(parsed) {
				return true
			}
			continue
		}
		if _, n, err := net.ParseCIDR(c); err == nil && n.Contains(parsed) {
			return true
		}
	}
	return false
}

// RemoteIP 从 "ip:port" 提取纯 IP
func RemoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
