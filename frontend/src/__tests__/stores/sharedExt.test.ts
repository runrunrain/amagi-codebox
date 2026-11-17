import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// 在 api/sharedExt.ts 边界打桩（store 测试不触 wailsjs）。
const fns = vi.hoisted(() => ({
  getSharedSkills: vi.fn(),
  getSharedMCPServers: vi.fn(),
  getAggregatedSkills: vi.fn(),
  importSkillFromZip: vi.fn(),
  importSkillFromDir: vi.fn(),
  deleteSkill: vi.fn(),
  setSkillEnabled: vi.fn(),
  setSkillTargets: vi.fn(),
  previewMCPJSON: vi.fn(),
  importSharedMCPServers: vi.fn(),
  deleteMCPServer: vi.fn(),
  setMCPServerEnabled: vi.fn(),
  setMCPServerTargets: vi.fn(),
}))

vi.mock('../../api/sharedExt', () => fns)

import { useSharedExtStore } from '../../stores/sharedExt'

function skill(name: string) {
  return { name, description: '', sourcePath: '', origin: 'manual', enabled: false, targets: [], deployed: [], createdAt: '' }
}

function server(name: string) {
  return { name, config: {}, origin: 'manual', enabled: false, targets: [], deployed: [], createdAt: '' }
}

function agg(name: string, origin: string) {
  return { name, description: '', origin, managed: origin === 'manual', enabled: true }
}

describe('stores/sharedExt（清单加载 / 变更刷新 / 部分成功语义）', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    for (const m of Object.values(fns)) m.mockReset()
  })

  it('loadSkills/loadMCPServers/loadAggregated 填充 state；失败写 error 不抛错', async () => {
    const store = useSharedExtStore()
    fns.getSharedSkills.mockResolvedValueOnce([skill('s1')])
    fns.getSharedMCPServers.mockResolvedValueOnce([server('m1')])
    fns.getAggregatedSkills.mockResolvedValueOnce([agg('s1', 'manual'), agg('p1', 'plugin')])

    await store.loadSkills()
    await store.loadMCPServers()
    await store.loadAggregated()

    expect(store.skills.map((s) => s.name)).toEqual(['s1'])
    expect(store.mcpServers.map((m) => m.name)).toEqual(['m1'])
    expect(store.aggregated.map((a) => a.origin)).toEqual(['manual', 'plugin'])
    expect(store.error).toBe('')
    expect(store.loading).toBe(false)

    fns.getSharedSkills.mockRejectedValueOnce(new Error('boom'))
    await expect(store.loadSkills()).resolves.toBeUndefined()
    expect(store.error).toContain('boom')
  })

  it('loadAggregated 把 force 透传到 api（刷新按钮语义）', async () => {
    const store = useSharedExtStore()
    fns.getAggregatedSkills.mockResolvedValueOnce([])
    await store.loadAggregated(true)
    expect(fns.getAggregatedSkills).toHaveBeenCalledWith(true)
    expect(fns.getAggregatedSkills).not.toHaveBeenCalledWith(false)
  })

  it('skill 变更成功后刷新清单；失败写 error 并重新抛出', async () => {
    const store = useSharedExtStore()
    fns.getSharedSkills.mockResolvedValue([skill('s1')])
    fns.deleteSkill.mockResolvedValueOnce(undefined)

    await store.deleteSkill('s1')
    expect(fns.deleteSkill).toHaveBeenCalledWith('s1')
    expect(fns.getSharedSkills).toHaveBeenCalledTimes(1) // 变更后刷新

    fns.setSkillEnabled.mockRejectedValueOnce(new Error('nope'))
    await expect(store.setSkillEnabled('s1', true)).rejects.toThrow('nope')
    expect(store.error).toContain('nope')
    expect(fns.getSharedSkills).toHaveBeenCalledTimes(1) // 失败不刷新

    fns.setSkillTargets.mockResolvedValueOnce(undefined)
    await store.setSkillTargets('s1', ['claude', 'pi'])
    expect(fns.setSkillTargets).toHaveBeenCalledWith('s1', ['claude', 'pi'])
    expect(fns.getSharedSkills).toHaveBeenCalledTimes(2)
  })

  it('skill 导入 zip/dir 返回信封：imported>0 刷新清单，全跳过（imported=0）不刷新', async () => {
    const store = useSharedExtStore()
    fns.getSharedSkills.mockResolvedValue([skill('z')])
    fns.importSkillFromZip.mockResolvedValueOnce({ imported: [skill('z')], skipped: ['dup（同名已存在，跳过）'] })

    const result = await store.importSkillFromZip('/tmp/z.zip')
    expect(result.imported.map((s: { name: string }) => s.name)).toEqual(['z'])
    expect(result.skipped[0]).toContain('dup')
    expect(store.error).toBe('') // 部分成功不写全局 error
    expect(fns.getSharedSkills).toHaveBeenCalledTimes(1)

    // imported=0（全跳过走信封）：服务端无变化，不刷新清单
    fns.importSkillFromDir.mockResolvedValueOnce({ imported: [], skipped: [] })
    await store.importSkillFromDir('/tmp/d')
    expect(fns.getSharedSkills).toHaveBeenCalledTimes(1)

    // 整体失败：写 error 并抛出
    fns.importSkillFromZip.mockRejectedValueOnce(new Error('无 SKILL.md'))
    await expect(store.importSkillFromZip('/bad')).rejects.toThrow('无 SKILL.md')
    expect(store.error).toContain('无 SKILL.md')
  })

  it('importMCPServers 部分成功（skipped 非空）不抛错，返回结果并刷新清单', async () => {
    const store = useSharedExtStore()
    fns.importSharedMCPServers.mockResolvedValueOnce({
      imported: [server('n1')],
      skipped: ['dup（同名已存在，跳过）'],
    })
    fns.getSharedMCPServers.mockResolvedValueOnce([server('n1')])

    const result = await store.importMCPServers([server('n1'), server('dup')])
    expect(result.imported).toHaveLength(1)
    expect(result.skipped[0]).toContain('dup')
    expect(store.error).toBe('')
    expect(fns.getSharedMCPServers).toHaveBeenCalledTimes(1)
  })

  it('importMCPServers 空导入（imported=0）不刷新清单；registry 错误写 error 并抛出', async () => {
    const store = useSharedExtStore()
    fns.importSharedMCPServers.mockResolvedValueOnce({ imported: [], skipped: [] })
    await store.importMCPServers([])
    expect(fns.getSharedMCPServers).not.toHaveBeenCalled()

    fns.importSharedMCPServers.mockRejectedValueOnce(new Error('write fail'))
    await expect(store.importMCPServers([server('x')])).rejects.toThrow('write fail')
    expect(store.error).toContain('write fail')
  })

  it('MCP 删除/启停/目标变更成功后刷新清单', async () => {
    const store = useSharedExtStore()
    fns.getSharedMCPServers.mockResolvedValue([])

    fns.deleteMCPServer.mockResolvedValueOnce(undefined)
    await store.deleteMCPServer('m')
    expect(fns.getSharedMCPServers).toHaveBeenCalledTimes(1)

    fns.setMCPServerEnabled.mockResolvedValueOnce(undefined)
    await store.setMCPServerEnabled('m', true)
    expect(fns.setMCPServerEnabled).toHaveBeenCalledWith('m', true)
    expect(fns.getSharedMCPServers).toHaveBeenCalledTimes(2)

    fns.setMCPServerTargets.mockResolvedValueOnce(undefined)
    await store.setMCPServerTargets('m', ['codex'])
    expect(fns.setMCPServerTargets).toHaveBeenCalledWith('m', ['codex'])
    expect(fns.getSharedMCPServers).toHaveBeenCalledTimes(3)
  })

  it('previewMCPJSON 纯透传：不写 state、非法文本原样抛错', async () => {
    const store = useSharedExtStore()
    fns.previewMCPJSON.mockResolvedValueOnce([server('p')])
    const servers = await store.previewMCPJSON('{"mcpServers":{"p":{}}}')
    expect(servers.map((s) => s.name)).toEqual(['p'])

    fns.previewMCPJSON.mockRejectedValueOnce(new Error('无法识别的 MCP JSON 格式'))
    await expect(store.previewMCPJSON('not-json')).rejects.toThrow('无法识别')
    expect(store.error).toBe('') // 预览错误就地展示，不污染全局 error
  })

  it('并发加载的 loading 合并态：任一在飞即 true，全部结束回落 false', async () => {
    const store = useSharedExtStore()
    let resolveSkills: (v: unknown) => void = () => {}
    fns.getSharedSkills.mockReturnValueOnce(new Promise((r) => { resolveSkills = r }))
    fns.getAggregatedSkills.mockResolvedValueOnce([])

    const p1 = store.loadSkills()
    const p2 = store.loadAggregated()
    expect(store.loading).toBe(true)
    resolveSkills([])
    await Promise.all([p1, p2])
    expect(store.loading).toBe(false)
  })
})
