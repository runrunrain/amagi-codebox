import { createRouter, createWebHashHistory } from 'vue-router'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/',
      name: 'SessionSettings',
      component: () => import('../views/SessionSettingsView.vue')
    },
    {
      path: '/terminal',
      name: 'TerminalPage',
      component: () => import('../views/TerminalPageView.vue')
    },
    {
      path: '/provider',
      name: 'ProviderCenter',
      component: () => import('../views/ProviderCenterView.vue')
    },
    {
      path: '/extensions',
      name: 'Extensions',
      component: () => import('../views/ExtensionsView.vue')
    },
    {
      path: '/rules',
      name: 'Rules',
      // TODO: P2 实现注入规则页
      component: () => import('../views/SessionSettingsView.vue')
    },
    {
      path: '/logs',
      name: 'Logs',
      // TODO: P2 实现系统日志页
      component: () => import('../views/SessionSettingsView.vue')
    },
    {
      path: '/:pathMatch(.*)*',
      redirect: '/'
    }
  ]
})

export default router
