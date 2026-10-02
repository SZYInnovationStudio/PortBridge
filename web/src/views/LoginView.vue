<template>
  <div class="login-page">
    <div class="login-card">
      <div class="brand">
        <el-icon :size="34" color="#409eff"><Share /></el-icon>
        <div>
          <div class="title">PortBridge</div>
          <div class="sub">四层端口转发 · 管理平台</div>
        </div>
      </div>

      <el-form ref="formRef" :model="form" :rules="rules" size="large" @keyup.enter="submit">
        <el-form-item prop="username">
          <el-input v-model="form.username" placeholder="用户名" :prefix-icon="User" clearable />
        </el-form-item>
        <el-form-item prop="password">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="密码"
            :prefix-icon="Lock"
            show-password
          />
        </el-form-item>
        <el-button type="primary" class="submit" :loading="loading" @click="submit">登 录</el-button>
      </el-form>

      <div class="tips">
        <div v-if="sysInfo" class="sys">
          当前节点：<span class="mono">{{ sysInfo.node_name }}</span>
          <el-tag size="small" :type="sysInfo.role === 'server' ? 'success' : 'warning'" effect="plain">
            {{ sysInfo.role === 'server' ? '中转端' : '原站端' }}
          </el-tag>
        </div>
        <div v-if="sysInfo?.initialized === false" class="warn">
          系统尚未初始化，默认账号 admin / admin123
        </div>
      </div>
    </div>
    <div class="footer">PortBridge {{ sysInfo?.version ? 'v' + sysInfo.version : '' }} · 仅限 Linux 部署</div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { Lock, User } from '@element-plus/icons-vue'
import { apiSystemInfo } from '@/api'
import type { SystemInfo } from '@/api/types'
import { useAuthStore } from '@/store/auth'
import { useRealtimeStore } from '@/store/realtime'

const auth = useAuthStore()
const rt = useRealtimeStore()
const router = useRouter()
const route = useRoute()

const formRef = ref<FormInstance>()
const loading = ref(false)
const sysInfo = ref<SystemInfo | null>(null)
const form = reactive({ username: 'admin', password: '' })
const rules: FormRules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

onMounted(async () => {
  try {
    sysInfo.value = await apiSystemInfo()
  } catch {
    /* 后端未就绪时忽略 */
  }
})

async function submit() {
  if (!formRef.value) return
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return
  loading.value = true
  try {
    await auth.login(form.username, form.password)
    ElMessage.success('登录成功')
    rt.connect()
    const redirect = (route.query.redirect as string) || '/overview'
    router.push(redirect)
  } catch {
    /* 错误提示已由 http 拦截器处理 */
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-page {
  height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #1f2d3d 0%, #2b4a6f 60%, #409eff 100%);
}

.login-card {
  width: 380px;
  background: #fff;
  border-radius: 12px;
  padding: 32px 30px 24px;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.25);
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 26px;
}

.title {
  font-size: 22px;
  font-weight: 700;
  line-height: 1.1;
}

.sub {
  color: #909399;
  font-size: 12px;
  margin-top: 4px;
}

.submit {
  width: 100%;
  margin-top: 4px;
}

.tips {
  margin-top: 16px;
  font-size: 12px;
  color: #909399;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.sys {
  display: flex;
  align-items: center;
  gap: 6px;
}

.warn {
  color: #e6a23c;
}

.footer {
  margin-top: 22px;
  color: rgba(255, 255, 255, 0.7);
  font-size: 12px;
}
</style>
