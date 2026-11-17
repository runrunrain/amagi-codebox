import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

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

import { useCommonMCPPanel } from '../../../../components/extensions/common/useCommonMCPPanel';
import { useMCPImportDialog } from '../../../../components/extensions/common/useMCPImportDialog';
import { useToast } from '../../../../composables/useToast';

function server(name: string, overrides: Record<string, unknown> = {}) {
  return {
    name,
    config: { command: `cmd-${name}` },
    origin: 'manual',
    enabled: true,
    targets: ['claude'],
    deployed: [],
    createdAt: '',
    ...overrides,
  }
}

describe('useCommonMCPPanel：清单行 / 启停 / 目标 / 删除', () => {
  beforeEach(() => {
    for (const m of Object.values(fns)) m.mockReset()
    useToast().toasts.splice(0)
  })

  async function freshPanel() {
    setActivePinia(createPinia())
    fns.getSharedMCPServers.mockResolvedValue([server('context7'), server('fetch', { config: { url: 'https://x/sse' }, enabled: false })])
    const panel = useCommonMCPPanel()
    await panel.initialLoad()
    return panel
  }

  it('清单渲染：command/url 摘要、启停态、targets 勾选态', async () => {
    const panel = await freshPanel()
    expect(panel.initialError.value).toBe('')
    const rows = panel.rows.value
    expect(rows.map((r) => r.name)).toEqual(['context7', 'fetch'])
    expect(rows[0].summary).toBe('cmd-context7')
    expect(rows[1].summary).toBe('https://x/sse')
    expect(rows[1].enabled).toBe(false)
    expect(rows[0].targets).toEqual(['claude'])
  })

  it('启停调 setMCPServerEnabled；目标勾选调 setMCPServerTargets（差集保存）', async () => {
    const panel = await freshPanel()
    fns.setMCPServerEnabled.mockResolvedValueOnce(undefined)
    await panel.handleToggleEnabled('fetch', true)
    expect(fns.setMCPServerEnabled).toHaveBeenCalledWith('fetch', true)

    fns.setMCPServerTargets.mockResolvedValueOnce(undefined)
    fns.getSharedMCPServers.mockResolvedValue([server('context7', { targets: ['claude', 'codex'] })])
    await panel.handleToggleTarget('context7', 'codex')
    expect(fns.setMCPServerTargets).toHaveBeenCalledWith('context7', ['claude', 'codex'])
  })

  it('删除确认流：确认后调 deleteMCPServer 并 toast；失败保留对话框', async () => {
    const panel = await freshPanel()
    panel.requestDelete(panel.rows.value[0])
    expect(panel.showDeleteDialog.value).toBe(true)

    fns.deleteMCPServer.mockResolvedValueOnce(undefined)
    await panel.confirmDelete()
    expect(fns.deleteMCPServer).toHaveBeenCalledWith('context7')
    expect(panel.showDeleteDialog.value).toBe(false)
    expect(useToast().toasts.some((t) => t.message.includes('已删除「context7」'))).toBe(true)

    // 失败路径：对话框保持打开，错误 toast
    panel.requestDelete(panel.rows.value[1])
    fns.deleteMCPServer.mockRejectedValueOnce(new Error('in use'))
    await panel.confirmDelete()
    expect(panel.showDeleteDialog.value).toBe(true)
    expect(useToast().toasts.some((t) => t.type === 'error' && t.message.includes('删除失败'))).toBe(true)
  })
})

describe('useMCPImportDialog：粘贴/选文件 → 解析预览 → 导入流', () => {
  beforeEach(() => {
    for (const m of Object.values(fns)) m.mockReset()
    useToast().toasts.splice(0)
  })

  function freshDialog() {
    setActivePinia(createPinia())
    return useMCPImportDialog()
  }

  it('预览流：解析填充 server 名+摘要列表；空映射与非法 JSON 就地报错', async () => {
    const dialog = freshDialog()
    dialog.openDialog()
    dialog.text.value = '{"context7": {"command": "npx"}}'
    fns.previewMCPJSON.mockResolvedValueOnce([server('context7')])

    await dialog.handleParse()
    expect(dialog.preview.value).toEqual([{ name: 'context7', summary: 'cmd-context7' }])
    expect(dialog.previewError.value).toBe('')
    expect(dialog.canImport.value).toBe(true)

    // 空映射：不算可导入
    dialog.text.value = '{}'
    fns.previewMCPJSON.mockResolvedValueOnce([])
    await dialog.handleParse()
    expect(dialog.preview.value).toEqual([])
    expect(dialog.previewError.value).toContain('未解析到任何 MCP server')
    expect(dialog.canImport.value).toBe(false)

    // 非法 JSON：后端报错就地展示，不进全局 error
    dialog.text.value = 'not-json'
    fns.previewMCPJSON.mockRejectedValueOnce(new Error('JSON 语法错误'))
    await dialog.handleParse()
    expect(dialog.previewError.value).toBe('JSON 语法错误')
  })

  it('修改文本后旧预览失效（不能导入旧解析结果）', async () => {
    const dialog = freshDialog()
    dialog.openDialog()
    dialog.text.value = '{"a": {"command": "x"}}'
    fns.previewMCPJSON.mockResolvedValueOnce([server('a')])
    await dialog.handleParse()
    expect(dialog.canImport.value).toBe(true)

    dialog.text.value = '{"b": {"command": "y"}}'
    expect(dialog.preview.value).toEqual([])
    expect(dialog.canImport.value).toBe(false)
  })

  it('导入流：imported 成功 toast + 关闭；skipped 部分成功不抛错并单独提示「已跳过同名」', async () => {
    const dialog = freshDialog()
    dialog.openDialog()
    dialog.text.value = '{"a": {"command": "x"}, "dup": {"command": "y"}}'
    fns.previewMCPJSON.mockResolvedValueOnce([server('a'), server('dup')])
    await dialog.handleParse()

    fns.importSharedMCPServers.mockResolvedValueOnce({ imported: [server('a')], skipped: ['dup'] })
    await dialog.handleImport()

    expect(fns.importSharedMCPServers).toHaveBeenCalledTimes(1)
    const payload = fns.importSharedMCPServers.mock.calls[0][0]
    expect(payload.map((s: { name: string }) => s.name)).toEqual(['a', 'dup']) // 预览结果整体作为载荷
    expect(dialog.open.value).toBe(false)
    const toasts = useToast().toasts.map((t) => `${t.type}:${t.message}`)
    expect(toasts).toContain('success:已导入 1 个 MCP server')
    expect(toasts).toContain('warn:已跳过同名: dup')
  })

  it('导入失败：错误 toast，对话框保持打开可重试', async () => {
    const dialog = freshDialog()
    dialog.openDialog()
    dialog.text.value = '{"a": {"command": "x"}}'
    fns.previewMCPJSON.mockResolvedValueOnce([server('a')])
    await dialog.handleParse()

    fns.importSharedMCPServers.mockRejectedValueOnce(new Error('registry 写入失败'))
    await dialog.handleImport()
    expect(dialog.open.value).toBe(true)
    expect(useToast().toasts.some((t) => t.type === 'error' && t.message.includes('导入失败'))).toBe(true)
    expect(dialog.importing.value).toBe(false) // busy 复位，可再次提交
  })

  it('从文件导入：pick → readTextFile 回填 textarea；取消不动作', async () => {
    const dialog = freshDialog()
    dialog.openDialog()
    fns.pickMCPJsonFile.mockResolvedValueOnce('/tmp/mcp.json')
    fns.readTextFile.mockResolvedValueOnce('{"from-file": {"command": "f"}}')

    await dialog.handlePickFile()
    expect(dialog.text.value).toBe('{"from-file": {"command": "f"}}')
    expect(useToast().toasts.some((t) => t.message.includes('已读取文件'))).toBe(true)

    fns.pickMCPJsonFile.mockResolvedValueOnce('') // 取消
    await dialog.handlePickFile()
    expect(fns.readTextFile).toHaveBeenCalledTimes(1)
  })
})
