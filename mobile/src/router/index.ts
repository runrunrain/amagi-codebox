import { createRouter, createWebHashHistory } from 'vue-router'

const ConnectPage = () => import('../views/ConnectPage.vue')
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
      name: 'connect',
      component: ConnectPage,
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
