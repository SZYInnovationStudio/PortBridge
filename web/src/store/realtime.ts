import { defineStore } from 'pinia'
import { apiMe } from '@/api'
import { getToken } from '@/api/http'
import type { LogEntry, WsMessage } from '@/api/types'

type Handler = (data: unknown) => void

const MAX_LOGS = 500
// 子协议传递令牌，避免令牌出现在 URL 与访问日志中；需与后端约定保持一致
const WS_TOKEN_PREFIX = 'pb-token.'

function wsURL(): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${location.host}/api/v1/ws/events`
}

interface State {
  open: boolean
  logs: LogEntry[]
  handlers: Record<string, Handler[]>
  ws: WebSocket | null
  retry: number
  timer: number | null
  manualClose: boolean
}

export const useRealtimeStore = defineStore('realtime', {
  state: (): State => ({
    open: false,
    logs: [],
    handlers: {},
    ws: null,
    retry: 0,
    timer: null,
    manualClose: false,
  }),
  actions: {
    on(type: string, cb: Handler) {
      if (!this.handlers[type]) this.handlers[type] = []
      this.handlers[type].push(cb)
      return () => {
        const list = this.handlers[type] || []
        const i = list.indexOf(cb)
        if (i >= 0) list.splice(i, 1)
      }
    },
    emit(type: string, data: unknown) {
      ;(this.handlers[type] || []).forEach((cb) => {
        try {
          cb(data)
        } catch {
          /* 单个处理器异常不影响其他 */
        }
      })
    },
    clearLogs() {
      this.logs = []
    },
    connect() {
      const token = getToken()
      if (!token) return
      if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
        return
      }
      this.manualClose = false
      try {
        const ws = new WebSocket(wsURL(), [WS_TOKEN_PREFIX + token])
        this.ws = ws
        ws.onopen = () => {
          this.open = true
          this.retry = 0
        }
        ws.onmessage = (ev) => {
          try {
            const msg = JSON.parse(ev.data) as WsMessage
            if (msg.type === 'log') {
              const entry = msg.data as LogEntry
              this.logs.push(entry)
              if (this.logs.length > MAX_LOGS) this.logs.splice(0, this.logs.length - MAX_LOGS)
            }
            this.emit(msg.type, msg.data)
          } catch {
            /* 忽略无法解析的消息 */
          }
        }
        ws.onclose = () => {
          this.open = false
          this.ws = null
          if (!this.manualClose) this.scheduleReconnect()
        }
        ws.onerror = () => {
          try {
            ws.close()
          } catch {
            /* noop */
          }
        }
      } catch {
        this.scheduleReconnect()
      }
    },
    async scheduleReconnect() {
      if (this.timer) return
      this.retry = Math.min(this.retry + 1, 10)
      const delay = Math.min(1000 * this.retry, 10000)
      // 连续多次失败后校验会话，确认令牌仍有效才继续重连，避免失效令牌无限重连
      if (this.retry >= 5) {
        const valid = await this.checkSession()
        if (!valid) return
      }
      // 校验期间可能已被手动断开（如登出），此时不再安排重连
      if (this.manualClose) return
      this.timer = window.setTimeout(() => {
        this.timer = null
        this.connect()
      }, delay)
    },
    async checkSession(): Promise<boolean> {
      try {
        await apiMe()
        return true
      } catch {
        // 令牌已失效：停止重连（401 拦截器已清理本地凭证并跳转登录页）
        this.manualClose = true
        return false
      }
    },
    disconnect() {
      this.manualClose = true
      if (this.timer) {
        clearTimeout(this.timer)
        this.timer = null
      }
      if (this.ws) {
        try {
          this.ws.close()
        } catch {
          /* noop */
        }
        this.ws = null
      }
      this.open = false
    },
  },
})
