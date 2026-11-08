import { describe, expect, it, vi } from 'vitest'

// 在 wailsjs 绑定边界打桩：api 包装层的职责是透传（P3-S1 WSL 配置供给状态
// 与视觉清单共享同意入口）。
const { statusBinding, consentBinding, unifiedEnableBinding, unifiedDisableBinding } = vi.hoisted(() => ({
  statusBinding: vi.fn(),
  consentBinding: vi.fn(),
  unifiedEnableBinding: vi.fn(),
  unifiedDisableBinding: vi.fn(),
}))

// 单一 mock 工厂：全部 App 绑定集中注册（P3-S1/S2/S3/S4 + P2-B 统一管理
// 共用；多个叠加的 vi.mock 工厂在同模块上胜者不确定，全量跑时会让后批绑定
// 解析失败）。
vi.mock('../../../wailsjs/go/main/App', () => ({
  GetWSLConfigSupplyStatus: statusBinding,
  GrantWSLVisionConsent: consentBinding,
  EnableWSLUnifiedSupply: unifiedEnableBinding,
  DisableWSLUnifiedSupply: unifiedDisableBinding,
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
        { key: 'vision-export', label: '视觉清单共享', state: 'awaiting-consent', detail: '尚未授权向 WSL 共享密钥副本', lastCheck: '' },
        { key: 'modality-kb', label: '多模态学习层', state: 'synced', detail: '已同步', lastCheck: '2026-10-16T12:00:00Z' },
      ],
      pendingPush: false,
    })

    const result = await getWSLConfigSupplyStatus()

    expect(statusBinding).toHaveBeenCalledTimes(1)
    expect(result.distro).toBe('Ubuntu')
    expect(result.faces).toHaveLength(2)
    expect(result.faces[0].state).toBe('awaiting-consent')
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
// 其余 App 绑定（S3 技能 / S4 CLI 配置与资源 / OpenCode MCP / 统一管理）。
const {
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

// ---------------------------------------------------------------------------
// P2-B：统一配置管理（一次启用 → 启动/保存/对账自动同步）。
import { enableWSLUnifiedSupply, disableWSLUnifiedSupply } from '../../api/wslSupply'

describe('api.wslSupply 统一配置管理 (P2-B)', () => {
  it('enableWSLUnifiedSupply 透传逐单元授权结果（不汇总成单一成功）', async () => {
    unifiedEnableBinding.mockResolvedValueOnce([
      { unit: 'claude/mcp', outcome: 'applied', detail: '已授权统一管理' },
      { unit: 'shared/vision-export', outcome: 'fresh', detail: '此前已授权，保持不变' },
      { unit: 'claude/settings', outcome: 'blocked', detail: '已保留本地（停跟随），跳过自动授权' },
    ])
    const outcomes = await enableWSLUnifiedSupply()
    expect(unifiedEnableBinding).toHaveBeenCalledTimes(1)
    expect(outcomes).toHaveLength(3)
    expect(outcomes[0].outcome).toBe('applied')
    expect(outcomes[2].outcome).toBe('blocked')
  })

  it('enableWSLUnifiedSupply 携带 unified 状态位返回前可见（状态快照透传）', async () => {
    statusBinding.mockResolvedValueOnce({
      distro: 'Ubuntu',
      available: true,
      unified: true,
      faces: [],
      pendingPush: false,
    })
    const st = await getWSLConfigSupplyStatus()
    expect(st.unified).toBe(true)
  })

  it('disableWSLUnifiedSupply 透传停止动作', async () => {
    unifiedDisableBinding.mockResolvedValueOnce(undefined)
    await disableWSLUnifiedSupply()
    expect(unifiedDisableBinding).toHaveBeenCalledTimes(1)
  })

  it('启用失败沿拒绝面透出（目标身份不可验证等）', async () => {
    unifiedEnableBinding.mockRejectedValueOnce(new Error('启用统一管理失败：无法验证 WSL 目标身份'))
    await expect(enableWSLUnifiedSupply()).rejects.toThrow('无法验证 WSL 目标身份')
  })
})
