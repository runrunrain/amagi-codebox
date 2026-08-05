// e2e/workspace-m3-int-multidevice.spec.ts — M3-INT 多设备并发 + 几何仲裁（真 harness，真 WS，Chromium-only）
// ---------------------------------------------------------------------------
// 权威依据：fuxi 20260804-m3-continuity-design/design.md §4–§8（控制仲裁 / E-06 / 几何 / grace）+
//   M3-A control-arbitration-design（exact writer / 30s grace / FIFO / acquire）。
// 证据分层（如实声明）：本 spec 是「relay-free 真 harness」层——双真实浏览器设备经真 WS
//   attach，控制态/写拒绝/grace/desktop 收回全部由真实 gate/arbiter/SessionEventHub 驱动；
//   desktop 收回/释放/被动 resize 经 harness 控制面（test-only，与桌面 Wails 边界同级）。
//   fake CLI 边界：harness resolver/launch 指向确定性 fake CLI（不启真进程）；会话生命周期
//   与 WS 因果投递链全真实。routeWebSocket 不用于本 spec（无整形需求）。
//
// 覆盖（design §8 九格 + task 1 编排）：
//   · 多设备 choreography：双观察→A acquire→B 写拒(含原因)→desktop 收回(E-06 lost)→
//     A 重申→几何冲突(A controller resize vs B observer resize)→A 断连 grace 内 B 写仍拒→
//     grace 过期 B 可申请。
//   · C4：A/B 同时 acquire barrier → {200,409} 各一 / loser E-06 conflict / loser raw=0。
//   · C5a：A 持权；desktop passive resize 被拒(R-06)；B observer resize 被拒；仅 A resize commit。
//   · C5b：A resize 停 checkpoint（relay holdClientResize defer）→ desktop explicit take 先 commit →
//     A resize 到达 server 被 checkpoint 1 拒（rawResize 仅 [desktop-dims]）→ A 见 E-06 lost/desktop。
//     （relay defer 使 desktop take 先 commit 成为确定性序；flush 后 A 的 resize 经真实 gate 被 fence。）
// ---------------------------------------------------------------------------

import { expect, test, type Browser, type Page } from '@playwright/test'
import {
  ctl,
  ctlRaw,
  closeDevice,
  enterWorkspace,
  freshDevicePage,
  GUIDE_KEY,
  pairBrowser,
  startHarness,
  stopHarness,
  watchConsole,
  type CreatedSession,
  type RawIO,
} from './helpers/harness'
import { installWsRelay } from './helpers/network'

type HarnessHandle = Awaited<ReturnType<typeof startHarness>>

const GRACE_SECONDS = 2.5 // 缩短 grace 到可观测窗口（design §7.2 / C-004；harness set-grace endpoint）
const RESIZE_DEBOUNCE_MS = 250 // WorkspacePage onWindowResize 去抖

/** 捕获某 page 上 WS 接收的服务端帧（control.state / output 等），用于事件序列 oracle。 */
function captureServerFrames(page: Page): { type: string; raw: string }[] {
  const frames: { type: string; raw: string }[] = []
  page.on('websocket', (ws) => {
    ws.on('framereceived', (frame) => {
      try {
        const o = JSON.parse(String(frame.payload)) as { type?: string }
        frames.push({ type: o.type ?? '', raw: String(frame.payload) })
      } catch {
        /* 非 JSON 忽略 */
      }
    })
  })
  return frames
}

/** 触发设备终端 resize 上报（viewport 变化 → onWindowResize → 去抖 → sendResize）。 */
async function triggerResize(page: Page, w: number, h: number): Promise<void> {
  await page.setViewportSize({ width: w, height: h })
  // 去抖窗口 + 网络往返余量。
  await page.waitForTimeout(RESIZE_DEBOUNCE_MS + 350)
}

test.describe('M3-INT 多设备并发 + 几何仲裁（真 harness，真 WS）', () => {
  let harness: HarnessHandle

  test.beforeEach(async () => {
    harness = await startHarness()
  })
  test.afterEach(async () => {
    await stopHarness(harness.proc)
  })

  test('多设备 choreography：双观察→acquire→写拒→desktop收回(E-06)→重申→几何冲突→grace内写拒→grace过期可申', async ({ browser }) => {
    const { info } = harness
    const consoleErrors: string[] = []
    // 缩短 grace，使「grace 内拒绝 / 过期可申」可在 wall-clock 内验证。
    await ctl(info, 'POST', '/control/grace-duration', { seconds: GRACE_SECONDS })
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    // —— 1. 双设备同时观察 ——
    const pageA = await freshDevicePage(browser)
    const pageB = await freshDevicePage(browser)
    consoleErrors.push(...watchConsole(pageA), ...watchConsole(pageB))
    await pageA.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    await pageB.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    await pairBrowser(pageA, info, '编排·设备A')
    await pairBrowser(pageB, info, '编排·设备B')
    await enterWorkspace(pageA, info, created.sessionId, created.title)
    await enterWorkspace(pageB, info, created.sessionId, created.title)
    // 两端初始均为 observer（无人控制）。
    await expect(pageA.locator('.control-bar')).toContainText('无人控制')
    await expect(pageB.locator('.control-bar')).toContainText('无人控制')

    // —— 2. 设备 A acquire 控制 ——
    await pageA.getByRole('button', { name: '获取控制权' }).click()
    await expect(pageA.locator('.control-bar')).toContainText('你正在控制')
    await expect(pageA.locator('.composer-input')).toBeEnabled()

    // —— 3. 设备 B 写拒绝（含原因）：B 是 observer，composer 禁用 + 原因 ——
    await expect(pageB.locator('.composer-input')).toBeDisabled()
    await expect(pageB.locator('.composer-block-reason')).toContainText('控制权在')
    // B 的 control-bar 显示控制权在 A（other + A 设备名）。
    await expect(pageB.locator('.control-bar')).toContainText('控制')

    // —— 4. 桌面收回（E-06 被收回原因）：desktop-take → A 见 E-06 lost/desktop ——
    await ctl(info, 'POST', `/control/session/${created.sessionId}/desktop-take`)
    const notice = pageA.locator('[data-testid=control-notice]')
    await expect(notice).toBeVisible({ timeout: 5_000 })
    await expect(notice).toHaveAttribute('data-e', 'e06')
    await expect(notice).toHaveAttribute('data-kind', 'lost')
    await expect(notice).toHaveAttribute('data-control-state', 'desktop')
    await expect(notice).toContainText('桌面端已收回控制权')
    await expect(pageA.locator('.composer-input')).toBeDisabled()
    await expect(pageA.locator('.control-bar')).toContainText('桌面端控制中')
    // B 也看到控制权转 desktop（observer 投影同步）。
    await expect(pageB.locator('.control-bar')).toContainText('桌面端控制中')

    // —— 5. 设备重新申请：desktop-release → holder=none → A 重申成功 ——
    await ctl(info, 'POST', `/control/session/${created.sessionId}/desktop-release`)
    await expect(pageA.locator('.control-bar')).toContainText('无人控制', { timeout: 5_000 })
    await pageA.getByRole('button', { name: '获取控制权' }).click()
    await expect(pageA.locator('.control-bar')).toContainText('你正在控制')

    // —— 6. 几何冲突（两端不同尺寸 resize 仲裁）：A controller resize vs B observer resize ——
    // 先建立 baseline（attach 时两端已各自上报一次 resize，但 observer 的被拒；A 作为 controller
    // 的 attach resize 已 commit）。读当前 resizeCount 作为基线。
    let io = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
    const resizeBaseA = io.resizeCount
    // A（controller）改视口 → sendResize → 服务端 commit（A 仍 you）。
    await triggerResize(pageA, 380, 760)
    await expect.poll(async () => (await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)).resizeCount, {
      timeout: 5_000,
    }).toBeGreaterThan(resizeBaseA)
    const afterA = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
    // B（observer）改视口 → sendResize → 服务端拒（非 controller）→ resizeCount 不增。
    await triggerResize(pageB, 340, 700)
    await expect.poll(async () => (await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)).resizeCount, {
      timeout: 3_000,
    }).toBe(afterA.resizeCount) // 仅 A 的 resize commit，B 被拒
    // A 仍是 controller（几何仲裁不改变控制权）。
    await expect(pageA.locator('.control-bar')).toContainText('你正在控制')

    // —— 7. 控制者断连 grace 期内观察者写仍拒绝：关闭 A 的 context → A 进入 grace（无 wire 事件，
    //    holder wire state 仍 device）；B 仍 observer，composer 仍禁用 ——
    await closeDevice(pageA)
    // barrier：确认 B 仍处于「控制权在 A」（grace 不发 wire 事件，B 看到的 holder 不变）。
    await expect(pageB.locator('.composer-input')).toBeDisabled()
    await expect(pageB.locator('.control-bar')).toContainText('控制')

    // —— 8. grace 过期后他设备可申请：服务端 control.state(connection_expired→none) 广播 →
    //    B 控制栏回到「无人控制」→ B acquire 成功 ——
    await expect(pageB.locator('.control-bar')).toContainText('无人控制', { timeout: Math.round(GRACE_SECONDS * 1000) + 4000 })
    await pageB.getByRole('button', { name: '获取控制权' }).click()
    await expect(pageB.locator('.control-bar')).toContainText('你正在控制')
    await expect(pageB.locator('.composer-input')).toBeEnabled()
    await pageB.screenshot({ path: 'test-results/m3-int-choreography-grace-acquire.png', fullPage: false })
    expect(consoleErrors).toEqual([])
    await closeDevice(pageB)
  })

  test('R4-001. 本地 release（真实产品链 REST+WS）：主动释放不误显 E-06；被动收回（desktop take）仍显示原因', async ({ browser }) => {
    // 权威依据：diting Round4 R4-001——server 在 release 事务内同步入队
    // control.state(reason=released)，WS 事件可先于 REST response 到达；client intent
    // 必须在发起 REST 前 armed。本用例走真实 Go harness + 真实配对 + 真实 UI 点击，
    // 不论 WS/REST 实际顺序如何，主动 release 后都不得出现「控制权已收回」。
    const { info } = harness
    const consoleErrors: string[] = []
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    const pageA = await freshDevicePage(browser)
    consoleErrors.push(...watchConsole(pageA))
    await pageA.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    await pairBrowser(pageA, info, 'R4-001·设备A')
    await enterWorkspace(pageA, info, created.sessionId, created.title)
    const notice = pageA.locator('[data-testid=control-notice]')

    // —— 1. 被动收回正对照：acquire → desktop take → E-06 lost/desktop 必须显示 ——
    await pageA.getByRole('button', { name: '获取控制权' }).click()
    await expect(pageA.locator('.control-bar')).toContainText('你正在控制')
    await ctl(info, 'POST', `/control/session/${created.sessionId}/desktop-take`)
    await expect(notice).toBeVisible({ timeout: 5_000 })
    await expect(notice).toHaveAttribute('data-e', 'e06')
    await expect(notice).toHaveAttribute('data-kind', 'lost')
    await expect(notice).toContainText('桌面端已收回控制权')

    // —— 2. 主动释放：desktop-release → 重申 → UI 点击「释放控制权」→ 不得出现 E-06 ——
    await ctl(info, 'POST', `/control/session/${created.sessionId}/desktop-release`)
    await expect(pageA.locator('.control-bar')).toContainText('无人控制', { timeout: 5_000 })
    await pageA.getByRole('button', { name: '获取控制权' }).click()
    await expect(pageA.locator('.control-bar')).toContainText('你正在控制')
    // 重申后旧 notice 已清除（control.state you → notice=null）。
    await expect(notice).toBeHidden()
    await pageA.getByRole('button', { name: '释放控制权' }).click()
    await expect(pageA.locator('.control-bar')).toContainText('无人控制', { timeout: 5_000 })
    // 等 WS/REST 两种顺序都充分落地后，仍无任何 E-06 收回提示。
    await pageA.waitForTimeout(800)
    await expect(notice).toBeHidden()
    await expect(pageA.locator('.control-bar')).not.toContainText('控制权已收回')
    await expect(pageA.locator('body')).not.toContainText('控制权已收回')
    await pageA.screenshot({ path: 'test-results/m3-int-r4-001-local-release.png', fullPage: false })
    expect(consoleErrors).toEqual([])
    await closeDevice(pageA)
  })

  test('C4. A/B 同时 acquire barrier：恰一 200 / 恰一 409，loser 见 E-06 conflict 且 raw=0', async ({ browser }) => {
    const { info } = harness
    const consoleErrors: string[] = []
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    const pageA = await freshDevicePage(browser)
    const pageB = await freshDevicePage(browser)
    consoleErrors.push(...watchConsole(pageA), ...watchConsole(pageB))
    // 捕获两页 control.state 帧（design §8 C4 oracle：control.state(acquired)=1）。
    const framesA = captureServerFrames(pageA)
    const framesB = captureServerFrames(pageB)
    await pageA.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    await pageB.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    await pairBrowser(pageA, info, 'C4·设备A')
    await pairBrowser(pageB, info, 'C4·设备B')
    await enterWorkspace(pageA, info, created.sessionId, created.title)
    await enterWorkspace(pageB, info, created.sessionId, created.title)
    // barrier：两端均 observer 且 acquire 按钮可点（避免 Promise.all 跨页 click 的 actionability 死锁）。
    await expect(pageA.locator('.control-bar')).toContainText('无人控制')
    await expect(pageB.locator('.control-bar')).toContainText('无人控制')
    const btnA = pageA.getByRole('button', { name: '获取控制权' })
    const btnB = pageB.getByRole('button', { name: '获取控制权' })
    await expect(btnA).toBeEnabled()
    await expect(btnB).toBeEnabled()

    // 两端同时点 acquire（服务端 stateMu 串行化 → 恰一胜出）。竞态留在网络/服务端，非 UI 层。
    // 预解析两端按钮 elementHandle（barrier 已证 enabled）：随后用 handle.evaluate(el=>el.click())
    // 直发 DOM click，不经 locator auto-wait。否则胜出方的 control.state 广播会重渲染败者控制栏
    // （按钮 detach），败者的 click/dispatchEvent auto-wait 会阻塞至超时（flaky）。两 handle 在 acquire
    // 响应返回前已就绪，近同时发出 → 服务端 stateMu 串行化 → 恰一 200 / 恰一 409。
    const handleA = await btnA.elementHandle()
    const handleB = await btnB.elementHandle()
    await Promise.all([
      handleA!.evaluate((el) => (el as HTMLElement).click()),
      handleB!.evaluate((el) => (el as HTMLElement).click()),
    ])
    // 恰一页「你正在控制」，另一页 E-06 conflict（409）。
    const aCtrl = pageA.locator('.control-bar')
    const bCtrl = pageB.locator('.control-bar')
    await expect.poll(
      async () => {
        const aYou = await aCtrl.textContent().then((t) => t?.includes('你正在控制') ?? false)
        const bYou = await bCtrl.textContent().then((t) => t?.includes('你正在控制') ?? false)
        return aYou || bYou
      },
      { timeout: 5_000 },
    ).toBe(true)
    const aYou = (await aCtrl.textContent())?.includes('你正在控制') ?? false
    const bYou = (await bCtrl.textContent())?.includes('你正在控制') ?? false
    expect(aYou || bYou).toBe(true)
    expect(aYou && bYou).toBe(false) // 不得双 200
    const winner = aYou ? pageA : pageB
    const loser = aYou ? pageB : pageA
    // loser 见 E-06 conflict（不改权威态）+ Composer 禁用。
    const loserNotice = loser.locator('[data-testid=control-notice]')
    await expect(loserNotice).toBeVisible({ timeout: 5_000 })
    await expect(loserNotice).toHaveAttribute('data-e', 'e06')
    await expect(loserNotice).toHaveAttribute('data-kind', 'conflict')
    await expect(loser.locator('.composer-input')).toBeDisabled()
    // winner 仍可写；loser input/resize raw=0（未获权，无任何 raw 写入）。
    await expect(winner.locator('.composer-input')).toBeEnabled()
    const io = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
    expect(io.writeCount, 'C4 loser 不得产生任何 raw 写入').toBe(0)
    expect(io.resizeCount, 'C4 双方均未 resize').toBe(0)
    // C4 冻结 oracle：control.state(acquired)=1（恰一设备收到 you；snapshot you/busy 各一）。
    const youEvents = [...framesA, ...framesB].filter((f) => {
      try {
        const o = JSON.parse(f.raw) as { type?: string; state?: string }
        return o.type === 'control.state' && o.state === 'you'
      } catch {
        return false
      }
    })
    expect(youEvents.length, 'C4 control.state(acquired)=1（恰一 you）').toBe(1)
    await loser.screenshot({ path: 'test-results/m3-int-c4-conflict-loser.png', fullPage: false })
    expect(consoleErrors).toEqual([])
    await closeDevice(pageA)
    await closeDevice(pageB)
  })

  test('C5a. A 持权：desktop passive resize 被拒(R-06)；B observer resize 被拒；仅 A resize commit', async ({ browser }) => {
    const { info } = harness
    const consoleErrors: string[] = []
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    const pageA = await freshDevicePage(browser)
    const pageB = await freshDevicePage(browser)
    consoleErrors.push(...watchConsole(pageA), ...watchConsole(pageB))
    await pageA.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    await pageB.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    // 捕获 A 的服务端帧：desktop passive 被拒不应产生 control.state 事件。
    captureServerFrames(pageA)
    await pairBrowser(pageA, info, 'C5a·控制者A')
    await pairBrowser(pageB, info, 'C5a·观察者B')
    await enterWorkspace(pageA, info, created.sessionId, created.title)
    await enterWorkspace(pageB, info, created.sessionId, created.title)
    await pageA.getByRole('button', { name: '获取控制权' }).click()
    await expect(pageA.locator('.control-bar')).toContainText('你正在控制')

    // desktop passive resize：device 持权 → 被拒（design §6.2 R-06，DenyNotController）。
    // 必须是策略拒（非 "runtime not ready" 接线 bug）。
    const passive = await ctlRaw(info, 'POST', `/control/session/${created.sessionId}/desktop-passive-resize`, { cols: 120, rows: 40 })
    expect(passive.status, 'desktop passive resize 必须被 device holder 拒绝(R-06)').toBeGreaterThanOrEqual(400)
    expect(JSON.stringify(passive.body), 'desktop passive resize 拒因须为策略(R-06)而非接线 not ready').not.toContain('not ready')

    // A（controller）resize → commit。
    let io = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
    const base = io.resizeCount
    await triggerResize(pageA, 380, 760)
    await expect.poll(async () => (await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)).resizeCount, {
      timeout: 5_000,
    }).toBe(base + 1)
    // B（observer）resize → 被拒（非 controller），resizeCount 不再增。
    const afterA = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
    await triggerResize(pageB, 340, 700)
    await expect.poll(async () => (await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)).resizeCount, {
      timeout: 3_000,
    }).toBe(afterA.resizeCount)
    // A 仍是 controller（passive/observer resize 不改变控制权）。
    await expect(pageA.locator('.control-bar')).toContainText('你正在控制')
    await pageA.screenshot({ path: 'test-results/m3-int-c5a-controller-resize.png', fullPage: false })
    expect(consoleErrors).toEqual([])
    await closeDevice(pageA)
    await closeDevice(pageB)
  })

  test('C5b. A resize defer 到 relay → desktop take 先 commit → release 后 resize 到 server 被 production gate 拒（不 commit）：wire 帧 post-fence 转发、resizeCount 不增、rawResize 仅 [desktop-dims]、A 见 E-06 lost/desktop', async ({ browser }) => {
    const { info } = harness
    const consoleErrors: string[] = []
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    const pageA = await freshDevicePage(browser)
    consoleErrors.push(...watchConsole(pageA))
    const serverFrames = captureServerFrames(pageA)
    await pageA.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    // R3 冻结 oracle：不再用 raw-port barrier（那是 post-checkpoint 且依赖 fake raw ctx.Done()
    // 取消——谛听 R3 报告判定「证明 ctx-aware fake raw 可取消，非 checkpoint 拒，不能映射生产
    // raw 行为」）。改用 **relay defer + post-fence flush + production gate 拒绝**：A 的 resize
    // 在 relay 排队（未到 server），desktop take 先 commit（entry.owner=desktop），releaseHolds
    // 后 A 的 resize 帧才到达 server → production DoDevicePTY 的 createDevicePTYPermit
    // owner-check 判 A 已非 holder → 返 DenyNotController → mutate(checkpoint+raw) 根本不执行
    // → ResizeRaw 不被调用 → resizeCount 不增。这是 **production gate 真实拒绝**（permit 创建
    // 层，先于 checkpoint），不依赖任何 fake raw ctx 行为。wire 帧精确断言：relay 在 fence
    // 之后才 forward resize（clientToServerForwarded post-flush 递增），server 回 error 帧。
    //
    // 边界如实声明：本 oracle 证明 resize 在 fence 后到达 server 被 **permit-creation owner-check**
    // 拒（DoDevicePTY 第一道 authority 栅栏）。报告期望的「permit 取得后、raw effect 前的
    // checkpoint(1) 拒绝」需 production checkpoint test seam（ws_v1_session.go resize closure
    // 内 permit.Checkpoint 调用点注入 test barrier），属 luban 边界（只改测试/harness 约束下
    // 不可达）。permit-creation owner-check 是先于 checkpoint 的等价/更强 authority 栅栏
    // （A 连 permit 都拿不到，更不会到 checkpoint）；本 oracle 以此为冻结层级。
    const { controller, cleanup } = await installWsRelay(pageA, '**/ws/v1', {})
    try {
      await pairBrowser(pageA, info, 'C5b·控制者A')
      await enterWorkspace(pageA, info, created.sessionId, created.title)
      await pageA.getByRole('button', { name: '获取控制权' }).click()
      await expect(pageA.locator('.control-bar')).toContainText('你正在控制')

      // baseline：A acquire 后的 resizeCount（含 attach 时初始尺寸上报，已透传 commit）。
      const baseline = (await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)).resizeCount
      // 在初始 resize 已 commit 之后再 arm resize defer（仅 defer 测试触发的 resize）。
      controller.armHoldClientResize()
      const fwdBefore = controller.counts.clientToServerForwarded
      const errBefore = serverFrames.filter((f) => f.type === 'error').length

      // A 触发 resize（380,760）→ resize 帧到 relay 被 defer（未转发到 server）。
      await triggerResize(pageA, 380, 760)
      // wire 帧 oracle（pre-fence）：resize 被 defer，relay 未 forward（clientToServerForwarded 不增）。
      expect(controller.counts.clientToServerForwarded, 'C5b pre-fence：A resize 被 defer 未 forward').toBe(fwdBefore)
      let ioDeferred = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
      expect(ioDeferred.resizeCount, 'C5b A resize defer 期间不得 commit（未到 server）').toBe(baseline)

      // desktop explicit take（HTTP）→ production gate.TakeDesktop commit（entry.owner=desktop，
      // controlEpoch++）→ A 见 E-06 lost/desktop。fence 发生在 A 的 resize 到达 server **之前**。
      await ctl(info, 'POST', `/control/session/${created.sessionId}/desktop-take`)
      const notice = pageA.locator('[data-testid=control-notice]')
      await expect(notice).toBeVisible({ timeout: 5_000 })
      await expect(notice).toHaveAttribute('data-e', 'e06')
      await expect(notice).toHaveAttribute('data-kind', 'lost')
      await expect(notice).toHaveAttribute('data-control-state', 'desktop')
      await expect(pageA.locator('.composer-input')).toBeDisabled()

      // releaseHolds → flush deferred resize 帧 → 到达 server（post-fence）。A 的 WS pair 仍开
      // （未断连），resize 帧经原 pair 送 server。
      controller.releaseHolds()
      // wire 帧 oracle（post-fence）：resize 帧 **在 fence 之后** 才 forward 到 server
      // （clientToServerForwarded 递增）——证明 resize 确实到达 production server，不是被
      // relay 静默吞掉。
      await expect.poll(() => controller.counts.clientToServerForwarded, {
        timeout: 5_000,
        message: 'C5b post-fence：deferred resize 被 forward 到 server（clientToServerForwarded 递增）',
      }).toBeGreaterThan(fwdBefore)
      // production gate 拒绝证据：server 回 error 帧（createDevicePTYPermit → DenyNotController
      // → sendWSGateError）——正向信号证明 server **已处理** 该 resize 并拒绝（而非未到达）。
      await expect.poll(
        () => serverFrames.filter((f) => f.type === 'error').length,
        { timeout: 5_000, message: 'C5b server 回 error 帧（production gate 拒绝 stale-holder resize）' },
      ).toBeGreaterThan(errBefore)
      // server-side 权威 oracle：resizeCount 仍为 baseline（A 的 resize 被 production gate 拒，
      // ResizeRaw 根本未被调用 → 不 commit）。
      let ioRejected = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
      expect(ioRejected.resizeCount, 'C5b A resize 被 production gate 拒（不 commit）：resizeCount 仍 baseline').toBe(baseline)

      // desktop passive resize（holder=desktop）→ commit desktop-dims。
      await ctl(info, 'POST', `/control/session/${created.sessionId}/desktop-passive-resize`, { cols: 100, rows: 30 })
      await expect.poll(async () => (await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)).resizeCount, {
        timeout: 3_000,
        message: 'desktop passive resize commit → resizeCount = baseline+1',
      }).toBe(baseline + 1)
      const afterDesktop = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
      // C5b 冻结 oracle：rawResize 仅 [desktop-dims]（A dims=0：到达 server 但被 production gate 拒）。
      expect(afterDesktop.resizeCount, 'rawResize 仅 desktop 一次（A 被 production gate 拒）').toBe(baseline + 1)
      expect(afterDesktop.lastResizeCols, '最后一次 rawResize 为 desktop-dims cols=100').toBe(100)
      expect(afterDesktop.lastResizeRows, '最后一次 rawResize 为 desktop-dims rows=30').toBe(30)

      // events 恰 [desktop,takeover] 1 条（control.state desktop 抢占；A 的 resize 被 gate 拒不产生 control 事件）。
      const desktopEvents = serverFrames.filter((f) => {
        try {
          const o = JSON.parse(f.raw) as { type?: string; state?: string }
          return o.type === 'control.state' && o.state === 'desktop'
        } catch {
          return false
        }
      })
      expect(desktopEvents.length, 'events 恰 [desktop,takeover] 1 条').toBe(1)

      await pageA.screenshot({ path: 'test-results/m3-int-c5b-relay-defer-gate-reject.png', fullPage: false })
      expect(consoleErrors).toEqual([])
    } finally {
      await cleanup()
      await closeDevice(pageA)
    }
  })
})
