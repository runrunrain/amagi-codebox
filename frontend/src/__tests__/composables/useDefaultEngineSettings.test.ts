import { reactive } from 'vue'
import { describe, expect, it } from 'vitest'
import { defaultEngineOptions, useDefaultEngineSettings } from '../../composables/useDefaultEngineSettings'

function savedDefaults() {
  return reactive({
    claudeMode: 'embedded', claudeShell: 'zsh',
    openCodeMode: 'terminal', openCodeShell: 'bash',
    codexMode: 'embedded', codexShell: 'zsh',
    piMode: 'terminal', piShell: 'bash',
    ompMode: 'embedded', ompShell: 'zsh',
  })
}

const engines = [
  ['claude', 'claudeMode', 'claudeShell'],
  ['opencode', 'openCodeMode', 'openCodeShell'],
  ['codex', 'codexMode', 'codexShell'],
  ['pi', 'piMode', 'piShell'],
  ['omp', 'ompMode', 'ompShell'],
] as const

describe('仪表盘默认配置的五引擎编辑', () => {
  it('为全部五个引擎提供入口', () => {
    expect(defaultEngineOptions.map((option) => option.value)).toEqual(engines.map(([engine]) => engine))
  })

  it.each(engines)('%s 读取已保存值，修改仅落到对应字段', (engine, modeKey, shellKey) => {
    const defaults = savedDefaults()
    const before = { ...defaults }
    const editor = useDefaultEngineSettings(defaults)
    editor.activeEngineTab.value = engine
    expect(editor.currentEngineMode.value).toBe(before[modeKey])
    expect(editor.currentEngineShell.value).toBe(before[shellKey])

    const mode = before[modeKey] === 'embedded' ? 'terminal' : 'embedded'
    editor.currentEngineMode.value = mode
    editor.currentEngineShell.value = ''
    expect({ ...defaults }).toEqual({ ...before, [modeKey]: mode, [shellKey]: '' })

    // 切换 Tab 不重置编辑结果；defaults 本身即保存载荷的数据源。
    editor.activeEngineTab.value = engine === 'claude' ? 'pi' : 'claude'
    editor.activeEngineTab.value = engine
    expect(editor.currentEngineMode.value).toBe(mode)
    expect(editor.currentEngineShell.value).toBe('')
  })

  it('异步加载默认值后，当前编辑项跟随更新', () => {
    const defaults = savedDefaults()
    const editor = useDefaultEngineSettings(defaults)
    editor.activeEngineTab.value = 'omp'
    Object.assign(defaults, { ompMode: 'terminal', ompShell: 'bash' })
    expect(editor.currentEngineMode.value).toBe('terminal')
    expect(editor.currentEngineShell.value).toBe('bash')
  })

  it('未知引擎不回落到 Codex 或污染其他引擎', () => {
    const defaults = savedDefaults()
    const editor = useDefaultEngineSettings(defaults)
    editor.activeEngineTab.value = 'pi'
    editor.activeEngineTab.value = 'unknown'
    expect(editor.activeEngineTab.value).toBe('pi')
    expect(defaults).toEqual(savedDefaults())
  })
})
