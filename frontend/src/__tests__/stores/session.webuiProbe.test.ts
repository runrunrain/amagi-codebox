import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// 在 api/webui.ts 边界打桩（不真调 wailsjs 绑定）。
const { probe } = vi.hoisted(() => ({
  probe: vi.fn(),
}))

vi.mock('../../api/webui', () => ({
  probeWebUI: probe,
}))

import { useSessionStore } from '../../stores/session'

describe('stores/session.webuiProbe（探测轮询节奏与终态语义）', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setActivePinia(createPinia())
    probe.mockReset()
  })

  it('probing 以 0.5–1s 契约节奏持续轮询', async () => {
    probe.mockResolvedValue({ state: 'probing' })
    const store = useSessionStore()

    store.ensureWebUIProbe('s1')
    await vi.advanceTimersByTimeAsync(0)
    expect(probe).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(800)
    expect(probe).toHaveBeenCalledTimes(2)
  })

  it('unavailable 不再是终态：转低频复活探测，晚就绪后恢复 available', async () => {
    // 复现 2026-11-17 会话 b5415434：WSL pi 的 webui server 晚于探测窗口
    // 就绪（实测 3m42s > 3min 窗口）。unavailable 后若停探，Web 视图入口
    // 永不出现；复活探测应在服务就绪后恢复 available。
    probe
      .mockResolvedValue({ state: 'available', url: 'http://127.0.0.1:65191/' })
      .mockResolvedValueOnce({ state: 'probing' })
      .mockResolvedValueOnce({ state: 'unavailable' })
    const store = useSessionStore()

    store.ensureWebUIProbe('s2')
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(800)
    expect(store.webuiStatus['s2']).toEqual({ state: 'unavailable' })

    // 复活节奏 10s：高频窗口（<10s）内不再有探测。
    await vi.advanceTimersByTimeAsync(800 * 5)
    expect(probe).toHaveBeenCalledTimes(2)

    // 10s 后复活探测：服务就绪 → 恢复 available（同一 advance 窗口内后续
    // 保活探测继续，只断言已至少复活到位）。
    await vi.advanceTimersByTimeAsync(10_000)
    expect(probe.mock.calls.length).toBeGreaterThanOrEqual(3)
    expect(store.webuiStatus['s2']).toEqual({
      state: 'available',
      url: 'http://127.0.0.1:65191/',
    })

    // 恢复后维持 available（3s 保活节奏继续跟随，不回落、不停止）。
    await vi.advanceTimersByTimeAsync(10_000)
    expect(probe.mock.calls.length).toBeGreaterThanOrEqual(4)
    expect(store.webuiStatus['s2']).toEqual({
      state: 'available',
      url: 'http://127.0.0.1:65191/',
    })
  })

  it('ended 仍是终态：停止轮询', async () => {
    probe
      .mockResolvedValueOnce({ state: 'probing' })
      .mockResolvedValueOnce({ state: 'ended' })
    const store = useSessionStore()

    store.ensureWebUIProbe('s3')
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(800)
    expect(store.webuiStatus['s3']).toEqual({ state: 'ended' })

    // 终态后不再轮询（保活/复活节奏都不排）。
    await vi.advanceTimersByTimeAsync(60_000)
    expect(probe).toHaveBeenCalledTimes(2)
  })

  it('available 转低频保活（3s）', async () => {
    probe.mockResolvedValue({ state: 'available', url: 'http://127.0.0.1:47000/' })
    const store = useSessionStore()

    store.ensureWebUIProbe('s4')
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(3000)
    expect(probe).toHaveBeenCalledTimes(2)
  })

  it('removeSession 停止该会话的复活探测', async () => {
    probe.mockResolvedValue({ state: 'unavailable' })
    const store = useSessionStore()

    store.ensureWebUIProbe('s5')
    await vi.advanceTimersByTimeAsync(0)
    expect(probe).toHaveBeenCalledTimes(1)

    store.removeSession('s5')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(probe).toHaveBeenCalledTimes(1)
    expect(store.webuiStatus['s5']).toBeUndefined()
  })
})
