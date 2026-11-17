import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

// 在 api/sharedExt.ts 边界打桩（组件面板测试不触 wailsjs；store 同样消费此 mock）
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
  pickSkillZip: vi.fn(),
  pickSkillDir: vi.fn(),
  pickMCPJsonFile: vi.fn(),
  readTextFile: vi.fn(),
}));

vi.mock('../../../../api/sharedExt', () => ({
  ...fns,
  SHARED_EXT_ENGINES: ['claude', 'codex', 'opencode', 'pi', 'omp'],
}))

import { useCommonSkillsPanel } from '../../../../components/extensions/common/useCommonSkillsPanel';
import { usePluginStore } from '../../../../stores/plugin';
import { useToast } from '../../../../composables/useToast';

function manualSkill(name: string, targets: string[] = []) {
  return { name, description: '', sourcePath: '', origin: 'manual', enabled: true, targets, deployed: [], createdAt: '' }
}

function manualAgg(name: string) {
  return { name, description: '', origin: 'manual', managed: true, enabled: true }
}

function pluginAgg(name: string, engine = 'codex') {
  return {
    name, description: '', origin: 'plugin', pluginEngine: engine,
    pluginId: `${engine}-pid`, pluginName: `${engine} 插件`, managed: false, enabled: true,
  }
}

/** 聚合 + registry 双清单的常规成功态。 */
function seedLists() {
  fns.getAggregatedSkills.mockResolvedValue([manualAgg('mine'), pluginAgg('p-skill')])
  fns.getSharedSkills.mockResolvedValue([manualSkill('mine', ['claude', 'pi'])])
}

async function freshPanel() {
  setActivePinia(createPinia())
  seedLists()
  const panel = useCommonSkillsPanel()
  await panel.initialLoad()
  return panel
}

describe('useCommonSkillsPanel：聚合视图与行决策', () => {
  beforeEach(() => {
    for (const m of Object.values(fns)) m.mockReset()
    useToast().toasts.splice(0) // toast 为模块级单例，逐用例清理
  })

  it('首屏聚合 manual+plugin 条目：manual 带 registry targets，plugin 行删除禁用且无勾选器', async () => {
    const panel = await freshPanel()
    expect(panel.initialLoading.value).toBe(false)
    expect(panel.initialError.value).toBe('')

    const rows = panel.rows.value
    expect(rows.map((r) => r.name)).toEqual(['mine', 'p-skill'])
    const manual = rows[0]
    expect(manual.managed).toBe(true)
    expect(manual.canDelete).toBe(true)
    expect(manual.canEditTargets).toBe(true)
    expect(manual.targets).toEqual(['claude', 'pi'])

    const plugin = rows[1]
    expect(plugin.managed).toBe(false)
    expect(plugin.canDelete).toBe(false) // 删除禁用（统一在插件处管理）
    expect(plugin.deleteTitle).toContain('来自插件')
    expect(plugin.canEditTargets).toBe(false)
    expect(plugin.sourceText).toBe('插件·codex 插件')
  })

  it('首屏加载失败进入错误态（store.error 不抛）', async () => {
    setActivePinia(createPinia())
    fns.getAggregatedSkills.mockRejectedValue(new Error('聚合失败'))
    fns.getSharedSkills.mockResolvedValue([])
    const panel = useCommonSkillsPanel()
    await panel.initialLoad()
    expect(panel.initialLoading.value).toBe(false)
    expect(panel.initialError.value).toContain('聚合失败')
  })

  it('targets 勾选触发 setSkillTargets：新集合保持引擎固定顺序；失败回滚 + toast', async () => {
    const panel = await freshPanel()
    fns.setSkillTargets.mockResolvedValueOnce(undefined)
    fns.getSharedSkills.mockResolvedValue([manualSkill('mine', ['claude', 'pi', 'codex'])])

    await panel.handleToggleTarget('mine', 'codex')
    expect(fns.setSkillTargets).toHaveBeenCalledWith('mine', ['claude', 'codex', 'pi'])

    // 保存失败：store 不变（保留上次成功保存的 targets，不含本次的 omp），仅 toast 提示
    fns.setSkillTargets.mockRejectedValueOnce(new Error('boom'))
    await panel.handleToggleTarget('mine', 'omp')
    expect(panel.rows.value[0].targets).toEqual(['claude', 'codex', 'pi'])
    const toastTexts = useToast().toasts.map((t) => t.message).join('|')
    expect(toastTexts).toContain('保存分发目标失败')
  })
})

describe('useCommonSkillsPanel：导入 / 删除 / 启停 / 跳转', () => {
  beforeEach(() => {
    for (const m of Object.values(fns)) m.mockReset()
    useToast().toasts.splice(0)
  })

  it('zip 导入流：pick 返回路径 → import → 成功 toast + 聚合刷新；用户取消（空串）不导入', async () => {
    const panel = await freshPanel()
    fns.pickSkillZip.mockResolvedValueOnce('/tmp/skills.zip')
    fns.importSkillFromZip.mockResolvedValueOnce({ imported: [manualSkill('a')], skipped: [] })
    fns.getAggregatedSkills.mockClear()

    await panel.handleImportZip()
    expect(fns.importSkillFromZip).toHaveBeenCalledWith('/tmp/skills.zip')
    expect(fns.getAggregatedSkills).toHaveBeenCalledTimes(1) // 导入后刷新聚合
    const toasts = useToast().toasts.map((t) => `${t.type}:${t.message}`)
    expect(toasts).toContain('success:已导入 1 个技能')
    expect(toasts.some((t) => t.startsWith('warn:'))).toBe(false) // 无跳过不弹 warn

    fns.pickSkillZip.mockResolvedValueOnce('') // 取消
    await panel.handleImportZip()
    expect(fns.importSkillFromZip).toHaveBeenCalledTimes(1)
  })

  it('zip 导入部分成功：成功 toast + 跳过 warn 双透出，列表仍刷新（M3）', async () => {
    const panel = await freshPanel()
    fns.pickSkillZip.mockResolvedValueOnce('/tmp/skills.zip')
    fns.importSkillFromZip.mockResolvedValueOnce({
      imported: [manualSkill('a')],
      skipped: ['dup（同名已存在，跳过）'],
    })
    fns.getAggregatedSkills.mockClear()

    await panel.handleImportZip()
    expect(fns.getAggregatedSkills).toHaveBeenCalledTimes(1) // 部分成功也刷新聚合
    const toasts = useToast().toasts.map((t) => `${t.type}:${t.message}`)
    expect(toasts).toContain('success:已导入 1 个技能')
    expect(toasts.some((t) => t.startsWith('warn:') && t.includes('dup'))).toBe(true)
    expect(toasts.some((t) => t.startsWith('error:'))).toBe(false) // 不再误报「导入失败」
  })

  it('文件夹导入失败：错误 toast，不刷新聚合', async () => {
    const panel = await freshPanel()
    fns.pickSkillDir.mockResolvedValueOnce('/tmp/dir')
    fns.importSkillFromDir.mockRejectedValueOnce(new Error('无 SKILL.md'))
    fns.getAggregatedSkills.mockClear()

    await panel.handleImportDir()
    expect(fns.getAggregatedSkills).not.toHaveBeenCalled()
    expect(useToast().toasts.some((t) => t.type === 'error' && t.message.includes('导入失败'))).toBe(true)
  })

  it('删除确认流：requestDelete 仅受理 manual 行 → confirmDelete → 刷新聚合', async () => {
    const panel = await freshPanel()
    fns.deleteSkill.mockResolvedValueOnce(undefined)
    fns.getAggregatedSkills.mockClear()

    panel.requestDelete(panel.rows.value[1]) // plugin 行：不弹确认
    expect(panel.showDeleteDialog.value).toBe(false)

    panel.requestDelete(panel.rows.value[0])
    expect(panel.showDeleteDialog.value).toBe(true)
    expect(panel.deleteTarget.value?.name).toBe('mine')

    await panel.confirmDelete()
    expect(fns.deleteSkill).toHaveBeenCalledWith('mine')
    expect(fns.getAggregatedSkills).toHaveBeenCalledTimes(1)
    expect(panel.showDeleteDialog.value).toBe(false)
    expect(useToast().toasts.some((t) => t.message.includes('已删除「mine」'))).toBe(true)
  })

  it('启停开关调 setSkillEnabled 并刷新聚合', async () => {
    const panel = await freshPanel()
    fns.setSkillEnabled.mockResolvedValueOnce(undefined)
    fns.getAggregatedSkills.mockClear()

    await panel.handleToggleEnabled('mine', false)
    expect(fns.setSkillEnabled).toHaveBeenCalledWith('mine', false)
    expect(fns.getAggregatedSkills).toHaveBeenCalledTimes(1)
  })

  it('「查看插件」跳转：切到插件管理 tab + 对应引擎 + 尽力选中 pluginId', async () => {
    const panel = await freshPanel()
    const pluginStore = usePluginStore()
    pluginStore.setExtMainTab('common')

    panel.viewPlugin(panel.rows.value[1]) // codex 插件行
    expect(pluginStore.extMainTab).toBe('plugins')
    expect(pluginStore.pluginEngine).toBe('codex')
    expect(pluginStore.activePluginId).toBe('codex-pid')

    // 未知引擎行不跳转
    panel.viewPlugin({ ...panel.rows.value[1], pluginEngine: 'wsl' })
    expect(pluginStore.pluginEngine).toBe('codex')
  })
})
