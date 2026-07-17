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
      component: () => import('../views/RulesView.vue')
    },
    {
      path: '/envcheck',
      name: 'EnvCheck',
      component: () => import('../views/EnvCheckView.vue')
    },
    {
      path: '/logs',
      name: 'Logs',
      component: () => import('../views/LogsView.vue')
    },
    {
      // 使用统计：AI 模型用量与成本 / Usage statistics: AI model usage & cost
      path: '/usage',
      name: 'Usage',
      component: () => import('../views/UsageView.vue')
    },
    {
      path: '/:pathMatch(.*)*',
      redirect: '/'
    }
  ]
})

export default router
