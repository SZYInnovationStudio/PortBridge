import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/store/auth'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { public: true, title: '登录' },
  },
  {
    path: '/',
    component: () => import('@/layouts/AppLayout.vue'),
    redirect: '/overview',
    children: [
      {
        path: 'overview',
        name: 'overview',
        component: () => import('@/views/OverviewView.vue'),
        meta: { title: '概览', icon: 'Odometer' },
      },
      {
        path: 'nodes',
        name: 'nodes',
        component: () => import('@/views/NodesView.vue'),
        meta: { title: '节点管理', icon: 'Connection', role: 'server' },
      },
      {
        path: 'proxies',
        name: 'proxies',
        component: () => import('@/views/ProxiesView.vue'),
        meta: { title: '规则管理', icon: 'Share' },
      },
      {
        path: 'logs',
        name: 'logs',
        component: () => import('@/views/LogsView.vue'),
        meta: { title: '实时日志', icon: 'Document' },
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('@/views/SettingsView.vue'),
        meta: { title: '系统设置', icon: 'Setting' },
      },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/overview' },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (to.meta.public) {
    if (auth.isLogged && to.name === 'login') return { path: '/overview' }
    return true
  }
  if (!auth.isLogged) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }
  // 首次进入需拉取系统信息以确定角色与菜单
  if (!auth.systemInfo) {
    await auth.bootstrap()
  }
  const needRole = to.meta.role as string | undefined
  if (needRole && auth.role !== needRole) {
    return { path: '/overview' }
  }
  return true
})

router.afterEach((to) => {
  const title = (to.meta.title as string) || 'PortBridge'
  document.title = `${title} · PortBridge`
})

export default router
