package protocol

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Sign 计算 HMAC-SHA256 签名（十六进制）
func Sign(key, payload string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// HmacEqual 恒定时间比较签名
func HmacEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

// NewMessage 构造消息
func NewMessage(t MsgType, data any) (Message, error) {
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return Message{}, err
		}
		raw = b
	}
	return Message{Type: t, Ts: time.Now().UnixMilli(), Data: raw}, nil
}

// Decode 解析消息载荷
func Decode[T any](m Message) (T, error) {
	var v T
	if len(m.Data) == 0 {
		return v, nil
	}
	err := json.Unmarshal(m.Data, &v)
	return v, err
}

// WorkHandshake 数据连接首帧握手
type WorkHandshake struct {
	V         int    `json:"v"`
	RunID     string `json:"run_id"`
	ProxyName string `json:"proxy_name"`
	SessionID string `json:"session_id"`
	Type      string `json:"type"` // tcp/udp
	Mode      string `json:"mode"` // reverse/forward
	Ts        int64  `json:"ts"`
	Sign      string `json:"sign"`
}

// WorkSignPayload 生成数据连接签名原文
func WorkSignPayload(runID, proxyName, sessionID string, ts int64) string {
	return runID + "|" + proxyName + "|" + sessionID + "|" + strconv.FormatInt(ts, 10)
}

// NewWorkHandshake 构造握手并签名
func NewWorkHandshake(sessionKey, runID, proxyName, sessionID, typ, mode string) WorkHandshake {
	ts := time.Now().UnixMilli()
	return WorkHandshake{
		V:         1,
		RunID:     runID,
		ProxyName: proxyName,
		SessionID: sessionID,
		Type:      typ,
		Mode:      mode,
		Ts:        ts,
		Sign:      Sign(sessionKey, WorkSignPayload(runID, proxyName, sessionID, ts)),
	}
}

// Verify 校验握手签名与时间戳
func (h WorkHandshake) Verify(sessionKey string) error {
	now := time.Now().UnixMilli()
	if h.Ts == 0 || now-h.Ts > 60000 || h.Ts-now > 60000 {
		return fmt.Errorf("握手时间戳超时")
	}
	expect := Sign(sessionKey, WorkSignPayload(h.RunID, h.ProxyName, h.SessionID, h.Ts))
	if !HmacEqual(expect, h.Sign) {
		return fmt.Errorf("握手签名校验失败")
	}
	return nil
}
