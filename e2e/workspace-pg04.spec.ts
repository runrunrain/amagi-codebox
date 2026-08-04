// e2e/workspace-pg04.spec.ts — M2-D PG-04 原始终端按需诊断视图（Chromium-only）
// ---------------------------------------------------------------------------
// WS mock 形态如实声明（同 M2-C）：Playwright routeWebSocket 全 mock，不连接
// 真实服务器；服务端事件经 ws.send(JSON) 注入，客户端帧经 ws.onMessage 观测。
// xterm 为真实引擎（dev 下动态 import 真实 chunk）；真实 WS E2E 属 M2-INT。
// 覆盖：菜单进入渲染/返回主面（连接不重建）/权限一致（观察者禁用+控制者可发）/
// 深链身份/软键盘视口跟随/console 无 error。xterm 主 bundle 断言见
// mobile/scripts/check-bundle-no-xterm.mjs（构建产物级，非本 spec 职责）。
// ---------------------------------------------------------------------------

import { expect, test, type Page, type Route } from '@playwright/test'

const BASE = '**/api/remote/v1'
const GUIDE_KEY = 'amagi.pg03.guide.dismissed'

// ---------------------------------------------------------------------------
// 夹具
// ---------------------------------------------------------------------------

function b64(text: string): string {
  return Buffer.from(text, 'utf-8').toString('base64')
}

function output(seq: number, text: string) {
  return { type: 'output', sessionId: 'sess-1', seq, chunk: b64(text) }
}

function snapshot(over: Record<string, unknown> = {}) {
  return {
    connection: { state: 'connected' },
    auth: { state: 'authorized' },
    session: { state: 'running' },
    control: { state: 'you' },
    history: { state: 'continuous' },
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
    snapshot: snapshot(),
    ...over,
  }
}

function makeDetail(over: Record<string, unknown> = {}) {
  return {
    id: 'sess-1',
    title: 'Claude Code · pg04-demo',
    cliType: 'claudecode',
    state: 'running',
    control: { state: 'you' },
    lastActivityAt: new Date().toISOString(),
    workdir: '/users/dev/pg04-demo',
    startedAt: new Date(Date.now() - 3_600_000).toISOString(),
    earliestSeq: 0,
    latestSeq: 0,
    ...over,
  }
}

/** 含 ANSI 序列的诊断输出（原始终端流语义）。 */
const ANSI_HISTORY = [
  output(1, '$ pi test --watch\r\n'),
  output(2, '\x1b[32m✓ auth/login (12)\x1b[0m\r\n'),
  output(3, '\x1b[31m✗ remote/ws timeout\x1b[0m\r\n'),
]

interface WsMock {
  frames: Record<string, unknown>[]
  connections: number
  send: (payload: unknown) => void
}

async function mockWs(page: Page, opts: { autoAttach?: (frames: Record<string, unknown>[]) => unknown } = {}): Promise<WsMock> {
  const state: WsMock = { frames: [], connections: 0, send: () => {} }
  await page.routeWebSocket('**/ws/v1', (ws) => {
    state.connections += 1
    state.send = (payload) => ws.send(JSON.stringify(payload))
    ws.onMessage((msg) => {
      const frame = JSON.parse(String(msg)) as Record<string, unknown>
      state.frames.push(frame)
      if (frame.type === 'attach') {
        const response = opts.autoAttach?.(state.frames) ?? attached()
        ws.send(JSON.stringify(response))
      }
    })
  })
  return state
}

async function mockRest(page: Page, detail: Record<string, unknown> = makeDetail()) {
  await page.route(`${BASE}/sessions/sess-1`, (route: Route) => {
    const method = route.request().method()
    if (method === 'GET') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(detail) })
    return route.fallback()
  })
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

function shotName(testInfo: { project: { name: string } }, name: string) {
  return `test-results/pg04-${testInfo.project.name}-${name}.png`
}

async function dismissGuide(page: Page) {
  await page.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)
}

/** 经 overflow 菜单进入诊断视图（CHG-20260801-05：菜单入口，非默认、非并列 tab）。 */
async function openDiagnosticViaMenu(page: Page) {
  await page.locator('.menu-btn').click()
  await page.locator('.menu-item', { hasText: '原始终端诊断视图' }).click()
  await expect(page.locator('.raw-terminal-host .xterm-rows')).toBeVisible()
}

// ---------------------------------------------------------------------------
// 用例
// ---------------------------------------------------------------------------

test.describe('M2-D PG-04 原始终端按需诊断视图', () => {
  test('菜单进入 → xterm 渲染原始流（ANSI 保真）；诊断身份与返回入口明示', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    await mockWs(page, {
      autoAttach: () => attached({ earliestSeq: 1, latestSeq: 3, history: ANSI_HISTORY }),
    })
    await page.goto('/#/workspace/sess-1')
    await expect(page.locator('.workspace-title')).toBeVisible()

    // 默认面是结构化主形态（诊断视图非默认）。
    await expect(page.locator('.raw-terminal-view')).toHaveCount(0)

    await openDiagnosticViaMenu(page)

    // 路由：?view=terminal（P5 §2.1 权威形态）。
    expect(page.url()).toContain('view=terminal')
    // 身份标识 + 返回主阅读面入口（≥44px 触控目标）。
    await expect(page.locator('.diagnostic-badge')).toHaveText('诊断视图')
    const backBtn = page.locator('.back-btn--primary')
    await expect(backBtn).toContainText('返回主阅读面')
    const box = await backBtn.boundingBox()
    expect(box!.height).toBeGreaterThanOrEqual(44)

    // 原始流渲染（ANSI 文本保真；红色错误行在网格中逐行可读）。
    const rows = page.locator('.raw-terminal-host .xterm-rows')
    await expect(rows).toContainText('$ pi test --watch')
    await expect(rows).toContainText('✓ auth/login (12)')
    await expect(rows).toContainText('✗ remote/ws timeout')

    // 直播续写与返回/连接复用见下一用例。
    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: shotName(testInfo, 'diagnostic-open') })
    expect(consoleErrors).toEqual([])
  })

  test('直播输出续写 + 返回主阅读面（WS 不重建、时间线状态保留）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page, {
      autoAttach: () => attached({ earliestSeq: 1, latestSeq: 3, history: ANSI_HISTORY }),
    })
    await page.goto('/#/workspace/sess-1')
    await expect(page.locator('.workspace-title')).toBeVisible()
    await openDiagnosticViaMenu(page)

    // 直播帧：进入诊断视图后服务端继续推流。
    ws.send(output(4, 'live chunk after diagnostic open\r\n'))
    await expect(page.locator('.raw-terminal-host .xterm-rows')).toContainText('live chunk after diagnostic open')

    // 返回主阅读面 → 结构化时间线仍在原位（MonoBlock 兜底块含原始文本）。
    await page.locator('.back-btn--primary').click()
    await expect(page.locator('.mono-block').first()).toBeVisible()
    await expect(page.locator('.raw-terminal-view')).toHaveCount(0)
    expect(page.url()).not.toContain('view=terminal')

    // WS 连接未重建（同一会话订阅复用，非重连）。
    expect(ws.connections).toBe(1)

    // 再次进入：回放缓冲 + 无重连。
    await openDiagnosticViaMenu(page)
    await expect(page.locator('.raw-terminal-host .xterm-rows')).toContainText('$ pi test --watch')
    expect(ws.connections).toBe(1)

    await page.screenshot({ path: shotName(testInfo, 'roundtrip') })
    expect(consoleErrors).toEqual([])
  })

  test('权限一致：控制者在诊断视图可经同一 Composer 输入（input 帧同路径）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page, { autoAttach: () => attached() })
    await page.goto('/#/workspace/sess-1?view=terminal')
    await expect(page.locator('.raw-terminal-host .xterm-rows')).toBeVisible()

    // 诊断视图输入 = 主面同一 Composer/同一 store 过滤路径（P-04 不扩权限）。
    await page.locator('.composer-input').fill('continue refactor')
    await page.locator('.composer-send').click()
    const inputFrames = ws.frames.filter((f) => f.type === 'input')
    expect(inputFrames).toHaveLength(1)
    expect(Buffer.from(String(inputFrames[0].data), 'base64').toString('utf-8')).toBe('continue refactor\r')

    // 网格本身只读：终端区域不产生任何 input 帧（无键托盘、无网格键盘通道）。
    await page.locator('.raw-terminal-host').click()
    expect(ws.frames.filter((f) => f.type === 'input')).toHaveLength(1)

    await page.screenshot({ path: shotName(testInfo, 'controller-input') })
    expect(consoleErrors).toEqual([])
  })

  test('权限一致：观察者诊断视图只读（Composer 禁用并明示，与主面同语义）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page, makeDetail({ control: { state: 'other', deviceName: 'iPad' } }))
    const ws = await mockWs(page, {
      autoAttach: () =>
        attached({
          latestSeq: 3,
          history: ANSI_HISTORY,
          snapshot: snapshot({ control: { state: 'other', deviceName: 'iPad' } }),
        }),
    })
    await page.goto('/#/workspace/sess-1?view=terminal')

    // 网格可见（观察权限保留）；Composer 禁用并明示原因（与主面完全一致）。
    await expect(page.locator('.raw-terminal-host .xterm-rows')).toContainText('$ pi test --watch')
    await expect(page.locator('.composer-input')).toBeDisabled()
    await expect(page.locator('.composer-send')).toBeDisabled()
    await expect(page.locator('.composer-block-reason')).toContainText('控制权在 iPad')
    // 观察者无「停止运行」显式按钮（主面同规则）。
    await expect(page.locator('.composer-stop')).toHaveCount(0)

    // 尝试输入不可行：无 input 帧。
    expect(ws.frames.filter((f) => f.type === 'input')).toHaveLength(0)

    // 状态条控制层与主面同投影。
    await expect(page.locator('.status-bar')).toContainText('由 iPad 控制')

    await page.screenshot({ path: shotName(testInfo, 'observer-readonly') })
    expect(consoleErrors).toEqual([])
  })

  test('深链 ?view=terminal 直接可达（诊断身份明示；菜单提供返回大厅）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    await mockWs(page, { autoAttach: () => attached({ latestSeq: 1, history: [output(1, 'deep link output\r\n')] }) })
    await page.goto('/#/workspace/sess-1?view=terminal')
    await expect(page.locator('.diagnostic-badge')).toHaveText('诊断视图')
    await expect(page.locator('.raw-terminal-host .xterm-rows')).toContainText('deep link output')
    // overflow 菜单：诊断面提供返回大厅（离开方式 §PG-04）。
    await page.locator('.menu-btn').click()
    await expect(page.locator('.menu-item', { hasText: '返回会话大厅' })).toBeVisible()
    expect(consoleErrors).toEqual([])
  })

  test('软键盘视口跟随：视口收缩后 Composer 保持可达、网格 refit 上报真实尺寸', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page, {
      autoAttach: () => attached({ earliestSeq: 1, latestSeq: 3, history: ANSI_HISTORY }),
    })
    await page.goto('/#/workspace/sess-1?view=terminal')
    await expect(page.locator('.raw-terminal-host .xterm-rows')).toBeVisible()

    // 初始 fit 上报一次真实网格（诊断视图替代主面近似换算；attach 后补报有 120ms 去抖）。
    await expect
      .poll(() => ws.frames.filter((f) => f.type === 'resize').length, { timeout: 5000 })
      .toBeGreaterThanOrEqual(1)
    const resizeBefore = ws.frames.filter((f) => f.type === 'resize')

    // 模拟软键盘弹出：可视视口高度收缩（Chromium 中 visualViewport 跟随）。
    const heightBefore = await page.evaluate(() => window.visualViewport?.height ?? 0)
    await page.setViewportSize({ width: page.viewportSize()!.width, height: Math.round(heightBefore * 0.55) })

    // Composer 保持可见且在视口内（AC-10 最新输入区可达）。
    await expect(page.locator('.composer')).toBeInViewport()
    await expect(page.locator('.composer-input')).toBeInViewport()

    // refit 后上报新的真实网格尺寸（行数应变小）。
    await expect
      .poll(() => ws.frames.filter((f) => f.type === 'resize').length, { timeout: 5000 })
      .toBeGreaterThan(resizeBefore.length)
    const lastResize = ws.frames.filter((f) => f.type === 'resize').at(-1)!
    const firstResize = resizeBefore[0]
    expect(Number(lastResize.rows)).toBeLessThan(Number(firstResize.rows))

    await page.screenshot({ path: shotName(testInfo, 'keyboard-follow') })
    expect(consoleErrors).toEqual([])
  })
})
