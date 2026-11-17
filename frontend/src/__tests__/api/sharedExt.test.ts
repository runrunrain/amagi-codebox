import { describe, expect, it, vi, beforeEach } from 'vitest'

// 在 wailsjs 绑定边界打桩：api 包装层的职责是透传参数与归一化返回。
// 双绑定面（sharedext/Service 简单 CRUD + main/App 组合入口）分别打桩；
// ImportMCPServers 双桩用于断言 api 不走「丢弃 skipped」的服务面绑定。
const svc = vi.hoisted(() => ({
  ListSharedSkills: vi.fn(),
  ImportSkillFromZip: vi.fn(),
  ImportSkillFromDir: vi.fn(),
  DeleteSharedSkill: vi.fn(),
  SetSharedSkillEnabled: vi.fn(),
  SetSharedSkillTargets: vi.fn(),
  ListSharedMCPServers: vi.fn(),
  DeleteSharedMCPServer: vi.fn(),
  SetSharedMCPServerEnabled: vi.fn(),
  SetSharedMCPServerTargets: vi.fn(),
  // 陷阱面：Wails 生成器对该方法只保留第一个返回值（skipped 被丢弃），
  // api 层禁止使用；此处打桩仅为断言零调用。
  ImportMCPServers: vi.fn(),
}))

const app = vi.hoisted(() => ({
  PreviewSharedMCPJSON: vi.fn(),
  ImportSharedMCPServers: vi.fn(),
  GetAllSharedSkillsAggregated: vi.fn(),
  PickSharedSkillZipFile: vi.fn(),
  PickSharedSkillDir: vi.fn(),
  PickMCPJSONFile: vi.fn(),
  ReadTextFile: vi.fn(),
  InvalidateSharedExtCache: vi.fn(),
}))

vi.mock('../../../wailsjs/go/sharedext/Service', () => svc)
vi.mock('../../../wailsjs/go/main/App', () => app)

import * as api from '../../api/sharedExt'

function skill(name: string) {
  return { name, description: '', sourcePath: '', origin: 'manual', enabled: false, targets: [], deployed: [], createdAt: '' }
}

function server(name: string) {
  return { name, config: { command: 'npx' }, origin: 'manual', enabled: false, targets: [], deployed: [], createdAt: '' }
}

describe('api/sharedExt 绑定路由与透传', () => {
  beforeEach(() => {
    for (const m of Object.values(svc)) m.mockReset()
    for (const m of Object.values(app)) m.mockReset()
  })

  it('简单 skill CRUD 走 sharedext/Service 绑定，列表返回归一化 null→[]', async () => {
    svc.ListSharedSkills.mockResolvedValueOnce([skill('a')])
    await expect(api.getSharedSkills()).resolves.toHaveLength(1)
    expect(svc.ListSharedSkills).toHaveBeenCalledTimes(1)

    svc.ListSharedSkills.mockResolvedValueOnce(null)
    await expect(api.getSharedSkills()).resolves.toEqual([])

    await api.deleteSkill('a')
    expect(svc.DeleteSharedSkill).toHaveBeenCalledWith('a')

    await api.setSkillEnabled('a', true)
    expect(svc.SetSharedSkillEnabled).toHaveBeenCalledWith('a', true)

    await api.setSkillTargets('a', ['claude', 'pi'])
    expect(svc.SetSharedSkillTargets).toHaveBeenCalledWith('a', ['claude', 'pi'])
  })

  it('skill 导入 zip/dir 透传路径并归一化返回', async () => {
    svc.ImportSkillFromZip.mockResolvedValueOnce([skill('z')])
    await expect(api.importSkillFromZip('/tmp/z.zip')).resolves.toHaveLength(1)
    expect(svc.ImportSkillFromZip).toHaveBeenCalledWith('/tmp/z.zip')

    svc.ImportSkillFromDir.mockResolvedValueOnce(null)
    await expect(api.importSkillFromDir('/tmp/dir')).resolves.toEqual([])
    expect(svc.ImportSkillFromDir).toHaveBeenCalledWith('/tmp/dir')
  })

  it('MCP 读/删/启停/目标走 Service；预览与导入走 App 绑定', async () => {
    svc.ListSharedMCPServers.mockResolvedValueOnce([server('m')])
    await expect(api.getSharedMCPServers()).resolves.toHaveLength(1)

    await api.deleteMCPServer('m')
    expect(svc.DeleteSharedMCPServer).toHaveBeenCalledWith('m')

    await api.setMCPServerEnabled('m', false)
    expect(svc.SetSharedMCPServerEnabled).toHaveBeenCalledWith('m', false)

    await api.setMCPServerTargets('m', ['codex'])
    expect(svc.SetSharedMCPServerTargets).toHaveBeenCalledWith('m', ['codex'])

    app.PreviewSharedMCPJSON.mockResolvedValueOnce([server('p')])
    await expect(api.previewMCPJSON('{}')).resolves.toHaveLength(1)
    expect(app.PreviewSharedMCPJSON).toHaveBeenCalledWith('{}')
  })

  it('importSharedMCPServers 走 App 信封，绝不调用丢弃 skipped 的 Service.ImportMCPServers', async () => {
    app.ImportSharedMCPServers.mockResolvedValueOnce({ imported: [server('n')], skipped: ['dup（同名已存在，跳过）'] })
    const result = await api.importSharedMCPServers([server('n'), server('dup')])
    expect(app.ImportSharedMCPServers).toHaveBeenCalledTimes(1)
    expect(result.imported).toHaveLength(1)
    expect(result.skipped[0]).toContain('dup')
    expect(svc.ImportMCPServers).not.toHaveBeenCalled()

    // 绑定返回 null 时归一化为空信封，不抛错
    app.ImportSharedMCPServers.mockResolvedValueOnce(null)
    await expect(api.importSharedMCPServers([])).resolves.toEqual({ imported: [], skipped: [] })
  })

  it('聚合视图透传 force；对话框与文件读取走 App 绑定', async () => {
    app.GetAllSharedSkillsAggregated.mockResolvedValueOnce([])
    await api.getAggregatedSkills(true)
    expect(app.GetAllSharedSkillsAggregated).toHaveBeenCalledWith(true)
    await api.getAggregatedSkills()
    expect(app.GetAllSharedSkillsAggregated).toHaveBeenLastCalledWith(false)

    await api.pickSkillZip()
    expect(app.PickSharedSkillZipFile).toHaveBeenCalledTimes(1)
    await api.pickSkillDir()
    expect(app.PickSharedSkillDir).toHaveBeenCalledTimes(1)
    await api.pickMCPJsonFile()
    expect(app.PickMCPJSONFile).toHaveBeenCalledTimes(1)

    app.ReadTextFile.mockResolvedValueOnce('{"mcpServers":{}}')
    await expect(api.readTextFile('/tmp/m.json')).resolves.toBe('{"mcpServers":{}}')
    expect(app.ReadTextFile).toHaveBeenCalledWith('/tmp/m.json')

    await api.invalidateSharedExtCache()
    expect(app.InvalidateSharedExtCache).toHaveBeenCalledTimes(1)
  })

  it('SHARED_EXT_ENGINES 为五引擎固定顺序', () => {
    expect(api.SHARED_EXT_ENGINES).toEqual(['claude', 'codex', 'opencode', 'pi', 'omp'])
  })
})
