import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// 在 api/quota.ts 边界打桩（任务要求：测试不真调 wailsjs）。
const { getQuotas, probeOne, probeAll } = vi.hoisted(() => ({
  getQuotas: vi.fn(),
  probeOne: vi.fn(),
  probeAll: vi.fn(),
}))

vi.mock('../../api/quota', () => ({
  getProviderQuotas: getQuotas,
  probeProviderQuota: probeOne,
  probeAllProviderQuotas: probeAll,
}))

import { useQuotaStore } from '../../stores/quota'

function entry(name: string, overrides: Record<string, unknown> = {}) {
  return {
    provider: name,
    family: 'glm-bigmodel',
    status: 'ok',
    source: 'glm-api',
    probed_at: new Date().toISOString(),
    ...overrides,
  }
}

function minutesAgo(min: number): string {
  return new Date(Date.now() - min * 60_000).toISOString()
}

describe('stores/quota（额度缓存 + 单飞 + ensure 冷却 + quotaFor 映射）', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    getQuotas.mockReset()
    probeOne.mockReset()
    probeAll.mockReset()
  })

  it('loadAll 写入全量缓存；silent 失败保留旧数据并 console.warn', async () => {
    const store = useQuotaStore()
    getQuotas.mockResolvedValueOnce({ glm: entry('glm') })
    await store.loadAll()
    expect(store.entries['glm'].provider).toBe('glm')

    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    getQuotas.mockRejectedValueOnce(new Error('boom'))
    await store.loadAll({ silent: true })
    // 已有数据：静默保留，不写 error
    expect(store.entries['glm'].provider).toBe('glm')
    expect(store.error).toBe('')
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })

  it('首次 loadAll 失败写 error（ErrorState 展示）', async () => {
    const store = useQuotaStore()
    getQuotas.mockRejectedValueOnce(new Error('boom'))
    await store.loadAll()
    expect(store.error).toContain('boom')
    expect(store.loading).toBe(false)
  })

  it('probe 单飞：并发两次只发一次请求，probing 态起落，结果写回 entries', async () => {
    const store = useQuotaStore()
    let resolveProbe: (v: unknown) => void = () => {}
    probeOne.mockReturnValueOnce(new Promise((r) => { resolveProbe = r }))

    const first = store.probe('glm')
    const second = store.probe('glm')
    expect(store.probing['glm']).toBe(true)

    resolveProbe(entry('glm', { level: 'pro' }))
    const [a, b] = await Promise.all([first, second])
    expect(probeOne).toHaveBeenCalledTimes(1)
    expect(a).toBe(b)
    expect(store.entries['glm'].level).toBe('pro')
    expect(store.probing['glm']).toBe(false)
  })

  it('probe 失败：错误向上抛、probing 复位、不写 entries', async () => {
    const store = useQuotaStore()
    probeOne.mockRejectedValueOnce(new Error('net down'))
    await expect(store.probe('glm')).rejects.toThrow('net down')
    expect(store.probing['glm']).toBe(false)
    expect(store.entries['glm']).toBeUndefined()
  })

  it('probeAll：完成后写入全量并复位 probingAll', async () => {
    const store = useQuotaStore()
    probeAll.mockImplementationOnce(async () => {
      expect(store.probingAll).toBe(true)
      return { glm: entry('glm'), codex: entry('codex', { family: 'codex-sub' }) }
    })
    const result = await store.probeAll()
    expect(Object.keys(result)).toHaveLength(2)
    expect(store.entries['codex'].family).toBe('codex-sub')
    expect(store.probingAll).toBe(false)
  })

  describe('ensure 冷却（缓存缺失或 >10min 且非 in-flight 才后台探测）', () => {
    it('缓存新鲜（<10min）不触发', async () => {
      const store = useQuotaStore()
      getQuotas.mockResolvedValueOnce({ glm: entry('glm', { probed_at: minutesAgo(5) }) })
      await store.loadAll()
      await store.ensure('glm')
      expect(probeOne).not.toHaveBeenCalled()
    })

    it('缓存缺失触发一次', async () => {
      const store = useQuotaStore()
      probeOne.mockResolvedValueOnce(entry('glm'))
      await store.ensure('glm')
      expect(probeOne).toHaveBeenCalledTimes(1)
    })

    it('probed_at 超 10min 触发一次', async () => {
      const store = useQuotaStore()
      getQuotas.mockResolvedValueOnce({ glm: entry('glm', { probed_at: minutesAgo(15) }) })
      await store.loadAll()
      probeOne.mockResolvedValueOnce(entry('glm'))
      await store.ensure('glm')
      expect(probeOne).toHaveBeenCalledTimes(1)
    })

    it('in-flight 时重复 ensure 不重复探测', async () => {
      const store = useQuotaStore()
      let resolveProbe: (v: unknown) => void = () => {}
      probeOne.mockReturnValueOnce(new Promise((r) => { resolveProbe = r }))

      const pending = store.ensure('glm')
      await store.ensure('glm') // 应命中单飞，不再发第二次
      resolveProbe(entry('glm'))
      await pending
      expect(probeOne).toHaveBeenCalledTimes(1)
    })

    it('探测失败静默（不抛错）', async () => {
      const store = useQuotaStore()
      const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
      probeOne.mockRejectedValueOnce(new Error('boom'))
      await expect(store.ensure('glm')).resolves.toBeUndefined()
      expect(warn).toHaveBeenCalled()
      warn.mockRestore()
    })
  })

  describe('quotaFor 映射', () => {
    it('直接命中缓存条目', async () => {
      const store = useQuotaStore()
      getQuotas.mockResolvedValueOnce({ glm: entry('glm') })
      await store.loadAll()
      expect(store.quotaFor('glm')?.status).toBe('ok')
    })

    it('无条目且非 codex → unsupported 占位（provider 名保留）', () => {
      const store = useQuotaStore()
      const placeholder = store.quotaFor('some-openai')
      expect(placeholder?.status).toBe('unsupported')
      expect(placeholder?.provider).toBe('some-openai')
    })

    it('无条目且名为 codex → null（待探测，非「不可查」）', () => {
      const store = useQuotaStore()
      expect(store.quotaFor('codex')).toBeNull()
      expect(store.quotaFor('')).toBeNull()
    })
  })
})
