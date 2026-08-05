// e2e/helpers/harness.ts — 真服务器 harness bootstrap（M2-INT / M3-C / M3-INT 共用）
// ---------------------------------------------------------------------------
// 复用 e2e/harness/remote-server（真 Go Server + 真实 gate/ledger/stream/causal
// drain）。harness 二进制构建缓存跨 spec 共享（同 worker 进程）；每用例 spawn
// 新进程（独立 config dir / 端口）。控制面（/control/*）是 TEST-ONLY 辅助通道。
// fake CLI 边界如实声明：resolver/launch seam 指向确定性 fake CLI，不查找真实
// 二进制、不启动真实进程；会话生命周期与 WS 因果投递链全真实。
// ---------------------------------------------------------------------------

import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import { expect, type Browser, type Page } from '@playwright/test'
import * as fs from 'node:fs'
import * as os from 'node:os'
import * as path from 'node:path'

const REPO_ROOT = path.resolve(__dirname, '..', '..')
export const MOBILE_DIST = path.join(REPO_ROOT, 'mobile', 'dist')
const HARNESS_MODULE = './e2e/harness/remote-server'
export const GUIDE_KEY = 'amagi.pg03.guide.dismissed'

export interface HarnessInfo {
  origin: string
  controlOrigin: string
}

let harnessBinPromise: Promise<string> | null = null

export function buildHarness(): Promise<string> {
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

export async function startHarness(): Promise<{ proc: ChildProcess; info: HarnessInfo }> {
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

export async function stopHarness(proc: ChildProcess): Promise<void> {
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
export async function ctl<T = unknown>(
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

/** 控制面调用，返回完整 Response（用于断言 4xx 状态码，如 409）。 */
export async function ctlRaw(
  info: HarnessInfo,
  method: 'GET' | 'POST',
  p: string,
  body?: unknown,
): Promise<{ status: number; body: unknown }> {
  const res = await fetch(`${info.controlOrigin}${p}`, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  let parsed: unknown = null
  try {
    parsed = await res.json()
  } catch {
    parsed = await res.text()
  }
  return { status: res.status, body: parsed }
}

export interface PairingWindow {
  generation: number
  code: string
  expiresAt: string
  addressRequired: boolean
}

export interface CreatedSession {
  sessionId: string
  title: string
  state: string
  cliType: string
}

export interface RawIO {
  writeCount: number
  writeBytes: number
  resizeCount: number
  lastResizeCols: number
  lastResizeRows: number
  /** M3-007 C5b test-only：resize barrier 命中次数（证明 resize 到达 raw port in-flight）。 */
  resizeBarrierHits: number
  /** M3-007 C2b（R3 冻结 oracle）：不可逆 FIFO 顺序摘要（rolling SHA-256 hex）。
   *  测试侧据已知 FIFO 序列独立复算同一链比对，证明 raw port 见到 N 项顺序与入队一致。 */
  writeOrderChain: string
}

export function watchConsole(page: Page): string[] {
  const errors: string[] = []
  page.on('console', (msg) => {
    if (msg.type() !== 'error') return
    if (msg.text().startsWith('Failed to load resource')) return
    errors.push(msg.text())
  })
  page.on('pageerror', (err) => errors.push(String(err)))
  return errors
}

/** 新建一台隔离浏览器设备页（独立 context，与默认 page 隔离 cookie/状态）。 */
export async function freshDevicePage(browser: Browser): Promise<Page> {
  // 读取当前 project 的 use 选项（视口/移动），保持设备页与默认页同视口。
  // 注：test.info() 仅在 test 回调内可用；此处经闭包由调用方传入更稳。
  const context = await browser.newContext()
  return context.newPage()
}

/** 真实配对一台浏览器设备（深链 code + expiresAt），落在大厅。 */
export async function pairBrowser(page: Page, info: HarnessInfo, deviceName: string): Promise<void> {
  const win = await ctl<PairingWindow>(info, 'POST', '/pairing-window')
  await page.goto(
    `${info.origin}/#/connect?code=${encodeURIComponent(win.code)}&expiresAt=${encodeURIComponent(win.expiresAt)}`,
  )
  await expect(page.locator('#pair-code')).toHaveValue(win.code)
  await page.locator('#pair-device-name').fill(deviceName)
  await page.getByRole('button', { name: '完成配对' }).click()
  await expect(page).toHaveURL(/#\/lobby$/)
}

/** 进入指定会话工作区并等待真 WS attach 完成。 */
export async function enterWorkspace(page: Page, info: HarnessInfo, sessionId: string, title: string): Promise<void> {
  await page.locator('.session-card', { hasText: title }).click()
  await expect(page).toHaveURL(new RegExp(`#/workspace/${sessionId}$`))
  await expect(page.locator('.status-bar')).toContainText('连接：已连接', { timeout: 8_000 })
}

/** 关闭浏览器设备页及其 context（grace/多设备用例清理）。 */
export async function closeDevice(page: Page): Promise<void> {
  try {
    await page.context().close()
  } catch {
    /* 已关闭 */
  }
}
