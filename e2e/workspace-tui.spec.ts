// e2e/workspace-tui.spec.ts — P1-C 全屏 TUI 会话场景（pi/omp 终端仿真面；Chromium-only）
// ---------------------------------------------------------------------------
// WS mock 形态如实声明（同 workspace-pg04）：Playwright routeWebSocket 全 mock，
// 不连接真实服务器；服务端事件经 ws.send(JSON) 注入，客户端帧经 ws.onMessage 观测；
// xterm 为真实引擎（动态 import 真实 chunk）。
// 帧样本：mobile/src/__tests__/fixtures/tui-frames.ts（SYNTHETIC 合成样本，标注
// 来源与构造依据；非真实 pi 录制）——同一批样本在 vitest 层经迷你屏幕 oracle
// 验证语义（见 tui-frames.test.ts / raw-terminal-view-tui.test.ts），本 spec 在真
// xterm DOM 上验证终端仿真面的用户可见契约：
//   · pi cliType 缺省直接进入终端面（R3）+ KeyTray 可点（canonical input 帧）；
//   · 整屏重绘帧不堆积、\r 覆写无残留、长输出滚入 scrollback（真 xterm）；
//   · 观察态降级只读（横幅出现）+ 控制权恢复横幅消失可交互。
// 与 mock 服务器的 input 帧交互：onMessage 回 input.ack（模拟确认服务器），
// 使 canonical outbox 窗口推进、后续按键可上线。
// ---------------------------------------------------------------------------

import { expect, test, type Page, type Route } from '@playwright/test'
import { LONG_OUTPUT_SCROLLBACK, PI_FULLSCREEN_REDRAW } from '../mobile/src/__tests__/fixtures/tui-frames'

const BASE = '**/api/remote/v1'
const GUIDE_KEY = 'amagi.pg03.guide.dismissed'
const ROWS = '[data-testid="terminal-host"] .xterm-rows'

// ---------------------------------------------------------------------------
// 夹具（与 workspace-pg04 同构）
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
    inputAckMode: 'session-window-v1',
    ...over,
  }
}

function makeDetail(over: Record<string, unknown> = {}) {
  return {
    id: 'sess-1',
    title: 'Pi · tui-demo',
    cliType: 'pi',
    state: 'running',
    control: { state: 'you' },
    lastActivityAt: new Date().toISOString(),
    workdir: '/users/dev/tui-demo',
    startedAt: new Date(Date.now() - 3_600_000).toISOString(),
    earliestSeq: 0,
    latestSeq: 0,
    ...over,
  }
}

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
      } else if (frame.type === 'input') {
        // 确认服务器：ACK 使 canonical outbox 窗口推进（后续按键可上线）。
        ws.send(
          JSON.stringify({
            type: 'input.ack',
            sessionId: 'sess-1',
            id: frame.id,
            requestId: frame.requestId,
          }),
        )
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

function inputPayloads(ws: WsMock): string[] {
  return ws.frames
    .filter((f) => f.type === 'input')
    .map((f) => Buffer.from(String(f.data), 'base64').toString('utf-8'))
}

function shotName(testInfo: { project: { name: string } }, name: string) {
  return `test-results/tui-${testInfo.project.name}-${name}.png`
}

async function dismissGuide(page: Page) {
  await page.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)
}

// ---------------------------------------------------------------------------
// 用例
// ---------------------------------------------------------------------------

test.describe('P1-C 全屏 TUI 会话（pi 终端仿真面）', () => {
  test('pi 会话缺省直接进入终端面；KeyTray 可点且 canonical input 帧载荷精确', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page)
    await page.goto('/#/workspace/sess-1')

    // R3：无 ?view 参数即终端仿真面（非诊断语义）。
    await expect(page.locator(ROWS)).toBeVisible()
    expect(page.url()).not.toContain('view=')
    await expect(page.locator('.tui-cli-badge')).toHaveText('终端仿真')
    await expect(page.locator('.diagnostic-badge')).toHaveCount(0)
    await expect(page.locator('.back-btn')).toContainText('大厅')

    // KeyTray 可点：^C 中断 → input 帧 \x03；Esc → \x1b；⌥W 看板 → \x1bw。
    const tray = page.locator('[data-testid="terminal-key-tray"]')
    await expect(tray).toBeVisible()
    await page.locator('[data-testid="key-ctrl-c"]').click()
    await expect.poll(() => inputPayloads(ws)).toEqual(['\x03'])
    await page.locator('[data-testid="key-esc"]').click()
    await expect.poll(() => inputPayloads(ws)).toEqual(['\x03', '\x1b'])
    await page.locator('[data-testid="key-alt-w"]').click()
    await expect.poll(() => inputPayloads(ws)).toEqual(['\x03', '\x1b', '\x1bw'])

    await page.screenshot({ path: shotName(testInfo, 'pi-default-terminal') })
    expect(consoleErrors).toEqual([])
  })

  test('全屏重绘帧不堆积、\\r 覆写无残留、长输出滚入 scrollback（真 xterm DOM）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page)
    await page.goto('/#/workspace/sess-1')
    await expect(page.locator(ROWS)).toBeVisible()

    // 整屏重绘 ×3（SYNTHETIC 帧，curses 风格 ED2+CUP）：终屏只含最后一帧。
    ws.send(output(1, PI_FULLSCREEN_REDRAW.frames[0]))
    await expect(page.locator(ROWS)).toContainText('[pi-tui] frame 1 ready')
    ws.send(output(2, PI_FULLSCREEN_REDRAW.frames[1]))
    ws.send(output(3, PI_FULLSCREEN_REDRAW.frames[2]))
    await expect(page.locator(ROWS)).toContainText('status: idle')

    const screenText = await page.locator(ROWS).textContent()
    expect((screenText!.match(/\[pi-tui\] frame \d ready/g) ?? []).length).toBe(1)
    expect(screenText).not.toContain('frame 1 ready')
    expect(screenText).not.toContain('frame 2 ready')

    // \r spinner 覆写：终态单行，旧帧与 braille 字符零残留。
    ws.send(output(4, 'generating ⠋\r'))
    ws.send(output(5, 'generating ⠙\r'))
    ws.send(output(6, '\r\x1b[2K✓ done in 1.2s\r\n'))
    await expect(page.locator(ROWS)).toContainText('✓ done in 1.2s')
    const afterSpinner = await page.locator(ROWS).textContent()
    expect(afterSpinner).not.toContain('generating')
    expect((afterSpinner!.match(/[⠋⠙⠹]/g) ?? []).length).toBe(0)

    // 长输出（61 行）滚入 scrollback：缓冲高于视口（slider 变短）；拖拽滚动条到顶，
    // 早期行仍在（历史正确滚动、尾部不丢）。（xterm 6 滚动由 scrollable-element
 // 接管，.xterm-viewport 不再反映滚动尺寸，故以 slider 比例 + 拖拽验证。）
    ws.send(output(7, LONG_OUTPUT_SCROLLBACK.frames[0]))
    await expect(page.locator(ROWS)).toContainText('tail status OK')

    const scrollbar = page.locator('.xterm-scrollable-element .scrollbar.vertical')
    const slider = scrollbar.locator('.slider')
    await expect
      .poll(async () => {
        const sb = await scrollbar.boundingBox()
        const sl = await slider.boundingBox()
        return sb && sl ? sb.height - sl.height : 0
      })
      .toBeGreaterThan(0)

    const sb = (await scrollbar.boundingBox())!
    const sl = (await slider.boundingBox())!
    await page.mouse.move(sl.x + sl.width / 2, sl.y + sl.height / 2)
    await page.mouse.down()
    await page.mouse.move(sl.x + sl.width / 2, sb.y + 2, { steps: 10 })
    await page.mouse.up()
    await expect(page.locator(ROWS)).toContainText('scrollback probe line 001')

    await page.screenshot({ path: shotName(testInfo, 'redraw-spinner-scrollback') })
    expect(consoleErrors).toEqual([])
  })

  test('观察态降级只读：横幅出现、按键禁用、无 input 帧；恢复控制后横幅消失可交互', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page, makeDetail({ control: { state: 'other', deviceName: 'iPad Pro' } }))
    const ws = await mockWs(page, {
      autoAttach: () =>
        attached({ snapshot: snapshot({ control: { state: 'other', deviceName: 'iPad Pro' } }) }),
    })
    await page.goto('/#/workspace/sess-1')

    // 观察态仍默认终端面（可观察），只读横幅出现并说明原因。
    await expect(page.locator(ROWS)).toBeVisible()
    const banner = page.locator('[data-testid="terminal-readonly-indicator"]')
    await expect(banner).toBeVisible()
    await expect(banner).toContainText('iPad Pro')

    // KeyTray 全禁用：无任何 input 帧（P-04 不扩权）。
    await expect(page.locator('[data-testid="key-esc"]')).toBeDisabled()
    await expect(page.locator('[data-testid="key-ctrl-c"]')).toBeDisabled()
    expect(inputPayloads(ws)).toEqual([])

    // 桌面释放控制 → 横幅消失、按键恢复、按键可上线。
    ws.send({ type: 'control.state', sessionId: 'sess-1', state: 'you', reason: 'acquired', occurredAt: '2026-09-10T00:00:00Z' })
    await expect(banner).toHaveCount(0)
    await expect(page.locator('[data-testid="key-esc"]')).toBeEnabled()
    await page.locator('[data-testid="key-esc"]').click()
    await expect.poll(() => inputPayloads(ws)).toEqual(['\x1b'])

    await page.screenshot({ path: shotName(testInfo, 'readonly-degrade-restore') })
    expect(consoleErrors).toEqual([])
  })
})
