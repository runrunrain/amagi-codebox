import { createRouter, createWebHashHistory } from 'vue-router'

const ConnectPage = () => import('../views/ConnectPage.vue')
const WorkspacePage = () => import('../views/WorkspacePage.vue')
const SessionsPage = () => import('../views/SessionsPage.vue')
const SettingsPage = () => import('../views/SettingsPage.vue')

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/',
      // P5 IA：PG-01 权威路由为 #/connect；'/' 为入口别名（P5-DIR-01）。
      alias: '/connect',
      name: 'connect',
      component: ConnectPage,
      // PG-01 独立壳：不挂 legacy top-bar/bottom-nav（M1-D1）
      meta: { bare: true },
    },
    {
      // PG-02 会话大厅（M2-B 本体）。权威路由名 lobby 不变（PG-01 与既有
      // E2E 均以 #/lobby 为目标）；SessionsPage.vue 已重写为 PG-02。
      path: '/lobby',
      name: 'lobby',
      component: SessionsPage,
      // PG-02 独立壳：VT 浅色、不挂 legacy top-bar/bottom-nav
      meta: { bare: true },
    },
    {
      // PG-03 会话工作区本体（M2-C）：内容转化阅读面（Timeline/Composer/ControlBar）。
      path: '/workspace/:sessionId',
      name: 'workspace',
      component: WorkspacePage,
      meta: { bare: true },
    },
    {
      // P2-A：三 legacy 死页下线。Dashboard 走 Bearer 且数据由大厅接管；
      // 重定向到 /settings 内的桌面端管理静态引导块。
      path: '/dashboard',
      redirect: '/settings',
    },
    {
      // 旧版终端深链统一迁移到 v1 工作区诊断视图。旧 TerminalPage 使用的
      // /ws/terminal 兼容通道现为 input-only，继续挂载会导致远程页面无输出。
      path: '/terminal/:sessionId',
      redirect: (to) => ({
        name: 'workspace',
        params: { sessionId: to.params.sessionId },
        query: { ...to.query, view: 'terminal' },
      }),
    },
    {
      // legacy #/sessions 已由 PG-02 大厅接管（P5 IA）：重定向到 #/lobby，
      // 保留旧链接（AppLayout/DrawerNav）可达性。
      path: '/sessions',
      redirect: { name: 'lobby' },
    },
    {
      // P2-A：Providers 端点服务端 loopback-only，手机端无法修改；
      // 重定向到 /settings 内的桌面端管理静态引导块。
      path: '/providers',
      redirect: '/settings',
    },
    {
      path: '/settings',
      name: 'settings',
      component: SettingsPage,
    },
  ],
})

export default router
