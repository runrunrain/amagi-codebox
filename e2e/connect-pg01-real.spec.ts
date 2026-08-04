// e2e/connect-pg01-real.spec.ts — M1-D2 PG-01 真服务器配对 E2E（Chromium-only，无 route mock）
// ---------------------------------------------------------------------------
// 与 D1（connect-pg01.spec.ts，route mock 组件级证据）的区别：
//   本 spec **不注册任何 page.route**，API 全部由 e2e/harness/remote-server
//   拉起的真实 Go Server 应答（生产装配：NewServerWithSecurity +
//   NewProductionSecurityOptions + LoadSecurityState + 真实 Device Store /
//   durable sink + SetWebRoot 同源伺服 mobile/dist）。页面也从真服务器
//   同源伺服（非 vite dev），Cookie / Origin / Host / 错误分类全是真实行为。
//
// 覆盖（验收 a–g）：
//   a. 未配对访问受保护 API → 真 401 auth.unpaired → PG-01 未配对分类
//   b. harness 开窗 → 手动路径 + 真 code → 真 POST /pairing/complete → 201
//   c. Set-Cookie：HttpOnly + SameSite=Strict + Path=/（context.cookies() 读 flag）；
//      document.cookie 不含凭据；201 响应体无 token/cookie 字段（契约）
//   d. 配对后真 GET /host/summary 成功（Cookie 自动携带）→ 大厅占位投影
//   e. 窗口一次性：同 code 二次 complete → 真 410 → E-02；窗口关闭（取消）→ 真 410 → E-02
//   f. harness 撤销 device → 下一次请求真 401 auth.revoked → E-03 踢回 PG-01 + 材料清除
//   g. console 无 error；360×800 截图关键态
//
// harness 生命周期：每个用例 beforeEach 拉起独立 harness 进程（随机 loopback
// 端口 × 2：数据面 + 测试专用控制面），afterEach SIGTERM 回收。控制面仅扮演
// 桌面用户动作等价物（开窗/取消/查设备/撤销），不改写服务端任何行为。
// 契约常量一律 import mobile/src/lib/contract（M0-03 冻结），不复制字符串；
// Cookie 名 amagi_codebox_device 是服务端 wire 名（internal/remote/device_auth.go），
// 前端契约刻意不含它，此处引用仅作真实响应断言。
// ---------------------------------------------------------------------------

import { expect, test, type Browser, type Page } from '@playwright/test'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import * as fs from 'node:fs'
import * as os from 'node:os'
import * as path from 'node:path'
import {
  ERROR_CODE_AUTH_REVOKED,
  ERROR_CODE_AUTH_UNPAIRED,
  ERROR_CODE_AUTH_WINDOW_EXPIRED,
} from '../mobile/src/lib/contract'

const REPO_ROOT = path.resolve(__dirname, '..')
const MOBILE_DIST = path.join(REPO_ROOT, 'mobile', 'dist')
const HARNESS_MODULE = './e2e/harness/remote-server'
/** harness 数据面 serverVersion（与 e2e/harness/remote-server/main.go 的常量一致）。 */
const HARNESS_SERVER_VERSION = '1.0.5-e2e-real-harness'
/** 服务端 wire Cookie 名（internal/remote/device_auth.go deviceCookieName）。 */
const DEVICE_COOKIE_NAME = 'amagi_codebox_device'

interface HarnessInfo {
  origin: string
  controlOrigin: string
}

// 每个 worker 进程编译一次 harness 二进制（go build 有缓存，冷启动也可接受）。
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

/** 拉起一个独立 harness；解析 stdout 的 HARNESS_READY 行拿真实端口。 */
async function startHarness(): Promise<{ proc: ChildProcess; info: HarnessInfo }> {
  const bin = await buildHarness()
  const proc = spawn(bin, ['-web-root', MOBILE_DIST], { stdio: ['ignore', 'pipe', 'pipe'] })
  let stderr = ''
  proc.stderr?.on('data', (chunk) => {
    stderr += String(chunk)
  })
  const info = await new Promise<HarnessInfo>((resolve, reject) => {
    let buf = ''
    const timer = setTimeout(() => reject(new Error(`harness ready timeout; stderr:\n${stderr}`)), 20_000)
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

/** 控制面调用（测试侧桌面用户动作等价物；返回真实服务端投影）。 */
async function ctl<T = unknown>(info: HarnessInfo, method: 'GET' | 'POST', p: string, body?: unknown): Promise<T> {
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

interface DeviceEntry {
  id: string
  name: string
  pairedAt: string
}

/** 收集 console error；豁免 Chromium 对 4xx/5xx 自动打出的网络资源日志
 *  （那是真服务器有意的错误响应，不是应用 console.error）。 */
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

/** 新开一个干净 browser context（无 Cookie / localStorage），模拟一台新设备。
 *  browser.newContext 不继承 project use 选项，显式从 test.info() 透传
 *  视口/移动语义，保证截图仍是 360×800 移动态。 */
async function freshDevicePage(browser: Browser): Promise<Page> {
  const useOptions = test.info().project.use
  const context = await browser.newContext({
    viewport: useOptions.viewport,
    isMobile: useOptions.isMobile,
    hasTouch: useOptions.hasTouch,
  })
  return context.newPage()
}

/** 在 PG-01 手动路径用真实 code 发起配对，返回 201 响应（调用方保证窗口已开）。 */
async function pairViaManualForm(page: Page, origin: string, code: string, deviceName: string) {
  await page.goto(origin)
  await expect(page.locator('.diagnosis-title')).toHaveText('这台设备还没有配对')
  await page.getByRole('button', { name: '手动输入地址与配对码' }).click()
  await page.locator('#pair-code').fill(code)
  await page.locator('#pair-device-name').fill(deviceName)
  const responsePromise = page.waitForResponse((r) => r.url().includes('/pairing/complete'))
  await page.getByRole('button', { name: '完成配对' }).click()
  return responsePromise
}

test.describe('M1-D2 PG-01 真服务器配对 E2E（无 mock）', () => {
  let harness: { proc: ChildProcess; info: HarnessInfo }

  test.beforeEach(async () => {
    harness = await startHarness()
  })

  test.afterEach(async () => {
    await stopHarness(harness.proc)
  })

  test('a. 未配对 → 真 401 auth.unpaired → PG-01 未配对分类', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    const { origin } = harness.info

    const summaryResponse = page.waitForResponse((r) => r.url().includes('/host/summary'))
    await page.goto(origin)
    const res = await summaryResponse
    // 真服务器响应：401 + 契约错误体 auth.unpaired。
    expect(res.status()).toBe(401)
    const body = (await res.json()) as { code: string; layer: string; actionHint: string }
    expect(body.code).toBe(ERROR_CODE_AUTH_UNPAIRED)

    await expect(page.locator('.diagnosis-title')).toHaveText('这台设备还没有配对')
    await expect(page.locator('.risk-banner')).toContainText('局域网明文 HTTP')
    await expect(page.locator('.status-chips')).toContainText('连接：正常')
    await expect(page.locator('.status-chips')).toContainText('授权：未配对')
    await page.screenshot({ path: 'test-results/pg01-real-unpaired.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('b/c/d. 真配对 201 → Set-Cookie 契约 + 响应体无凭据 + Cookie 自动携带进大厅', async ({
    page,
    context,
  }) => {
    const consoleErrors = watchConsole(page)
    const { origin } = harness.info
    const deviceName = 'E2E 真服务器证据设备'

    // harness 开窗（桌面用户动作等价物）：真实 code + 真实过期时间。
    const win = await ctl<PairingWindow>(harness.info, 'POST', '/pairing-window')
    expect(win.code).toMatch(/^[A-Z2-7]+$/)
    expect(Date.parse(win.expiresAt)).toBeGreaterThan(Date.now())

    // 深链携带真实 code + expiresAt（真实 QR 载荷同路径）：倒计时芯片走真实过期时间。
    await page.goto(`${origin}/#/connect?code=${encodeURIComponent(win.code)}&expiresAt=${encodeURIComponent(win.expiresAt)}`)
    await expect(page.locator('.diagnosis-title')).toHaveText('这台设备还没有配对')
    await expect(page.locator('.countdown-chip')).toBeVisible()
    await expect(page.locator('#pair-code')).toHaveValue(win.code)
    await page.locator('#pair-device-name').fill(deviceName)
    await page.screenshot({ path: 'test-results/pg01-real-pair-form.png', fullPage: true })

    const pairResponsePromise = page.waitForResponse((r) => r.url().includes('/pairing/complete'))
    await page.getByRole('button', { name: '完成配对' }).click()
    const pairRes = await pairResponsePromise

    // b. 真 201。
    expect(pairRes.status()).toBe(201)
    // c. 响应体无凭据字段：顶层恰为 device+host；device 恰为 id/name/pairedAt。
    const pairBody = (await pairRes.json()) as Record<string, unknown>
    expect(Object.keys(pairBody).sort()).toEqual(['device', 'host'])
    const device = pairBody.device as Record<string, unknown>
    expect(Object.keys(device).sort()).toEqual(['id', 'name', 'pairedAt'])
    const bodyText = JSON.stringify(pairBody).toLowerCase()
    expect(bodyText).not.toMatch(/token|cookie|secret|credential|apikey/)

    // c. Set-Cookie 真实 flag（context.cookies() 读取，而非解析 header 字符串）。
    const cookies = await context.cookies(origin)
    const deviceCookie = cookies.find((c) => c.name === DEVICE_COOKIE_NAME)
    expect(deviceCookie).toBeDefined()
    expect(deviceCookie?.httpOnly).toBe(true)
    expect(deviceCookie?.sameSite).toBe('Strict')
    expect(deviceCookie?.path).toBe('/')
    // 凭据值恰好一条（无重复 cookie）。
    expect(cookies.filter((c) => c.name === DEVICE_COOKIE_NAME)).toHaveLength(1)
    // c. document.cookie 不可读凭据（HttpOnly）。
    const jsVisibleCookie = await page.evaluate(() => document.cookie)
    expect(jsVisibleCookie).not.toContain(DEVICE_COOKIE_NAME)

    // d. 配对后真 GET /host/summary（Cookie 由浏览器自动携带）→ PG-02 大厅投影。
    await expect(page).toHaveURL(/#\/lobby$/)
    await expect(page.locator('.lobby-title')).toHaveText('会话大厅')
    // PG-02 本体（M2-B）：设备/宿主非密投影呈现于头部。
    await expect(page.locator('.lobby-host-line')).toContainText(deviceName)
    await expect(page.locator('.lobby-host-line')).toContainText(HARNESS_SERVER_VERSION)
    // 诚实降级 → M2-INT：harness 现已装配真实 session adapter，index 2-9 +
    // /ws/v1 全激活，大厅不再呈「宿主会话服务不可用」，而是真实空态
    // （尚未造会话）。这是 M2-INT harness 接线的直接证据。
    await expect(page.locator('.empty-state')).toContainText('还没有会话')
    await expect(page.locator('.status-bar')).toContainText('会话：无会话')
    // 配对材料不进地址栏。
    expect(page.url()).not.toContain('code=')
    await page.screenshot({ path: 'test-results/pg01-real-lobby.png', fullPage: true })

    // d（加固）：整页重载后以真实 Cookie 直接诊断为已授权（不依赖 SPA 内存态）。
    const authedSummary = page.waitForResponse((r) => r.url().includes('/host/summary') && r.status() === 200)
    await page.goto(origin)
    await authedSummary
    await expect(page.locator('.diagnosis-title')).toHaveText('已授权，可以进入')
    await expect(page.locator('.status-chips')).toContainText('授权：已配对')
    await page.screenshot({ path: 'test-results/pg01-real-authorized-reload.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('e1. 窗口一次性：同 code 二次 complete → 真 410 → E-02 窗口关闭回态', async ({ browser }) => {
    const { origin } = harness.info

    // 第一台设备：真配对成功（窗口被消费）。
    const pageA = await freshDevicePage(browser)
    const consoleErrorsA = watchConsole(pageA)
    const win = await ctl<PairingWindow>(harness.info, 'POST', '/pairing-window')
    const resA = await pairViaManualForm(pageA, origin, win.code, '一次性窗口·设备A')
    expect(resA.status()).toBe(201)
    await expect(pageA).toHaveURL(/#\/lobby$/)
    expect(consoleErrorsA).toEqual([])

    // 第二台设备（干净 context，真实未配对）：同一 code 再次 complete → 真 410。
    const pageB = await freshDevicePage(browser)
    const consoleErrorsB = watchConsole(pageB)
    const resB = await pairViaManualForm(pageB, origin, win.code, '一次性窗口·设备B')
    expect(resB.status()).toBe(410)
    const bodyB = (await resB.json()) as { code: string }
    expect(bodyB.code).toBe(ERROR_CODE_AUTH_WINDOW_EXPIRED)

    // E-02 分类回态：窗口关闭面板 + 配对材料清除，不是笼统失败。
    await expect(pageB.locator('.window-closed-title')).toHaveText('配对窗口已关闭')
    await expect(pageB.locator('.window-closed')).toContainText('重新发起配对')
    await expect(pageB.locator('#pair-code')).toHaveValue('')
    await pageB.screenshot({ path: 'test-results/pg01-real-window-consumed.png', fullPage: true })
    expect(consoleErrorsB).toEqual([])
    await pageA.context().close()
    await pageB.context().close()
  })

  test('e2. 窗口关闭（桌面取消）后 complete → 真 410 → E-02 分类', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    const { origin } = harness.info

    const win = await ctl<PairingWindow>(harness.info, 'POST', '/pairing-window')
    const cancel = await ctl<{ cancelled: boolean }>(harness.info, 'POST', '/pairing-window/cancel', {
      generation: win.generation,
    })
    expect(cancel.cancelled).toBe(true)

    // 窗口已关闭：真实服务端 410 auth.window_expired（与"过期"同一分类路径；
    // 3min TTL 自然过期受生产 systemClock 约束不在浏览器层等待，见报告覆盖声明）。
    const res = await pairViaManualForm(page, origin, win.code, '窗口关闭·设备')
    expect(res.status()).toBe(410)
    const body = (await res.json()) as { code: string }
    expect(body.code).toBe(ERROR_CODE_AUTH_WINDOW_EXPIRED)

    await expect(page.locator('.window-closed-title')).toHaveText('配对窗口已关闭')
    await expect(page.locator('#pair-code')).toHaveValue('')
    await page.screenshot({ path: 'test-results/pg01-real-window-cancelled.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('f. 撤销：revoke → 下一次请求真 401 auth.revoked → E-03 踢回 PG-01 + 材料清除', async ({
    page,
    context,
  }) => {
    const consoleErrors = watchConsole(page)
    const { origin } = harness.info
    const deviceName = 'E2E 待撤销设备'

    // 先完成真实配对并落在大厅。
    const win = await ctl<PairingWindow>(harness.info, 'POST', '/pairing-window')
    const pairRes = await pairViaManualForm(page, origin, win.code, deviceName)
    expect(pairRes.status()).toBe(201)
    await expect(page).toHaveURL(/#\/lobby$/)
    // 本地投影已写（非密 device 投影）。
    const projectionBefore = await page.evaluate(() => localStorage.getItem('remote.device.projection'))
    expect(projectionBefore).toContain(deviceName)

    // harness 撤销该设备（桌面撤销动作等价物）。
    const devices = await ctl<DeviceEntry[]>(harness.info, 'GET', '/devices')
    const target = devices.find((d) => d.name === deviceName)
    expect(target).toBeDefined()
    await ctl(harness.info, 'POST', '/devices/revoke', { deviceId: target?.id })

    // 移动端下一次真实请求：重载大厅 → 真 401 auth.revoked（服务端同时下发清 Cookie）。
    const revokedResponse = page.waitForResponse((r) => r.url().includes('/host/summary'))
    await page.reload()
    const res = await revokedResponse
    expect(res.status()).toBe(401)
    const body = (await res.json()) as { code: string }
    expect(body.code).toBe(ERROR_CODE_AUTH_REVOKED)

    // E-03：踢回 PG-01 + 撤销横幅（真因呈现）。
    // 注：PG-01 权威路由为 #/connect，'/' 是入口别名（P5-DIR-01），两种形态都接受。
    await expect(page).toHaveURL(/#\/(connect)?$/)
    await expect(page.locator('.kick-banner')).toContainText('授权已被桌面端撤销')
    await expect(page.locator('.diagnosis-title')).toHaveText('这台设备还没有配对')
    // Major-07：撤销必须落 E-03，绝不可误报为 service-down（真服务器证据）。
    await expect(page.locator('.diagnosis-title')).not.toHaveText('远程服务未开启或版本不兼容')
    await expect(page.locator('.diagnosis-card')).not.toContainText('服务未开启')
    // 材料清除：服务端清 Cookie（context.cookies 已无凭据），本地投影已删。
    const cookiesAfter = await context.cookies(origin)
    expect(cookiesAfter.filter((c) => c.name === DEVICE_COOKIE_NAME && c.value !== '')).toHaveLength(0)
    const projectionAfter = await page.evaluate(() => localStorage.getItem('remote.device.projection'))
    expect(projectionAfter).toBeNull()
    await page.screenshot({ path: 'test-results/pg01-real-kick-revoked.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })
})
