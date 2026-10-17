import { describe, expect, it } from 'vitest'

// SA-03 UI 决策逻辑钉子：零导入不报成功、空选择显式撤回、部分失败诚实提示。
// 后端贯通（全路径键 → 目标文件内容）由 Go 侧 App 门测试覆盖；此处覆盖驱动
// WSLCLISettings.vue 提示分支的纯逻辑。
import { cliImportOutcomeMessage } from '../../../views/settings/wslSupplyMessages'

describe('cliImportOutcomeMessage（SA-03 诚实提示）', () => {
  it('所选键全部未写入（fresh）不报成功', () => {
    const m = cliImportOutcomeMessage(2, [{ outcome: 'fresh', detail: 'no-change' }])
    expect(m.kind).toBe('info')
    expect(m.text).toContain('未写入任何条目')
  })

  it('正常导入（applied）报成功并写明写入数', () => {
    const m = cliImportOutcomeMessage(2, [
      { outcome: 'applied', detail: 'written' },
      { outcome: 'applied', detail: 'written' },
    ])
    expect(m.kind).toBe('success')
    expect(m.text).toContain('已写入 2 项')
  })

  it('部分失败（blocked 保持现状）不报成功，逐项说明', () => {
    const m = cliImportOutcomeMessage(2, [
      { outcome: 'applied', detail: 'written' },
      { outcome: 'blocked', detail: 'local-modification-frozen' },
    ])
    expect(m.kind).toBe('info')
    expect(m.text).toContain('1 项写入')
    expect(m.text).toContain('1 项保持现状')
  })

  it('空选择 + applied = 显式撤回成功（托管条目已移除）', () => {
    const m = cliImportOutcomeMessage(0, [{ outcome: 'applied', detail: 'written' }])
    expect(m.kind).toBe('success')
    expect(m.text).toContain('撤回')
  })

  it('空选择 + fresh = 无托管条目可撤回（不报成功也不报失败）', () => {
    const m = cliImportOutcomeMessage(0, [{ outcome: 'fresh', detail: 'no-change' }])
    expect(m.kind).toBe('info')
    expect(m.text).toContain('无托管条目需要移除')
  })

  it('后端未返回结果时给 info（不冒充成功）', () => {
    const m = cliImportOutcomeMessage(1, [])
    expect(m.kind).toBe('info')
  })
})
