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
      meta: { keepAlive: true },
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
      // 使用统计分页化（设计 §5）：/usage 拆为「使用统计 / 额度查询」两个子页，
      // 侧边栏 /usage 入口经重定向无破坏进入 stats；子页懒加载。
      // Usage statistics → paginated: stats + quota sub-pages / 使用统计分页
      path: '/usage',
      component: () => import('../views/UsageView.vue'),
      redirect: '/usage/stats',
      children: [
        {
          path: 'stats',
          name: 'UsageStats',
          component: () => import('../components/usage/UsageStatsPanel.vue'),
        },
        {
          path: 'quota',
          name: 'UsageQuota',
          component: () => import('../components/usage/QuotaPanel.vue'),
        },
      ],
    },
    {
      path: '/:pathMatch(.*)*',
      redirect: '/'
    }
  ]
})

export default router
