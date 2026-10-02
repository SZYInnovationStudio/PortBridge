// PortBridge 管理平台 API 类型定义，与后端 model / handler 契约保持一致

export type Role = 'server' | 'client'
export type ProxyType = 'tcp' | 'udp'
export type ProxyDirection = 'reverse' | 'forward'

/** 统一响应包装 */
export interface ApiResp<T> {
  code: number
  message: string
  data: T
}

export interface UserInfo {
  id: number
  username: string
  nickname: string
  role: string
  last_login_at?: string | null
  last_login_ip?: string
  created_at?: string
  updated_at?: string
}

export interface LoginResult {
  token: string
  user: UserInfo
}

export interface SystemInfo {
  version: string
  build_time: string
  role: Role
  node_name: string
  uptime_sec: number
  server_time: string
  initialized: boolean
  // server 侧
  node_count?: number
  proxy_count?: number
  // client 侧
  connected?: boolean
  latency_ms?: number
  last_error?: string
  server_addr?: string
}

export interface NodeItem {
  id: number
  name: string
  role: string
  status: string
  last_seen?: string | null
  latency_ms: number
  version: string
  os: string
  arch: string
  remote_ip: string
  ip_whitelist: string
  proxy_count: number
  remark: string
  online: boolean
  created_at: string
  updated_at: string
}

export interface NodeForm {
  name: string
  remark: string
  role: string
  ip_whitelist: string
}

export interface NodeCreateResult {
  node: NodeItem
  token: string
}

export interface ProxyItem {
  id: number
  node_id: number
  node_name: string
  name: string
  type: ProxyType
  direction: ProxyDirection
  remote_addr: string
  remote_port: number
  local_ip: string
  local_port: number
  enabled: boolean
  origin: string
  rate_limit_kb: number
  traffic_limit: number
  traffic_in: number
  traffic_out: number
  total_conns: number
  remark: string
  created_at: string
  updated_at: string
  conn_count: number
  run_status: string
  last_error: string
}

export interface ProxyForm {
  node_name?: string
  name: string
  type: ProxyType
  direction: ProxyDirection
  remote_addr: string
  remote_port: number
  local_ip: string
  local_port: number
  enabled: boolean
  rate_limit_kb: number
  traffic_limit: number
  remark: string
}

export interface Overview {
  node_total: number
  node_online: number
  proxy_total: number
  proxy_enabled: number
  traffic_in: number
  traffic_out: number
  total_conns: number
  current_conns: number
  uptime_sec: number
  web_clients: number
}

export interface TrafficPoint {
  time: string
  bytes_in: number
  bytes_out: number
  conns: number
}

export interface LogEntry {
  time: string
  level: string
  msg: string
}

export type ConnLogMode = 'mirror' | 'remote'
export type ConnLogBackend = 'server' | 'local'

/** 连接记录（时间戳为 Unix 毫秒） */
export interface ConnLogItem {
  id: number
  session_id: string
  proxy_name: string
  node_name: string
  type: ProxyType
  direction: ProxyDirection
  source_ip: string
  source_port: number
  target: string
  started_at: number
  ended_at: number
  duration_ms: number
  bytes_in: number
  bytes_out: number
  status: 'active' | 'closed'
}

export interface ConnLogListResult {
  items: ConnLogItem[]
  total: number
  page: number
  page_size: number
  mode: ConnLogMode
  backend: ConnLogBackend
}

/** 连接记录排除 IP 列表（由中转端统一维护） */
export interface ConnLogExcludedResult {
  ips: string[]
}

export interface PortCheckResult {
  port: number
  type: string
  addr: string
  available: boolean
  message: string
}

export interface SettingsPayload {
  role: Role
  node_name: string
  admin_port: number
  heartbeat_sec: number
  tls_enabled: boolean
  web_dist_path: string
  log_level: string
  proxy_port_min?: number
  proxy_port_max?: number
  data_port?: number
  server_addr?: string
  server_data_addr?: string
  connected?: boolean
  latency_ms?: number
  last_error?: string
  extra: Record<string, string>
}

export interface ExportPayload {
  version: string
  exported_at: string
  settings: Record<string, string>
  proxies: ProxyItem[]
}

export interface ImportResult {
  created: number
  updated: number
}

/** WebSocket 推送消息 */
export interface WsMessage {
  type: string
  data: unknown
  ts?: number
}
