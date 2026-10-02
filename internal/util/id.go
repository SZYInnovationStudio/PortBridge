package util

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
)

// NewID 生成带前缀的短 ID
func NewID(prefix string) string {
	id := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if prefix == "" {
		return id
	}
	return prefix + "-" + id
}

// NewToken 生成 32 字节随机密钥（hex 编码）
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return uuid.NewString()
	}
	return hex.EncodeToString(b)
}
