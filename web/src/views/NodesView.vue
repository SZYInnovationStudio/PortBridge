<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">节点管理</h2>
        <div class="page-sub">原站端节点注册、密钥与在线状态，共 {{ nodes.length }} 个节点</div>
      </div>
      <div class="toolbar">
        <el-button size="small" :icon="Refresh" @click="load">刷新</el-button>
        <el-button size="small" type="primary" :icon="Plus" @click="openCreate">新增节点</el-button>
      </div>
    </div>

    <el-card shadow="never">
      <el-table v-loading="loading" :data="nodes" size="small">
        <el-table-column label="节点名" min-width="140">
          <template #default="{ row }">
            <div class="node-cell">
              <span class="dot" :class="row.online ? 'on' : 'off'" />
              <span class="mono">{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.online ? 'success' : 'info'" effect="plain">
              {{ row.online ? '在线' : '离线' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="延迟" width="90">
          <template #default="{ row }">{{ row.online ? row.latency_ms + ' ms' : '-' }}</template>
        </el-table-column>
        <el-table-column prop="remote_ip" label="远端 IP" width="140">
          <template #default="{ row }"><span class="mono">{{ row.remote_ip || '-' }}</span></template>
        </el-table-column>
        <el-table-column label="环境" min-width="150">
          <template #default="{ row }">
            <span class="muted">
              {{ row.os && row.arch ? `${row.os}/${row.arch}` : '-' }}
              <template v-if="row.version"> · v{{ row.version }}</template>
            </span>
          </template>
        </el-table-column>
        <el-table-column prop="proxy_count" label="规则数" width="80" />
        <el-table-column label="最后在线" width="160">
          <template #default="{ row }">{{ row.online ? '在线中' : formatRelative(row.last_seen) }}</template>
        </el-table-column>
        <el-table-column prop="remark" label="备注" min-width="120" show-overflow-tooltip />
        <el-table-column label="操作" width="250" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" size="small" @click="openEdit(row)">编辑</el-button>
            <el-button link type="warning" size="small" @click="resetToken(row)">重置密钥</el-button>
            <el-button link type="info" size="small" :disabled="!row.online" @click="kick(row)">下线</el-button>
            <el-button link type="danger" size="small" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div v-if="!loading && !nodes.length" class="empty-tip">暂无节点，请点击「新增节点」创建</div>
    </el-card>

    <!-- 新增 / 编辑 -->
    <el-dialog v-model="formVisible" :title="editing ? '编辑节点' : '新增节点'" width="480px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="90px">
        <el-form-item label="节点名" prop="name">
          <el-input v-model="form.name" :disabled="editing" placeholder="例如 nb-unicom" />
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="form.role" style="width: 100%">
            <el-option value="client" label="原站端 Client" />
          </el-select>
        </el-form-item>
        <el-form-item label="IP 白名单">
          <el-input
            v-model="form.ip_whitelist"
            type="textarea"
            :rows="2"
            placeholder="允许连接数据面的来源 CIDR，逗号分隔，留空表示不限制"
          />
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

    <!-- 一次性密钥展示 -->
    <el-dialog v-model="tokenVisible" title="节点密钥" width="560px" :close-on-click-modal="false">
      <el-alert type="warning" :closable="false" show-icon class="mb-16">
        密钥仅在本次显示，请立即复制并配置到原站端 config.yaml 的 <b>token</b> 字段。关闭后无法再次查看。
      </el-alert>
      <div class="token-box">
        <span class="mono">{{ tokenValue }}</span>
        <el-button size="small" type="primary" plain @click="copyToken">复制</el-button>
      </div>
      <div class="mt-16 muted">节点名：<span class="mono">{{ tokenNodeName }}</span></div>
      <template #footer>
        <el-button type="primary" @click="tokenVisible = false">我已保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { apiCreateNode, apiDeleteNode, apiKickNode, apiListNodes, apiResetNodeToken, apiUpdateNode } from '@/api'
import type { NodeForm, NodeItem } from '@/api/types'
import { useRealtimeStore } from '@/store/realtime'
import { formatRelative } from '@/utils/format'

defineOptions({ name: 'NodesView' })

const rt = useRealtimeStore()

const nodes = ref<NodeItem[]>([])
const loading = ref(false)
const saving = ref(false)
const formVisible = ref(false)
const editing = ref(false)
const formRef = ref<FormInstance>()
const form = reactive<NodeForm>({ name: '', remark: '', role: 'client', ip_whitelist: '' })
const rules: FormRules = {
  name: [
    { required: true, message: '请输入节点名', trigger: 'blur' },
    { pattern: /^[a-zA-Z0-9_-]{1,64}$/, message: '仅支持字母、数字、下划线与短横线', trigger: 'blur' },
  ],
}

const tokenVisible = ref(false)
const tokenValue = ref('')
const tokenNodeName = ref('')

let editId = 0

async function load() {
  loading.value = true
  try {
    nodes.value = await apiListNodes()
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = false
  editId = 0
  Object.assign(form, { name: '', remark: '', role: 'client', ip_whitelist: '' })
  formVisible.value = true
}

function openEdit(row: NodeItem) {
  editing.value = true
  editId = row.id
  Object.assign(form, {
    name: row.name,
    remark: row.remark,
    role: row.role || 'client',
    ip_whitelist: row.ip_whitelist || '',
  })
  formVisible.value = true
}

async function submit() {
  if (!formRef.value) return
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return
  saving.value = true
  try {
    if (editing.value) {
      await apiUpdateNode(editId, { ...form })
      ElMessage.success('节点已更新')
    } else {
      const res = await apiCreateNode({ ...form })
      tokenValue.value = res.token
      tokenNodeName.value = res.node.name
      tokenVisible.value = true
      ElMessage.success('节点创建成功')
    }
    formVisible.value = false
    await load()
  } finally {
    saving.value = false
  }
}

async function resetToken(row: NodeItem) {
  await ElMessageBox.confirm(
    `重置后节点「${row.name}」将立即断开，需使用新密钥重新连接。确定继续？`,
    '重置密钥',
    { type: 'warning' },
  ).catch(() => 'cancel')
  const res = await apiResetNodeToken(row.id)
  tokenValue.value = res.token
  tokenNodeName.value = row.name
  tokenVisible.value = true
  await load()
}

async function kick(row: NodeItem) {
  await apiKickNode(row.id)
  ElMessage.success(`节点「${row.name}」已强制下线`)
  await load()
}

async function remove(row: NodeItem) {
  await ElMessageBox.confirm(
    `删除节点「${row.name}」会同时删除其全部转发规则，且不可恢复。确定删除？`,
    '删除节点',
    { type: 'warning', confirmButtonText: '删除', confirmButtonClass: 'el-button--danger' },
  ).catch(() => 'cancel')
  await apiDeleteNode(row.id)
  ElMessage.success('节点已删除')
  await load()
}

async function copyToken() {
  try {
    await navigator.clipboard.writeText(tokenValue.value)
    ElMessage.success('已复制到剪贴板')
  } catch {
    ElMessage.warning('复制失败，请手动选择复制')
  }
}

const offs: Array<() => void> = []
onMounted(() => {
  load()
  offs.push(rt.on('node_status', () => load()))
  offs.push(rt.on('node_deleted', () => load()))
})
onBeforeUnmount(() => offs.forEach((f) => f()))
</script>

<style scoped>
.node-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  display: inline-block;
  flex: none;
}

.dot.on {
  background: #67c23a;
  box-shadow: 0 0 0 3px rgba(103, 194, 58, 0.18);
}

.dot.off {
  background: #c0c4cc;
}

.token-box {
  display: flex;
  align-items: center;
  gap: 10px;
  background: #f5f7fa;
  border: 1px solid #e4e7ed;
  border-radius: 6px;
  padding: 12px 14px;
  word-break: break-all;
  font-size: 13px;
}
</style>
