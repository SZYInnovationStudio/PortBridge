import { request } from './http'
import type {
  ExportPayload,
  ImportResult,
  LogEntry,
  LoginResult,
  NodeCreateResult,
  NodeForm,
  NodeItem,
  Overview,
  PortCheckResult,
  ProxyForm,
  ProxyItem,
  SettingsPayload,
  SystemInfo,
  TrafficPoint,
  UserInfo,
} from './types'

// ---------- 认证 ----------
export const apiLogin = (username: string, password: string) =>
  request<LoginResult>({ url: '/login', method: 'post', data: { username, password } })

export const apiSystemInfo = () => request<SystemInfo>({ url: '/system/info', method: 'get' })

export const apiMe = () => request<UserInfo>({ url: '/me', method: 'get' })

export const apiChangePassword = (old_password: string, new_password: string) =>
  request<null>({ url: '/me/password', method: 'put', data: { old_password, new_password } })

// ---------- 节点（仅中转端） ----------
export const apiListNodes = () => request<NodeItem[]>({ url: '/nodes', method: 'get' })

export const apiCreateNode = (data: NodeForm) =>
  request<NodeCreateResult>({ url: '/nodes', method: 'post', data })

export const apiUpdateNode = (id: number, data: NodeForm) =>
  request<NodeItem>({ url: `/nodes/${id}`, method: 'put', data })

export const apiDeleteNode = (id: number) => request<null>({ url: `/nodes/${id}`, method: 'delete' })

export const apiResetNodeToken = (id: number) =>
  request<{ token: string }>({ url: `/nodes/${id}/token`, method: 'post' })

export const apiKickNode = (id: number) =>
  request<null>({ url: `/nodes/${id}/kick`, method: 'post' })

// ---------- 规则 ----------
export const apiListProxies = (params?: { node?: string; type?: string }) =>
  request<ProxyItem[]>({ url: '/proxies', method: 'get', params })

export const apiCreateProxy = (data: ProxyForm) =>
  request<ProxyItem>({ url: '/proxies', method: 'post', data })

export const apiUpdateProxy = (id: number, data: ProxyForm) =>
  request<ProxyItem>({ url: `/proxies/${id}`, method: 'put', data })

export const apiDeleteProxy = (id: number) =>
  request<null>({ url: `/proxies/${id}`, method: 'delete' })

export const apiToggleProxy = (id: number) =>
  request<{ enabled: boolean }>({ url: `/proxies/${id}/toggle`, method: 'post' })

export const apiCheckPort = (port: number, type: string, addr = '0.0.0.0') =>
  request<PortCheckResult>({ url: '/ports/check', method: 'get', params: { port, type, addr } })

// ---------- 统计（概览仅中转端；traffic 仅中转端） ----------
export const apiOverview = () => request<Overview>({ url: '/stats/overview', method: 'get' })

export const apiTrafficSeries = (hours = 24) =>
  request<TrafficPoint[]>({ url: '/stats/traffic', method: 'get', params: { hours } })

// ---------- 日志 / 设置 / 配置 ----------
export const apiLogs = (limit = 200) =>
  request<LogEntry[]>({ url: '/logs', method: 'get', params: { limit } })

export const apiGetSettings = () => request<SettingsPayload>({ url: '/settings', method: 'get' })

export const apiUpdateSettings = (data: Record<string, string>) =>
  request<Record<string, string>>({ url: '/settings', method: 'put', data })

export const apiExportConfig = () => request<ExportPayload>({ url: '/config/export', method: 'get' })

export const apiImportConfig = (payload: ExportPayload) =>
  request<ImportResult>({ url: '/config/import', method: 'post', data: payload })
