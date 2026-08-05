// e2e/workspace-m3c-relay.spec.ts — M3-C 整形 E2E（真 harness + installWsRelay；Chromium-only）
// ---------------------------------------------------------------------------
// 权威依据：fuxi 20260804-m3-continuity-design/design.md §8（证据分层 + 机器 oracle）。
// 证据分层（如实声明）：
//   · 本 spec 是「relay + 真 harness」层：协议/服务端 ledger/arbiter/stream 全真实
//     （e2e/harness/remote-server 真 Go Server + fake CLI seam），故障是应用帧级
//     确定性注入（installWsRelay），不是 TCP packet/带宽模型，不冒充真机 conditioner。
//   · CDP offline/online 用例是「CDP 真实 transport probe」层；Chromium loopback
//     豁免时按设计标 UNSUPPORTED（test.skip），不改用假事件写 PASS。
// 机器 oracle（design §8 [R2/M-05]）：
//   · DOM 固定属性：data-testid=continuity-banner/gap-marker/timeline-item；
//   · relay typed 计数与派生布尔（sameMessageID/distinctRequestIDs）；
//   · harness raw-io 计数（rawInput 调用次数 = 输入 0 重复断言）；
//   · Seq 内容唯一性（每帧唯一文本在 DOM 恰出现一次 = applied-once 的 DOM 层证据）。
//   · 内存 continuitySnapshot 全量 Seq 多重集断言属 vite dev 层（workspace-m3c.spec.ts）；
//     dist 产物无 source import seam，本 spec 不伪造该层证据。
// 边界：Playwright 无 unrouteWebSocket——每用例独占 page/context（test 默认隔离），
// cleanup 关闭 active pair 后不在该 page 继续作行为证明。
// ---------------------------------------------------------------------------

import { expect, test, type Browser, type Page } from '@playwright/test'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import * as fs from 'node:fs'
import * as os from 'node:os'
import * as path from 'node:path'
import { installWsRelay } from './helpers/network'

const REPO_ROOT = path.resolve(__dirname, '..')
const MOBILE_DIST = path.join(REPO_ROOT, 'mobile', 'dist')
const HARNESS_MODULE = './e2e/harness/remote-server'
const GUIDE_KEY = 'amagi.pg03.guide.dismissed'

interface HarnessInfo {
  origin: string
  controlOrigin: string
}

let harnessBinPromise: Promise<string> | null = null

function buildHarness(): Promise<string> {
  if (!harnessBinPromise) {
    harnessBinPromise = Promise.resolve().then(() => {
      const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'amagi-e2e-harness-bin-'))
      const bin = path.join(dir, 'remote-server')
      execFileSync('go', ['build', '-o', bin, HARNESS_MODULE], { cwd: REPO_ROOT, stdio: 'pipe' })
      return bin
    })
  }
  return harnessBinPromise
}

async function startHarness(): Promise<{ proc: ChildProcess; info: HarnessInfo }> {
  const bin = await buildHarness()
  const proc = spawn(bin, ['-web-root', MOBILE_DIST], { stdio: ['ignore', 'pipe', 'pipe'] })
  let stderr = ''
  proc.stderr?.on('data', (chunk) => {
    stderr += String(chunk)
  })
  const info = await new Promise<HarnessInfo>((resolve, reject) => {
    let buf = ''
    const timer = setTimeout(() => reject(new Error(`harness ready timeout; stderr:\n${stderr}`)), 30_000)
    proc.on('exit', (code) => {
      clearTimeout(timer)
      reject(new Error(`harness exited early (code ${code}); stderr:\n${stderr}`))
    })
    proc.stdout?.on('data', (chunk) => {
      buf += String(chunk)
      const line = buf.split('\n').find((l) => l.startsWith('HARNESS_READY '))
      if (line) {
        clearTimeout(timer)
        resolve(JSON.parse(line.slice('HARNESS_READY '.length)) as HarnessInfo)
      }
    })
  })
  return { proc, info }
}

async function stopHarness(proc: ChildProcess): Promise<void> {
  if (proc.exitCode !== null) return
  proc.kill('SIGTERM')
  await new Promise<void>((resolve) => {
    const timer = setTimeout(() => {
      proc.kill('SIGKILL')
      resolve()
    }, 5_000)
    proc.on('exit', () => {
      clearTimeout(timer)
      resolve()
    })
  })
}

async function ctl<T = unknown>(
  info: HarnessInfo,
  method: 'GET' | 'POST',
  p: string,
  body?: unknown,
): Promise<T> {
  const res = await fetch(`${info.controlOrigin}${p}`, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    throw new Error(`control ${method} ${p} -> ${res.status}: ${await res.text()}`)
  }
  return (await res.json()) as T
}

interface PairingWindow {
  generation: number
  code: string
  expiresAt: string
  addressRequired: boolean
}

interface CreatedSession {
  sessionId: string
  title: string
  state: string
  cliType: string
}

interface RawIO {
  writeCount: number
  writeBytes: number
  resizeCount: number
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

async function freshDevicePage(browser: Browser): Promise<Page> {
  const useOptions = test.info().project.use
  const context = await browser.newContext({
    viewport: useOptions.viewport,
    isMobile: useOptions.isMobile,
    hasTouch: useOptions.hasTouch,
  })
  return context.newPage()
}

async function pairBrowser(page: Page, info: HarnessInfo, deviceName: string): Promise<void> {
  const win = await ctl<PairingWindow>(info, 'POST', '/pairing-window')
  await page.goto(
    `${info.origin}/#/connect?code=${encodeURIComponent(win.code)}&expiresAt=${encodeURIComponent(win.expiresAt)}`,
  )
  await expect(page.locator('#pair-code')).toHaveValue(win.code)
  await page.locator('#pair-device-name').fill(deviceName)
  await page.getByRole('button', { name: '完成配对' }).click()
  await expect(page).toHaveURL(/#\/lobby$/)
}

test.describe('M3-C 整形 E2E（真 harness + WS relay / CDP probe）', () => {
  let harness: { proc: ChildProcess; info: HarnessInfo }

  test.beforeEach(async () => {
    harness = await startHarness()
  })

  test.afterEach(async () => {
    await stopHarness(harness.proc)
  })

  test('R1. late-attach 修复端到端：真服务器先产出历史，新设备首 attach 全量渲染', async ({ browser }, testInfo) => {
    const { info } = harness
    // 服务端先产出 3 帧历史（此时无任何客户端 attach）。
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })
    await ctl(info, 'POST', `/control/session/${created.sessionId}/output`, { text: 'late-one\n' })
    await ctl(info, 'POST', `/control/session/${created.sessionId}/output`, { text: 'late-two\n' })
    await ctl(info, 'POST', `/control/session/${created.sessionId}/output`, { text: 'late-three\n' })

    const page = await freshDevicePage(browser)
    const consoleErrors = watchConsole(page)
    await page.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)
    await pairBrowser(page, info, 'M3C late-attach')
    await page.locator('.session-card', { hasText: created.title }).click()
    await expect(page.locator('.status-bar')).toContainText('连接：已连接', { timeout: 8_000 })

    // design §3：首次 attach omit cursor → 全量 retained tail 渲染。
    // 每帧唯一文本在 DOM 恰出现一次（applied-once 的 DOM 层证据）。
    const block = page.locator('.mono-block').first()
    await expect(block).toContainText('late-one', { timeout: 8_000 })
    await expect(block).toContainText('late-two')
    await expect(block).toContainText('late-three')
    await expect(page.locator('.mono-block', { hasText: 'late-one' })).toHaveCount(1)
    await expect(page.locator('.mono-block', { hasText: 'late-two' })).toHaveCount(1)
    await expect(page.locator('.mono-block', { hasText: 'late-three' })).toHaveCount(1)
    await page.screenshot({ path: 'test-results/m3c-relay-late-attach-full-history.png', fullPage: false })
    expect(consoleErrors).toEqual([])
    await page.context().close()
  })

  test('R2. 断连-恢复-缺口-草稿全链：relay drop→recoverable gap→≤5s 恢复→原位消除→ACK 结算', async ({ browser }, testInfo) => {
    const { info } = harness
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    const page = await freshDevicePage(browser)
    const consoleErrors = watchConsole(page)
    await page.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)

    // relay 必须在页面 WS 创建前安装。conn1 S→C 次序（经下方 DEBUG 实证）：
    //   #1 session.attached、#2 error(resize 在 control=none 下被拒)、#3 output(seq1)→DROP、#4 output(seq2)。
    // barrier：先等 #2 error 到达（S→C 转发计数≥2）再注入，杜绝 occurrence 竞态。
    const { controller, cleanup } = await installWsRelay(page, '**/ws/v1', {
      clientToServerLatencyMs: 0,
      serverToClientLatencyMs: 0,
      dropServerToClientAt: 3,
    })
    try {
      await pairBrowser(page, info, 'M3C chain')
      await page.locator('.session-card', { hasText: created.title }).click()
      await expect(page.locator('.status-bar')).toContainText('连接：已连接', { timeout: 8_000 })
      // barrier：resize-forbidden error（#2）已到达，后续注入的 seq1 必为 #3。
      await expect.poll(() => controller.counts.serverToClientForwarded, { timeout: 8_000 }).toBeGreaterThanOrEqual(2)

      // 注入 seq1（被 relay 丢弃）+ seq2（到达）→ 越洞缓冲 + recoverable 原位标记。
      await ctl(info, 'POST', `/control/session/${created.sessionId}/output`, { text: 'chain-one\n' })
      await ctl(info, 'POST', `/control/session/${created.sessionId}/output`, { text: 'chain-two\n' })
      const gap = page.locator('[data-testid=gap-marker]')
      await expect(gap).toBeVisible({ timeout: 8_000 })
      await expect(gap).toHaveAttribute('data-gap-state', 'recoverable')
      await expect(gap).toHaveAttribute('data-from-seq', '1')
      await expect(gap).toHaveAttribute('data-to-seq', '1')
      // design §3：高帧 seq2 不投影（越洞缓冲，不伪造连续）。
      await expect(page.locator('.mono-block', { hasText: 'chain-two' })).toHaveCount(0)
      await page.screenshot({ path: 'test-results/m3c-relay-gap-recoverable.png', fullPage: false })

      // 断连（relay 以 1011 关闭 active pair；1000=terminal 不重连、1006 为保留码
      // 不可进 close 帧）→ E-07 reconnecting。
      const disconnectAt = Date.now()
      await controller.disconnectAll(1011)
      const banner = page.locator('[data-testid=continuity-banner]')
      await expect(banner).toBeVisible({ timeout: 5_000 })
      await expect(banner).toHaveAttribute('data-state', 'reconnecting')

      // ≤5s 恢复（wall-clock sentinel；design §8 允许预算用 wall-clock 哨兵）。
      await expect(banner).toHaveAttribute('data-state', 'restored', { timeout: 5_000 })
      const recoveredMs = Date.now() - disconnectAt
      expect(recoveredMs, `恢复耗时 ${recoveredMs}ms 超出 5000ms 预算`).toBeLessThanOrEqual(5000)

      // 恢复后 reattach 权威历史补齐：缺口原位消除 + seq1/seq2 各恰一次。
      await expect(gap).toHaveCount(0, { timeout: 5_000 })
      await expect(page.locator('.mono-block', { hasText: 'chain-one' })).toHaveCount(1)
      await expect(page.locator('.mono-block', { hasText: 'chain-two' })).toHaveCount(1)
      await page.screenshot({ path: 'test-results/m3c-relay-restored-gap-filled.png', fullPage: false })

      // 草稿：获取控制权 → 发送 → 幂等 ACK 结算（已确认）。
      await page.getByRole('button', { name: '获取控制权' }).click()
      await expect(page.locator('.control-bar')).toContainText('你正在控制')
      await page.locator('.composer-input').fill('chain-draft')
      await page.locator('.composer-send').click()
      const userCard = page.locator('.user-message', { hasText: 'chain-draft' })
      await expect(userCard.locator('.user-delivery')).toContainText('已确认', { timeout: 8_000 })

      // 输入 0 重复（机器断言）：harness rawInput 恰 1 次；relay typed 计数一致。
      const io = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
      expect(io.writeCount, 'rawInput 必须恰 1 次（输入 0 重复）').toBe(1)
      expect(controller.counts.sameMessageID).toBe(true)
      expect(controller.counts.distinctRequestIDs).toBe(true)
      await page.screenshot({ path: 'test-results/m3c-relay-draft-settled.png', fullPage: false })
      expect(consoleErrors).toEqual([])
    } finally {
      await cleanup()
      await page.context().close()
    }
  })

  test('R3. ACK 丢失重试（C3）：rawInput=1 / ackProduced=2 / attempts=2 / settled=1', async ({ browser }, testInfo) => {
    const { info } = harness
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    const page = await freshDevicePage(browser)
    const consoleErrors = watchConsole(page)
    await page.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)

    // conn1 S→C 次序（经实证）：#1 session.attached、#2 error(resize forbidden)、
    //   #3 control.state(acquire 广播)、#4 input.ack→DROP；重试后服务端 re-ACK（#5）转发→结算。
    const { controller, cleanup } = await installWsRelay(page, '**/ws/v1', {
      clientToServerLatencyMs: 0,
      serverToClientLatencyMs: 0,
      dropServerToClientAt: 4,
    })
    try {
      await pairBrowser(page, info, 'M3C ack-drop')
      await page.locator('.session-card', { hasText: created.title }).click()
      await expect(page.locator('.status-bar')).toContainText('连接：已连接', { timeout: 8_000 })
      await page.getByRole('button', { name: '获取控制权' }).click()
      await expect(page.locator('.control-bar')).toContainText('你正在控制')
      // barrier：control.state 广播（#3）已转发，随后 draft 的 ACK 必为 #4（drop 定点）。
      await expect.poll(() => controller.counts.serverToClientForwarded, { timeout: 8_000 }).toBeGreaterThanOrEqual(3)

      await page.locator('.composer-input').fill('ack-drop-draft')
      await page.locator('.composer-send').click()
      const userCard = page.locator('.user-message', { hasText: 'ack-drop-draft' })
      // 首个 ACK 被丢 → 卡片保持发送中；barrier：首个 input 帧已转发。
      await expect(userCard.locator('.user-delivery')).toContainText('发送中')
      const baseForwarded = await expect
        .poll(() => controller.counts.clientToServerForwarded, { timeout: 8_000 })
        .toBeGreaterThanOrEqual(3)
        .then(() => controller.counts.clientToServerForwarded)

      // re-ACK 到达 → settled；且结算前必有第二次 wire attempt（attempts=2 机器证据）。
      await expect(userCard.locator('.user-delivery')).toContainText('已确认', { timeout: 8_000 })
      expect(
        controller.counts.clientToServerForwarded,
        'ACK 丢失后必须发生第二次 wire attempt（同 MessageID 重试）',
      ).toBeGreaterThanOrEqual(baseForwarded + 1)

      // 机器 oracle（design §8 C3）：rawInput=1、ackProduced=2、ackDropped=1、attempts=2（两个 input 帧）、
      // sameMessageID=true、distinctRequestIDs=true、client settled=1。
      const io = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
      expect(io.writeCount, 'rawInput 必须恰 1 次（ACK 丢失重试不重复写入）').toBe(1)
      expect(controller.counts.ackFramesObserved, 'C3 ackProduced=2（原始 commit ACK + 重试后 re-ACK）').toBe(2)
      expect(controller.counts.ackFramesDropped, 'C3 ackDropped=1（首个 ACK 被 relay 丢）').toBe(1)
      expect(controller.counts.inputFramesObserved, 'C3 attempts=2（两个 input 帧）').toBe(2)
      expect(controller.counts.sameMessageID).toBe(true)
      expect(controller.counts.distinctRequestIDs).toBe(true)
      await page.screenshot({ path: 'test-results/m3c-relay-ack-drop-settled.png', fullPage: false })
      expect(consoleErrors).toEqual([])
    } finally {
      await cleanup()
      await page.context().close()
    }
  })

  test('R4. CDP 真实 transport probe：offline→online 恢复（loopback 豁免则 UNSUPPORTED）', async ({ browser }, testInfo) => {
    const { info } = harness
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    const page = await freshDevicePage(browser)
    const consoleErrors = watchConsole(page)
    await page.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)
    await pairBrowser(page, info, 'M3C cdp-probe')
    await page.locator('.session-card', { hasText: created.title }).click()
    await expect(page.locator('.status-bar')).toContainText('连接：已连接', { timeout: 8_000 })

    // CDP 真实 offline：已建真 WS 是否确实 close（loopback 豁免探测）。
    const cdp = await page.context().newCDPSession(page)
    await cdp.send('Network.enable')
    const offlineAt = Date.now()
    await cdp.send('Network.emulateNetworkConditions', {
      offline: true,
      latency: 0,
      downloadThroughput: -1,
      uploadThroughput: -1,
    })

    const banner = page.locator('[data-testid=continuity-banner]')
    const closedDetected = await banner
      .waitFor({ state: 'visible', timeout: 3_000 })
      .then(() => true)
      .catch(() => false)
    if (!closedDetected) {
      // design §8：loopback 豁免 → UNSUPPORTED，不得改用假事件写 PASS。
      test.skip(true, 'CDP loopback offline 不作用于已建 WS（UNSUPPORTED；系统 conditioner 归 M4-C）')
      return
    }
    await expect(banner).toHaveAttribute('data-state', 'reconnecting')
    await page.screenshot({ path: 'test-results/m3c-cdp-offline-reconnecting.png', fullPage: false })

    // 恢复 online：合格 online 事件立即重试（design §4）→ restored ≤5s。
    await cdp.send('Network.emulateNetworkConditions', {
      offline: false,
      latency: 0,
      downloadThroughput: -1,
      uploadThroughput: -1,
    })
    await expect(banner).toHaveAttribute('data-state', 'restored', { timeout: 5_000 })
    const recoveredMs = Date.now() - offlineAt
    expect(recoveredMs, `offline→restored ${recoveredMs}ms 超出 5000ms 预算`).toBeLessThanOrEqual(5000)
    await page.screenshot({ path: 'test-results/m3c-cdp-online-restored.png', fullPage: false })
    expect(consoleErrors).toEqual([])
    await cdp.detach()
    await page.context().close()
  })
})
