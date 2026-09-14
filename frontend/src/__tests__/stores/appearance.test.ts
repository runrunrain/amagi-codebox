import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// 在 wailsjs 绑定边界打桩：store 的职责是归一化、并发去重与失败 fail-safe。
const { getBinding, setBinding } = vi.hoisted(() => ({
  getBinding: vi.fn(),
  setBinding: vi.fn(),
}))

vi.mock('../../../wailsjs/go/settings/Service', () => ({
  GetWebPlaneSkin: getBinding,
  SetWebPlaneSkin: setBinding,
}))

import { useAppearanceStore } from '../../stores/appearance'

describe('stores.appearance（会话 Web 平面配色方向）', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    getBinding.mockReset()
    setBinding.mockReset()
  })

  it('缺省 dark；ensureLoaded 读取后端值并归一（light 直取，未知值回落 dark）', async () => {
    const store = useAppearanceStore()
    expect(store.webPlaneSkin).toBe('dark')

    getBinding.mockResolvedValueOnce('light')
    await store.ensureLoaded()
    expect(store.webPlaneSkin).toBe('light')

    // 未知值（后端理论已归一，双端同规则 fail-safe）——新 pinia 实例隔离 store 单例
    setActivePinia(createPinia())
    const store2 = useAppearanceStore()
    getBinding.mockResolvedValueOnce('blue')
    await store2.ensureLoaded()
    expect(store2.webPlaneSkin).toBe('dark')
  })

  it('ensureLoaded 并发去重：同时多次调用只发一次请求', async () => {
    const store = useAppearanceStore()
    getBinding.mockResolvedValueOnce('light')

    await Promise.all([store.ensureLoaded(), store.ensureLoaded(), store.ensureLoaded()])

    expect(getBinding).toHaveBeenCalledTimes(1)
  })

  it('读取失败保持 dark 缺省不抛错（console.warn）', async () => {
    const store = useAppearanceStore()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    getBinding.mockRejectedValueOnce(new Error('boom'))

    await expect(store.ensureLoaded()).resolves.toBeUndefined()
    expect(store.webPlaneSkin).toBe('dark')
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })

  it('setSkin 持久化成功后即时更新本地状态', async () => {
    const store = useAppearanceStore()
    setBinding.mockResolvedValueOnce(undefined)

    await store.setSkin('light')

    expect(setBinding).toHaveBeenCalledWith('light')
    expect(store.webPlaneSkin).toBe('light')
  })
})
