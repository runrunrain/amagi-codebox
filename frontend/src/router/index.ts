import { createRouter, createWebHashHistory } from 'vue-router'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/',
      redirect: '/dashboard'
    },
    {
      path: '/dashboard',
      name: 'Dashboard',
      component: () => import('../views/Dashboard.vue')
    },
    {
      path: '/providers',
      name: 'Providers',
      component: () => import('../views/Providers.vue')
    },
    {
      path: '/providers/:name',
      name: 'ProviderDetail',
      component: () => import('../views/ProviderDetail.vue'),
      props: true
    },
    {
      path: '/rules',
      name: 'Rules',
      component: () => import('../views/Rules.vue')
    },
    {
      path: '/extensions',
      component: () => import('../views/ExtensionsLayout.vue'),
      redirect: '/extensions/plugins',
      children: [
        {
          path: 'plugins',
          name: 'Plugins',
          component: () => import('../views/PluginsView.vue')
        },
        {
          path: 'envvars',
          name: 'EnvVars',
          component: () => import('../views/EnvVarsView.vue')
        }
      ]
    },
    {
      path: '/logs',
      name: 'Logs',
      component: () => import('../views/Logs.vue')
    },
    {
      path: '/terminals',
      name: 'Terminals',
      component: () => import('../views/Terminals.vue')
    },
    {
      path: '/settings',
      name: 'Settings',
      component: () => import('../views/Settings.vue')
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'NotFound',
      redirect: '/'
    }
  ]
})

export default router
