<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">概览</h2>
        <div class="page-sub">
          {{ auth.isServer ? '中转端运行总览' : '原站端运行总览' }} · 更新于 {{ updatedAt }}
        </div>
      </div>
      <div class="toolbar">
        <el-select v-if="auth.isServer" v-model="hours" size="small" style="width: 120px" @change="loadTraffic">
          <el-option :value="6" label="近 6 小时" />
          <el-option :value="24" label="近 24 小时" />
          <el-option :value="72" label="近 3 天" />
          <el-option :value="168" label="近 7 天" />
        </el-select>
        <el-button size="small" :icon="Refresh" @click="refresh">刷新</el-button>
      </div>
    </div>

    <div class="stat-grid">
      <div class="stat-card">
        <div class="stat-label"><el-icon><Connection /></el-icon>{{ auth.isServer ? '在线节点' : '中转端连接' }}</div>
        <div class="stat-value" :style="{ color: primaryColor }">{{ primaryValue }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label"><el-icon><Share /></el-icon>{{ auth.isServer ? '启用规则' : '启用规则' }}</div>
        <div class="stat-value">{{ proxyEnabledText }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label"><el-icon><Link /></el-icon>当前连接数</div>
        <div class="stat-value">{{ currentConns }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label"><el-icon><DataAnalysis /></el-icon>累计连接数</div>
        <div class="stat-value">{{ totalConns }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label"><el-icon><Download /></el-icon>入站流量</div>
        <div class="stat-value">{{ formatBytes(bytesIn) }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label"><el-icon><Upload /></el-icon>出站流量</div>
        <div class="stat-value">{{ formatBytes(bytesOut) }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label"><el-icon><Timer /></el-icon>运行时长</div>
        <div class="stat-value">{{ formatDuration(uptime) }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label"><el-icon><Cpu /></el-icon>{{ auth.isServer ? 'Web 订阅数' : '连接延迟' }}</div>
        <div class="stat-value">{{ extraValue }}</div>
      </div>
    </div>

    <div v-if="auth.isServer" class="chart-wrap mb-16">
      <TrafficChart :points="traffic" :title="`流量趋势（近 ${hours} 小时）`" />
    </div>

    <el-row :gutter="16">
      <el-col :xs="24" :md="14">
        <el-card shadow="never" class="mb-16">
          <template #header>
            <div class="card-head">
              <span>规则运行状态</span>
              <el-button link type="primary" @click="$router.push('/proxies')">全部规则</el-button>
            </div>
          </template>
          <el-table :data="proxies" size="small" :max-height="320">
            <el-table-column prop="name" label="规则名" min-width="120" show-overflow-tooltip />
            <el-table-column prop="type" label="类型" width="70">
              <template #default="{ row }">
                <el-tag size="small" :type="row.type === 'udp' ? 'warning' : 'primary'" effect="plain">
                  {{ row.type.toUpperCase() }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="监听 → 目标" min-width="200" show-overflow-tooltip>
              <template #default="{ row }">
                <span class="mono">{{ row.remote_addr }}:{{ row.remote_port }}</span>
                <el-icon style="margin: 0 4px"><Right /></el-icon>
                <span class="mono">{{ row.local_ip }}:{{ row.local_port }}</span>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="90">
              <template #default="{ row }">
                <el-tag size="small" :type="statusType(row)">{{ statusText(row) }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="conn_count" label="连接" width="70" />
          </el-table>
          <div v-if="!proxies.length" class="empty-tip">暂无规则</div>
        </el-card>
      </el-col>
      <el-col :xs="24" :md="10">
        <el-card shadow="never" class="mb-16">
          <template #header>
            <div class="card-head">
              <span>最近日志</span>
              <el-button link type="primary" @click="$router.push('/logs')">查看全部</el-button>
            </div>
          </template>
          <div class="mini-log">
            <div v-for="(l, i) in recentLogs" :key="i" class="log-line">
              <span class="log-time">{{ shortTime(l.time) }}</span>
              <span :class="'log-' + l.level">{{ l.msg }}</span>
            </div>
            <div v-if="!recentLogs.length" class="empty-tip">暂无日志</div>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Refresh, Right } from '@element-plus/icons-vue'
import { apiListProxies, apiLogs, apiOverview, apiTrafficSeries } from '@/api'
import type { LogEntry, Overview, ProxyItem, TrafficPoint } from '@/api/types'
import TrafficChart from '@/components/TrafficChart.vue'
import { useAuthStore } from '@/store/auth'
import { useRealtimeStore } from '@/store/realtime'
import { formatBytes, formatDuration, formatTime } from '@/utils/format'

defineOptions({ name: 'OverviewView' })

const auth = useAuthStore()
const rt = useRealtimeStore()

const hours = ref(24)
const updatedAt = ref('-')
const overview = ref<Overview | null>(null)
const traffic = ref<TrafficPoint[]>([])
const proxies = ref<ProxyItem[]>([])
const recentLogs = ref<LogEntry[]>([])

const bytesIn = computed(() =>
  auth.isServer ? overview.value?.traffic_in || 0 : sum(proxies.value.map((p) => p.traffic_in)),
)
const bytesOut = computed(() =>
  auth.isServer ? overview.value?.traffic_out || 0 : sum(proxies.value.map((p) => p.traffic_out)),
)
const currentConns = computed(() =>
  auth.isServer ? overview.value?.current_conns || 0 : sum(proxies.value.map((p) => p.conn_count)),
)
const totalConns = computed(() =>
  auth.isServer ? overview.value?.total_conns || 0 : sum(proxies.value.map((p) => p.total_conns)),
)
const uptime = computed(() => auth.systemInfo?.uptime_sec || overview.value?.uptime_sec || 0)

const primaryColor = computed(() => {
  if (auth.isServer) return '#409eff'
  return auth.systemInfo?.connected ? '#67c23a' : '#f56c6c'
})
const primaryValue = computed(() => {
  if (auth.isServer) return `${overview.value?.node_online ?? 0} / ${overview.value?.node_total ?? 0}`
  return auth.systemInfo?.connected ? '已连接' : '未连接'
})
const proxyEnabledText = computed(() => {
  const total = auth.isServer ? overview.value?.proxy_total ?? proxies.value.length : proxies.value.length
  const enabled = auth.isServer
    ? overview.value?.proxy_enabled ?? 0
    : proxies.value.filter((p) => p.enabled).length
  return `${enabled} / ${total}`
})
const extraValue = computed(() =>
  auth.isServer ? String(overview.value?.web_clients ?? 0) : `${auth.systemInfo?.latency_ms ?? 0} ms`,
)

function sum(arr: number[]): number {
  return arr.reduce((a, b) => a + (b || 0), 0)
}

function statusText(row: ProxyItem): string {
  if (!row.enabled) return '已停用'
  if (row.run_status === 'running') return '运行中'
  return '已停止'
}

function statusType(row: ProxyItem): 'success' | 'info' | 'warning' {
  if (!row.enabled) return 'info'
  return row.run_status === 'running' ? 'success' : 'warning'
}

function shortTime(t: string): string {
  return formatTime(t).slice(11)
}

async function loadTraffic() {
  if (!auth.isServer) return
  try {
    traffic.value = await apiTrafficSeries(hours.value)
  } catch {
    traffic.value = []
  }
}

async function refresh() {
  try {
    const tasks: Promise<unknown>[] = [loadTraffic(), auth.fetchSystemInfo()]
    if (auth.isServer) {
      tasks.push(
        apiOverview().then((o) => {
          overview.value = o
        }),
      )
    }
    tasks.push(
      apiListProxies().then((list) => {
        proxies.value = list
      }),
    )
    tasks.push(
      apiLogs(15).then((l) => {
        recentLogs.value = l.slice(-15)
      }),
    )
    await Promise.all(tasks)
    updatedAt.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
  } catch {
    /* 提示由拦截器处理 */
  }
}

let timer = 0
const offs: Array<() => void> = []

onMounted(() => {
  refresh()
  timer = window.setInterval(refresh, 5000)
  offs.push(rt.on('proxy_change', () => refresh()))
  offs.push(rt.on('proxy_status', () => refresh()))
  offs.push(rt.on('node_status', () => refresh()))
  offs.push(rt.on('conn_status', () => refresh()))
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
  offs.forEach((f) => f())
})
</script>

<style scoped>
.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}

.mini-log {
  height: 320px;
  overflow-y: auto;
  font-family: 'JetBrains Mono', Consolas, Menlo, monospace;
  font-size: 12px;
  line-height: 1.7;
}
</style>
