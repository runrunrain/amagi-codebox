import { describe, expect, it } from 'vitest'

// QuotaBar 是纯展示组件（node 环境无 DOM/vue 插件），阈值分档/取整决策
// 在 quotaModel.ts —— 这里测的是驱动渲染的全部分支逻辑。
import { clampPercent, quotaTone } from '../../../components/usage/quotaModel'

describe('QuotaBar 阈值色（<70 正常 / 70–90 warn / >90 danger，边界含 70/90）', () => {
  it('低于 70 归 normal 档', () => {
    expect(quotaTone(0)).toBe('normal')
    expect(quotaTone(50)).toBe('normal')
    expect(quotaTone(69.9)).toBe('normal')
  })

  it('70 为 warn 下边界（含）', () => {
    expect(quotaTone(70)).toBe('warn')
  })

  it('90 仍为 warn（含），>90 才进 danger', () => {
    expect(quotaTone(85)).toBe('warn')
    expect(quotaTone(90)).toBe('warn')
    expect(quotaTone(90.1)).toBe('danger')
    expect(quotaTone(100)).toBe('danger')
  })

  it('非法输入 fail-safe 为 normal', () => {
    expect(quotaTone(Number.NaN)).toBe('normal')
  })
})

describe('QuotaBar 宽度取值 clampPercent', () => {
  it('负数与 NaN 归 0，超界封顶 100', () => {
    expect(clampPercent(-5)).toBe(0)
    expect(clampPercent(Number.NaN)).toBe(0)
    expect(clampPercent(62)).toBe(62)
    expect(clampPercent(120)).toBe(100)
  })
})
