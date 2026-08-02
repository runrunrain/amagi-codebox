import { createRouter, createWebHashHistory } from 'vue-router'

const ConnectPage = () => import('../views/ConnectPage.vue')
const LobbyPlaceholderPage = () => import('../views/LobbyPlaceholderPage.vue')
const DashboardPage = () => import('../views/DashboardPage.vue')
const TerminalPage = () => import('../views/TerminalPage.vue')
const SessionsPage = () => import('../views/SessionsPage.vue')
const ProvidersPage = () => import('../views/ProvidersPage.vue')
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
      // 会话大厅诚实占位（大厅本体 M2 交付，届时按 P5 IA 接管 #/sessions）
      path: '/lobby',
      name: 'lobby',
      component: LobbyPlaceholderPage,
      meta: { bare: true },
    },
    {
      path: '/dashboard',
      name: 'dashboard',
      component: DashboardPage,
    },
    {
      path: '/terminal/:sessionId',
      name: 'terminal',
      component: TerminalPage,
    },
    {
      path: '/sessions',
      name: 'sessions',
      component: SessionsPage,
    },
    {
      path: '/providers',
      name: 'providers',
      component: ProvidersPage,
    },
    {
      path: '/settings',
      name: 'settings',
      component: SettingsPage,
    },
  ],
})

export default router
