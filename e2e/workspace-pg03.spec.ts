// e2e/workspace-pg03.spec.ts — M2-C PG-03 内容转化工作区（Chromium-only）
// ---------------------------------------------------------------------------
// WS mock 形态如实声明：Playwright routeWebSocket 全 mock（不连接真实服务器）。
//   · page.routeWebSocket('**/ws/v1') 拦截升级；handler 内不调用 connectToServer，
//     服务端事件由测试脚本经 ws.send(JSON) 注入，客户端帧经 ws.onMessage 观测；
//   · REST（detail/stop/control）一律 page.route mock（形状对齐 M2-A mapper）。
// 真实 WS 服务器 E2E（真 attach/真重连/真 backfill）属 M2-INT，本 spec 不伪证。
// 事件/帧形状逐字段对齐 mobile/src/lib/contract（M0-03 冻结）；本文件构造的是
// 测试夹具，不 import 前端实现（route mock 在浏览器外）。
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

function boundary(seq: number, occurredAt = '2026-08-03T01:00:00Z') {
  return { type: 'session.state', sessionId: 'sess-1', state: 'running', restartBoundary: true, seq, occurredAt }
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
    // M3-001：capability-capable server（inputAckMode 协商）= 正常生产场景；
    // 缺失时新客户端只读（fail-closed）。mock 夹具默认协商 canonical 能力。
    inputAckMode: 'session-window-v1',
    ...over,
  }
}

function makeDetail(over: Record<string, unknown> = {}) {
  return {
    id: 'sess-1',
    title: 'Claude Code · pg03-demo',
    cliType: 'claudecode',
    state: 'running',
    control: { state: 'you' },
    lastActivityAt: new Date().toISOString(),
    workdir: '/users/dev/pg03-demo',
    startedAt: new Date(Date.now() - 3_600_000).toISOString(),
    earliestSeq: 0,
    latestSeq: 0,
    ...over,
  }
}

/** 七类转化组件各一份的演示输出（seq 1..7）。 */
const SEVEN_KINDS: { seq: number; text: string }[] = [
  { seq: 1, text: 'Do you want to run this command?\n❯ 1. Yes\n  2. No\n' },
  { seq: 2, text: 'Pick a color scheme:\n1. Solarized\n2. Nord\n' },
  { seq: 3, text: Array.from({ length: 15 }, (_, i) => `build output line ${i + 1}`).join('\n') + '\n' },
  { seq: 4, text: 'Bash(npm run build)\n⎿  compiled in 4.2s\n' },
  { seq: 5, text: 'Error: ECONNREFUSED 127.0.0.1:3000\n' },
  { seq: 6, text: '⠋ Building project…\n' },
  { seq: 7, text: 'plain terminal output line\n' },
]

// ---------------------------------------------------------------------------
// Mock helpers
// ---------------------------------------------------------------------------

interface WsMock {
  /** 已收到的客户端帧。 */
  frames: Record<string, unknown>[]
  /** 连接次数（重连=新连接）。 */
  connections: number
  /** 向页面注入服务端事件。 */
  send: (payload: unknown) => void
  /** 服务端主动断开（触发客户端重连）。 */
  closeFromServer: (code: number) => void
}

async function mockWs(page: Page, opts: { autoAttach?: (frames: Record<string, unknown>[]) => unknown } = {}): Promise<WsMock> {
  const state: WsMock = {
    frames: [],
    connections: 0,
    send: () => {},
    closeFromServer: () => {},
  }
  await page.routeWebSocket('**/ws/v1', (ws) => {
    state.connections += 1
    state.send = (payload) => ws.send(JSON.stringify(payload))
    state.closeFromServer = (code) => ws.close(code)
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

async function enterWorkspace(page: Page, sessionId = 'sess-1') {
  await page.goto(`/#/workspace/${sessionId}`)
  await expect(page.locator('.workspace-title')).toBeVisible()
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
  return `test-results/pg03-${testInfo.project.name}-${name}.png`
}

async function dismissGuide(page: Page) {
  await page.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)
}

// ---------------------------------------------------------------------------
// 用例
// ---------------------------------------------------------------------------

test.describe('M2-C PG-03 内容转化工作区', () => {
  test('七类内容转化组件渲染（PromptAction/OptionCard/FoldBlock/ToolCallCard/ErrorCard/ProgressCard/MonoBlock）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    await mockWs(page, {
      autoAttach: () =>
        attached({
          earliestSeq: 1,
          latestSeq: SEVEN_KINDS.length,
          history: SEVEN_KINDS.map((k) => output(k.seq, k.text)),
        }),
    })
    await enterWorkspace(page)

    await expect(page.locator('.prompt-action')).toBeVisible()
    await expect(page.locator('.prompt-action .prompt-btn')).toHaveCount(2)
    await expect(page.locator('.option-card')).toBeVisible()
    await expect(page.locator('.option-card .option-btn')).toHaveCount(2)
    await expect(page.locator('.fold-block')).toBeVisible()
    await expect(page.locator('.fold-block .fold-count')).toContainText('15 行')
    await expect(page.locator('.tool-card')).toBeVisible()
    await expect(page.locator('.tool-card .tool-title')).toContainText('Bash')
    await expect(page.locator('.error-card')).toBeVisible()
    await expect(page.locator('.error-card .error-next')).toContainText('下一步')
    await expect(page.locator('.progress-card')).toBeVisible()
    await expect(page.locator('.progress-card .progress-stop')).toHaveText('停止运行')
    await expect(page.locator('.mono-block').last()).toContainText('plain terminal output line')

    // 五层状态条：附着后全绿
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')
    await expect(page.locator('.status-bar')).toContainText('授权：已授权')
    await expect(page.locator('.status-bar')).toContainText('会话：运行中')
    await expect(page.locator('.status-bar')).toContainText('控制：你正在控制')
    await expect(page.locator('.status-bar')).toContainText('历史：连续')

    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: shotName(testInfo, 'seven-components'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('重启边界原位渲染（PR-05）+ 边界两侧输出分属不同段', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    await mockWs(page, {
      autoAttach: () =>
        attached({
          earliestSeq: 1,
          latestSeq: 3,
          history: [output(1, 'before restart\n'), boundary(2), output(3, 'after restart\n')],
        }),
    })
    await enterWorkspace(page)

    const marker = page.locator('.boundary-marker')
    await expect(marker).toBeVisible()
    await expect(marker).toContainText('会话已重启')

    // 边界原位：before → boundary → after 的顺序
    const texts = await page.locator('.timeline-row').allTextContents()
    const joined = texts.join('|')
    expect(joined.indexOf('before restart')).toBeLessThan(joined.indexOf('会话已重启'))
    expect(joined.indexOf('会话已重启')).toBeLessThan(joined.indexOf('after restart'))

    await page.screenshot({ path: shotName(testInfo, 'restart-boundary'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('Composer 发送：input 帧形状 + 用户指令 coral 块上屏 + 防连点 + 历史复用', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page)
    await enterWorkspace(page)

    // resize 帧随附着上报
    await expect.poll(() => ws.frames.some((f) => f.type === 'resize')).toBe(true)

    await page.locator('.composer-input').fill('npm test')
    await page.locator('.composer-send').click()
    const inputFrame = ws.frames.find((f) => f.type === 'input')
    expect(inputFrame).toBeDefined()
    expect(typeof inputFrame?.id).toBe('string')
    expect(Buffer.from(String(inputFrame?.data), 'base64').toString('utf-8')).toBe('npm test\r')

    // 用户指令块（coral 左边条）+ 草稿清空（确认发送语义）
    await expect(page.locator('.user-message')).toContainText('npm test')
    await expect(page.locator('.composer-input')).toHaveValue('')
    // 防连点：草稿已空，发送按钮禁用
    await expect(page.locator('.composer-send')).toBeDisabled()

    // 历史复用：点选回填草稿，不直接发送
    await page.locator('.composer-history').click()
    await page.locator('.history-item', { hasText: 'npm test' }).click()
    await expect(page.locator('.composer-input')).toHaveValue('npm test')
    expect(ws.frames.filter((f) => f.type === 'input')).toHaveLength(1)

    await page.screenshot({ path: shotName(testInfo, 'composer-flow'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('PromptAction 点按即答：发送编号输入帧', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page, {
      autoAttach: () =>
        attached({
          earliestSeq: 1,
          latestSeq: 1,
          history: [output(1, 'Do you want to proceed?\n❯ 1. Yes\n  2. No\n')],
        }),
    })
    await enterWorkspace(page)

    await page.locator('.prompt-btn', { hasText: 'Yes' }).click()
    const inputFrame = ws.frames.find((f) => f.type === 'input')
    expect(Buffer.from(String(inputFrame?.data), 'base64').toString('utf-8')).toBe('1\r')
    expect(consoleErrors).toEqual([])
  })

  test('观察者禁用：输入/应答/停止全部禁用并明示原因', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page, makeDetail({ control: { state: 'desktop' } }))
    await mockWs(page, {
      autoAttach: () =>
        attached({
          earliestSeq: 1,
          latestSeq: 2,
          snapshot: snapshot({ control: { state: 'desktop' } }),
          history: [
            output(1, 'Do you want to proceed?\n❯ 1. Yes\n  2. No\n'),
            output(2, '⠋ Working…\n'),
          ],
        }),
    })
    await enterWorkspace(page)

    await expect(page.locator('.composer-input')).toBeDisabled()
    await expect(page.locator('.composer-block-reason')).toContainText('桌面端正在控制')
    await expect(page.locator('.prompt-btn').first()).toBeDisabled()
    await expect(page.locator('.composer-stop')).toHaveCount(0) // 非控制者无停止按钮
    await expect(page.locator('.progress-stop')).toHaveCount(0)
    await expect(page.locator('.control-bar')).toContainText('桌面端控制中')
    await expect(page.locator('.control-bar')).toContainText('观察中')

    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: shotName(testInfo, 'observer-disabled'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('停止运行：显式按钮 → PG-06 确认 → stop 请求（confirm:true）→ 会话态更新', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const stopBodies: unknown[] = []
    await page.route(`${BASE}/sessions/sess-1/stop`, (route: Route) => {
      stopBodies.push(route.request().postDataJSON())
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(makeDetail({ state: 'stopped' })),
      })
    })
    await mockWs(page)
    await enterWorkspace(page)

    await page.locator('.composer-stop').click()
    const dialog = page.locator('.confirm-dialog')
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText('停止运行')

    // Esc 取消零副作用
    await page.keyboard.press('Escape')
    expect(stopBodies).toHaveLength(0)

    // 确认
    await page.locator('.composer-stop').click()
    await dialog.getByRole('button', { name: '停止运行' }).click()
    await expect.poll(() => stopBodies.length).toBe(1)
    expect(stopBodies[0]).toEqual({ confirm: true })
    await expect(page.locator('.status-bar')).toContainText('会话：已停止')

    await page.screenshot({ path: shotName(testInfo, 'stop-flow'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('断线自动重连（≤5s）与 attach 恢复（携带 lastSeq）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    let attachCount = 0
    const ws = await mockWs(page, {
      autoAttach: () => {
        attachCount += 1
        return attached({
          earliestSeq: 1,
          latestSeq: 2,
          history: attachCount === 1 ? [output(1, 'first\n'), output(2, 'second\n')] : [],
          snapshot: snapshot({ history: attachCount === 1 ? { state: 'continuous' } : { state: 'backfilled' } }),
        })
      },
    })
    await enterWorkspace(page)
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')

    // 服务端断开（1006 异常）→ 客户端自动重连
    ws.closeFromServer(1006)
    await expect(page.locator('.status-bar')).toContainText('恢复中', { timeout: 3000 })
    await expect.poll(() => ws.connections, { timeout: 3000 }).toBe(2)

    // 重连 attach 携带 lastSeq=2（expectedRunPosition 由服务端保证；客户端呈现恢复）
    const reattach = ws.frames.filter((f) => f.type === 'attach').at(-1)
    expect(reattach?.lastSeq).toBe(2)
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')
    await expect(page.locator('.status-bar')).toContainText('历史：已补齐')

    await page.screenshot({ path: shotName(testInfo, 'reconnect-recovered'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('GapMarker：首次 attach 全量历史（late-attach 修复）+ live 缺口原位补齐', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    // design §3：detail.latestSeq 是 REST advisory bound，不作游标。
    // 新设备首 attach 省略 lastSeq → 服务端返回全量 retained tail（late-attach 修复端到端可见）。
    await mockRest(page, makeDetail({ earliestSeq: 1, latestSeq: 50 }))
    const ws = await mockWs(page, {
      autoAttach: () =>
        attached({
          earliestSeq: 1,
          latestSeq: 3,
          history: [output(1, 'retained one\n'), output(2, 'retained two\n'), output(3, 'retained three\n')],
        }),
    })
    await enterWorkspace(page)

    // 首次 attach 必须 omit lastSeq（绝不用 detail.latestSeq=50）——在 handler 外断言。
    const firstAttach = ws.frames.find((f) => f.type === 'attach')
    expect(firstAttach).toBeDefined()
    expect('lastSeq' in (firstAttach ?? {})).toBe(false)

    // 全量可用历史渲染：新设备看到 seq1–3 全部 retained 帧（late-attach 修复端到端可见）。
    await expect(page.locator('.mono-block').first()).toContainText('retained one')
    await expect(page.locator('.mono-block').first()).toContainText('retained three')

    // live seq=5 越洞 [4,4]：recoverable 原位标记 + 机器属性（addendum §1.2：
    // 可恢复缺口只能来自 live reorder；attached-time gap 恒为已逐出 origin 段）。
    ws.send(output(5, 'live five\n'))
    const gap = page.locator('[data-testid=gap-marker]')
    await expect(gap).toBeVisible()
    await expect(gap).toContainText('历史缺口：第 4–4 段未保留')
    await expect(gap).toHaveAttribute('data-gap-state', 'recoverable')
    await expect(gap).toHaveAttribute('data-from-seq', '4')
    await expect(gap).toHaveAttribute('data-to-seq', '4')
    await expect(page.locator('.status-bar')).toContainText('历史：存在缺口')
    await page.screenshot({ path: shotName(testInfo, 'gap-marker'), fullPage: false })

    // 尝试补齐 → backfill 帧 → 内容原位替换（标记消失）。
    await gap.getByRole('button', { name: '尝试补齐' }).click()
    await expect(gap).toHaveAttribute('data-gap-state', 'filling')
    const bf = ws.frames.find((f) => f.type === 'backfill')
    expect(bf).toBeDefined()
    expect([bf?.fromSeq, bf?.toSeq]).toEqual([4, 4])
    ws.send({
      type: 'backfill.result',
      requestId: String(bf?.requestId),
      sessionId: 'sess-1',
      fromSeq: 4,
      toSeq: 4,
      earliestSeq: 1,
      latestSeq: 5,
      frames: [output(4, 'filled four\n')],
    })
    await expect(page.locator('[data-testid=gap-marker]')).toHaveCount(0)
    await expect(page.locator('.mono-block', { hasText: 'filled four' })).toBeVisible()

    await page.screenshot({ path: shotName(testInfo, 'gap-filled'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('GapMarker：低于保留窗的缺口裁定 settled-unavailable（exhausted 原位保留，无补齐按钮）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page, makeDetail({ earliestSeq: 10, latestSeq: 10 }))
    await mockWs(page, {
      autoAttach: () =>
        attached({
          earliestSeq: 10,
          latestSeq: 10,
          snapshot: snapshot({ history: { state: 'gap', gap: { code: 'history.gap', fromSeq: 1, toSeq: 9 } } }),
          history: [output(10, 'retained latest\n')],
        }),
    })
    await enterWorkspace(page)

    // design §3：attached earliestSeq(10)>F+1(1) → [1,9] 已逐出 → 显式不可恢复提示。
    const gap = page.locator('[data-testid=gap-marker]')
    await expect(gap).toBeVisible()
    await expect(gap).toHaveAttribute('data-gap-state', 'exhausted')
    await expect(gap).toContainText('该段已不可补齐，从最新继续')
    await expect(gap.getByRole('button', { name: '尝试补齐' })).toHaveCount(0)
    await expect(page.locator('.status-bar')).toContainText('历史：存在缺口')
    await page.screenshot({ path: shotName(testInfo, 'gap-exhausted'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('新输出 pill：离底时新帧计数，点击回底', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    // 空行分隔 → 30 个独立 mono 块（保证可滚动）
    const many = Array.from({ length: 30 }, (_, i) => output(i + 1, `line ${i + 1}\n\n`))
    const ws = await mockWs(page, {
      autoAttach: () => attached({ earliestSeq: 1, latestSeq: 30, history: many }),
    })
    await enterWorkspace(page)

    // 滚到顶部（离底），再注入新输出
    await page.locator('.timeline').evaluate((el) => (el.scrollTop = 0))
    ws.send(output(31, 'fresh output after scroll\n'))
    const pill = page.locator('.new-output-pill')
    await expect(pill).toBeVisible()
    await expect(pill).toContainText('新输出')
    await page.screenshot({ path: shotName(testInfo, 'new-output-pill'), fullPage: false })

    await pill.click()
    await expect(pill).toHaveCount(0)
    await expect(page.locator('.mono-block', { hasText: 'fresh output after scroll' }).last()).toBeVisible()
    expect(consoleErrors).toEqual([])
  })

  test('E-09 引导卡 + 原始终端诊断视图入口（M2-D 本体已交付，不改权限）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await mockRest(page)
    await mockWs(page)
    await enterWorkspace(page)

    // E-09 首次进入引导（本 context 未设 dismissed）
    const guide = page.locator('.guide-card')
    await expect(guide).toBeVisible()
    await expect(guide).toContainText('结构化阅读')
    await page.screenshot({ path: shotName(testInfo, 'guide-e09'), fullPage: false })

    // 菜单 → 诊断视图（M2-D 本体：?view=terminal，身份明示，不改权限）
    await page.locator('.menu-btn').click()
    await page.locator('.menu-item', { hasText: '原始终端诊断视图' }).click()
    await expect(page.locator('.diagnostic-badge')).toHaveText('诊断视图')
    await expect(page.locator('.raw-terminal-host .xterm-rows')).toBeVisible()
    expect(page.url()).toContain('view=terminal')
    await page.screenshot({ path: shotName(testInfo, 'diagnostic-open'), fullPage: false })
    // 返回主阅读面（引导卡依旧可见——进入诊断不改变任何状态）
    await page.locator('.back-btn--primary').click()
    await expect(page.locator('.raw-terminal-view')).toHaveCount(0)
    await expect(guide).toBeVisible()

    // 关闭引导 → 不再出现（localStorage 记忆）
    await page.locator('.guide-close').click()
    await expect(guide).toHaveCount(0)
    await page.reload()
    await expect(page.locator('.workspace-title')).toBeVisible()
    await expect(page.locator('.guide-card')).toHaveCount(0)
    // 重载后权限与连接不受影响（仍已连接）
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')
    expect(consoleErrors).toEqual([])
  })

  test('控制被收回原因提示（control.state 事件）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page)
    await enterWorkspace(page)
    await expect(page.locator('.control-bar')).toContainText('你正在控制')

    // 确定性等待：先确认 WS 已 attach（snapshot 已落盘），再发 control.state，
    // 避免重连/初始 attach 快照 control:'you' 在断言窗口内覆盖收回事件。
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')

    ws.send({
      type: 'control.state',
      sessionId: 'sess-1',
      state: 'desktop',
      reason: 'takeover',
      occurredAt: '2026-08-03T02:00:00Z',
    })
    await expect(page.locator('.control-bar')).toContainText('桌面端控制中')
    // E-06（design §7）：takeover 映射固定文案（unknown reason 不直出）。
    await expect(page.locator('[data-testid=control-notice]')).toContainText('桌面端已收回控制权')
    await expect(page.locator('[data-testid=control-notice]')).toHaveAttribute('data-e', 'e06')
    await expect(page.locator('[data-testid=control-notice]')).toHaveAttribute('data-kind', 'lost')
    await expect(page.locator('[data-testid=control-notice]')).toHaveAttribute('data-control-state', 'desktop')
    // 写操作即时禁用
    await expect(page.locator('.composer-input')).toBeDisabled()
    expect(consoleErrors).toEqual([])
  })
})
