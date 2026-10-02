<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">规则管理</h2>
        <div class="page-sub">
          端口映射与四层转发规则，共 {{ proxies.length }} 条（启用 {{ enabledCount }} 条）
        </div>
      </div>
      <div class="toolbar">
        <el-select
          v-if="auth.isServer"
          v-model="filterNode"
          size="small"
          clearable
          placeholder="全部节点"
          style="width: 150px"
          @change="load"
        >
          <el-option v-for="n in nodes" :key="n.id" :value="n.name" :label="n.name" />
        </el-select>
        <el-select v-model="filterType" size="small" clearable placeholder="全部协议" style="width: 120px" @change="load">
          <el-option value="tcp" label="TCP" />
          <el-option value="udp" label="UDP" />
        </el-select>
        <el-button size="small" :icon="Refresh" @click="load">刷新</el-button>
        <el-button size="small" type="primary" :icon="Plus" @click="openCreate">新增规则</el-button>
      </div>
    </div>

    <el-card shadow="never">
      <el-table v-loading="loading" :data="proxies" size="small">
        <el-table-column label="规则名" min-width="130">
          <template #default="{ row }">
            <div>{{ row.name }}</div>
            <div class="muted sub">{{ row.remark || '-' }}</div>
          </template>
        </el-table-column>
        <el-table-column v-if="auth.isServer" prop="node_name" label="节点" width="120" show-overflow-tooltip />
        <el-table-column label="协议" width="80">
          <template #default="{ row }">
            <el-tag size="small" :type="row.type === 'udp' ? 'warning' : 'primary'" effect="plain">
              {{ row.type.toUpperCase() }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="方向" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.direction === 'forward' ? 'info' : 'success'" effect="plain">
              {{ row.direction === 'forward' ? '正向' : '反向' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="监听 → 目标" min-width="230">
          <template #default="{ row }">
            <span class="mono">{{ row.remote_addr }}:{{ row.remote_port }}</span>
            <el-icon class="arrow"><Right /></el-icon>
            <span class="mono">{{ row.local_ip || '-' }}:{{ row.local_port }}</span>
          </template>
        </el-table-column>
        <el-table-column label="流量" width="150">
          <template #default="{ row }">
            <div class="muted">入 {{ formatBytes(row.traffic_in) }}</div>
            <div class="muted">出 {{ formatBytes(row.traffic_out) }}</div>
          </template>
        </el-table-column>
        <el-table-column label="连接" width="90">
          <template #default="{ row }">
            <div>{{ row.conn_count }} / {{ row.total_conns }}</div>
            <div class="muted">当前/累计</div>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="statusType(row)">{{ statusText(row) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="启用" width="70">
          <template #default="{ row }">
            <el-switch v-model="row.enabled" size="small" @change="toggle(row)" />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="120" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" size="small" @click="openEdit(row)">编辑</el-button>
            <el-button link type="danger" size="small" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div v-if="!loading && !proxies.length" class="empty-tip">暂无规则，请点击「新增规则」创建</div>
    </el-card>

    <el-dialog v-model="formVisible" :title="editing ? '编辑规则' : '新增规则'" width="620px" top="6vh">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="110px">
        <el-form-item v-if="auth.isServer" label="所属节点" prop="node_name">
          <el-select v-model="form.node_name" placeholder="选择原站端节点" style="width: 100%">
            <el-option v-for="n in nodes" :key="n.id" :value="n.name" :label="`${n.name}${n.online ? ' (在线)' : ' (离线)'}`" />
          </el-select>
        </el-form-item>

        <el-form-item label="规则名" prop="name">
          <el-input v-model="form.name" placeholder="全局唯一，例如 web-nginx" />
        </el-form-item>

        <el-form-item label="协议类型">
          <el-radio-group v-model="form.type">
            <el-radio-button value="tcp">TCP</el-radio-button>
            <el-radio-button value="udp">UDP</el-radio-button>
          </el-radio-group>
        </el-form-item>

        <el-form-item label="转发方向">
          <el-radio-group v-model="form.direction">
            <el-radio-button value="reverse">反向（中转端监听）</el-radio-button>
            <el-radio-button value="forward">正向（原站端监听）</el-radio-button>
          </el-radio-group>
        </el-form-item>

        <el-divider content-position="left">{{ listenLabel }}</el-divider>
        <el-form-item label="监听地址">
          <el-input v-model="form.remote_addr" placeholder="0.0.0.0" style="width: 100%" />
        </el-form-item>
        <el-form-item label="监听端口" prop="remote_port">
          <div class="port-row">
            <el-input-number v-model="form.remote_port" :min="1" :max="65535" controls-position="right" style="width: 180px" />
            <el-button size="small" :loading="checking" @click="checkPort">检查端口占用</el-button>
            <span v-if="checkResult" class="check-result" :class="checkResult.available ? 'ok' : 'bad'">
              {{ checkResult.available ? '端口可用' : '端口被占用' }}
            </span>
          </div>
        </el-form-item>

        <el-divider content-position="left">{{ targetLabel }}</el-divider>
        <el-form-item label="目标地址">
          <el-input v-model="form.local_ip" placeholder="127.0.0.1 或内网 IP" style="width: 100%" />
        </el-form-item>
        <el-form-item label="目标端口" prop="local_port">
          <el-input-number v-model="form.local_port" :min="1" :max="65535" controls-position="right" style="width: 180px" />
        </el-form-item>

        <el-divider content-position="left">高级</el-divider>
        <el-form-item label="限速">
          <el-input-number v-model="form.rate_limit_kb" :min="0" :max="1048576" controls-position="right" style="width: 180px" />
          <span class="muted unit">KB/s（0 = 不限速）</span>
        </el-form-item>
        <el-form-item label="月流量上限">
          <el-input-number v-model="form.traffic_limit_mb" :min="0" :max="4194304" controls-position="right" style="width: 180px" />
          <span class="muted unit">MB（0 = 不限，超限自动停用）</span>
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="form.remark" placeholder="可选" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submit">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { Plus, Refresh, Right } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import {
  apiCheckPort,
  apiCreateProxy,
  apiDeleteProxy,
  apiListNodes,
  apiListProxies,
  apiToggleProxy,
  apiUpdateProxy,
} from '@/api'
import type { NodeItem, PortCheckResult, ProxyForm, ProxyItem } from '@/api/types'
import { useAuthStore } from '@/store/auth'
import { useRealtimeStore } from '@/store/realtime'
import { formatBytes } from '@/utils/format'

defineOptions({ name: 'ProxiesView' })

const auth = useAuthStore()
const rt = useRealtimeStore()

interface FormState extends ProxyForm {
  traffic_limit_mb: number
}

const proxies = ref<ProxyItem[]>([])
const nodes = ref<NodeItem[]>([])
const loading = ref(false)
const saving = ref(false)
const checking = ref(false)
const checkResult = ref<PortCheckResult | null>(null)

const filterNode = ref('')
const filterType = ref('')

const formVisible = ref(false)
const editing = ref(false)
const formRef = ref<FormInstance>()
const form = reactive<FormState>({
  node_name: '',
  name: '',
  type: 'tcp',
  direction: 'reverse',
  remote_addr: '0.0.0.0',
  remote_port: 6000,
  local_ip: '127.0.0.1',
  local_port: 80,
  enabled: true,
  rate_limit_kb: 0,
  traffic_limit: 0,
  traffic_limit_mb: 0,
  remark: '',
})

let editId = 0

const enabledCount = computed(() => proxies.value.filter((p) => p.enabled).length)
const listenLabel = computed(() =>
  form.direction === 'forward' ? '监听（原站端本地）' : '监听（中转端公网）',
)
const targetLabel = computed(() =>
  form.direction === 'forward' ? '目标（中转端侧）' : '目标（原站端本地）',
)

const rules: FormRules = {
  name: [
    { required: true, message: '请输入规则名', trigger: 'blur' },
    { pattern: /^[a-zA-Z0-9_-]{1,64}$/, message: '仅支持字母、数字、下划线与短横线', trigger: 'blur' },
  ],
  remote_port: [{ required: true, message: '请输入监听端口', trigger: 'blur' }],
  local_port: [{ required: true, message: '请输入目标端口', trigger: 'blur' }],
}

function statusText(row: ProxyItem): string {
  if (!row.enabled) return '已停用'
  return row.run_status === 'running' ? '运行中' : '已停止'
}

function statusType(row: ProxyItem): 'success' | 'info' | 'warning' {
  if (!row.enabled) return 'info'
  return row.run_status === 'running' ? 'success' : 'warning'
}

async function load() {
  loading.value = true
  try {
    const params: { node?: string; type?: string } = {}
    if (filterNode.value) params.node = filterNode.value
    if (filterType.value) params.type = filterType.value
    proxies.value = await apiListProxies(params)
  } finally {
    loading.value = false
  }
}

async function loadNodes() {
  if (!auth.isServer) return
  nodes.value = await apiListNodes()
}

function openCreate() {
  editing.value = false
  editId = 0
  checkResult.value = null
  Object.assign(form, {
    node_name: nodes.value.length ? nodes.value[0].name : '',
    name: '',
    type: 'tcp',
    direction: 'reverse',
    remote_addr: '0.0.0.0',
    remote_port: 6000,
    local_ip: '127.0.0.1',
    local_port: 80,
    enabled: true,
    rate_limit_kb: 0,
    traffic_limit: 0,
    traffic_limit_mb: 0,
    remark: '',
  })
  formVisible.value = true
}

function openEdit(row: ProxyItem) {
  editing.value = true
  editId = row.id
  checkResult.value = null
  Object.assign(form, {
    node_name: row.node_name,
    name: row.name,
    type: row.type,
    direction: row.direction,
    remote_addr: row.remote_addr || '0.0.0.0',
    remote_port: row.remote_port,
    local_ip: row.local_ip,
    local_port: row.local_port,
    enabled: row.enabled,
    rate_limit_kb: row.rate_limit_kb,
    traffic_limit: row.traffic_limit,
    traffic_limit_mb: row.traffic_limit ? Math.round(row.traffic_limit / 1024 / 1024) : 0,
    remark: row.remark,
  })
  formVisible.value = true
}

async function checkPort() {
  if (!form.remote_port) {
    ElMessage.warning('请先填写监听端口')
    return
  }
  checking.value = true
  try {
    checkResult.value = await apiCheckPort(form.remote_port, form.type, form.remote_addr || '0.0.0.0')
  } catch {
    checkResult.value = null
  } finally {
    checking.value = false
  }
}

async function submit() {
  if (!formRef.value) return
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return
  saving.value = true
  try {
    const payload: ProxyForm = {
      node_name: form.node_name,
      name: form.name,
      type: form.type,
      direction: form.direction,
      remote_addr: form.remote_addr || '0.0.0.0',
      remote_port: form.remote_port,
      local_ip: form.local_ip,
      local_port: form.local_port,
      enabled: form.enabled,
      rate_limit_kb: form.rate_limit_kb,
      traffic_limit: Math.round((form.traffic_limit_mb || 0) * 1024 * 1024),
      remark: form.remark,
    }
    if (editing.value) {
      await apiUpdateProxy(editId, payload)
      ElMessage.success('规则已更新')
    } else {
      await apiCreateProxy(payload)
      ElMessage.success('规则已创建')
    }
    formVisible.value = false
    await load()
  } finally {
    saving.value = false
  }
}

async function toggle(row: ProxyItem) {
  try {
    const res = await apiToggleProxy(row.id)
    row.enabled = res.enabled
    ElMessage.success(res.enabled ? '规则已启用' : '规则已停用')
  } catch {
    row.enabled = !row.enabled
  }
}

async function remove(row: ProxyItem) {
  const ok = await ElMessageBox.confirm(`确定删除规则「${row.name}」吗？该操作不可恢复。`, '删除规则', {
    type: 'warning',
    confirmButtonText: '删除',
    confirmButtonClass: 'el-button--danger',
  })
    .then(() => true)
    .catch(() => false)
  if (!ok) return
  await apiDeleteProxy(row.id)
  ElMessage.success('规则已删除')
  await load()
}

let timer = 0
const offs: Array<() => void> = []
onMounted(async () => {
  await loadNodes()
  await load()
  timer = window.setInterval(load, 10000)
  offs.push(rt.on('proxy_change', () => load()))
  offs.push(rt.on('proxy_status', () => load()))
  offs.push(rt.on('node_status', () => loadNodes()))
})
onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
  offs.forEach((f) => f())
})
</script>

<style scoped>
.sub {
  font-size: 12px;
  margin-top: 2px;
}

.arrow {
  margin: 0 6px;
  color: #909399;
  vertical-align: -2px;
}

.port-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.check-result {
  font-size: 12px;
}

.check-result.ok {
  color: #67c23a;
}

.check-result.bad {
  color: #f56c6c;
}

.unit {
  margin-left: 10px;
  font-size: 12px;
}
</style>
