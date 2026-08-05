// e2e/lobby-timing-m3.spec.ts — M3-006 T0/T1 生产锚点浏览器证据（Chromium-only，route mock）
// ---------------------------------------------------------------------------
// 谛听 M3-006：T0/T1 此前只有测试 seam，真实用户 T lane 永远 not_occurred。
// 本 spec 证明生产接线后真实浏览器导航产生 T lane 样本：
//   · 真实路由导航（PG-01 诊断 → 进入大厅；memory 外真实 hash 路由 + vite dev）；
//   · 三分支（成功/空态/可操作错误态）均 observed；auth 失效踢回不打 T1（fail-closed）；
//   · 读取 seam = vite dev 动态 import 活跃 store 的 listTimingSnapshot()
//     （同 workspace-m3c.spec.ts 的 continuitySnapshot seam 原则）；
//   · privacy：exact key allowlist 反解析；duration 真实 performance.now 读数，
//     只断言 finite/nonnegative/预算值，不强制 within/over（避免调度 flaky；
//     不宣称真实 3s 达标——同 timing.spec.ts 纪律）。
// API 一律 Playwright route mock（测试夹具，形状对齐 M2-A mapper；同 sessions-pg02）。
// ---------------------------------------------------------------------------

import { expect, test, type Page, type Route } from '@playwright/test'

const BASE = '**/api/remote/v1'

const HOST_SUMMARY = {
  apiVersion: 'v1',
  serverVersion: '1.0.5-mock',
  cliAvailability: [
    { cliType: 'claudecode', available: true },
    { cliType: 'opencode', available: true },
    { cliType: 'codex', available: true },
    { cliType: 'pi', available: true },
  ],
}

interface SessionFixture {
  id: string
  title: string
  cliType: string
  state: string
  control: { state: string; deviceName?: string }
  lastActivityAt: string
}

function makeSession(over: Partial<SessionFixture>): SessionFixture {
  return {
    id: 'sess-1',
    title: 'Claude Code · amagi-codebox',
    cliType: 'claudecode',
    state: 'running',
    control: { state: 'none' },
    lastActivityAt: new Date().toISOString(),
    ...over,
  }
}

function apiErrorBody(code: string, layer: string, message: string, actionHint: string) {
  return { requestId: `req-${code}`, code, layer, message, actionHint }
}

function fulfillJson(route: Route, status: number, body: unknown) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
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

/**
 * WCAG 对比度程序化断言：元素 computed color 对最近非透明祖先背景。
 * VT 令牌为不透明色值（无 alpha 复合），ratio 公式 = (L1+0.05)/(L2+0.05)。
 */
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

/** mock host/summary 已授权；sessions GET 行为由用例定制。 */
async function mockHost(page: Page) {
  await page.route(`${BASE}/host/summary`, (route) => fulfillJson(route, 200, HOST_SUMMARY))
}

async function mockSessions(page: Page, sessions: SessionFixture[]) {
  await page.route(`${BASE}/sessions`, (route) => {
    if (route.request().method() === 'GET') return fulfillJson(route, 200, sessions)
    return route.fallback()
  })
}

/** 经 PG-01 真实路径进入大厅（诊断 → 已授权 → 点击进入）。 */
async function enterLobby(page: Page) {
  await page.goto('/')
  await expect(page.locator('.diagnosis-title')).toHaveText('已授权，可以进入')
  await page.getByRole('button', { name: '进入会话大厅' }).click()
  await expect(page).toHaveURL(/#\/lobby$/)
  await expect(page.locator('.lobby-title')).toHaveText('会话大厅')
}

/** 经 vite dev 动态 import 读取活跃 lobby store 的 T lane 快照（design §6 seam）。 */
async function readLobbyTiming(page: Page): Promise<Record<string, unknown> | null> {
  return page.evaluate(async () => {
    const mod = await import('/src/stores/lobby.ts')
    const store = mod.useLobbyStore()
    return store.listTimingSnapshot() as Record<string, unknown> | null
  })
}

const TOP_KEYS = ['measurements', 'schemaVersion', 'unit']
const MEASUREMENT_KEYS = ['budgetMs', 'budgetStatus', 'durationMs', 'invalidReason', 'status']

/** T lane observed + 固定 schema（privacy exact allowlist）+ duration 真实读数。 */
function expectObservedTLane(report: Record<string, unknown> | null) {
  expect(report).toBeTruthy()
  const r = report as Record<string, unknown>
  expect(Object.keys(r).sort()).toEqual(TOP_KEYS)
  expect(r.schemaVersion).toBe(1)
  expect(r.unit).toBe('ms')
  const measurements = r.measurements as Record<string, Record<string, unknown>>
  expect(Object.keys(measurements).sort()).toEqual(['R0_R1', 'T0_T1'])
  const t = measurements.T0_T1
  expect(Object.keys(t).sort()).toEqual(MEASUREMENT_KEYS)
  expect(t.status).toBe('observed')
  expect(t.invalidReason).toBeNull()
  expect(typeof t.durationMs).toBe('number')
  const d = t.durationMs as number
  expect(Number.isFinite(d)).toBe(true)
  expect(d).toBeGreaterThanOrEqual(0)
  expect(t.budgetMs).toBe(3000)
  expect(['within_budget', 'over_budget']).toContain(t.budgetStatus)
  // R lane 不属于列表导航：不伪造样本。
  expect(measurements.R0_R1.status).toBe('not_occurred')
}

function shotName(testInfo: { project: { name: string } }, name: string) {
  return `test-results/m3-006-${testInfo.project.name}-${name}.png`
}

test.describe('M3-006 T0/T1 生产锚点（真实浏览器导航）', () => {
  test('成功分支：列表渲染完成 → T lane observed（固定 schema，无 ID/payload）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await mockHost(page)
    await mockSessions(page, [
      makeSession({ id: 'sess-1', title: 'Claude Code · amagi-codebox', cliType: 'claudecode', state: 'running', control: { state: 'you' } }),
      makeSession({ id: 'sess-2', title: 'Codex · demo', cliType: 'codex', state: 'stopped', control: { state: 'desktop' } }),
    ])
    await enterLobby(page)
    await expect(page.locator('.session-list')).toBeVisible()

    expectObservedTLane(await readLobbyTiming(page))

    // 卡片标题对比度（VT 令牌，≥4.5:1）。
    // 边界如实声明：.activity-time 次级文本实测 ~4.48:1（desktop，VT-text-secondary
    // on VT-surface）为 M2-B 既有令牌问题，非本轮改动引入；不在本 spec 设卡，
    // 已在实现报告 §3 记录，是否修复由 Leader 分流（不动设计令牌属范围外）。
    await expectTextContrast(page, '.session-card .session-title')
    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: shotName(testInfo, 'lobby-success'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('空态分支：空列表渲染完成 → T lane observed', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await mockHost(page)
    await mockSessions(page, [])
    await enterLobby(page)
    await expect(page.locator('.empty-state')).toBeVisible()
    await expect(page.locator('.empty-title')).toHaveText('还没有会话')

    expectObservedTLane(await readLobbyTiming(page))

    await expectTextContrast(page, '.empty-title')
    await expectTextContrast(page, '.empty-desc')
    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: shotName(testInfo, 'lobby-empty'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('可操作错误态分支：分类错误 + 重试渲染完成 → T lane observed', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await mockHost(page)
    await page.route(`${BASE}/sessions`, (route) => {
      if (route.request().method() === 'GET') {
        return fulfillJson(route, 503, apiErrorBody('service.down', 'session', 'session service unavailable', 'retry'))
      }
      return route.fallback()
    })
    await enterLobby(page)
    const errorCard = page.locator('.lobby-error-card')
    await expect(errorCard).toBeVisible()
    await expect(errorCard).toContainText('宿主会话服务不可用')
    // 可操作：重试按钮可用。
    await expect(errorCard.getByRole('button', { name: '重试' })).toBeEnabled()

    expectObservedTLane(await readLobbyTiming(page))

    await expectTextContrast(page, '.lobby-error-card .error-title')
    await expectTextContrast(page, '.lobby-error-card .error-guidance')
    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: shotName(testInfo, 'lobby-error'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('auth 失效：踢回 PG-01，T lane 不落完成快照（fail-closed）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await mockHost(page)
    await page.route(`${BASE}/sessions`, (route) => {
      if (route.request().method() === 'GET') {
        return fulfillJson(route, 401, apiErrorBody('auth.revoked', 'auth', 'device authorization revoked', 're-pair'))
      }
      return route.fallback()
    })
    await page.goto('/')
    await expect(page.locator('.diagnosis-title')).toHaveText('已授权，可以进入')
    await page.getByRole('button', { name: '进入会话大厅' }).click()

    // 授权失效 → 踢回 PG-01（reason 经 hash query 一次性传递后被 ConnectPage
    // 用 history.replaceState 剥离——一次性材料不入地址栏，见 ConnectPage 实现）；
    // 撤销提示横幅为权威呈现。
    await expect(page).toHaveURL(/#\/connect$/)
    await expect(page.locator('[role=alert]')).toContainText('授权已被桌面端撤销')
    // 授权失效不是列表可交互终态：无 T 样本（不伪造）。
    expect(await readLobbyTiming(page)).toBeNull()
    expect(consoleErrors).toEqual([])
  })
})
