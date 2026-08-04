// e2e/workspace-real.spec.ts — M2-INT PG-03 真服务器全链 E2E（Chromium-only，无 route mock）
// ---------------------------------------------------------------------------
// 与 workspace-pg03.spec.ts（Playwright routeWebSocket 全 mock）的区别：
//   本 spec **不注册任何 page.route**，REST/WS 全部由 e2e/harness/remote-server
//   拉起的真实 Go Server 应答。M2-INT harness 已装配真实 RemoteSessionAdapter
//   （ControlRuntime/Catalog/Streams/Journal + 真实 gate），故 v1 session REST
//   index 2-9 + /ws/v1 全激活。页面从真服务器同源伺服（mobile/dist），Cookie /
//   Origin / Host / WS 升级 / 因果投递全是真实行为。
//
// fake CLI 边界（如实声明）：harness 的 resolver/launch seam 指向确定性 fake CLI
// （不查找真实二进制、不启动真实进程）。会话生命周期（gate/catalog/stream/journal）
// 与 WS 因果投递链全真实；输出由控制面经真实 SessionStreamStore + SessionEventHub
// （与生产 H1→pump 同一目的地）注入。真实四类 CLI 本机端到端（真二进制→真 PTY→
// 完整 run-observation 路径）属 M4/最终验收，本 spec 不伪证。
//
// 覆盖：
//   a. 真实配对 → 大厅真 list（控制面造的会话可见）→ PG-03 attach 真 WS
//   b. backfill 渲染（attach 前注入的历史帧经真实 replay 窗口）
//   c. Composer 发 input（真 WS input 帧 → 真 gate DoDevicePTY）
//   d. fake CLI 回输出（控制面注入 → 真实因果 drain → 七类组件真实帧转化）
//   e. stop/restart 边界真实渲染
//   f. 观察者（第二 device）写拒绝 vs 控制者成功对照
//   g. revoke 后 1008 device_revoked 真实踢出（CG-01 符号）
//   h. console 无 error + 关键态截图
// ---------------------------------------------------------------------------

import { expect, test, type Browser, type Page } from '@playwright/test'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import * as fs from 'node:fs'
import * as os from 'node:os'
import * as path from 'node:path'
import { ERROR_CODE_AUTH_REVOKED } from '../mobile/src/lib/contract'

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

/** 控制面调用（测试侧桌面用户/fake-CLI 动作等价物；返回真实服务端投影）。 */
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

/** 真实配对一台浏览器设备（深链 code + expiresAt），落在大厅。 */
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

/** 七类内容转化组件的演示输出（与 workspace-pg03.spec.ts 同源脚本）。 */
const SEVEN_KINDS: string[] = [
  'Do you want to run this command?\n❯ 1. Yes\n  2. No\n',
  'Pick a color scheme:\n1. Solarized\n2. Nord\n',
  Array.from({ length: 15 }, (_, i) => `build output line ${i + 1}`).join('\n') + '\n',
  'Bash(npm run build)\n⎿  compiled in 4.2s\n',
  'Error: ECONNREFUSED 127.0.0.1:3000\n',
  '⠋ Building project…\n',
  'plain terminal output line\n',
]

test.describe('M2-INT PG-03 真服务器全链 E2E', () => {
  let harness: { proc: ChildProcess; info: HarnessInfo }

  test.beforeEach(async () => {
    harness = await startHarness()
  })

  test.afterEach(async () => {
    await stopHarness(harness.proc)
  })

  test('a–d. 真配对→真 list→真 WS attach→live 七类组件真实帧转化 + Composer input', async ({
    page,
  }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await page.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)
    const { info } = harness

    // 控制面造会话（真实 REST，走完整 gate/catalog/stream）。此时 stream 为空→
    // detail.latestSeq=0，首 attach 传 omitted lastSeq（拿全量历史=空）。
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })
    expect(created.sessionId).toBeTruthy()

    // 真实配对 → 大厅真 list（控制面造的会话可见）。
    await pairBrowser(page, info, 'E2E 真链·设备A')
    await expect(page.locator('.session-card', { hasText: created.title })).toBeVisible()
    await page.locator('.session-card', { hasText: created.title }).click()
    await expect(page).toHaveURL(new RegExp(`#/workspace/${created.sessionId}$`))

    // 真 WS attach（同源 Cookie 自动携带；真实升级 + 因果 attach）。
    await expect(page.locator('.workspace-title')).toHaveText(created.title)
    await expect(page.locator('.status-bar')).toContainText('连接：已连接', { timeout: 8_000 })
    await expect(page.locator('.status-bar')).toContainText('授权：已授权')
    await expect(page.locator('.status-bar')).toContainText('会话：运行中')
    await page.screenshot({ path: 'test-results/ws-real-attached.png', fullPage: false })

    // 获取控制权（真 REST acquire；首 attach 控制=none，须显式获取才能写）。
    await page.getByRole('button', { name: '获取控制权' }).click()
    await expect(page.locator('.control-bar')).toContainText('你正在控制')

    // live：attach 后注入七类输出，全部经真实因果 drain（SessionEventHub → 订阅队列 →
    // socket）。至少权限确认/错误/进度三类真实帧转化 + 其余四类补齐。
    for (const text of SEVEN_KINDS) {
      await ctl(info, 'POST', `/control/session/${created.sessionId}/output`, { text })
    }
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

    // Composer 发 input（真 WS input 帧 → 真 gate DoDevicePTY）。
    await page.locator('.composer-input').fill('npm test')
    await page.locator('.composer-send').click()
    await expect(page.locator('.user-message')).toContainText('npm test')

    // fake CLI 回输出（控制面注入 → 真实因果 drain → error-card 转化）。
    await ctl(info, 'POST', `/control/session/${created.sessionId}/output`, {
      text: 'Error: build failed in 3.1s\n',
    })
    await expect(page.locator('.error-card').last()).toContainText('build failed')

    await expect(page.locator('.status-bar')).toContainText('控制：你正在控制')
    await page.screenshot({ path: 'test-results/ws-real-seven-components.png', fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('e. stop 真实态切换 + 重启边界真实渲染', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await page.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)
    const { info } = harness

    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })
    await pairBrowser(page, info, 'E2E 边界·设备')
    await page.locator('.session-card', { hasText: created.title }).click()
    await expect(page.locator('.status-bar')).toContainText('连接：已连接', { timeout: 8_000 })
    await expect(page.locator('.status-bar')).toContainText('会话：运行中')
    // 获取控制权（stop 须控制者）。
    await page.getByRole('button', { name: '获取控制权' }).click()
    await expect(page.locator('.control-bar')).toContainText('你正在控制')

    // stop：真 WS 控制者点显式按钮 → PG-06 确认 → 真 REST stop → 真实态切换。
    await page.locator('.composer-stop').click()
    const dialog = page.locator('.confirm-dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: '停止运行' }).click()
    await expect(page.locator('.status-bar')).toContainText('会话：已停止')
    await page.screenshot({ path: 'test-results/ws-real-stopped.png', fullPage: false })

    // 重启边界：控制面经真实 M2/H3 路径注入 session.state(restartBoundary) 帧 → 原位渲染。
    await ctl(info, 'POST', `/control/session/${created.sessionId}/boundary`)
    const marker = page.locator('.boundary-marker')
    await expect(marker).toBeVisible()
    await expect(marker).toContainText('会话已重启')
    await page.screenshot({ path: 'test-results/ws-real-boundary.png', fullPage: false })
    expect(consoleErrors).toEqual([])
  })

  test('f. 观察者（第二 device）写拒绝 vs 控制者成功对照', async ({ browser }, testInfo) => {
    const { info } = harness
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    // 设备 A：配对 → attach → 获取控制权（真 REST acquire）。
    const pageA = await freshDevicePage(browser)
    const consoleErrorsA = watchConsole(pageA)
    await pageA.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)
    await pairBrowser(pageA, info, 'E2E 观察·控制者A')
    await pageA.locator('.session-card', { hasText: created.title }).click()
    await expect(pageA.locator('.status-bar')).toContainText('连接：已连接', { timeout: 8_000 })
    await expect(pageA.locator('.control-bar')).toContainText('无人控制')
    await pageA.getByRole('button', { name: '获取控制权' }).click()
    await expect(pageA.locator('.control-bar')).toContainText('你正在控制')
    // 控制者：Composer 可用。
    await expect(pageA.locator('.composer-input')).toBeEnabled()

    // 设备 B：第二台真实配对设备 → attach → 观察者（控制权在 A）。
    const pageB = await freshDevicePage(browser)
    const consoleErrorsB = watchConsole(pageB)
    await pageB.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)
    await pairBrowser(pageB, info, 'E2E 观察·观察者B')
    await pageB.locator('.session-card', { hasText: created.title }).click()
    await expect(pageB.locator('.status-bar')).toContainText('连接：已连接', { timeout: 8_000 })
    // 观察者投影：控制权在 A（other + A 的设备名）。
    await expect(pageB.locator('.control-bar')).toContainText('控制')
    await expect(pageB.locator('.composer-input')).toBeDisabled()
    await expect(pageB.locator('.composer-block-reason')).toContainText('控制权在')
    await pageB.screenshot({ path: 'test-results/ws-real-observer-disabled.png', fullPage: false })

    // 控制者注入输出 → 观察者也经真实因果订阅收到（两设备同会话广播）。
    await ctl(info, 'POST', `/control/session/${created.sessionId}/output`, {
      text: 'Error: shared broadcast\n',
    })
    await expect(pageA.locator('.error-card').last()).toContainText('shared broadcast')
    await expect(pageB.locator('.error-card').last()).toContainText('shared broadcast')

    await pageA.screenshot({ path: 'test-results/ws-real-controller-enabled.png', fullPage: false })
    expect(consoleErrorsA).toEqual([])
    expect(consoleErrorsB).toEqual([])
    await pageA.context().close()
    await pageB.context().close()
  })

  test('g. revoke 后 1008 device_revoked 真实踢出（CG-01 符号）', async ({ page, context }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await page.addInitScript((key: string) => localStorage.setItem(key, '1'), GUIDE_KEY)
    const { info } = harness

    // CG-01 (M-008): capture the REAL WS frames to assert that the typed
    // auth.revoked{reason:device_revoked} event is received BEFORE the socket
    // closes — not merely inferred from the 1008 close code / status-bar text.
    const wsFramesReceived: { payload: string; at: number }[] = []
    let wsCloseAt = 0
    page.on('websocket', (ws) => {
      ws.on('framereceived', (frame) => {
        wsFramesReceived.push({ payload: String(frame.payload), at: Date.now() })
      })
      ws.on('close', () => {
        if (wsCloseAt === 0) wsCloseAt = Date.now()
      })
    })
    // Record /host/summary responses: the auth.revoked event navigates the client
    // to PG-01, whose auth check returns 401 auth.revoked + clears the cookie.
    // Recording (vs a single waitForResponse) is robust to that early nav timing.
    const summaryResponses: { status: number; code: string }[] = []
    page.on('response', async (r) => {
      if (r.url().includes('/host/summary')) {
        let code = ''
        try {
          code = ((await r.json()) as { code?: string }).code ?? ''
        } catch {
          /* ignore body parse failure */
        }
        summaryResponses.push({ status: r.status(), code })
      }
    })

    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })
    await pairBrowser(page, info, 'E2E 撤销·待踢设备')
    await page.locator('.session-card', { hasText: created.title }).click()
    await expect(page.locator('.status-bar')).toContainText('连接：已连接', { timeout: 8_000 })

    // 控制面撤销该浏览器设备（桌面撤销动作等价物）。
    interface DeviceEntry {
      id: string
      name: string
      pairedAt: string
    }
    const devices = await ctl<DeviceEntry[]>(info, 'GET', '/devices')
    const target = devices.find((d) => d.name === 'E2E 撤销·待踢设备')
    expect(target).toBeDefined()
    await ctl(info, 'POST', '/devices/revoke', { deviceId: target!.id })

    // CG-01 (M-008): 服务端经唯一 writer 先发送 auth.revoked{reason:device_revoked}
    // 再 close 1008。客户端 auth.revoked 处理器（workspace store authLost→
    // revoked → router.replace(connect)）立即清态踢回 PG-01 撤销横幅——比仅凭
    // close code 推断终端原因更强。
    // 1) WS 必须关闭。
    await expect.poll(async () => (wsCloseAt > 0 ? true : false), {
      timeout: 8_000,
      message: 'WS should close after device revoke',
    }).toBe(true)
    // 2) auth.revoked{device_revoked} 事件帧必须先于 WS close 到达（不得仅凭
    //    close code 推断）。
    const arIdx = wsFramesReceived.findIndex(
      (f) => f.payload.includes('"auth.revoked"') && f.payload.includes('"device_revoked"'),
    )
    expect(arIdx, 'expected an auth.revoked{device_revoked} event frame on the WS').toBeGreaterThanOrEqual(0)
    const arAt = wsFramesReceived[arIdx].at
    expect(arAt, 'auth.revoked event must precede the WS close').toBeLessThanOrEqual(wsCloseAt)
    // Validate the frozen event fields.
    const arParsed = JSON.parse(wsFramesReceived[arIdx].payload) as {
      type: string
      reason: string
      occurredAt: string
    }
    expect(arParsed.type).toBe('auth.revoked')
    expect(arParsed.reason).toBe('device_revoked')
    expect(arParsed.occurredAt.length, 'occurredAt must be non-empty').toBeGreaterThan(0)
    // 3) 客户端因 auth.revoked 事件立即到达撤销态（kick banner）。
    await expect(page.locator('.kick-banner')).toContainText('授权已被桌面端撤销', { timeout: 8_000 })
    await page.screenshot({ path: 'test-results/ws-real-revoked-1008.png', fullPage: false })

    // 真实踢出验证：auth.revoked 事件驱动客户端立即到达撤销态；PG-01 的
    // /host/summary auth 校验返回 401 auth.revoked 并清除 device cookie。
    await expect.poll(
      async () => summaryResponses.some((s) => s.status === 401 && s.code === ERROR_CODE_AUTH_REVOKED),
      { timeout: 8_000, message: 'PG-01 /host/summary should return 401 auth.revoked' },
    ).toBe(true)
    // 凭据清除（服务端 401 清 Cookie）。
    await expect.poll(async () => {
      const cs = await context.cookies(info.origin)
      return cs.filter((c) => c.name === 'amagi_codebox_device' && c.value !== '').length
    }, { timeout: 8_000, message: 'device cookie should be cleared after revoke' }).toBe(0)
    // 撤销持久化：整页重载后仍无法进入工作区（被踢回 PG-01 配对面；cookie 已清
    // 除，页面呈现未配对态而非工作区）。
    await page.goto(info.origin)
    await expect(page).toHaveURL(/#\/(connect)?$/)
    await expect(page.getByRole('heading', { name: '还没有配对' })).toBeVisible()
    await page.screenshot({ path: 'test-results/ws-real-revoked-kick.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })
})
