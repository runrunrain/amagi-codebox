// e2e/workspace-notice-stack-m3.spec.ts — M3-008 NoticeStack 渲染控制浏览器证据（Chromium-only，WS 全 mock）
// ---------------------------------------------------------------------------
// 谛听 M3-008：优先级此前只计算 store.primaryNotice 枚举，degradedNotice/lastError
// 仍各自渲染（高压级在位时并列），ControlBar busy 只看 WS state（fatal+attached
// 残留操作入口）。本 spec 用真实 wire 事件驱动冻结优先级（design §7 P0>P1>P2>P3）：
//   · P1(error 事件) 压制 P3(unknown force-read-only 降级)，dismiss P1 后 P3 自然恢复；
//   · P0(session.state removed，socket 仍开) 压制全部低优先级横幅并直接禁 ControlBar
//     —— 修复前该场景 busy=false，「获取控制权」仍可点；
//   · P0(terminal close 1002) → terminal banner + ControlBar 禁用。
// WS mock 形态如实声明：page.routeWebSocket 全 mock（同 workspace-pg03.spec.ts），
// 不连接真实服务器；事件形状逐字段对齐 mobile/src/lib/contract（测试夹具）。
// ---------------------------------------------------------------------------

import { expect, test, type Page, type Route } from '@playwright/test'

const BASE = '**/api/remote/v1'
const GUIDE_KEY = 'amagi.pg03.guide.dismissed'

function makeDetail(over: Record<string, unknown> = {}) {
  return {
    id: 'sess-1',
    title: 'Claude Code · notice-stack',
    cliType: 'claudecode',
    state: 'running',
    control: { state: 'you' },
    lastActivityAt: new Date().toISOString(),
    workdir: '/users/dev/notice-stack',
    startedAt: new Date(Date.now() - 3_600_000).toISOString(),
    earliestSeq: 0,
    latestSeq: 0,
    ...over,
  }
}

function attached(over: Record<string, unknown> = {}) {
  return {
    type: 'session.attached',
    requestId: 'req-attached',
    apiVersion: 'v1',
    sessionId: 'sess-1',
    history: [],
    earliestSeq: 0,
    latestSeq: 0,
    snapshot: {
      connection: { state: 'connected' },
      auth: { state: 'authorized' },
      session: { state: 'running' },
      control: { state: 'you' },
      history: { state: 'continuous' },
    },
    // capability-capable server（M3-001 fail-closed 对齐；mock 夹具默认协商 canonical）。
    inputAckMode: 'session-window-v1',
    ...over,
  }
}

interface WsMock {
  frames: Record<string, unknown>[]
  connections: number
  send: (payload: unknown) => void
  closeFromServer: (code: number) => void
}

async function mockWs(page: Page): Promise<WsMock> {
  const state: WsMock = { frames: [], connections: 0, send: () => {}, closeFromServer: () => {} }
  await page.routeWebSocket('**/ws/v1', (ws) => {
    state.connections += 1
    state.send = (payload) => ws.send(JSON.stringify(payload))
    // close 需对象参数（positional number 不会成为 close code，会导致 1005 语义偏移）。
    state.closeFromServer = (code) => ws.close({ code, reason: 'm3-008-test' })
    ws.onMessage((msg) => {
      const frame = JSON.parse(String(msg)) as Record<string, unknown>
      state.frames.push(frame)
      if (frame.type === 'attach') ws.send(JSON.stringify(attached()))
    })
  })
  return state
}

async function mockRest(page: Page) {
  await page.route(`${BASE}/sessions/sess-1`, (route: Route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(makeDetail()) })
    }
    return route.fallback()
  })
}

async function dismissGuide(page: Page) {
  await page.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)
}

async function enterWorkspace(page: Page) {
  await page.goto('/#/workspace/sess-1')
  await expect(page.locator('.workspace-title')).toBeVisible()
  await expect(page.locator('.status-bar')).toContainText('连接：已连接')
}

function watchConsole(page: Page): string[] {
  const errors: string[] = []
  page.on('console', (msg) => {
    if (msg.type() !== 'error') return
    if (msg.text().startsWith('Failed to load resource')) return
    errors.push(msg.text())
  })
  page.on('pageerror', (err) => errors.push(String(err)))
  return errors
}

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
}

/** WCAG 对比度程序化断言（VT 令牌不透明色值；同 lobby-timing-m3 原则）。 */
async function expectTextContrast(page: Page, selector: string, minRatio = 4.5) {
  const ratio = await page.evaluate((sel) => {
    const el = document.querySelector(sel)
    if (!el) return null
    const parse = (c: string): { r: number; g: number; b: number; a: number } | null => {
      const m = c.match(/rgba?\(([^)]+)\)/)
      if (!m) return null
      const p = m[1].split(',').map((v) => Number(v.trim()))
      return { r: p[0], g: p[1], b: p[2], a: p.length >= 4 && Number.isFinite(p[3]) ? p[3] : 1 }
    }
    const lum = (rgb: { r: number; g: number; b: number }): number => {
      const f = (v: number): number => {
        const s = v / 255
        return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4)
      }
      return 0.2126 * f(rgb.r) + 0.7152 * f(rgb.g) + 0.0722 * f(rgb.b)
    }
    const fg = parse(getComputedStyle(el).color)
    if (!fg) return null
    let node: Element | null = el
    let bg: { r: number; g: number; b: number; a: number } | null = null
    while (node) {
      const c = parse(getComputedStyle(node).backgroundColor)
      if (c && c.a > 0) {
        bg = c
        break
      }
      node = node.parentElement
    }
    if (!bg) return null
    const l1 = lum(fg)
    const l2 = lum(bg)
    return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05)
  }, selector)
  expect(ratio, `${selector} 应存在且可解析颜色`).not.toBeNull()
  expect(ratio as number, `${selector} 对比度 ≥ ${minRatio}:1`).toBeGreaterThanOrEqual(minRatio)
}

function shotName(testInfo: { project: { name: string } }, name: string) {
  return `test-results/m3-008-${testInfo.project.name}-${name}.png`
}

/** wire 驱动到 P3：control.state 携带未知控制态枚举 → normalizer fail-closed
 *  规整为 unknown + force-read-only（control→none + degraded 提示）。 */
function sendForceReadOnly(ws: WsMock) {
  ws.send({
    type: 'control.state',
    sessionId: 'sess-1',
    state: 'hijacked-by-alien',
    occurredAt: '2026-08-04T01:00:00Z',
  })
}

test.describe('M3-008 NoticeStack 优先级渲染控制（design §7）', () => {
  test('P1 压制 P3；dismiss P1 后 P3 自然恢复（store 状态不被压制方改变）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page)
    await enterWorkspace(page)

    // P3：degraded 横幅渲染。
    sendForceReadOnly(ws)
    const degraded = page.locator('.banner--warning')
    await expect(degraded).toBeVisible()
    await expect(degraded).toContainText('已按只读观察处理')
    // control→none：获取控制权按钮可用（非 fatal）。
    const acquire = page.locator('.control-bar .control-action')
    await expect(acquire).toHaveText('获取控制权')
    await expect(acquire).toBeEnabled()

    // P1：error 事件 → 仅 error 横幅，P3 被压制不并列。
    ws.send({
      type: 'error',
      requestId: 'r-e1',
      sessionId: 'sess-1',
      code: 'session.unavailable',
      layer: 'session',
      message: '写入暂时不可用',
      actionHint: 'retry',
      occurredAt: '2026-08-04T01:00:00Z',
    })
    const errorBanner = page.locator('.banner--danger')
    await expect(errorBanner).toBeVisible()
    await expect(errorBanner).toContainText('写入暂时不可用')
    await expect(degraded).toHaveCount(0)
    await expect(page.locator('[data-testid=continuity-banner]')).toHaveCount(0)
    // P1 非 fatal：ControlBar 仍可用。
    await expect(acquire).toBeEnabled()
    await page.screenshot({ path: shotName(testInfo, 'p1-over-p3'), fullPage: false })

    // dismiss P1 → P3 自然恢复（dismiss 只清 lastError，不动 degradedNotice）。
    await errorBanner.getByRole('button', { name: '关闭' }).click()
    await expect(errorBanner).toHaveCount(0)
    await expect(degraded).toBeVisible()
    await page.screenshot({ path: shotName(testInfo, 'p3-restored'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('P0(removed) 但 socket 仍 attached：低优先级全隐藏 + ControlBar 直接禁用', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page)
    await enterWorkspace(page)

    // 先造 P3（control→none，操作入口出现）——覆盖谛听场景：removed 但 socket
    // 尚 attached 时「获取控制权」仍可用的回归。
    sendForceReadOnly(ws)
    await expect(page.locator('.banner--warning')).toBeVisible()
    const acquire = page.locator('.control-bar .control-action')
    await expect(acquire).toHaveText('获取控制权')
    await expect(acquire).toBeEnabled()

    // P0：session.state removed（socket 未关）。
    ws.send({ type: 'session.state', sessionId: 'sess-1', state: 'removed', occurredAt: '2026-08-04T01:05:00Z' })
    const removedBanner = page.locator('.banner--danger', { hasText: '会话已被移除' })
    await expect(removedBanner).toBeVisible()
    // 低优先级横幅全部隐藏（互斥）。
    await expect(page.locator('.banner--warning')).toHaveCount(0)
    await expect(page.locator('[data-testid=continuity-banner]')).toHaveCount(0)
    // fatal 直接禁 ControlBar（修复前 busy 只看 wsState → 残留可用）。
    await expect(acquire).toBeDisabled()

    // 横幅文本对比度 + 无横向溢出（320/360 双视口）。
    await expectTextContrast(page, '.banner--danger .banner-body strong')
    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: shotName(testInfo, 'p0-removed-attached'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('P0(terminal close 1002)：terminal banner + ControlBar 禁用', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page)
    await enterWorkspace(page)

    // control=you → 释放控制权按钮在 terminal 前可用。
    const release = page.locator('.control-bar .control-action')
    await expect(release).toHaveText('释放控制权')
    await expect(release).toBeEnabled()

    ws.closeFromServer(1002)
    await expect(page.locator('[data-testid=terminal-banner]')).toBeVisible({ timeout: 3000 })
    await expect(page.locator('[data-testid=terminal-banner]')).toContainText('连接已终止')
    await expect(release).toBeDisabled()
    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: shotName(testInfo, 'p0-terminal'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })
})
