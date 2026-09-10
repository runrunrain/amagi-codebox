import { describe, expect, it } from 'vitest'
import router from '../../router'

describe('Router 路由与重定向表 (P2-A 验证)', () => {
  it('死页路由重定向：/dashboard 与 /providers 重定向至 /settings', async () => {
    await router.push('/dashboard')
    expect(router.currentRoute.value.path).toBe('/settings')

    await router.push('/providers')
    expect(router.currentRoute.value.path).toBe('/settings')
  })

  it('旧版 sessions 重定向：/sessions 重定向至 /lobby', async () => {
    await router.push('/sessions')
    expect(router.currentRoute.value.path).toBe('/lobby')
  })

  it('旧版终端深链重定向：/terminal/:id 重定向至 /workspace/:id?view=terminal', async () => {
    await router.push('/terminal/sess-abc')
    expect(router.currentRoute.value.path).toBe('/workspace/sess-abc')
    expect(router.currentRoute.value.query.view).toBe('terminal')
  })

  it('有效路由可达性：/settings、/lobby、/connect 均正常解析', async () => {
    await router.push('/settings')
    expect(router.currentRoute.value.name).toBe('settings')

    await router.push('/lobby')
    expect(router.currentRoute.value.name).toBe('lobby')

    await router.push('/connect')
    expect(router.currentRoute.value.name).toBe('connect')
  })
})
