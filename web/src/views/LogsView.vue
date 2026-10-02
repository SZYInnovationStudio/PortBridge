<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">实时日志</h2>
        <div class="page-sub">
          通过 WebSocket 实时推送运行日志 · {{ filtered.length }} 条
          <el-tag size="small" :type="rt.open ? 'success' : 'info'" effect="plain" style="margin-left: 8px">
            {{ rt.open ? '已连接' : '未连接' }}
          </el-tag>
        </div>
      </div>
      <div class="toolbar">
        <el-select v-model="level" size="small" style="width: 120px">
          <el-option value="" label="全部级别" />
          <el-option value="error" label="error" />
          <el-option value="warn" label="warn" />
          <el-option value="info" label="info" />
          <el-option value="debug" label="debug" />
        </el-select>
        <el-checkbox v-model="autoScroll" size="small">自动滚动</el-checkbox>
        <el-button size="small" :icon="Delete" @click="clear">清空</el-button>
        <el-button size="small" :icon="Refresh" @click="reload">重新加载</el-button>
      </div>
    </div>

    <div ref="boxRef" class="log-view">
      <div v-for="(l, i) in filtered" :key="i" class="log-line">
        <span class="log-time">{{ formatTime(l.time) }}</span>
        <span :class="'log-' + l.level">[{{ l.level.toUpperCase() }}]</span>
        <span> {{ l.msg }}</span>
      </div>
      <div v-if="!filtered.length" class="empty-tip">暂无日志</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { Delete, Refresh } from '@element-plus/icons-vue'
import { apiLogs } from '@/api'
import type { LogEntry } from '@/api/types'
import { useRealtimeStore } from '@/store/realtime'
import { formatTime } from '@/utils/format'

defineOptions({ name: 'LogsView' })

const rt = useRealtimeStore()
const level = ref('')
const autoScroll = ref(true)
const boxRef = ref<HTMLElement | null>(null)
const history = ref<LogEntry[]>([])

// 合并 HTTP 历史快照与 WS 实时日志
const merged = computed<LogEntry[]>(() => {
  const seen = new Set<string>()
  const out: LogEntry[] = []
  for (const e of history.value) {
    const k = `${e.time}|${e.level}|${e.msg}`
    if (!seen.has(k)) {
      seen.add(k)
      out.push(e)
    }
  }
  for (const e of rt.logs) {
    const k = `${e.time}|${e.level}|${e.msg}`
    if (!seen.has(k)) {
      seen.add(k)
      out.push(e)
    }
  }
  return out
})

const filtered = computed(() =>
  level.value ? merged.value.filter((l) => l.level === level.value) : merged.value,
)

async function reload() {
  history.value = await apiLogs(500)
}

function clear() {
  history.value = []
  rt.clearLogs()
}

function scrollToBottom() {
  if (!autoScroll.value) return
  nextTick(() => {
    const el = boxRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

watch(() => rt.logs.length, scrollToBottom)
watch(filtered, scrollToBottom)

onMounted(async () => {
  await reload()
  scrollToBottom()
})
</script>
