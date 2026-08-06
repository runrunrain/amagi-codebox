// e2e/a11y-m4.spec.ts — M4-A 移动适配与 a11y 全量审计（Chromium-only）
// ---------------------------------------------------------------------------
// 范围：PG-01/02/03/04 四页 + 关键组件的无障碍与移动适配门禁。
//   · axe-core（@axe-core/playwright，wcag2a+wcag2aa）：critical/serious 零容忍，
//     moderate/minor 记录不 fail（整改跟踪见 M4-A 报告）。
//   · 44px 触控目标运行时测量：真实 Chromium 逐元素 bounding box（权威判定；
//     静态盘点见 mobile/scripts/audit-touch-targets.mjs）。
//   · 横屏紧凑模式（800×360）/ 软键盘 VisualViewport 模拟 / safe-area CSS 接入 /
//     live region 语义 / reduced-motion / 200% 缩放（等效视口法，如实声明）。
// 模拟边界（如实声明，真机手测归 M4-C 物理验证）：
//   · 软键盘：headless Chromium 无系统 IME——用视口收缩模拟键盘几何
//     （visualViewport.resize 真实触发），iOS offsetTop 行为无法模拟；
//   · safe-area：env() 在 Chromium 恒为 0——断言 CSS 规则接入（样式表文本），
//     真机 inset 值归 M4-C；
//   · 200% 缩放：浏览器缩放不可编程——用等效 CSS 视口（360px@200% → 180px
//     逻辑宽）近似，断言无横向溢出与关键控件可达；PG-04 xterm 字符栅格为
//     固定 cell 几何，不随等效视口缩放（真实双指缩放会整体放大 canvas，
//     模拟层无法代表）——其 200% 断言范围为页面骨架（header/Composer/控件），
//     栅格文本放大阅读由 PG-03 结构化主阅读面承担，真机缩放归 M4-C。
//   · 横屏：以 800×360 视口模拟手机横屏姿态。
// API 一律 route/WS mock（同 M1-D1/M2-C 纪律），测试夹具不 import 前端实现。
// ---------------------------------------------------------------------------

import { expect, test, type Page, type Route } from '@playwright/test'
import { AxeBuilder } from '@axe-core/playwright'

const BASE = '**/api/remote/v1'
const GUIDE_KEY = 'amagi.pg03.guide.dismissed'

// ---------------------------------------------------------------------------
// 夹具（形状逐字段对齐既有 PG spec；M0-03 冻结契约）
// ---------------------------------------------------------------------------

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
    history: [output(1, 'a11y demo output line')],
    earliestSeq: 0,
    latestSeq: 1,
    snapshot: snapshot(),
    inputAckMode: 'session-window-v1',
    ...over,
  }
}

function fulfillJson(route: Route, status: number, body: unknown) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

function apiErrorBody(code: string, layer: string, message: string, actionHint: string) {
  return { requestId: `req-${code}`, code, layer, message, actionHint }
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

/** PG-01：未配对诊断态（风险条 + 扫码/手动入口全交互面）。 */
async function mountPg01(page: Page) {
  await page.route(`${BASE}/host/summary`, (route) =>
    fulfillJson(route, 401, apiErrorBody('auth.unpaired', 'auth', 'device not paired', 're-pair')),
  )
  await page.goto('/')
  await expect(page.locator('.diagnosis-title')).toHaveText('这台设备还没有配对')
}

/** PG-02：两条会话的大厅（控制/观察双投影 + 危险菜单入口）。 */
async function mountPg02(page: Page) {
  const now = new Date().toISOString()
  // 配对前 host/summary 401 未配对；pair 201 成功后转为 200（状态化 mock，
  // 与真实后端 Cookie 语义一致——未配对凭据不得拿到宿主摘要）。
  let paired = false
  await page.route(`${BASE}/host/summary`, (route) =>
    paired
      ? fulfillJson(route, 200, HOST_SUMMARY)
      : fulfillJson(route, 401, apiErrorBody('auth.unpaired', 'auth', 'device not paired', 're-pair')),
  )
  await page.route(`${BASE}/sessions`, (route) => {
    if (route.request().method() !== 'GET') return route.fallback()
    return fulfillJson(route, 200, [
      {
        id: 'sess-1',
        title: 'Claude Code · a11y-demo',
        cliType: 'claudecode',
        state: 'running',
        control: { state: 'you' },
        lastActivityAt: now,
      },
      {
        id: 'sess-2',
        title: 'Codex · a11y-demo-2',
        cliType: 'codex',
        state: 'exited',
        control: { state: 'none' },
        lastActivityAt: now,
      },
    ])
  })
  // 已配对 Cookie 由 mock 下发（配对成功路径与 M1-D1 相同：POST /pairing/complete）。
  await page.route(`${BASE}/pairing/complete`, (route) => {
    paired = true
    return route.fulfill({
      status: 201,
      contentType: 'application/json',
      headers: { 'Set-Cookie': 'amagi_remote_device=mock-opaque; Path=/; HttpOnly; SameSite=Strict' },
      body: JSON.stringify({
        device: { id: 'dev-mock-1', name: 'a11y 设备', pairedAt: now },
        host: HOST_SUMMARY,
      }),
    })
  })
  await page.goto('/')
  await page.getByRole('button', { name: '手动输入地址与配对码' }).click()
  await page.locator('#pair-code').fill('A11Y-MOCK')
  await page.getByRole('button', { name: '完成配对' }).click()
  await expect(page).toHaveURL(/#\/lobby$/)
  await expect(page.locator('.session-card')).toHaveCount(2)
}

/** PG-03 mock 准备（必须在页面首次加载前注册）。
 *  实证约束（M4-A 调试记录）：routeWebSocket 在页面已加载后注册不拦截后续
 *  WS（连接直发 dev server → 失败重连 → 芯片卡「连接中」）；多阶段同页测试
 *  （PG-01→02→03）必须在首个 goto 前调用本函数。
 *  dismissGuide=true（默认）预置 E-09 引导已关闭；false 保留首次引导态
 *  （M4-R3 谛听 M4-006：窄屏首次 Guide 态的纵向可达性必须真实覆盖）。 */
async function preparePg03Mocks(page: Page, { dismissGuide = true }: { dismissGuide?: boolean } = {}) {
  if (dismissGuide) await page.addInitScript((key) => localStorage.setItem(key, '1'), GUIDE_KEY)
  await page.routeWebSocket('**/ws/v1', (ws) => {
    ws.onMessage((msg) => {
      const frame = JSON.parse(String(msg)) as Record<string, unknown>
      if (frame.type === 'attach') ws.send(JSON.stringify(attached()))
    })
  })
  await page.route(`${BASE}/sessions/sess-1`, (route) =>
    fulfillJson(route, 200, {
      id: 'sess-1',
      title: 'Claude Code · a11y-demo',
      cliType: 'claudecode',
      state: 'running',
      control: { state: 'you' },
      lastActivityAt: new Date().toISOString(),
      workdir: '/users/dev/project',
      startedAt: new Date(Date.now() - 3_600_000).toISOString(),
      earliestSeq: 0,
      latestSeq: 1,
    }),
  )
}

/** PG-03：进入工作区主面（时间线 + Composer + 五层芯片）。
 *  已加载页面用真实 SPA hash 导航（page.goto 纯 hash 变化在无 hash 起始 URL
 *  时不触发导航——Playwright 同文档语义）；未加载页面（about:blank）走 goto。
 *  SPA 导航同时是「路由切换焦点管理」的真实触发路径。
 *  前置：preparePg03Mocks 已注册（见其上实证约束）。 */
async function mountPg03(page: Page, query = '') {
  if (page.url().startsWith('http')) {
    await page.evaluate((q) => {
      window.location.hash = `#/workspace/sess-1${q}`
    }, query)
  } else {
    await page.goto(`/#/workspace/sess-1${query}`)
  }
  await expect(page).toHaveURL(new RegExp(`#/workspace/sess-1`))
  await expect(page.locator('.status-chips')).toContainText('连接：已连接', { timeout: 10_000 })
}

// ---------------------------------------------------------------------------
// 通用断言
// ---------------------------------------------------------------------------

/** axe：critical/serious 零容忍；moderate/minor 汇总返回（记录不 fail）。 */
async function expectNoSeriousAxeViolations(page: Page, context: string, include?: string[]) {
  let builder = new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa'])
  if (include) builder = builder.include(include.join(','))
  const results = await builder.analyze()
  const serious = results.violations.filter((v) => v.impact === 'critical' || v.impact === 'serious')
  const minor = results.violations.filter((v) => v.impact === 'moderate' || v.impact === 'minor')
  if (minor.length > 0) {
    console.log(
      `[axe:${context}] moderate/minor（记录不 fail）：`,
      minor.map((v) => `${v.id}(${v.impact})×${v.nodes.length}`).join(', '),
    )
  }
  expect(
    serious.map((v) => `${v.id}(${v.impact}): ${v.nodes.map((n) => n.target.join(' ')).slice(0, 3).join(' | ')}`),
    `axe critical/serious 违规（${context}）`,
  ).toEqual([])
}

/** 无横向溢出。 */
async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
}

/** header 关键控件几何右缘不超出视口（M4-R1）。
 *  flex 行溢出被裁切时不产生文档级 scrollWidth——expectNoHorizontalOverflow
 *  对此为盲；逐控件量 bounding rect 才是完整可达性的真实证据。 */
async function expectHeaderControlsInViewport(page: Page) {
  const geo = await page.evaluate(() => {
    const vw = document.documentElement.clientWidth
    const right = (sel: string) => {
      const el = document.querySelector(sel)
      return el ? el.getBoundingClientRect().right : null
    }
    return { vw, back: right('.workspace-header .back-btn'), menu: right('.workspace-header .menu-btn') }
  })
  expect(geo.back, `back-btn 右缘 ${geo.back} ≤ 视口 ${geo.vw}`).toBeLessThanOrEqual(geo.vw)
  expect(geo.menu, `menu-btn 右缘 ${geo.menu} ≤ 视口 ${geo.vw}`).toBeLessThanOrEqual(geo.vw)
}

/** 底部全部关键控件几何边界（谛听 M4-006 R2：不得只量单个控件或仅查 header）。
 *  量 Composer 内全部可见交互控件（history/input/send/stop 等）与时间线底部
 *  相邻控件（new-output-pill、mono 诊断入口）的四边；flex 行溢出被裁切时
 *  不产生文档级 scrollWidth——逐控件量 bounding rect 才是完整可达性证据。
 *  M4-R3（谛听 M4-006 R3）：补纵向四边断言 top>=0 && bottom<=vh——R2 oracle
 *  只查左右缘，180×800 首次 Guide 态停止按钮垂直落出视口（bottom=852>800）
 *  仍全绿假阴性；文档不可滚时 bottom 检查尤为关键。 */
async function expectBottomControlsInViewport(page: Page) {
  const results = await page.evaluate(() => {
    const vw = document.documentElement.clientWidth
    const vh = document.documentElement.clientHeight
    const selectors = ['.composer button', '.composer textarea', '.composer input', '.new-output-pill', '.mono-diagnostic']
    const controls: { sel: string; label: string; left: number; right: number; top: number; bottom: number }[] = []
    for (const sel of selectors) {
      for (const el of document.querySelectorAll<HTMLElement>(sel)) {
        const style = getComputedStyle(el)
        if (style.display === 'none' || style.visibility === 'hidden') continue
        const rect = el.getBoundingClientRect()
        if (rect.width === 0 && rect.height === 0) continue
        controls.push({
          sel,
          label: el.getAttribute('aria-label') ?? el.textContent?.trim().slice(0, 12) ?? '',
          left: Math.round(rect.left * 100) / 100,
          right: Math.round(rect.right * 100) / 100,
          top: Math.round(rect.top * 100) / 100,
          bottom: Math.round(rect.bottom * 100) / 100,
        })
      }
    }
    return { vw, vh, controls }
  })
  expect(results.controls.length, '底部控件枚举非空（Composer 至少含输入区/按钮）').toBeGreaterThan(0)
  for (const c of results.controls) {
    expect(c.left, `${c.sel} "${c.label}" 左缘 ${c.left} ≥ 0`).toBeGreaterThanOrEqual(0)
    expect(c.right, `${c.sel} "${c.label}" 右缘 ${c.right} ≤ 视口宽 ${results.vw}`).toBeLessThanOrEqual(results.vw)
    expect(c.top, `${c.sel} "${c.label}" 顶缘 ${c.top} ≥ 0`).toBeGreaterThanOrEqual(0)
    expect(c.bottom, `${c.sel} "${c.label}" 底缘 ${c.bottom} ≤ 视口高 ${results.vh}`).toBeLessThanOrEqual(results.vh)
  }
}

/** 44px 触控目标运行时测量（权威判定）。
 *  枚举视口内可见的原生/角色可交互元素；豁免：
 *   · 全屏 dismiss 层（overlay/backdrop，目标=整个视口）；
 *   · xterm 网格内部行（canvas/终端单元格非自定控件）；
 *   · 禁用的尺寸由内容撑开的文本内联元素（本四页无此类）。
 *  返回违规清单（应为空）。 */
async function measureTouchTargets(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const MIN = 44
    const bad: string[] = []
    const els = document.querySelectorAll<HTMLElement>(
      'button, a[href], input, select, textarea, [role="button"], [role="menuitem"], [role="tab"], [role="switch"], [role="checkbox"], [role="link"]',
    )
    for (const el of els) {
      if (el.closest('.xterm')) continue
      const cls = el.className.toString()
      if (/overlay|backdrop/.test(cls)) continue
      const rect = el.getBoundingClientRect()
      if (rect.width === 0 && rect.height === 0) continue // 不可见
      const style = getComputedStyle(el)
      if (style.visibility === 'hidden' || style.display === 'none') continue
      const label = el.getAttribute('aria-label') ?? el.textContent?.trim().slice(0, 20) ?? ''
      if (rect.height < MIN || rect.width < MIN) {
        bad.push(
          `<${el.tagName.toLowerCase()}> .${cls.split(' ')[0] ?? ''} "${label}" ${Math.round(rect.width)}×${Math.round(rect.height)}`,
        )
      }
    }
    return bad
  })
}

// ---------------------------------------------------------------------------
// 用例
// ---------------------------------------------------------------------------

test.describe('M4-A axe（wcag2a/aa，critical/serious 零容忍）', () => {
  test('PG-01 连接与配对页（未配对全交互面：风险条/入口/手动表单）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await mountPg01(page)
    await expectNoSeriousAxeViolations(page, 'PG-01 未配对')
    // 手动表单展开态（输入/提交/返回）。
    await page.getByRole('button', { name: '手动输入地址与配对码' }).click()
    await expect(page.locator('#pair-code')).toBeVisible()
    await expectNoSeriousAxeViolations(page, 'PG-01 手动表单')
    expect(consoleErrors).toEqual([])
  })

  test('PG-02 会话大厅（双会话 + 五层芯片 + 启动器）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await mountPg02(page)
    await expectNoSeriousAxeViolations(page, 'PG-02 大厅')
    expect(consoleErrors).toEqual([])
  })

  test('PG-03 会话工作区（时间线 + Composer + 控制条）', async ({ page }) => {
    await preparePg03Mocks(page)
    const consoleErrors = watchConsole(page)
    await mountPg03(page)
    await expect(page.locator('.composer-input')).toBeVisible()
    await expectNoSeriousAxeViolations(page, 'PG-03 工作区')
    expect(consoleErrors).toEqual([])
  })

  test('PG-04 原始终端诊断视图（xterm 网格 + 同一 Composer）', async ({ page }) => {
    await preparePg03Mocks(page)
    const consoleErrors = watchConsole(page)
    await mountPg03(page, '?view=terminal')
    await expect(page.locator('.xterm-rows')).toContainText('a11y demo output line', { timeout: 15_000 })
    await expectNoSeriousAxeViolations(page, 'PG-04 诊断视图')
    expect(consoleErrors).toEqual([])
  })
})

test.describe('M4-A 44px 触控目标（运行时测量，权威判定）', () => {
  test('PG-01/02/03/04 全部可见可交互元素 ≥44px', async ({ page }) => {
    await preparePg03Mocks(page)
    await mountPg01(page)
    expect(await measureTouchTargets(page), 'PG-01 未配对').toEqual([])
    await page.getByRole('button', { name: '手动输入地址与配对码' }).click()
    expect(await measureTouchTargets(page), 'PG-01 手动表单').toEqual([])

    await mountPg02(page)
    expect(await measureTouchTargets(page), 'PG-02 大厅').toEqual([])
    // 危险菜单展开态（menuitem 逐个量）。
    await page.getByRole('button', { name: /会话 .* 的操作菜单/ }).first().click()
    expect(await measureTouchTargets(page), 'PG-02 菜单展开').toEqual([])

    await mountPg03(page)
    await expect(page.locator('.composer-input')).toBeVisible()
    expect(await measureTouchTargets(page), 'PG-03 工作区').toEqual([])

    await mountPg03(page, '?view=terminal')
    await expect(page.locator('.xterm-rows')).toContainText('a11y demo output line', { timeout: 15_000 })
    expect(await measureTouchTargets(page), 'PG-04 诊断视图').toEqual([])
  })
})

test.describe('M4-A 横屏紧凑模式（800×360）', () => {
  test.use({ viewport: { width: 800, height: 360 } })

  test('PG-03 横屏：头部/底部紧凑、主区优先、无横向溢出', async ({ page }) => {
    await preparePg03Mocks(page)
    await mountPg03(page)
    await expect(page.locator('.composer-input')).toBeVisible()
    await expectNoHorizontalOverflow(page)
    // 紧凑断言：头部 ≤60px 高（竖屏 ~60+），时间线保有主区高度。
    const headerBox = await page.locator('.workspace-header').boundingBox()
    expect(headerBox!.height).toBeLessThanOrEqual(60)
    const timelineBox = await page.locator('.timeline').boundingBox()
    expect(timelineBox!.height).toBeGreaterThan(80)
    await page.screenshot({ path: 'test-results/m4a-pg03-landscape.png', fullPage: false })
  })

  test('PG-01 横屏：诊断主操作优先可达、无横向溢出', async ({ page }) => {
    await mountPg01(page)
    await expect(page.getByRole('button', { name: '扫码配对' })).toBeVisible()
    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: 'test-results/m4a-pg01-landscape.png', fullPage: false })
  })

  test('PG-02 横屏：列表优先、无横向溢出', async ({ page }) => {
    await mountPg02(page)
    await expect(page.locator('.session-card').first()).toBeVisible()
    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: 'test-results/m4a-pg02-landscape.png', fullPage: false })
  })
})

test.describe('M4-A 软键盘 VisualViewport（模拟断言；iOS/Android 真机待 M4-C）', () => {
  test('PG-03：--vvh 变量接入，视口收缩时 Composer 跟随保持可达', async ({ page }) => {
    await preparePg03Mocks(page)
    await mountPg03(page)
    await expect(page.locator('.composer-input')).toBeVisible()
    // 变量已写入（初始=全高）。
    const vvh0 = await page.evaluate(() => document.documentElement.style.getPropertyValue('--vvh'))
    expect(Number.parseInt(vvh0)).toBeGreaterThan(700)
    // 模拟软键盘弹出几何：视口高 800→420（visualViewport.resize 真实触发）。
    await page.setViewportSize({ width: 360, height: 420 })
    await expect
      .poll(async () =>
        page.evaluate(() => Number.parseInt(document.documentElement.style.getPropertyValue('--vvh'))),
      )
      .toBeLessThanOrEqual(430)
    // 布局跟随：页面容器收缩，Composer 仍在可视区内。
    const pageBox = await page.locator('.workspace-page').boundingBox()
    expect(pageBox!.height).toBeLessThanOrEqual(430)
    const composerBox = await page.locator('.composer').boundingBox()
    expect(composerBox!.y + composerBox!.height).toBeLessThanOrEqual(425)
    // 输入聚焦（软键盘真实触发源）后 Composer 不被挤出。
    await page.locator('.composer-input').focus()
    await expect(page.locator('.composer-send')).toBeVisible()
    await page.screenshot({ path: 'test-results/m4a-pg03-keyboard-sim.png', fullPage: false })
  })
})

test.describe('M4-A safe-area CSS 接入（Chromium env()=0，断言规则接入；真机 inset 待 M4-C）', () => {
  test('底部导航/Composer/页头/横幅均含 env(safe-area-inset-*)', async ({ page }) => {
    await preparePg03Mocks(page)
    await mountPg03(page)
    const hits = await page.evaluate(() => {
      const needles = ['safe-area-inset-bottom', 'safe-area-inset-left', 'safe-area-inset-right', 'safe-area-inset-top']
      const found: Record<string, number> = Object.fromEntries(needles.map((n) => [n, 0]))
      for (const sheet of Array.from(document.styleSheets)) {
        let rules: CSSRuleList
        try {
          rules = sheet.cssRules
        } catch {
          continue
        }
        for (const rule of Array.from(rules)) {
          const text = rule.cssText
          for (const n of needles) if (text.includes(n)) found[n] += 1
        }
      }
      return found
    })
    expect(hits['safe-area-inset-bottom']).toBeGreaterThan(0)
    expect(hits['safe-area-inset-left']).toBeGreaterThan(0)
    expect(hits['safe-area-inset-right']).toBeGreaterThan(0)
    expect(hits['safe-area-inset-top']).toBeGreaterThan(0)
  })
})

test.describe('M4-A live region / 焦点 / 读屏路径', () => {
  test('PG-01→02→03：状态芯片 role=status、横幅 role=alert、路由切换焦点落页标题', async ({ page }) => {
    await preparePg03Mocks(page)
    await mountPg01(page)
    await expect(page.locator('.status-chips[role="status"][aria-live="polite"]')).toBeVisible()

    await mountPg02(page)
    await expect(page.locator('.status-chips[role="status"][aria-live="polite"]')).toBeVisible()
    // 路由切换焦点管理：大厅页标题获焦（tabindex=-1 程序化焦点，读屏逻辑起点）。
    await expect(page.locator('.lobby-title')).toBeFocused()

    await mountPg03(page)
    await expect(page.locator('.status-chips[role="status"][aria-live="polite"]')).toBeVisible()
    await expect(page.locator('.workspace-title')).toBeFocused()
  })

  test('PG-03 菜单：打开焦点进首项、Esc 关闭焦点回触发钮', async ({ page }) => {
    await preparePg03Mocks(page)
    await mountPg03(page)
    await page.locator('.menu-btn').click()
    await expect(page.locator('.menu-panel [role="menuitem"]').first()).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(page.locator('.menu-panel')).toHaveCount(0)
    await expect(page.locator('.menu-btn')).toBeFocused()
  })

  test('PG-02 回执/冲突横幅语义（role=status / role=alert）', async ({ page }) => {
    await mountPg02(page)
    // 停止 → 确认 → 回执 role=status（操作完成通告不抢焦点）。
    await page.getByRole('button', { name: /会话 .* 的操作菜单/ }).first().click()
    await page.route(`${BASE}/sessions/sess-1/stop`, (route) =>
      fulfillJson(route, 200, {
        id: 'sess-1',
        title: 'Claude Code · a11y-demo',
        cliType: 'claudecode',
        state: 'exited',
        control: { state: 'none' },
        lastActivityAt: new Date().toISOString(),
        workdir: '/users/dev/project',
        startedAt: new Date(Date.now() - 3_600_000).toISOString(),
        earliestSeq: 0,
        latestSeq: 1,
      }),
    )
    await page.getByRole('menuitem', { name: '停止会话' }).click()
    await page.getByRole('button', { name: '停止会话', exact: true }).click()
    await expect(page.locator('.receipt[role="status"]')).toBeVisible()
  })
})

test.describe('M4-A 截图证据（360 竖 / 320 回流 / 800×360 横）', () => {
  test('PG-01 + PG-03 三形态截图', async ({ page }) => {
    await preparePg03Mocks(page)
    // 360×800 竖屏（项目默认视口）
    await mountPg01(page)
    await page.screenshot({ path: 'test-results/m4a-pg01-360.png', fullPage: false })
    await mountPg03(page)
    await expect(page.locator('.composer-input')).toBeVisible()
    await page.screenshot({ path: 'test-results/m4a-pg03-360.png', fullPage: false })

    // 320×800 窄回流
    await page.setViewportSize({ width: 320, height: 800 })
    await page.evaluate(() => {
      window.location.hash = '#/connect'
    })
    await expect(page.locator('.diagnosis-title')).toHaveText('这台设备还没有配对')
    await page.screenshot({ path: 'test-results/m4a-pg01-320.png', fullPage: false })
    await mountPg03(page)
    await expect(page.locator('.composer-input')).toBeVisible()
    await page.screenshot({ path: 'test-results/m4a-pg03-320.png', fullPage: false })

    // 800×360 横屏紧凑
    await page.setViewportSize({ width: 800, height: 360 })
    await page.screenshot({ path: 'test-results/m4a-pg03-800x360.png', fullPage: false })
  })
})

test.describe('M4-A reduced-motion / 200% 缩放', () => {
  test('reduced-motion：过渡与动画收缩到即时', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await mountPg02(page)
    const duration = await page.evaluate(() => {
      const el = document.querySelector('.skeleton-line') ?? document.querySelector('.chip')
      if (!el) return '0s'
      const cs = getComputedStyle(el)
      return `${cs.animationDuration}|${cs.transitionDuration}`
    })
    // 全局兜底网把动画/过渡压到 ≤0.01ms（序列化可能是 0s / 0.00001s / 1e-05s）。
    const durations = duration
      .split('|')
      .flatMap((d) => d.split(','))
      .map((d) => d.trim())
      .filter(Boolean)
    const ok = durations.every((d) => {
      const n = Number.parseFloat(d)
      return d.endsWith('s') && Number.isFinite(n) && n <= 0.0001
    })
    expect(ok, `reduced-motion 下时长=${duration}`).toBe(true)
  })

  test('200% 缩放等效视口（360px@200% → 180px 逻辑宽）：四页无横向溢出、关键控件可达', async ({ page }) => {
    await preparePg03Mocks(page)
    // 如实声明：浏览器缩放不可编程；200% 缩放下 360px 设备宽 = 180 CSS px 逻辑视口。
    // meta viewport 已解禁 user-scalable（index.html），真机双指缩放可用。
    await page.setViewportSize({ width: 180, height: 800 })
    await mountPg01(page)
    await expectNoHorizontalOverflow(page)
    await expect(page.getByRole('button', { name: '扫码配对' })).toBeVisible()

    await mountPg02(page)
    await expectNoHorizontalOverflow(page)
    await expect(page.locator('.session-card').first()).toBeVisible()

    await mountPg03(page)
    await expect(page.locator('.composer-input')).toBeVisible()
    await expectNoHorizontalOverflow(page)
    // header 控件完整在视口内（flex 溢出被裁不产生文档级横向滚动——scrollWidth
    // 检测在此为盲，逐控件量几何右缘；M4-R1 实测 PG-04 menu-btn 曾溢出 18px）。
    await expectHeaderControlsInViewport(page)
    // 底部全部关键控件边界（M4-R2 谛听 M4-006：180px 实测 composer-stop 被右缘
    // 裁切；oracle 不得只查 input 可见）——Composer 全部按钮/输入区逐控件量左右缘。
    await expectBottomControlsInViewport(page)

    // PG-04 诊断视图（M4-R1 补全，谛听 M4-006）：xterm 字符栅格为固定 cell 几何，
    // 等效视口法下栅格不缩放（真实双指缩放会整体放大 canvas，模拟层无法代表）。
    // 本断言范围为页面骨架：诊断视图 header/Composer/控件无横向溢出且可达；
    // 栅格文本的放大阅读由 PG-03 结构化主阅读面承担（上述 PG-03 断言已覆盖 reflow），
    // 真机双指缩放下的栅格表现归 M4-C 物理验证。
    await mountPg03(page, '?view=terminal')
    await expect(page.locator('.xterm-rows')).toContainText('a11y demo output line', { timeout: 15_000 })
    await expectNoHorizontalOverflow(page)
    await expect(page.locator('.composer-input')).toBeVisible()
    await expectHeaderControlsInViewport(page)
    await expectBottomControlsInViewport(page)
    await page.screenshot({ path: 'test-results/m4a-pg04-200pct.png', fullPage: false })
  })

  test('底部控件边界三宽度（180/240/320）：PG-03/PG-04 Composer 全部控件几何完整', async ({ page }) => {
    // M4-R2 谛听 M4-006：断言扩展为全部底部控件 × 三档窄宽度，截图留证读图。
    await preparePg03Mocks(page)
    for (const width of [180, 240, 320]) {
      await page.setViewportSize({ width, height: 800 })
      await mountPg03(page)
      await expect(page.locator('.composer-input')).toBeVisible()
      await expectNoHorizontalOverflow(page)
      await expectBottomControlsInViewport(page)
      await page.screenshot({ path: `test-results/m4a-pg03-bottom-${width}.png`, fullPage: false })

      await mountPg03(page, '?view=terminal')
      await expect(page.locator('.xterm-rows')).toContainText('a11y demo output line', { timeout: 15_000 })
      await expectNoHorizontalOverflow(page)
      await expect(page.locator('.composer-input')).toBeVisible()
      await expectBottomControlsInViewport(page)
      await page.screenshot({ path: `test-results/m4a-pg04-bottom-${width}.png`, fullPage: false })
    }
  })
})

test.describe('M4-R3 E-09 Guide 首次态 × 窄屏（谛听 M4-006 R3）', () => {
  test('Guide 未 dismiss 首次态 180/240/320：Guide 内部滚动不挤压 Composer，关闭后恢复保持', async ({ page }) => {
    // M4-R3 谛听 M4-006：既有窄屏用例全部预置 guide.dismissed=1，绕过首次
    // Guide 高内容状态——真实产品 180×800 首次态停止按钮 bottom=852>800 且
    // 文档不可滚。本用例保留首次引导态，断言 Guide 与 Composer 纵向共存。
    await preparePg03Mocks(page, { dismissGuide: false })
    const consoleErrors = watchConsole(page)
    for (const width of [180, 240, 320]) {
      await page.setViewportSize({ width, height: 800 })
      if (page.url().startsWith('http')) {
        // 上一轮已 dismiss（localStorage=1）：清除并整页重载，恢复首次引导态。
        await page.evaluate((key) => localStorage.removeItem(key), GUIDE_KEY)
        await page.reload()
      }
      await mountPg03(page)

      // Guide 在位且可交互。
      const guide = page.locator('.guide-card')
      await expect(guide).toBeVisible()
      await expect(page.locator('.composer-input')).toBeVisible()
      await expectNoHorizontalOverflow(page)
      // 底部全部控件四边均在视口内（含停止运行；文档不可滚时 bottom 关键）。
      await expectBottomControlsInViewport(page)
      // 关闭钮本身在视口内（Guide 可关闭）。
      const closeBox = await page.locator('.guide-close').boundingBox()
      expect(closeBox, 'guide-close boundingBox 非空').not.toBeNull()
      expect(closeBox!.y, 'guide-close 顶缘 ≥ 0').toBeGreaterThanOrEqual(0)
      expect(closeBox!.y + closeBox!.height, `guide-close 底缘 ≤ 800`).toBeLessThanOrEqual(800)
      // Guide 内容超出时可内部滚动（收缩让位于 Composer，不是把 Composer 挤出去）。
      const guideGeo = await guide.evaluate((el) => {
        const before = el.scrollTop
        el.scrollTop = el.scrollHeight
        const scrolled = el.scrollTop
        el.scrollTop = before
        return { scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, canScroll: scrolled > before }
      })
      if (guideGeo.scrollHeight > guideGeo.clientHeight) {
        expect(guideGeo.canScroll, 'Guide 内容超出时必须可内部滚动').toBe(true)
      }
      await page.screenshot({ path: `test-results/m4a-pg03-guide-${width}.png`, fullPage: false })

      // 关闭 Guide 后恢复：引导消失、Composer 保持四边在视口、关闭状态持久化。
      await page.locator('.guide-close').click()
      await expect(guide).toHaveCount(0)
      await expect(page.locator('.composer-input')).toBeVisible()
      await expectBottomControlsInViewport(page)
      expect(await page.evaluate((key) => localStorage.getItem(key), GUIDE_KEY)).toBe('1')
      await page.screenshot({ path: `test-results/m4a-pg03-guide-dismissed-${width}.png`, fullPage: false })
    }
    expect(consoleErrors).toEqual([])
  })

  test('M4-R3 Settings 页 Auto Start 开关：真实 checkbox 触控面 ≥44px 且点击/键盘可交互（谛听 M4-R3-001 真实页面核查）', async ({ page }) => {
    // 谛听 R3 补全枚举后浮出的真实页面 checkbox（旧实现 0×0 透明隐藏，
    // 审计盲区内）：修复为覆盖整个 44×44 触控面的真实 hit area。
    // 本用例为该真实控件补运行时量测与交互门禁（静态审计已纳入 --gate）。
    await page.route('**/api/settings', (route) => {
      if (route.request().method() === 'GET') {
        return fulfillJson(route, 200, { remotePort: 8680, remoteToken: 'mock', autoStart: false, logLevel: 'info' })
      }
      return route.fallback()
    })
    await page.goto('/#/settings')
    // SettingsPage 挂载时检查 legacy connection store 的 connected（模块级 ref，
    // UI 侧无可达置位路径——DashboardPage.refresh 同样要求已连接）。经 Vite dev
    // 模块图取同一模块实例直接置位（如实走页面自身 store，不改产品代码），
    // 再 hash 导航回 /settings 触发重挂载。
    await page.evaluate(async () => {
      const mod = (await import('/src/stores/connection.ts')) as {
        useConnection: () => { connected: { value: boolean } }
      }
      mod.useConnection().connected.value = true
      window.location.hash = '#/settings'
    })
    const toggle = page.locator('.toggle-input')
    await expect(toggle).toBeAttached()
    const box = await toggle.boundingBox()
    expect(box, 'toggle-input boundingBox 非空').not.toBeNull()
    expect(box!.width, `toggle 触控面宽 ${box!.width} ≥ 44`).toBeGreaterThanOrEqual(44)
    expect(box!.height, `toggle 触控面高 ${box!.height} ≥ 44`).toBeGreaterThanOrEqual(44)
    // 点击透明 input 本身（真实 hit area）切换状态。
    await toggle.click()
    await expect(toggle).toBeChecked()
    // 键盘可达：focus 落在 input 上（焦点态经 :focus-visible 映射到视觉轨道）。
    await toggle.focus()
    await expect(toggle).toBeFocused()
    await page.screenshot({ path: 'test-results/m4a-settings-toggle.png', fullPage: false })
  })
})
