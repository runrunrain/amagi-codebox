import { describe, expect, it } from 'vitest'
import appLayoutSource from '../../components/AppLayout.vue?raw'

describe('AppLayout router view lifecycle', () => {
  it('keys routed pages by fullPath so terminal session changes remount the page', () => {
    expect(appLayoutSource).toContain('<router-view :key="routerViewKey()" />')
  })

  it('M2-D：workspace 按 path 作 key（?view=terminal 呈现切换不重挂载、复用 WS attach）；其余页面保持 fullPath', () => {
    expect(appLayoutSource).toContain("route.name === 'workspace' ? route.path : route.fullPath")
  })
})
