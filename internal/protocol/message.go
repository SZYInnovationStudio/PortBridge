package protocol

import "encoding/json"

// MsgType 控制通道消息类型
type MsgType string

const (
	MsgLogin         MsgType = "login"
	MsgLoginResp     MsgType = "login_resp"
	MsgPing          MsgType = "ping"
	MsgPong          MsgType = "pong"
	MsgProxyReport   MsgType = "proxy_report"
	MsgProxySync     MsgType = "proxy_sync"
	MsgProxyCreate   MsgType = "proxy_create"
	MsgProxyUpdate   MsgType = "proxy_update"
	MsgProxyDelete   MsgType = "proxy_delete"
	MsgProxyAck      MsgType = "proxy_ack"
	MsgNewWorkConn   MsgType = "new_work_conn"
	MsgStatusReport  MsgType = "status_report"
	MsgTrafficReport MsgType = "traffic_report"
	MsgError         MsgType = "error"
)

// Message 统一消息信封
type Message struct {
	Type MsgType         `json:"type"`
	Seq  int64           `json:"seq"`
	Ts   int64           `json:"ts"`
	Data json.RawMessage `json:"data,omitempty"`
}

// LoginReq 原站端登录请求
type LoginReq struct {
	RunID    string `json:"run_id"`
	NodeName string `json:"node_name"`
	Version  string `json:"version"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Ts       int64  `json:"ts"`
	Token    string `json:"token"`
}

// LoginResp 登录响应
type LoginResp struct {
	OK            bool   `json:"ok"`
	RunID         string `json:"run_id"`
	ServerVersion string `json:"server_version"`
	HeartbeatSec  int    `json:"heartbeat_sec"`
	DataPort      int    `json:"data_port"`
	SessionKey    string `json:"session_key"` // 会话密钥，用于数据连接握手签名
	Msg           string `json:"msg,omitempty"`
}

// PingReq 心跳
type PingReq struct {
	Load float64 `json:"load"`
}

// WorkConnReq 请求建立数据连接
type WorkConnReq struct {
	ProxyName string `json:"proxy_name"`
	SessionID string `json:"session_id"`
	Type      string `json:"type"` // tcp/udp
	Mode      string `json:"mode"` // reverse/forward
}

// ProxySpec 规则摘要（用于同步）
type ProxySpec struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Direction    string `json:"direction"`
	RemoteAddr   string `json:"remote_addr"`
	RemotePort   int    `json:"remote_port"`
	LocalIP      string `json:"local_ip"`
	LocalPort    int    `json:"local_port"`
	Enabled      bool   `json:"enabled"`
	Origin       string `json:"origin"`
	RateLimitKB  int    `json:"rate_limit_kb"`
	TrafficLimit int64  `json:"traffic_limit"`
	UpdatedAt    int64  `json:"updated_at"` // UnixMilli，用于 LWW 合并
}

// ProxyReport 全量规则上报 / 同步
type ProxyReport struct {
	Proxies []ProxySpec `json:"proxies"`
}

// ProxyAck 规则变更确认
type ProxyAck struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Msg  string `json:"msg,omitempty"`
}

// StatusReport 运行状态上报
type StatusReport struct {
	Connections int    `json:"connections"`
	TrafficIn   int64  `json:"traffic_in"`
	TrafficOut  int64  `json:"traffic_out"`
	Uptime      int64  `json:"uptime"`
	Version     string `json:"version"`
}

// TrafficItem 单条规则的流量增量
type TrafficItem struct {
	ProxyName string `json:"proxy_name"`
	BytesIn   int64  `json:"bytes_in"`
	BytesOut  int64  `json:"bytes_out"`
	Conns     int64  `json:"conns"`
}

// TrafficReport 流量上报
type TrafficReport struct {
	Items []TrafficItem `json:"items"`
}

// ErrorMsg 错误
type ErrorMsg struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
