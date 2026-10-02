<template>
  <el-container class="layout">
    <el-aside width="216px" class="sidebar">
      <div class="brand">
        <el-icon :size="22" color="#409eff"><Share /></el-icon>
        <div class="brand-text">
          <div class="brand-title">PortBridge</div>
          <div class="brand-sub">四层端口转发平台</div>
        </div>
      </div>
      <el-menu
        :default-active="activeMenu"
        class="menu"
        background-color="#1f2d3d"
        text-color="#b8c4d0"
        active-text-color="#ffffff"
        router
      >
        <el-menu-item v-for="m in menus" :key="m.path" :index="m.path">
          <el-icon><component :is="m.icon" /></el-icon>
          <span>{{ m.title }}</span>
        </el-menu-item>
      </el-menu>
      <div class="sidebar-footer">
        <div class="role-badge">{{ roleLabel }}</div>
        <div class="version">v{{ auth.systemInfo?.version || '-' }}</div>
      </div>
    </el-aside>

    <el-container>
      <el-header class="header">
        <div class="header-left">
          <el-tag :type="auth.isClient ? 'warning' : 'success'" effect="dark" size="small">
            {{ roleLabel }}
          </el-tag>
          <span class="node-name mono">{{ auth.systemInfo?.node_name || '-' }}</span>
        </div>
        <div class="header-right">
          <template v-if="auth.isClient">
            <el-tag :type="clientOnline ? 'success' : 'danger'" size="small" effect="plain">
              <el-icon class="dot"><component :is="clientOnline ? 'SuccessFilled' : 'CircleCloseFilled'" /></el-icon>
              {{ clientOnline ? '中转端已连接' : '中转端未连接' }}
            </el-tag>
            <span v-if="clientOnline" class="muted latency">{{ auth.systemInfo?.latency_ms ?? 0 }} ms</span>
          </template>
          <el-tag :type="wsOpen ? 'success' : 'info'" size="small" effect="plain">
            {{ wsOpen ? '实时推送已连接' : '实时推送断开' }}
          </el-tag>
          <el-dropdown @command="onCommand">
            <span class="user">
              <el-icon><UserFilled /></el-icon>
              {{ auth.user?.nickname || auth.user?.username }}
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="password">修改密码</el-dropdown-item>
                <el-dropdown-item command="logout" divided>退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <el-main class="main">
        <router-view v-slot="{ Component }">
          <keep-alive :include="['ProxiesView', 'NodesView', 'LogsView', 'ConnLogsView']">
            <component :is="Component" />
          </keep-alive>
        </router-view>
      </el-main>
    </el-container>

    <el-dialog v-model="pwdVisible" title="修改密码" width="420px">
      <el-form ref="pwdFormRef" :model="pwdForm" :rules="pwdRules" label-width="90px">
        <el-form-item label="原密码" prop="old_password">
          <el-input v-model="pwdForm.old_password" type="password" show-password />
        </el-form-item>
        <el-form-item label="新密码" prop="new_password">
          <el-input v-model="pwdForm.new_password" type="password" show-password />
        </el-form-item>
        <el-form-item label="确认新密码" prop="confirm">
          <el-input v-model="pwdForm.confirm" type="password" show-password />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="pwdVisible = false">取消</el-button>
        <el-button type="primary" :loading="pwdSaving" @click="submitPassword">确定</el-button>
      </template>
    </el-dialog>
  </el-container>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { apiChangePassword } from '@/api'
import { useAuthStore } from '@/store/auth'
import { useRealtimeStore } from '@/store/realtime'

defineOptions({ name: 'AppLayout' })

const auth = useAuthStore()
const rt = useRealtimeStore()
const route = useRoute()
const router = useRouter()

const activeMenu = computed(() => route.path)

const menus = computed(() => {
  const all = [
    { path: '/overview', title: '概览', icon: 'Odometer', role: '' },
    { path: '/nodes', title: '节点管理', icon: 'Connection', role: 'server' },
    { path: '/proxies', title: '规则管理', icon: 'Share', role: '' },
    { path: '/conn-logs', title: '连接记录', icon: 'Link', role: '' },
    { path: '/logs', title: '实时日志', icon: 'Document', role: '' },
    { path: '/settings', title: '系统设置', icon: 'Setting', role: '' },
  ]
  return all.filter((m) => !m.role || m.role === auth.role)
})

const roleLabel = computed(() => {
  if (!auth.systemInfo) return '加载中…'
  return auth.isServer ? '中转端 Server' : '原站端 Client'
})
const wsOpen = computed(() => rt.open)
const clientOnline = computed(() => !!auth.systemInfo?.connected)

let sysTimer = 0

onMounted(() => {
  rt.connect()
  auth.fetchSystemInfo().catch(() => undefined)
  // 周期刷新系统信息（中转端连接状态 / 延迟）
  sysTimer = window.setInterval(() => {
    auth.fetchSystemInfo().catch(() => undefined)
  }, 10000)
})

onBeforeUnmount(() => {
  if (sysTimer) clearInterval(sysTimer)
})

// ---- 修改密码 ----
const pwdVisible = ref(false)
const pwdSaving = ref(false)
const pwdFormRef = ref<FormInstance>()
const pwdForm = reactive({ old_password: '', new_password: '', confirm: '' })
const pwdRules: FormRules = {
  old_password: [{ required: true, message: '请输入原密码', trigger: 'blur' }],
  new_password: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    { min: 6, message: '新密码至少 6 位', trigger: 'blur' },
  ],
  confirm: [
    {
      validator: (_r, v, cb) =>
        v === pwdForm.new_password ? cb() : cb(new Error('两次输入的密码不一致')),
      trigger: 'blur',
    },
  ],
}

async function submitPassword() {
  if (!pwdFormRef.value) return
  const valid = await pwdFormRef.value.validate().catch(() => false)
  if (!valid) return
  pwdSaving.value = true
  try {
    await apiChangePassword(pwdForm.old_password, pwdForm.new_password)
    ElMessage.success('密码修改成功，请重新登录')
    pwdVisible.value = false
    auth.logout()
    rt.disconnect()
    router.push('/login')
  } finally {
    pwdSaving.value = false
  }
}

async function onCommand(cmd: string) {
  if (cmd === 'password') {
    pwdForm.old_password = ''
    pwdForm.new_password = ''
    pwdForm.confirm = ''
    pwdVisible.value = true
    return
  }
  if (cmd === 'logout') {
    const ok = await ElMessageBox.confirm('确定要退出登录吗？', '提示', { type: 'warning' })
      .then(() => true)
      .catch(() => false)
    if (!ok) return
    auth.logout()
    rt.disconnect()
    router.push('/login')
  }
}
</script>

<style scoped>
.layout {
  height: 100vh;
}

.sidebar {
  background: var(--pb-sidebar-bg);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 16px 18px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}

.brand-title {
  color: #fff;
  font-size: 16px;
  font-weight: 600;
  line-height: 1.2;
}

.brand-sub {
  color: #8a97a6;
  font-size: 11px;
  margin-top: 2px;
}

.menu {
  flex: 1;
  border-right: none;
  overflow-y: auto;
}

.menu :deep(.el-menu-item.is-active) {
  background: rgba(64, 158, 255, 0.18) !important;
  border-right: 3px solid var(--pb-primary);
}

.sidebar-footer {
  padding: 12px 18px;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.role-badge {
  color: #8a97a6;
  font-size: 12px;
}

.version {
  color: #5f6b78;
  font-size: 12px;
}

.header {
  height: var(--pb-header-h);
  background: #fff;
  border-bottom: 1px solid #e4e7ed;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 20px;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 10px;
}

.node-name {
  font-size: 14px;
  color: #303133;
}

.header-right {
  display: flex;
  align-items: center;
  gap: 12px;
}

.latency {
  font-size: 12px;
}

.dot {
  margin-right: 2px;
  vertical-align: -2px;
}

.user {
  display: flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  font-size: 14px;
  color: #303133;
  outline: none;
}

.main {
  background: var(--pb-bg);
  padding: 0;
  overflow-y: auto;
}
</style>
