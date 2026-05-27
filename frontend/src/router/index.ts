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
      path: '/provider-center',
      name: 'ProviderCenter',
      component: () => import('../views/ProviderCenter.vue')
    },
    {
      path: '/providers',
      redirect: '/provider-center'
    },
    {
      path: '/providers/:name',
      redirect: to => ({ path: '/provider-center' })
    },
    {
      path: '/rules',
      name: 'Rules',
      component: () => import('../views/Rules.vue')
    },
    {
      path: '/extensions',
      component: () => import('../views/ExtensionsLayout.vue'),
      redirect: '/extensions/plugins/claude',
      children: [
        {
          path: 'plugins',
          component: () => import('../views/PluginManagementLayout.vue'),
          redirect: '/extensions/plugins/claude',
          children: [
            {
              path: 'claude',
              name: 'ClaudePlugins',
              component: () => import('../views/PluginsView.vue')
            },
            {
              path: 'codex',
              name: 'CodexPlugins',
              component: () => import('../views/CodexPluginsView.vue')
            }
          ]
        },
        {
          path: 'workspaces',
          name: 'Workspaces',
          component: () => import('../views/WorkspacesView.vue')
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
