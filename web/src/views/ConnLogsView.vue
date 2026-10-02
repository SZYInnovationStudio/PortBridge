<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">连接记录</h2>
        <div class="page-sub">
          真实访客 IP:端口 连接明细（UTC+8）· 共 {{ total }} 条
          <el-tag size="small" effect="plain" style="margin-left: 8px">
            模式：{{ modeText }}
          </el-tag>
          <el-tag
            size="small"
            :type="backend === 'server' ? 'success' : 'info'"
            effect="plain"
            style="margin-left: 6px"
          >
            {{ backend === 'server' ? '数据源：中转端' : '数据源：本地镜像' }}
          </el-tag>
        </div>
      </div>
      <div class="toolbar">
        <el-input
          v-model="keyword"
          size="small"
          clearable
          placeholder="IP / 规则 / 节点 / 目标"
          style="width: 190px"
          @keyup.enter="reload"
          @clear="reload"
        />
        <el-select v-model="status" size="small" clearable placeholder="全部状态" style="width: 120px" @change="reload">
          <el-option value="active" label="进行中" />
          <el-option value="closed" label="已结束" />
        </el-select>
        <el-select
          v-if="auth.isServer"
          v-model="mode"
          size="small"
          style="width: 130px"
          @change="changeMode"
        >
          <el-option value="mirror" label="镜像模式" />
          <el-option value="remote" label="远程模式" />
        </el-select>
        <el-button size="small" :icon="Refresh" @click="reload">刷新</el-button>
        <el-button size="small" type="danger" :icon="Delete" @click="clearAll">清空记录</el-button>
      </div>
    </div>

    <el-card shadow="never">
      <el-table v-loading="loading" :data="items" size="small">
        <el-table-column label="来源 IP:端口" min-width="180">
          <template #default="{ row }">
            <span class="mono">{{ row.source_ip }}:{{ row.source_port }}</span>
          </template>
        </el-table-column>
        <el-table-column label="规则 / 节点" min-width="150">
          <template #default="{ row }">
            <div>{{ row.proxy_name }}</div>
            <div class="muted sub">{{ row.node_name || '-' }}</div>
          </template>
        </el-table-column>
        <el-table-column label="协议" width="80">
          <template #default="{ row }">
            <el-tag size="small" :type="row.type === 'udp' ? 'warning' : 'primary'" effect="plain">
              {{ row.type.toUpperCase() }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="方向" width="80">
          <template #default="{ row }">
            <el-tag size="small" :type="row.direction === 'forward' ? 'info' : 'success'" effect="plain">
              {{ row.direction === 'forward' ? '正向' : '反向' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="目标" min-width="170">
          <template #default="{ row }">
            <span class="mono">{{ row.target || '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="开始时间 (UTC+8)" width="170">
          <template #default="{ row }">
            <span class="mono">{{ formatUTC8(row.started_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="时长" width="100">
          <template #default="{ row }">{{ formatDuration(row.duration_ms / 1000) }}</template>
        </el-table-column>
        <el-table-column label="上行 / 下行" width="170">
          <template #default="{ row }">
            <div class="muted">↑ {{ formatBytes(row.bytes_in) }}</div>
            <div class="muted">↓ {{ formatBytes(row.bytes_out) }}</div>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.status === 'active' ? 'warning' : 'success'">
              {{ row.status === 'active' ? '进行中' : '已结束' }}
            </el-tag>
          </template>
        </el-table-column>
      </el-table>

      <div class="pager">
        <el-pagination
          small
          background
          layout="total, sizes, prev, pager, next"
          :total="total"
          :current-page="page"
          :page-size="pageSize"
          :page-sizes="[20, 50, 100]"
          @current-change="onPage"
          @size-change="onSize"
        />
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onActivated, onBeforeUnmount, onMounted, ref } from 'vue'
import { Delete, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClearConnLogs, apiListConnLogs, apiSetConnLogMode } from '@/api'
import type { ConnLogItem, ConnLogMode } from '@/api/types'
import { useAuthStore } from '@/store/auth'
import { useRealtimeStore } from '@/store/realtime'
import { formatBytes, formatDuration } from '@/utils/format'

defineOptions({ name: 'ConnLogsView' })

const auth = useAuthStore()
const rt = useRealtimeStore()

const items = ref<ConnLogItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const keyword = ref('')
const status = ref('')
const mode = ref<ConnLogMode>('mirror')
const backend = ref<'server' | 'local'>('server')
const loading = ref(false)

let offChange: (() => void) | null = null

const modeText = computed(() => (mode.value === 'remote' ? '远程模式' : '镜像模式'))

/** 以固定 UTC+8 展示时间，避免依赖浏览器本地时区 */
function formatUTC8(ms?: number): string {
  if (!ms) return '-'
  const d = new Date(ms + 8 * 3600 * 1000)
  const p = (v: number) => String(v).padStart(2, '0')
  return `${d.getUTCFullYear()}-${p(d.getUTCMonth() + 1)}-${p(d.getUTCDate())} ${p(d.getUTCHours())}:${p(d.getUTCMinutes())}:${p(d.getUTCSeconds())}`
}

async function load() {
  loading.value = true
  try {
    const res = await apiListConnLogs({
      page: page.value,
      page_size: pageSize.value,
      keyword: keyword.value,
      status: status.value,
    })
    items.value = res.items || []
    total.value = res.total || 0
    mode.value = res.mode
    backend.value = res.backend
  } finally {
    loading.value = false
  }
}

function reload() {
  page.value = 1
  load()
}

function onPage(p: number) {
  page.value = p
  load()
}

function onSize(s: number) {
  pageSize.value = s
  page.value = 1
  load()
}

async function changeMode(v: ConnLogMode) {
  try {
    await apiSetConnLogMode(v)
    ElMessage.success(v === 'remote' ? '已切换到远程模式（仅中转端存储）' : '已切换到镜像模式（下发到原站端）')
    reload()
  } catch {
    load()
  }
}

async function clearAll() {
  const okd = await ElMessageBox.confirm(
    '将清空全部连接记录，且两端一并清除，确定继续吗？',
    '提示',
    { type: 'warning' },
  )
    .then(() => true)
    .catch(() => false)
  if (!okd) return
  await apiClearConnLogs()
  ElMessage.success('已清空连接记录')
  reload()
}

onMounted(() => {
  load()
  // 连接记录变化时自动刷新（mirror 模式下发 / remote 变更通知）
  offChange = rt.on('conn_log_change', () => {
    if (page.value === 1) load()
  })
})

onActivated(() => {
  load()
})

onBeforeUnmount(() => {
  if (offChange) offChange()
})
</script>

<style scoped>
.sub {
  font-size: 12px;
}

.pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
</style>
