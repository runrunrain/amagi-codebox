import { describe, expect, it } from 'vitest'
import {
  WEB_PLANE_APP_TYPES,
  autoPreferWeb,
  webPlaneCapable,
  webPlaneToggleTitle,
} from '../../../components/terminal/webPlaneGate'

// 五 AppType（internal/session/types.go）中不具备 Web 平面的三类 + 脏值。
const NON_CAPABLE = ['omp', 'opencode', 'codex', 'unknown-cli']

// ---- 支持平面集合 ----

describe('WEB_PLANE_APP_TYPES（具备 Web 平面的会话类型集合）', () => {
  it('恰为 pi 与 claudecode（新增类型须同步本断言与闸门文档）', () => {
    expect([...WEB_PLANE_APP_TYPES]).toEqual(['pi', 'claudecode'])
  })
})

describe('webPlaneCapable（切换控件 / webui 探测的类型闸门）', () => {
  it('pi 与 claudecode：具备 Web 平面', () => {
    expect(webPlaneCapable('pi')).toBe(true)
    expect(webPlaneCapable('claudecode')).toBe(true)
  })

  it('omp / opencode / codex / 未知类型：不具备（与 happy-path 成对）', () => {
    for (const appType of NON_CAPABLE) {
      expect(webPlaneCapable(appType)).toBe(false)
    }
  })

  it('undefined（会话尚未解析）/ 空串：不具备', () => {
    expect(webPlaneCapable(undefined)).toBe(false)
    expect(webPlaneCapable()).toBe(false)
    expect(webPlaneCapable('')).toBe(false)
  })

  it('严格等值：大小写变体 / 带空白 / 前缀相近值不放行', () => {
    for (const appType of ['Pi', 'PI', 'ClaudeCode', 'CLAUDECODE', ' pi', 'pi ', 'claude', 'claudecode2']) {
      expect(webPlaneCapable(appType)).toBe(false)
    }
  })
})

// ---- 自动偏好（design.md §4-D5） ----

describe('autoPreferWeb（available 时是否自动切 Web 平面）', () => {
  it('pi：自动偏好 Web 平面（装了 web 插件才 available 的既有需求）', () => {
    expect(autoPreferWeb('pi')).toBe(true)
  })

  it('claudecode：具备平面但不自动偏好（不劫持 claude TUI 既有默认视图）', () => {
    expect(webPlaneCapable('claudecode')).toBe(true)
    expect(autoPreferWeb('claudecode')).toBe(false)
  })

  it('其他类型 / undefined / 空串：不自动偏好', () => {
    for (const appType of NON_CAPABLE) {
      expect(autoPreferWeb(appType)).toBe(false)
    }
    expect(autoPreferWeb(undefined)).toBe(false)
    expect(autoPreferWeb()).toBe(false)
    expect(autoPreferWeb('')).toBe(false)
  })

  it('自动偏好的类型必然具备平面（autoPreferWeb ⊆ webPlaneCapable）', () => {
    for (const appType of [...WEB_PLANE_APP_TYPES, ...NON_CAPABLE, undefined, '']) {
      if (autoPreferWeb(appType)) expect(webPlaneCapable(appType)).toBe(true)
    }
  })
})

// ---- 切换控件提示语 ----

describe('webPlaneToggleTitle（切换控件悬浮提示）', () => {
  it('pi：提示语带 Pi 字样', () => {
    expect(webPlaneToggleTitle('pi')).toBe('在终端与 Pi 网页平面之间切换')
  })

  it('claudecode：提示语带 Claude Code 字样，不含 pi 字样', () => {
    const title = webPlaneToggleTitle('claudecode')
    expect(title).toBe('在终端与 Claude Code 网页平面之间切换')
    expect(title).not.toMatch(/\bpi\b/i)
  })

  it('集合内每个类型都有非空且互不相同的提示语', () => {
    const titles = WEB_PLANE_APP_TYPES.map((appType) => webPlaneToggleTitle(appType))
    for (const title of titles) expect(title).not.toBe('')
    expect(new Set(titles).size).toBe(WEB_PLANE_APP_TYPES.length)
  })

  it('其他类型 / undefined / 空串：空串（这些会话不显示切换控件）', () => {
    for (const appType of NON_CAPABLE) {
      expect(webPlaneToggleTitle(appType)).toBe('')
    }
    expect(webPlaneToggleTitle(undefined)).toBe('')
    expect(webPlaneToggleTitle()).toBe('')
    expect(webPlaneToggleTitle('')).toBe('')
  })
})
