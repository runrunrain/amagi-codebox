import { describe, expect, it, vi } from 'vitest'

// 在 wailsjs 绑定边界打桩：api 包装层的职责是透传（P3-S1 WSL 配置供给状态
// 与视觉清单共享同意入口）。
const { statusBinding, consentBinding } = vi.hoisted(() => ({
  statusBinding: vi.fn(),
  consentBinding: vi.fn(),
}))

// 单一 mock 工厂：全部 App 绑定集中注册（P3-S1/S2/S3/S4 共用；多个叠加的
// vi.mock 工厂在同模块上胜者不确定，全量跑时会让后批绑定解析失败）。
vi.mock('../../../wailsjs/go/main/App', () => ({
  GetWSLConfigSupplyStatus: statusBinding,
  GrantWSLVisionConsent: consentBinding,
  CheckWSLPiConfigSupply: checkBinding,
  PreviewWSLPiConfigSupply: previewBinding,
  ApplyWSLPiConfigSupply: applyBinding,
  KeepWSLPiConfigUnitLocal: keepBinding,
  TakeOverWSLPiConfigUnit: takeOverBinding,
  EnableWSLPiConfigSupply: enableBinding,
  GetWSLSkillsSupplyStatus: skillsStatusBinding,
  CheckWSLSkillsSupply: skillsCheckBinding,
  PreviewWSLSkillsSupply: skillsPreviewBinding,
  ApplyWSLSkillsSupply: skillsApplyBinding,
  KeepWSLSkillLocal: skillsKeepBinding,
  TakeOverWSLSkill: skillsTakeOverBinding,
  GetWSLCliSupplyStatus: cliStatusBinding,
  PreviewWSLCliConfigSupply: cliPreviewBinding,
  ImportWSLCliConfigKeys: cliImportBinding,
  ApplyWSLCliConfigSupply: cliApplyBinding,
  KeepWSLCliConfigUnitLocal: cliKeepBinding,
  TakeOverWSLCliConfigUnit: cliTakeOverBinding,
  CheckWSLCliResourcesSupply: cliResCheckBinding,
  PreviewWSLCliResourcesSupply: cliResPreviewBinding,
  ApplyWSLCliResourcesSupply: cliResApplyBinding,
  KeepWSLCliResourceLocal: cliResKeepBinding,
  TakeOverWSLCliResource: cliResTakeOverBinding,
  GetWSLOpenCodeMCPPreview: ocPreviewBinding,
  SetWSLOpenCodeMCPSelection: ocSetBinding,
}))

import { getWSLConfigSupplyStatus, grantWSLVisionConsent } from '../../api/wslSupply'

describe('api.wslSupply', () => {
  it('getWSLConfigSupplyStatus 透传脱敏快照', async () => {
    statusBinding.mockResolvedValueOnce({
      distro: 'Ubuntu',
      available: true,
      faces: [
        { key: 'providers', label: '托管 Provider (Pi)', state: 'synced', detail: '已同步', lastCheck: '2026-10-16T12:00:00Z' },
        { key: 'vision-export', label: '视觉清单共享', state: 'awaiting-consent', detail: '尚未授权向 WSL 共享密钥副本', lastCheck: '' },
      ],
      pendingPush: false,
    })

    const result = await getWSLConfigSupplyStatus()

    expect(statusBinding).toHaveBeenCalledTimes(1)
    expect(result.distro).toBe('Ubuntu')
    expect(result.faces).toHaveLength(2)
    expect(result.faces[1].state).toBe('awaiting-consent')
  })

  it('grantWSLVisionConsent 透传授权动作', async () => {
    consentBinding.mockResolvedValueOnce(undefined)

    await grantWSLVisionConsent()

    expect(consentBinding).toHaveBeenCalledTimes(1)
  })

  it('后端错误沿 Promise 拒绝面透出（授权失败可见）', async () => {
    consentBinding.mockRejectedValueOnce(new Error('授权失败：未找到可用的 WSL 发行版'))
    await expect(grantWSLVisionConsent()).rejects.toThrow('授权失败')
  })
})

// ---------------------------------------------------------------------------
// P3-S2：Pi 配置键级投影动作面（检查/预览/应用/保留本地/接管）。
const {
  checkBinding,
  previewBinding,
  applyBinding,
  keepBinding,
  takeOverBinding,
  enableBinding,
  skillsStatusBinding,
  skillsCheckBinding,
  skillsPreviewBinding,
  skillsApplyBinding,
  skillsKeepBinding,
  skillsTakeOverBinding,
  cliStatusBinding,
  cliPreviewBinding,
  cliImportBinding,
  cliApplyBinding,
  cliKeepBinding,
  cliTakeOverBinding,
  cliResCheckBinding,
  cliResPreviewBinding,
  cliResApplyBinding,
  cliResKeepBinding,
  cliResTakeOverBinding,
  ocPreviewBinding,
  ocSetBinding,
} = vi.hoisted(() => ({
  checkBinding: vi.fn(),
  previewBinding: vi.fn(),
  applyBinding: vi.fn(),
  keepBinding: vi.fn(),
  takeOverBinding: vi.fn(),
  enableBinding: vi.fn(),
  skillsStatusBinding: vi.fn(),
  skillsCheckBinding: vi.fn(),
  skillsPreviewBinding: vi.fn(),
  skillsApplyBinding: vi.fn(),
  skillsKeepBinding: vi.fn(),
  skillsTakeOverBinding: vi.fn(),
  cliStatusBinding: vi.fn(),
  cliPreviewBinding: vi.fn(),
  cliImportBinding: vi.fn(),
  cliApplyBinding: vi.fn(),
  cliKeepBinding: vi.fn(),
  cliTakeOverBinding: vi.fn(),
  cliResCheckBinding: vi.fn(),
  cliResPreviewBinding: vi.fn(),
  cliResApplyBinding: vi.fn(),
  cliResKeepBinding: vi.fn(),
  cliResTakeOverBinding: vi.fn(),
  ocPreviewBinding: vi.fn(),
  ocSetBinding: vi.fn(),
}))

import {
  checkWSLPiConfigSupply,
  previewWSLPiConfigSupply,
  applyWSLPiConfigSupply,
  keepWSLPiConfigUnitLocal,
  takeOverWSLPiConfigUnit,
} from '../../api/wslSupply'

describe('api.wslSupply S2 动作面', () => {
  it('checkWSLPiConfigSupply 透传检查动作（只读探测）', async () => {
    checkBinding.mockResolvedValueOnce({ distro: 'Ubuntu', available: true, faces: [], units: [], pendingPush: false })
    const st = await checkWSLPiConfigSupply()
    expect(checkBinding).toHaveBeenCalledTimes(1)
    expect(st.units).toEqual([])
  })

  it('previewWSLPiConfigSupply 透传逐键预览与接管令牌', async () => {
    previewBinding.mockResolvedValueOnce([
      {
        unit: 'pi/mcp', relPath: 'mcp.json', policy: 'TrackedProjection',
        action: 'blocked', blockedReason: 'unmanaged-existing', frozen: true,
        targetHash: 'abc123', notes: [],
        keys: [
          { key: 'mcpServers/web', action: 'unmanaged-existing', reason: 'WSL 侧已有同名条目（用户自有），保留原样待确认接管' },
        ],
      },
    ])
    const rows = await previewWSLPiConfigSupply()
    expect(rows[0].targetHash).toBe('abc123')
    expect(rows[0].keys[0].action).toBe('unmanaged-existing')
  })

  it('apply/keepLocal/takeOver 透传单元与 CAS 令牌', async () => {
    applyBinding.mockResolvedValueOnce([{ unit: 'pi/amagi', outcome: 'applied', detail: 'partial; ...' }])
    keepBinding.mockResolvedValueOnce(undefined)
    takeOverBinding.mockResolvedValueOnce(undefined)

    await applyWSLPiConfigSupply()
    await keepWSLPiConfigUnitLocal('pi/mcp')
    await takeOverWSLPiConfigUnit('pi/mcp', 'abc123')

    expect(applyBinding).toHaveBeenCalledTimes(1)
    expect(keepBinding).toHaveBeenCalledWith('pi/mcp')
    expect(takeOverBinding).toHaveBeenCalledWith('pi/mcp', 'abc123')
  })

  it('接管过期错误沿拒绝面透出（旧预览不覆盖新编辑）', async () => {
    takeOverBinding.mockRejectedValueOnce(new Error('接管失败：预览已过期（WSL 侧文件已变化，旧预览不覆盖新编辑）；请重新检查后再接管'))
    await expect(takeOverWSLPiConfigUnit('pi/mcp', 'stale')).rejects.toThrow('预览已过期')
  })
})

// F-3（S2 diting）：启用投影（per-target 同意门）入口（绑定已并入首部
// 单一 mock 工厂）。

import { enableWSLPiConfigSupply } from '../../api/wslSupply'

describe('api.wslSupply 启用投影（F-3 同意门）', () => {
  it('enableWSLPiConfigSupply 透传三单元授权动作', async () => {
    enableBinding.mockResolvedValueOnce(undefined)
    await enableWSLPiConfigSupply()
    expect(enableBinding).toHaveBeenCalledTimes(1)
  })

  it('未同意状态在 units 快照中透出（awaiting-consent）', async () => {
    checkBinding.mockResolvedValueOnce({
      distro: 'Ubuntu', available: true, faces: [], pendingPush: false,
      units: [
        { unit: 'pi/mcp', relPath: 'mcp.json', policy: 'TrackedProjection', state: 'awaiting-consent', detail: '尚未同意向 WSL 投影该配置（同意后未手改条目跟随 Windows 面板）', actions: ['consent', 'preview'] },
      ],
    })
    const st = await checkWSLPiConfigSupply()
    expect(st.units[0].state).toBe('awaiting-consent')
    expect(st.units[0].actions).toContain('consent')
  })
})

// ---------------------------------------------------------------------------
// P3-S3：共享技能面（扫描/预览/勾选导入/保留本地/接管；绑定已并入首部
// 单一 mock 工厂）。

import {
  getWSLSkillsSupplyStatus,
  checkWSLSkillsSupply,
  previewWSLSkillsSupply,
  applyWSLSkillsSupply,
  keepWSLSkillLocal,
  takeOverWSLSkill,
} from '../../api/wslSupply'

describe('api.wslSupply skills (P3-S3)', () => {
  it('getWSLSkillsSupplyStatus 透传扫描进度与逐技能状态（扫描未完成不冒充）', async () => {
    skillsStatusBinding.mockResolvedValueOnce({
      distro: 'Ubuntu',
      available: true,
      scanStarted: true,
      scanRunning: true,
      scanScanned: 3,
      scanTotal: 12,
      scanIncomplete: true,
      skills: [
        { name: 'alpha', state: 'importable', detail: '可移植，未选择导入', selectable: true, actions: ['preview'] },
        { name: 'win', state: 'incompatible', detail: '含 1 项不兼容内容', incompatible: ['run.ps1：Windows 专用脚本（未支持）'], selectable: false, actions: ['preview'] },
      ],
    })

    const st = await getWSLSkillsSupplyStatus()

    expect(st.scanRunning).toBe(true)
    expect(st.scanIncomplete).toBe(true)
    expect(st.skills[1].incompatible?.[0]).toContain('run.ps1')
    expect(st.skills[0].selectable).toBe(true)
  })

  it('applyWSLSkillsSupply 透传勾选名单并返回逐项结果', async () => {
    skillsApplyBinding.mockResolvedValueOnce([{ unit: 'shared/skill/alpha', outcome: 'applied', detail: 'written' }])
    const out = await applyWSLSkillsSupply(['alpha'])
    expect(skillsApplyBinding).toHaveBeenCalledWith(['alpha'])
    expect(out[0].outcome).toBe('applied')
  })

  it('checkWSLSkillsSupply / previewWSLSkillsSupply 透传检查与预览动作', async () => {
    skillsCheckBinding.mockResolvedValueOnce({ distro: 'Ubuntu', available: true, skills: [] })
    skillsPreviewBinding.mockResolvedValueOnce([
      { name: 'alpha', portable: true, action: 'write', targetHash: 'tok', incompatible: [], excluded: 2 },
    ])
    await expect(checkWSLSkillsSupply()).resolves.toMatchObject({ distro: 'Ubuntu' })
    const rows = await previewWSLSkillsSupply()
    expect(rows[0].portable).toBe(true)
    expect(rows[0].excluded).toBe(2)
  })

  it('keepWSLSkillLocal / takeOverWSLSkill 透传停跟随与接管', async () => {
    skillsKeepBinding.mockResolvedValueOnce(undefined)
    skillsTakeOverBinding.mockResolvedValueOnce(undefined)
    await keepWSLSkillLocal('alpha')
    await takeOverWSLSkill('alpha', 'tok')
    expect(skillsKeepBinding).toHaveBeenCalledWith('alpha')
    expect(skillsTakeOverBinding).toHaveBeenCalledWith('alpha', 'tok')
  })
})

// ---------------------------------------------------------------------------
// P3-S4：Claude / Codex 用户配置导入面 + OpenCode 附加层 MCP 选择面
//（绑定已并入首部单一 mock 工厂）。

import {
  getWSLCliSupplyStatus,
  previewWSLCliConfigSupply,
  importWSLCliConfigKeys,
  applyWSLCliConfigSupply,
  keepWSLCliConfigUnitLocal,
  takeOverWSLCliConfigUnit,
  checkWSLCliResourcesSupply,
  previewWSLCliResourcesSupply,
  applyWSLCliResourcesSupply,
  keepWSLCliResourceLocal,
  takeOverWSLCliResource,
  getWSLOpenCodeMCPPreview,
  setWSLOpenCodeMCPSelection,
} from '../../api/wslSupply'

describe('api.wslSupply S4 CLI 用户配置导入', () => {
  it('getWSLCliSupplyStatus 透传键级单元与资源扫描状态', async () => {
    cliStatusBinding.mockResolvedValueOnce({
      distro: 'Ubuntu',
      available: true,
      units: [
        { unit: 'claude/mcp', relPath: '~/.claude.json', policy: 'TrackedProjection', state: 'awaiting-consent', detail: '尚未选择导入项；预览后勾选导入', actions: ['preview'] },
      ],
      resources: { distro: 'Ubuntu', available: true, scanStarted: true, scanRunning: false, skills: [] },
    })
    const st = await getWSLCliSupplyStatus('claude')
    expect(cliStatusBinding).toHaveBeenCalledWith('claude')
    expect(st.units[0].unit).toBe('claude/mcp')
    expect(st.resources.scanStarted).toBe(true)
  })

  it('previewWSLCliConfigSupply 透传转换前→转换后对照与选择状态', async () => {
    cliPreviewBinding.mockResolvedValueOnce([
      {
        unit: 'claude/mcp', relPath: '~/.claude.json', policy: 'TrackedProjection',
        action: 'write', blockedReason: '', frozen: false, targetHash: 'tok1', notes: [],
        keys: [
          { key: 'mcpServers/fetch', action: 'add', reason: '新增（来自 Windows）', before: 'stdio npx.cmd（参数 2 个）', after: 'stdio npx（参数 2 个）', selected: true, importable: true },
          { key: 'mcpServers/bat', action: 'add', reason: '未选择导入；勾选后随导入写入', before: 'stdio run.bat（参数 0 个）', after: '命令 "run.bat" 为 Windows 专属形态…', selected: false, importable: true },
        ],
      },
    ])
    const rows = await previewWSLCliConfigSupply('claude')
    expect(cliPreviewBinding).toHaveBeenCalledWith('claude')
    expect(rows[0].keys[0].after).toContain('npx')
    expect(rows[0].keys[0].selected).toBe(true)
    expect(rows[0].keys[1].importable).toBe(true)
  })

  it('importWSLCliConfigKeys 透传选择集（仅勾选项导入）', async () => {
    cliImportBinding.mockResolvedValueOnce([{ unit: 'claude/mcp', outcome: 'applied', detail: 'written' }])
    const out = await importWSLCliConfigKeys('claude', 'claude/mcp', ['web', 'fetch'])
    expect(cliImportBinding).toHaveBeenCalledWith('claude', 'claude/mcp', ['web', 'fetch'])
    expect(out[0].outcome).toBe('applied')
  })

  it('apply/keepLocal/takeOver 与资源面动作透传', async () => {
    cliApplyBinding.mockResolvedValueOnce([])
    cliKeepBinding.mockResolvedValueOnce(undefined)
    cliTakeOverBinding.mockResolvedValueOnce(undefined)
    cliResCheckBinding.mockResolvedValueOnce({ distro: 'Ubuntu', available: true, skills: [] })
    cliResPreviewBinding.mockResolvedValueOnce([{ name: 'CLAUDE.md', portable: true, action: 'write', targetHash: 'tok2' }])
    cliResApplyBinding.mockResolvedValueOnce([{ unit: 'claude/res/CLAUDE.md', outcome: 'applied', detail: 'written' }])
    cliResKeepBinding.mockResolvedValueOnce(undefined)
    cliResTakeOverBinding.mockResolvedValueOnce(undefined)

    await applyWSLCliConfigSupply('codex')
    await keepWSLCliConfigUnitLocal('codex', 'codex/config')
    await takeOverWSLCliConfigUnit('codex', 'codex/config', 'tok3')
    await checkWSLCliResourcesSupply('claude')
    await previewWSLCliResourcesSupply('claude')
    await applyWSLCliResourcesSupply('claude', ['CLAUDE.md'])
    await keepWSLCliResourceLocal('claude', 'CLAUDE.md')
    await takeOverWSLCliResource('claude', 'CLAUDE.md', 'tok2')

    expect(cliApplyBinding).toHaveBeenCalledWith('codex')
    expect(cliKeepBinding).toHaveBeenCalledWith('codex', 'codex/config')
    expect(cliTakeOverBinding).toHaveBeenCalledWith('codex', 'codex/config', 'tok3')
    expect(cliResApplyBinding).toHaveBeenCalledWith('claude', ['CLAUDE.md'])
    expect(cliResTakeOverBinding).toHaveBeenCalledWith('claude', 'CLAUDE.md', 'tok2')
  })
})

describe('api.wslSupply S4 OpenCode 附加层 MCP 选择', () => {
  it('getWSLOpenCodeMCPPreview 透传形态分类与全局同名冲突', async () => {
    ocPreviewBinding.mockResolvedValueOnce([
      { name: 'web', remote: true, nativeCommand: '', needsAdapt: false, reason: '', globalConflict: false, overrideGlobal: false },
      { name: 'fetch', remote: false, nativeCommand: 'npx', needsAdapt: false, reason: '', globalConflict: true, overrideGlobal: false },
      { name: 'bat', remote: false, nativeCommand: '', needsAdapt: true, reason: '命令 "setup.bat" 为 Windows 专属形态…', globalConflict: false, overrideGlobal: false },
    ])
    const rows = await getWSLOpenCodeMCPPreview()
    expect(rows[1].globalConflict).toBe(true)
    expect(rows[2].needsAdapt).toBe(true)
  })

  it('setWSLOpenCodeMCPSelection 透传覆盖名单', async () => {
    ocSetBinding.mockResolvedValueOnce(undefined)
    await setWSLOpenCodeMCPSelection(['fetch'])
    expect(ocSetBinding).toHaveBeenCalledWith(['fetch'])
  })
})
