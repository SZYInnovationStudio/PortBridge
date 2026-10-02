import { defineStore } from 'pinia'
import { apiLogin, apiMe, apiSystemInfo } from '@/api'
import { clearAuth, getToken, setToken, TOKEN_KEY, USER_KEY } from '@/api/http'
import type { Role, SystemInfo, UserInfo } from '@/api/types'

interface State {
  token: string
  user: UserInfo | null
  systemInfo: SystemInfo | null
  ready: boolean
}

function loadUser(): UserInfo | null {
  try {
    const raw = localStorage.getItem(USER_KEY)
    return raw ? (JSON.parse(raw) as UserInfo) : null
  } catch {
    return null
  }
}

export const useAuthStore = defineStore('auth', {
  state: (): State => ({
    token: getToken(),
    user: loadUser(),
    systemInfo: null,
    ready: false,
  }),
  getters: {
    isLogged: (s) => !!s.token,
    role: (s): Role => s.systemInfo?.role || 'server',
    isServer: (s) => s.systemInfo?.role === 'server',
    isClient: (s) => s.systemInfo?.role === 'client',
    displayName: (s) => s.user?.nickname || s.user?.username || '未登录',
  },
  actions: {
    async login(username: string, password: string) {
      const res = await apiLogin(username, password)
      this.token = res.token
      this.user = res.user
      this.ready = true
      setToken(res.token)
      localStorage.setItem(USER_KEY, JSON.stringify(res.user))
      localStorage.setItem(TOKEN_KEY, res.token)
      await this.fetchSystemInfo()
    },
    async fetchMe() {
      this.user = await apiMe()
      localStorage.setItem(USER_KEY, JSON.stringify(this.user))
    },
    async fetchSystemInfo() {
      this.systemInfo = await apiSystemInfo()
      return this.systemInfo
    },
    async bootstrap() {
      if (!this.token) {
        this.ready = true
        return
      }
      try {
        if (!this.user) await this.fetchMe()
        await this.fetchSystemInfo()
        this.ready = true
      } catch {
        this.logout()
        this.ready = true
      }
    },
    logout() {
      this.token = ''
      this.user = null
      this.systemInfo = null
      clearAuth()
    },
  },
})
