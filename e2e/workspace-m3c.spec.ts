// e2e/workspace-m3c.spec.ts — M3-C 前端五层状态与恢复交互（Chromium-only，WS 全 mock）
// ---------------------------------------------------------------------------
// 权威依据：fuxi 20260804-m3-continuity-design/design.md §7（E-06/07/08 显著交互 +
// NoticeStack 优先级 + GapMarker 原位消除语义）+ §8（机器 oracle 固定 DOM 属性）。
// WS mock 形态如实声明：Playwright routeWebSocket 全 mock（不连接真实服务器）；
// 真实服务器 + WS relay 整形全链见 workspace-m3c-relay.spec.ts。
// 覆盖：
//   · E-06：takeover(desktop/other) 映射文案、初始 observer 无收回文案、
//     本地 release 无 E-06、acquire 409 conflict、data-testid 机器属性；
//   · E-07：断线→reconnecting（退避倒计时/Composer 禁用/时间线保留）→restored
//     ≥3s 自动消退、restored-with-gap「已恢复，部分历史不可用」+跳到缺口、
//     terminal close → P0 覆盖（不显示已恢复）；
//   · E-08：data-gap-state recoverable→filling→exhausted + 原位消除；
//   · outbox 可视化：发送中/ACK 已确认/控制权接管停发/恢复后自动重发反馈；
//   · 五层 StatusBar 在断连/恢复/控制/缺口变迁下的全量投影。
// ---------------------------------------------------------------------------

import { expect, test, type Page, type Route } from '@playwright/test'

const BASE = '**/api/remote/v1'
const GUIDE_KEY = 'amagi.pg03.guide.dismissed'

// ---------------------------------------------------------------------------
// 夹具（与 workspace-pg03.spec.ts 同源形状）
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
    // M3-001：capability-capable server（inputAckMode 协商）= 正常生产场景；
    // 缺失时新客户端只读（fail-closed）。mock 夹具默认协商 canonical 能力。
    inputAckMode: 'session-window-v1',
    ...over,
  }
}

function makeDetail(over: Record<string, unknown> = {}) {
  return {
    id: 'sess-1',
    title: 'Claude Code · m3c-demo',
    cliType: 'claudecode',
    state: 'running',
    control: { state: 'you' },
    lastActivityAt: new Date().toISOString(),
    workdir: '/users/dev/m3c-demo',
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
    // close 需对象参数（positional number 不会成为 close code，会导致 1005 语义偏移）。
    state.closeFromServer = (code) => ws.close({ code, reason: 'm3c-test' })
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

async function enterWorkspace(page: Page) {
  await page.goto('/#/workspace/sess-1')
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

function shotName(testInfo: { project: { name: string } }, name: string) {
  return `test-results/m3c-${testInfo.project.name}-${name}.png`
}

async function dismissGuide(page: Page) {
  await page.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)
}

// ---------------------------------------------------------------------------
// 用例
// ---------------------------------------------------------------------------

test.describe('M3-C E-06 控制权收回/冲突（data-testid=control-notice）', () => {
  test('takeover→desktop/other 映射文案 + 机器属性；Composer 同拍禁用', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page)
    await enterWorkspace(page)
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')

    // you→desktop（takeover）：固定映射文案（unknown reason 不直出）。
    ws.send({ type: 'control.state', sessionId: 'sess-1', state: 'desktop', reason: 'takeover', occurredAt: '2026-08-04T01:00:00Z' })
    const notice = page.locator('[data-testid=control-notice]')
    await expect(notice).toBeVisible()
    await expect(notice).toHaveAttribute('data-e', 'e06')
    await expect(notice).toHaveAttribute('data-kind', 'lost')
    await expect(notice).toHaveAttribute('data-control-state', 'desktop')
    await expect(notice).toContainText('桌面端已收回控制权')
    await expect(page.locator('.composer-input')).toBeDisabled()
    await expect(page.locator('.status-bar')).toContainText('控制：桌面端控制中')
    await page.screenshot({ path: shotName(testInfo, 'e06-takeover-desktop'), fullPage: false })

    // you→other（takeover + deviceName）：「由{deviceName}取得」。
    ws.send({ type: 'control.state', sessionId: 'sess-1', state: 'you', reason: 'acquired', occurredAt: '2026-08-04T01:01:00Z' })
    await expect(notice).toHaveCount(0)
    ws.send({ type: 'control.state', sessionId: 'sess-1', state: 'other', deviceName: 'iPad Pro', reason: 'takeover', occurredAt: '2026-08-04T01:02:00Z' })
    await expect(notice).toBeVisible()
    await expect(notice).toHaveAttribute('data-control-state', 'other')
    await expect(notice).toContainText('控制权已由 iPad Pro 取得')
    await page.screenshot({ path: shotName(testInfo, 'e06-takeover-other'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('初始 observer 无收回文案；本地 release 无 E-06；acquire 409 标 conflict 不改权威态', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    // 初始即 desktop 控制（observer 视角）。
    await mockRest(page, makeDetail({ control: { state: 'desktop' } }))
    let releaseCount = 0
    await page.route(`${BASE}/sessions/sess-1/control/*`, (route: Route) => {
      const url = route.request().url()
      if (url.includes('release')) {
        releaseCount += 1
        return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ state: 'none' }) })
      }
      if (url.includes('acquire')) {
        // 首次 acquire 成功；第二次 409 冲突。
        if (releaseCount === 0) {
          return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ state: 'you' }) })
        }
        return route.fulfill({
          status: 409,
          contentType: 'application/json',
          body: JSON.stringify({ requestId: 'r-409', code: 'control.busy', layer: 'control', message: 'control held by another device', actionHint: 'retry' }),
        })
      }
      return route.fallback()
    })
    const ws = await mockWs(page, {
      autoAttach: () => attached({ snapshot: snapshot({ control: { state: 'desktop' } }) }),
    })
    await enterWorkspace(page)
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')
    // 初始 observer：只显示身份，不伪称「被收回」。
    await expect(page.locator('.control-bar')).toContainText('桌面端控制中')
    await expect(page.locator('[data-testid=control-notice]')).toHaveCount(0)

    // 取得控制权 → 本地 release → correlated control.state(none) 不产 E-06。
    ws.send({ type: 'control.state', sessionId: 'sess-1', state: 'none', reason: 'released', occurredAt: '2026-08-04T01:00:00Z' })
    await expect(page.locator('.control-bar')).toContainText('无人控制')
    await page.getByRole('button', { name: '获取控制权' }).click()
    await expect(page.locator('.control-bar')).toContainText('你正在控制')
    await page.getByRole('button', { name: '释放控制权' }).click()
    await expect(page.locator('.control-bar')).toContainText('无人控制')
    ws.send({ type: 'control.state', sessionId: 'sess-1', state: 'none', reason: 'released', occurredAt: '2026-08-04T01:01:00Z' })
    await expect(page.locator('[data-testid=control-notice]')).toHaveCount(0)

    // acquire 409：同一区域标 conflict（data-kind=conflict），权威 state 不被 409 改变。
    await page.getByRole('button', { name: '获取控制权' }).click()
    const notice = page.locator('[data-testid=control-notice]')
    await expect(notice).toBeVisible()
    await expect(notice).toHaveAttribute('data-kind', 'conflict')
    await expect(notice).toHaveAttribute('data-control-state', 'none')
    await expect(notice).toContainText('控制权冲突')
    await expect(page.locator('.control-bar')).toContainText('无人控制')
    await page.screenshot({ path: shotName(testInfo, 'e06-conflict-409'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })
})

test.describe('M3-C E-07 断线→重连→恢复（data-testid=continuity-banner）', () => {
  test('断线 → reconnecting（倒计时/Composer 禁用/时间线保留）→ restored ≥3s 消退', async ({ page }, testInfo) => {
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
          history: attachCount === 1 ? [output(1, 'before break\n'), output(2, 'still here\n')] : [],
        })
      },
    })
    await enterWorkspace(page)
    await expect(page.locator('.mono-block').first()).toContainText('before break')

    // 服务端断开（1006）→ E-07 reconnecting：banner 显著呈现，机器属性齐全。
    ws.closeFromServer(1006)
    const banner = page.locator('[data-testid=continuity-banner]')
    await expect(banner).toBeVisible({ timeout: 3000 })
    await expect(banner).toHaveAttribute('data-state', 'reconnecting')
    await expect(banner).toHaveAttribute('data-generation', '1')
    await expect(banner).toContainText('连接中断，正在恢复')
    await expect(banner).toContainText('后重试')
    // 时间线保留 + Composer 禁用。
    await expect(page.locator('.mono-block').first()).toContainText('before break')
    await expect(page.locator('.composer-input')).toBeDisabled()
    await expect(page.locator('.status-bar')).toContainText('恢复中')
    await page.screenshot({ path: shotName(testInfo, 'e07-reconnecting'), fullPage: false })

    // 自动重连（≤5s 退避）→ attached → restored success。
    await expect.poll(() => ws.connections, { timeout: 6000 }).toBe(2)
    await expect(banner).toHaveAttribute('data-state', 'restored')
    await expect(banner).toContainText('已恢复')
    await expect(banner).not.toContainText('部分历史不可用')
    await expect(page.locator('.composer-input')).toBeEnabled()
    await page.screenshot({ path: shotName(testInfo, 'e07-restored'), fullPage: false })

    // ≥3s 后自动消退。
    await expect(banner).toHaveCount(0, { timeout: 6000 })
    expect(consoleErrors).toEqual([])
  })

  test('restored-with-gap：「已恢复，部分历史不可用」+ 跳到缺口 + GapMarker 保留', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    let attachCount = 0
    const ws = await mockWs(page, {
      autoAttach: () => {
        attachCount += 1
        if (attachCount === 1) return attached({ earliestSeq: 0, latestSeq: 0 })
        // 恢复 attach：origin 已逐出（earliest=3），gap [1,2] settled-unavailable。
        return attached({
          earliestSeq: 3,
          latestSeq: 3,
          snapshot: snapshot({ history: { state: 'gap', gap: { code: 'history.gap', fromSeq: 1, toSeq: 2 } } }),
          history: [output(3, 'after recovery\n')],
        })
      },
    })
    await enterWorkspace(page)
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')

    ws.closeFromServer(1006)
    await expect(page.locator('[data-testid=continuity-banner]')).toBeVisible({ timeout: 3000 })
    await expect.poll(() => ws.connections, { timeout: 6000 }).toBe(2)

    // 恢复与缺口同拍：直接 restored-with-gap（不闪纯 success），编号 E-07+E-08 不重编。
    const banner = page.locator('[data-testid=continuity-banner]')
    await expect(banner).toHaveAttribute('data-state', 'restored')
    await expect(banner).toContainText('已恢复，部分历史不可用')
    const gap = page.locator('[data-testid=gap-marker]')
    await expect(gap).toBeVisible()
    await expect(gap).toHaveAttribute('data-gap-state', 'exhausted')
    await page.screenshot({ path: shotName(testInfo, 'e07-restored-with-gap'), fullPage: false })

    // 跳到缺口：滚动到原位标记（GapMarker 始终保留）。
    await banner.getByRole('button', { name: '跳到缺口' }).click()
    await expect(gap).toBeVisible()
    expect(consoleErrors).toEqual([])
  })

  test('terminal close（1002 协议错误）→ P0 覆盖：恢复条隐藏 + terminal banner', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page)
    await enterWorkspace(page)
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')

    ws.closeFromServer(1002)
    // terminal：无重连（connections 保持 1）、无「已恢复」、P0 terminal banner。
    await expect(page.locator('[data-testid=terminal-banner]')).toBeVisible({ timeout: 3000 })
    await expect(page.locator('[data-testid=terminal-banner]')).toContainText('连接已终止')
    await expect(page.locator('[data-testid=continuity-banner]')).toHaveCount(0)
    await expect(page.locator('.composer-input')).toBeDisabled()
    await page.waitForTimeout(1200) // 给潜在重连一个窗口（反向断言用）
    expect(ws.connections).toBe(1)
    await page.screenshot({ path: shotName(testInfo, 'e07-terminal-p0'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })
})

test.describe('M3-C outbox 可视化与幂等 ACK 结算（canonical capability）', () => {
  test('发送中 → ACK 已确认；重试同 MessageID 新 requestId；控制权接管停发', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page, {
      autoAttach: () => attached({ inputAckMode: 'session-window-v1' }),
    })
    await enterWorkspace(page)
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')

    // 发送草稿 → 用户卡「发送中」+ outbox 可视条。
    await page.locator('.composer-input').fill('npm test')
    await page.locator('.composer-send').click()
    const userCard = page.locator('.user-message', { hasText: 'npm test' })
    await expect(userCard).toBeVisible()
    await expect(userCard.locator('.user-delivery')).toContainText('发送中')
    const strip = page.locator('[data-testid=outbox-strip]')
    await expect(strip).toBeVisible()
    await expect(strip).toContainText('1 条指令待确认')
    await page.screenshot({ path: shotName(testInfo, 'outbox-sending'), fullPage: false })

    // canonical wire 帧：msg-v1-/req-v1- 形状。
    const inputFrame = ws.frames.find((f) => f.type === 'input')
    expect(String(inputFrame?.id)).toMatch(/^msg-v1-[0-9a-f]{32}$/)
    expect(String(inputFrame?.requestId)).toMatch(/^req-v1-[0-9a-f]{32}$/)

    // ACK 结算：卡片「已确认」+ 可视条消失。
    ws.send({ type: 'input.ack', requestId: String(inputFrame?.requestId), sessionId: 'sess-1', id: String(inputFrame?.id) })
    await expect(userCard.locator('.user-delivery')).toContainText('已确认')
    await expect(strip).toHaveCount(0)
    await page.screenshot({ path: shotName(testInfo, 'outbox-settled'), fullPage: false })

    // 第二条：不 ACK → 控制权被接管 → 停发（halted），不伪造 confirmed。
    await page.locator('.composer-input').fill('second cmd')
    await page.locator('.composer-send').click()
    const secondCard = page.locator('.user-message', { hasText: 'second cmd' })
    await expect(secondCard.locator('.user-delivery')).toContainText('发送中')
    ws.send({ type: 'control.state', sessionId: 'sess-1', state: 'desktop', reason: 'takeover', occurredAt: '2026-08-04T02:00:00Z' })
    await expect(secondCard.locator('.user-delivery')).toContainText('已停发')
    await expect(page.locator('[data-testid=control-notice]')).toContainText('桌面端已收回控制权')
    await page.screenshot({ path: shotName(testInfo, 'outbox-halted'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('断线重连后自动重发反馈：同 MessageID 重试 + restored resending + 迟到 ACK 结算', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page, {
      autoAttach: () => attached({ inputAckMode: 'session-window-v1' }),
    })
    await enterWorkspace(page)
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')

    // 发送但不 ACK；断线 → 重连 attached → outbox 立即重试队首。
    await page.locator('.composer-input').fill('resend me')
    await page.locator('.composer-send').click()
    const firstFrame = ws.frames.find((f) => f.type === 'input')
    ws.closeFromServer(1006)
    await expect(page.locator('[data-testid=continuity-banner]')).toBeVisible({ timeout: 3000 })
    await expect.poll(() => ws.connections, { timeout: 6000 }).toBe(2)

    // 重试帧：同 MessageID、distinct requestId（幂等机器断言）。
    await expect.poll(() => ws.frames.filter((f) => f.type === 'input').length, { timeout: 3000 }).toBe(2)
    const retries = ws.frames.filter((f) => f.type === 'input')
    expect(retries[1]?.id).toBe(retries[0]?.id)
    expect(retries[1]?.requestId).not.toBe(retries[0]?.requestId)

    // restored + 未确认 → 自动重发反馈。
    const strip = page.locator('[data-testid=outbox-strip]')
    await expect(strip).toBeVisible()
    await expect(strip).toContainText('已恢复，正在自动重发 1 条未确认指令')
    const userCard = page.locator('.user-message', { hasText: 'resend me' })
    await expect(userCard.locator('.user-delivery')).toContainText('第 2 次尝试')
    await page.screenshot({ path: shotName(testInfo, 'outbox-resend-feedback'), fullPage: false })

    // 迟到 ACK（首个 attempt 的 requestId）仍结算。
    ws.send({ type: 'input.ack', requestId: String(firstFrame?.requestId), sessionId: 'sess-1', id: String(firstFrame?.id) })
    await expect(userCard.locator('.user-delivery')).toContainText('已确认')
    expect(consoleErrors).toEqual([])
  })

  // R2-001：takeover 后重新获权，新 input 仍可发送（旧 entry 不重发）。真实浏览器 E2E。
  test('R2-001 takeover→重新获权→新 input 可发送（旧 entry halted 不重发）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    // acquire REST 返回 ControlSnapshot {state:'you'}；detail 返回 makeDetail。
    await page.route(`${BASE}/sessions/sess-1/control/*`, (route: Route) => {
      const url = route.request().url()
      if (url.includes('acquire')) {
        return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ state: 'you' }) })
      }
      return route.fallback()
    })
    await mockRest(page)
    const ws = await mockWs(page, {
      autoAttach: () => attached({ inputAckMode: 'session-window-v1' }),
    })
    await enterWorkspace(page)
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')

    // 首条命令发送（未 ACK）。
    await page.locator('.composer-input').fill('first-cmd')
    await page.locator('.composer-send').click()
    await expect(page.locator('.user-message', { hasText: 'first-cmd' })).toBeVisible()
    const firstFrame = ws.frames.find((f) => f.type === 'input')
    expect(firstFrame?.id).toMatch(/^msg-v1-[0-9a-f]{32}$/)

    // takeover（desktop 收回）→ 旧 entry halted（不重发），Composer 禁用。
    ws.send({ type: 'control.state', sessionId: 'sess-1', state: 'desktop', reason: 'takeover', occurredAt: '2026-08-04T02:00:00Z' })
    await expect(page.locator('.user-message', { hasText: 'first-cmd' }).locator('.user-delivery')).toContainText('已停发')
    await expect(page.locator('.composer-input')).toBeDisabled()
    // desktop 持有期间 ControlBar 显示「观察中」（无 acquire 按钮）。
    await expect(page.locator('.control-bar')).toContainText('桌面端控制中')
    await page.screenshot({ path: shotName(testInfo, 'r2-001-takeover-halted'), fullPage: false })

    // desktop 释放 → control 回到 none（acquire 按钮出现）。
    ws.send({ type: 'control.state', sessionId: 'sess-1', state: 'none', reason: 'released', occurredAt: '2026-08-04T02:01:00Z' })
    await expect(page.locator('.control-bar')).toContainText('无人控制')

    // 重新获权（REST acquire 成功 → control.state=you）→ Composer 恢复。
    await page.getByRole('button', { name: '获取控制权' }).click()
    await expect(page.locator('.control-bar')).toContainText('你正在控制')
    await expect(page.locator('.composer-input')).toBeEnabled()

    // 新命令可以发送：第二条是全新 MessageID，旧 entry（first-cmd）不重发。
    await page.locator('.composer-input').fill('second-cmd')
    await page.locator('.composer-send').click()
    await expect.poll(() => ws.frames.filter((f) => f.type === 'input').length, { timeout: 3000 }).toBe(2)
    const secondFrame = ws.frames.filter((f) => f.type === 'input')[1]
    expect(secondFrame?.id).toMatch(/^msg-v1-[0-9a-f]{32}$/)
    expect(secondFrame?.id).not.toBe(firstFrame?.id)
    await expect(page.locator('.user-message', { hasText: 'second-cmd' })).toBeVisible()
    await page.screenshot({ path: shotName(testInfo, 'r2-001-reacquire-new-input'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })
})

test.describe('M3-C 五层 StatusBar 全量核验（断连/恢复/控制/缺口变迁）', () => {
  test('五层芯片在 full cycle 下的投影序列 + 异常自动展开', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    let attachCount = 0
    const ws = await mockWs(page, {
      autoAttach: () => {
        attachCount += 1
        return attached({
          earliestSeq: 1,
          latestSeq: 1,
          // 重连 attached 快照权威：控制仍 desktop（收回事实不被连接态抹掉，design §7）。
          snapshot: attachCount === 1 ? snapshot() : snapshot({ control: { state: 'desktop' } }),
          history: attachCount === 1 ? [output(1, 'baseline\n')] : [],
        })
      },
    })
    await enterWorkspace(page)

    // 基线：五层全绿。
    const bar = page.locator('.status-bar')
    await expect(bar).toContainText('连接：已连接')
    await expect(bar).toContainText('授权：已授权')
    await expect(bar).toContainText('会话：运行中')
    await expect(bar).toContainText('控制：你正在控制')
    await expect(bar).toContainText('历史：连续')

    // 控制被收回 → 控制层 warning（异常自动展开明细）。
    ws.send({ type: 'control.state', sessionId: 'sess-1', state: 'desktop', reason: 'takeover', occurredAt: '2026-08-04T03:00:00Z' })
    await expect(bar).toContainText('控制：桌面端控制中')
    await expect(page.locator('.layer-details')).toBeVisible()

    // 断线 → 连接层 warning（恢复中 + ≤5s 倒计时 detail）。
    ws.closeFromServer(1006)
    await expect(bar).toContainText('恢复中', { timeout: 3000 })
    await expect(page.locator('.layer-details')).toContainText('≤5s 自动恢复')
    await page.screenshot({ path: shotName(testInfo, 'layers-disconnected'), fullPage: false })

    // 恢复（控制仍 desktop）→ 连接层回 ok，控制层保持 warning。
    await expect.poll(() => ws.connections, { timeout: 6000 }).toBe(2)
    await expect(bar).toContainText('连接：已连接')
    await expect(bar).toContainText('控制：桌面端控制中')

    // live 缺口 → 历史层 warning。
    ws.send(output(3, 'skip two\n'))
    await expect(bar).toContainText('历史：存在缺口')
    await expect(page.locator('[data-testid=gap-marker]')).toHaveAttribute('data-gap-state', 'recoverable')
    await page.screenshot({ path: shotName(testInfo, 'layers-gap-warning'), fullPage: false })
    expect(consoleErrors).toEqual([])
  })
})

test.describe('M3-C 机器 oracle（continuitySnapshot 内存 seam + timing R0/R1；vite dev 动态导入）', () => {
  /** 经 vite dev 动态 import 读取活跃 store 的 continuitySnapshot（design §8 seam）。 */
  async function readContinuity(page: Page) {
    return page.evaluate(async () => {
      const mod = await import('/src/stores/workspace.ts')
      const store = mod.useWorkspaceStore()
      return store.continuitySnapshot()
    })
  }

  test('C6b 链：drop N→deliver N+1→disconnect→reattach（wire lastSeq=N-1、Seq 各一次、F=N+1、marker 消失）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    let attachCount = 0
    const ws = await mockWs(page, {
      autoAttach: () => {
        attachCount += 1
        if (attachCount === 1) return attached({ earliestSeq: 1, latestSeq: 1, history: [output(1, 'one\n')] })
        // reattach：权威返回 [2,3]（含被丢的 N=2 与缓冲的 N+1=3）。
        return attached({
          earliestSeq: 1,
          latestSeq: 3,
          history: [output(2, 'two\n'), output(3, 'three\n')],
        })
      },
    })
    await enterWorkspace(page)
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')

    // F=1；relay 效果以 mock 直接复现：seq=3 先到（越洞 [2,2] 缓冲，不投影）。
    ws.send(output(3, 'three\n'))
    const gap = page.locator('[data-testid=gap-marker]')
    await expect(gap).toHaveAttribute('data-gap-state', 'recoverable')
    await expect(page.locator('.mono-block', { hasText: 'three' })).toHaveCount(0)
    let snap = await readContinuity(page)
    expect(snap.frontier).toBe(1)
    expect(snap.appliedSeqCounts).toEqual({ '1': 1 })

    // 断线 → 重连：第二次 wire attach 的 lastSeq 必须 = F = N-1 = 1（design §3/C6b）。
    ws.closeFromServer(1006)
    await expect.poll(() => ws.connections, { timeout: 6000 }).toBe(2)
    const reattach = ws.frames.filter((f) => f.type === 'attach').at(-1)
    expect(reattach?.lastSeq).toBe(1)

    // reattach history [2,3] 权威吸收：两 Seq 各恰一次、F=3、recoverable marker 消失。
    await expect(page.locator('[data-testid=gap-marker]')).toHaveCount(0)
    await expect(page.locator('.mono-block', { hasText: 'two' })).toHaveCount(1)
    await expect(page.locator('.mono-block', { hasText: 'three' })).toHaveCount(1)
    snap = await readContinuity(page)
    expect(snap.frontier).toBe(3)
    expect(snap.appliedSeqCounts).toEqual({ '1': 1, '2': 1, '3': 1 })
    expect(snap.gapRanges).toEqual([])
    expect(snap.attachedCount).toBe(2)
    expect(snap.recoveryGeneration).toBe(1)
    // 快照隐私：无 chunk 内容/MessageID。
    expect(JSON.stringify(snap)).not.toContain('one')
    expect(consoleErrors).toEqual([])
  })

  test('R0/R1 计时链：online 事件 → R0 打点 + 立即重试 → attached → R1（R0_R1 observed ≤5000）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await dismissGuide(page)
    await mockRest(page)
    const ws = await mockWs(page)
    await enterWorkspace(page)
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')

    // 断线（无 online）→ reconnecting；随后浏览器 online → R0 + 立即开新 socket。
    ws.closeFromServer(1006)
    await expect(page.locator('[data-testid=continuity-banner]')).toHaveAttribute('data-state', 'reconnecting', { timeout: 3000 })
    await page.evaluate(() => window.dispatchEvent(new Event('online')))
    // design §4：合格 online 取消退避 timer 立即重试（不等 750ms 首档）。
    await expect.poll(() => ws.connections, { timeout: 1500 }).toBe(2)

    await expect(page.locator('[data-testid=continuity-banner]')).toHaveAttribute('data-state', 'restored')
    const snap = await readContinuity(page)
    // R0/R1 各 1 次、observed、≤5000ms 预算内（design §8 C1 oracle 的计时部分）。
    expect(snap.timing.R0_R1.status).toBe('observed')
    expect(typeof snap.timing.R0_R1.durationMs).toBe('number')
    expect(snap.timing.R0_R1.budgetStatus).toBe('within_budget')
    expect(consoleErrors).toEqual([])
  })
})
