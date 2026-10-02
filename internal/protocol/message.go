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

	// 连接记录相关
	MsgConnLogEvent  MsgType = "conn_log_event"  // 中转端 -> 原站端：镜像单条记录
	MsgConnLogReport MsgType = "conn_log_report" // 原站端 -> 中转端：上报 forward 记录
	MsgConnLogPull   MsgType = "conn_log_pull"   // 原站端 -> 中转端：拉取对账
	MsgConnLogSync   MsgType = "conn_log_sync"   // 中转端 -> 原站端：对账数据
	MsgConnLogQuery  MsgType = "conn_log_query"  // 原站端 -> 中转端：远程查询
	MsgConnLogResp   MsgType = "conn_log_resp"   // 中转端 -> 原站端：查询结果
	MsgConnLogClear  MsgType = "conn_log_clear"  // 清空指令
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
	Version      int64  `json:"version"` // 配置版本号：仅真实编辑时递增，LWW 以它为唯一依据
	RateLimitKB  int    `json:"rate_limit_kb"`
	TrafficLimit int64  `json:"traffic_limit"`
	UpdatedAt    int64  `json:"updated_at"` // UnixMilli，仅用于展示
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

// ConnLogItem 连接记录传输对象（时间戳为 UnixMilli）
type ConnLogItem struct {
	ID         uint   `json:"id"`
	SessionID  string `json:"session_id"`
	ProxyName  string `json:"proxy_name"`
	NodeName   string `json:"node_name"`
	Type       string `json:"type"`      // tcp | udp
	Direction  string `json:"direction"` // reverse | forward
	SourceIP   string `json:"source_ip"`
	SourcePort int    `json:"source_port"`
	Target     string `json:"target"`
	StartedAt  int64  `json:"started_at"`
	EndedAt    int64  `json:"ended_at"`
	DurationMs int64  `json:"duration_ms"`
	BytesIn    int64  `json:"bytes_in"`
	BytesOut   int64  `json:"bytes_out"`
	Status     string `json:"status"` // active | closed
}

// ConnLogEventPayload 镜像单条记录
type ConnLogEventPayload struct {
	Item ConnLogItem `json:"item"`
}

// ConnLogReportPayload 原站端上报 forward 记录
type ConnLogReportPayload struct {
	Items []ConnLogItem `json:"items"`
}

// ConnLogPullReq 拉取对账请求
type ConnLogPullReq struct {
	SinceID uint `json:"since_id"`
	Limit   int  `json:"limit"`
}

// ConnLogSyncPayload 对账数据
type ConnLogSyncPayload struct {
	Items   []ConnLogItem `json:"items"`
	LastID  uint          `json:"last_id"`
	HasMore bool          `json:"has_more"`
	MinID   uint          `json:"min_id"`
	Total   int64         `json:"total"`
	Mode    string        `json:"mode"` // 当前连接记录模式：mirror | remote
}

// ConnLogQueryReq 远程查询请求
type ConnLogQueryReq struct {
	Keyword   string `json:"keyword"`
	ProxyName string `json:"proxy_name"`
	Status    string `json:"status"`
	Page      int    `json:"page"`
	PageSize  int    `json:"page_size"`
}

// ConnLogRespPayload 查询结果
type ConnLogRespPayload struct {
	OK    bool          `json:"ok"`
	Msg   string        `json:"msg,omitempty"`
	Items []ConnLogItem `json:"items"`
	Total int64         `json:"total"`
	Mode  string        `json:"mode"` // 当前连接记录模式：mirror | remote
}

// ConnLogClearPayload 清空指令
type ConnLogClearPayload struct {
	From string `json:"from"` // server | client
}
