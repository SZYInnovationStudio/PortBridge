<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">系统设置</h2>
        <div class="page-sub">查看运行配置、维护可持久化设置，并导入导出规则配置</div>
      </div>
      <div class="toolbar">
        <el-button size="small" :icon="Download" @click="exportConfig">导出配置</el-button>
        <el-button size="small" :icon="Upload" @click="triggerImport">导入配置</el-button>
        <input ref="fileRef" type="file" accept="application/json" style="display: none" @change="onFile" />
      </div>
    </div>

    <el-row :gutter="16">
      <el-col :xs="24" :md="12">
        <el-card shadow="never" class="mb-16">
          <template #header><span class="card-title">基本信息</span></template>
          <el-descriptions :column="1" border size="small">
            <el-descriptions-item label="运行角色">
              <el-tag size="small" :type="auth.isServer ? 'success' : 'warning'" effect="plain">
                {{ auth.isServer ? '中转端 Server' : '原站端 Client' }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="节点名称">{{ auth.systemInfo?.node_name || '-' }}</el-descriptions-item>
            <el-descriptions-item label="程序版本">v{{ auth.systemInfo?.version || '-' }}</el-descriptions-item>
            <el-descriptions-item label="构建时间">{{ auth.systemInfo?.build_time || '-' }}</el-descriptions-item>
            <el-descriptions-item label="运行时长">{{ formatDuration(auth.systemInfo?.uptime_sec) }}</el-descriptions-item>
            <el-descriptions-item v-if="auth.isClient" label="中转端地址">
              <span class="mono">{{ auth.systemInfo?.server_addr || '-' }}</span>
            </el-descriptions-item>
            <el-descriptions-item v-if="auth.isClient" label="连接状态">
              <el-tag size="small" :type="auth.systemInfo?.connected ? 'success' : 'danger'" effect="plain">
                {{ auth.systemInfo?.connected ? `已连接 · ${auth.systemInfo?.latency_ms ?? 0} ms` : '未连接' }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item v-if="auth.isClient && auth.systemInfo?.last_error" label="最近错误">
              <span class="err">{{ auth.systemInfo?.last_error }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="服务器时间">{{ formatTime(auth.systemInfo?.server_time) }}</el-descriptions-item>
          </el-descriptions>
        </el-card>
      </el-col>

      <el-col :xs="24" :md="12">
        <el-card shadow="never" class="mb-16">
          <template #header><span class="card-title">服务端配置（只读）</span></template>
          <el-descriptions :column="1" border size="small">
            <el-descriptions-item label="管理端口">{{ settings?.admin_port ?? '-' }}</el-descriptions-item>
            <el-descriptions-item v-if="settings?.data_port" label="数据端口">{{ settings.data_port }}</el-descriptions-item>
            <el-descriptions-item label="心跳间隔">{{ settings?.heartbeat_sec ?? '-' }} 秒</el-descriptions-item>
            <el-descriptions-item v-if="settings?.proxy_port_min" label="端口范围">
              {{ settings?.proxy_port_min }} - {{ settings?.proxy_port_max }}
            </el-descriptions-item>
            <el-descriptions-item label="TLS">{{ settings?.tls_enabled ? '已启用' : '未启用' }}</el-descriptions-item>
            <el-descriptions-item label="日志级别">{{ settings?.log_level || '-' }}</el-descriptions-item>
            <el-descriptions-item label="前端目录">
              <span class="mono">{{ settings?.web_dist_path || '-' }}</span>
            </el-descriptions-item>
          </el-descriptions>
          <div class="muted mt-8">
            以上配置项来源于服务端配置文件 / 环境变量，修改后需重启服务生效。
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-card shadow="never">
      <template #header>
        <div class="card-head">
          <span class="card-title">高级设置（可持久化）</span>
          <div>
            <el-button size="small" :icon="Plus" @click="addRow">添加</el-button>
            <el-button size="small" type="primary" :loading="saving" @click="save">保存</el-button>
          </div>
        </div>
      </template>
      <el-table :data="extraRows" size="small">
        <el-table-column label="键" min-width="200">
          <template #default="{ row }">
            <el-input v-model="row.key" size="small" placeholder="配置键" />
          </template>
        </el-table-column>
        <el-table-column label="值" min-width="280">
          <template #default="{ row }">
            <el-input v-model="row.value" size="small" placeholder="配置值" />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="80">
          <template #default="{ $index }">
            <el-button link type="danger" size="small" @click="removeRow($index)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div v-if="!extraRows.length" class="empty-tip">暂无可持久化设置项</div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Download, Plus, Upload } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiExportConfig, apiGetSettings, apiImportConfig, apiUpdateSettings } from '@/api'
import type { ExportPayload, SettingsPayload } from '@/api/types'
import { useAuthStore } from '@/store/auth'
import { formatDuration, formatTime } from '@/utils/format'

defineOptions({ name: 'SettingsView' })

const auth = useAuthStore()
const settings = ref<SettingsPayload | null>(null)
const extraRows = ref<Array<{ key: string; value: string }>>([])
const saving = ref(false)
const fileRef = ref<HTMLInputElement | null>(null)

async function load() {
  settings.value = await apiGetSettings()
  extraRows.value = Object.entries(settings.value.extra || {}).map(([key, value]) => ({ key, value }))
}

function addRow() {
  extraRows.value.push({ key: '', value: '' })
}

function removeRow(index: number) {
  extraRows.value.splice(index, 1)
}

async function save() {
  const payload: Record<string, string> = {}
  for (const row of extraRows.value) {
    const k = row.key.trim()
    if (!k) continue
    payload[k] = row.value
  }
  saving.value = true
  try {
    await apiUpdateSettings(payload)
    ElMessage.success('设置已保存')
    await load()
  } finally {
    saving.value = false
  }
}

async function exportConfig() {
  const data = await apiExportConfig()
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `portbridge-config-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.json`
  a.click()
  URL.revokeObjectURL(url)
  ElMessage.success('配置已导出')
}

function triggerImport() {
  fileRef.value?.click()
}

async function onFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  try {
    const text = await file.text()
    const payload = JSON.parse(text) as ExportPayload
    await ElMessageBox.confirm(
      `将导入 ${payload.proxies?.length || 0} 条规则与 ${Object.keys(payload.settings || {}).length} 项设置，同名规则将被覆盖。确定继续？`,
      '导入配置',
      { type: 'warning' },
    ).catch(() => 'cancel')
    const res = await apiImportConfig(payload)
    ElMessage.success(`导入完成：新增 ${res.created} 条，更新 ${res.updated} 条`)
    await load()
  } catch {
    ElMessage.error('导入失败，请检查文件格式')
  } finally {
    input.value = ''
  }
}

onMounted(load)
</script>

<style scoped>
.card-title {
  font-weight: 600;
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.err {
  color: #f56c6c;
  word-break: break-all;
}
</style>
