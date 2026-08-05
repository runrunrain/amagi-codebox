// e2e/workspace-m3-int-relay.spec.ts — M3-INT relay 整形 + eviction + 草稿全链（真 harness + installWsRelay；Chromium-only）
// ---------------------------------------------------------------------------
// 权威依据：fuxi 20260804-m3-continuity-design/design.md §3/§5/§8（late-attach / 输入幂等 / 缺口 / 整形矩阵）。
// 证据分层（如实声明）：本 spec 是「relay + 真 harness」层——协议/服务端 ledger/arbiter/stream 全真实
//   （e2e/harness/remote-server 真 Go Server + fake CLI seam），故障是应用帧级确定性注入（installWsRelay），
//   不是 TCP packet/带宽模型，不冒充真机 conditioner。
// 机器 oracle（design §8 [R2/M-05]）：relay typed 计数 + 派生布尔（sameMessageID/distinctRequestIDs）+
//   harness raw-io 计数（rawInput/resizeCount = 输入/resize 0 重复断言）+ DOM applied-once（每帧唯一文本
//   恰一次）+ wire lastSeq（reattach 游标）。
// 边界：Playwright 无 unrouteWebSocket——每用例独占 page/context（test 默认隔离）。
//
// 覆盖：
//   · C2a：首次 input C→S drop1+断 → reattach 重试（attempts=2/sameMsgID/distinctReq/raw=1/ack=1/settled=1）。
//   · C6a：4097 one-byte frame evict Seq1 → 新设备 late attach（earliest=2、marker [1,1] exhausted 仍=1）。
//   · 断线-恢复-缺口-草稿全链（task 3）：草稿断连期间在途 → 恢复后幂等结算 → 时间线只出现一次。
//   · C2b：relay gate 队首（holdAllClientInputs）→ accept 多 draft（outbox-strip）→ 断 → 新连接 FIFO drain
//     （rawInput=N、settled=N、outbox=0）。R2 证据修复：N=32 全量 FIFO drain（真
//     client→WS→gate→ledger→raw→ACK 32 项边界链）；32/33 + 256KiB±1 边界保留在
//     inputOutbox.test.ts 单测层（权威，确定性快）。
//   · C6b relay wire-lastSeq：drop S→C N、forward N+1 → 断 → reattach（wire lastSeq=N-1、Seq 各一次、marker 消除）。
// ---------------------------------------------------------------------------

import { expect, test, type Browser, type Page } from '@playwright/test'
import { createHash } from 'node:crypto'
import {
  ctl,
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

/** 捕获某 page 上 C→S 发送的 attach 帧（读 wire lastSeq 游标）。 */
function captureAttachFrames(page: Page): { lastSeq: number | undefined; generation: number }[] {
  const attaches: { lastSeq: number | undefined; generation: number }[] = []
  let gen = 0
  page.on('websocket', (ws) => {
    gen += 1
    const g = gen
    ws.on('framesent', (frame) => {
      try {
        const o = JSON.parse(String(frame.payload)) as { type?: string; lastSeq?: number }
        if (o.type === 'attach') attaches.push({ lastSeq: o.lastSeq, generation: g })
      } catch {
        /* 非 JSON 忽略 */
      }
    })
  })
  return attaches
}

test.describe('M3-INT relay 整形 + eviction + 草稿全链（真 harness + WS relay）', () => {
  let harness: HarnessHandle

  test.beforeEach(async () => {
    harness = await startHarness()
  })
  test.afterEach(async () => {
    await stopHarness(harness.proc)
  })

  test('C2a. 首次 input C→S drop1+断 → reattach 重试：attempts=2/sameMsgID/distinctReq/raw=1/ack=1/settled=1', async ({ browser }) => {
    const { info } = harness
    const consoleErrors: string[] = []
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    const page = await freshDevicePage(browser)
    consoleErrors.push(...watchConsole(page))
    await page.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    // relay：丢首个 input 类型 C→S 帧（全局只丢一次；首个 C→S 帧是 attach 不是 input）。
    // 不用 closeAfterDropInput（从 onMessage 内 closePair 会阻断 conn-2 建立）；
    // 改用验证过的 disconnectAll（从测试侧触发 clientSide close）制造「丢 1 并断」。
    const { controller, cleanup } = await installWsRelay(page, '**/ws/v1', {
      dropClientToServerInputN: 1,
    })
    try {
      await pairBrowser(page, info, 'C2a·重试设备')
      await enterWorkspace(page, info, created.sessionId, created.title)
      await page.getByRole('button', { name: '获取控制权' }).click()
      await expect(page.locator('.control-bar')).toContainText('你正在控制')

      // 发送草稿 → 首个 input 被 relay 丢弃（无 ACK）。
      await page.locator('.composer-input').fill('c2a-msg')
      await page.locator('.composer-send').click()
      // barrier：首个 input 已被 relay 观察并丢弃（在 ~1s 同连重试前断连）。
      await expect.poll(() => controller.counts.clientToServerDropped, { timeout: 8_000 }).toBeGreaterThanOrEqual(1)
      // 断连（丢 1 并断）→ 客户端 reconnecting。
      await controller.disconnectAll(1011)
      const banner = page.locator('[data-testid=continuity-banner]')
      await expect(banner).toBeVisible({ timeout: 5_000 })
      await expect(banner).toHaveAttribute('data-state', 'reconnecting')

      // 恢复（≤5s）→ 重试帧经新透明 pair 转发 → 服务端处理 → ACK → settled。
      await expect(banner).toHaveAttribute('data-state', 'restored', { timeout: 5_000 })
      const userCard = page.locator('.user-message', { hasText: 'c2a-msg' })
      await expect(userCard.locator('.user-delivery')).toContainText('已确认', { timeout: 8_000 })

      // 机器 oracle（design §8 C2a）：rawInput=1（输入 0 重复）、sameMsgID、distinctReq、
      // attempts=2（两个 input 帧：首个 drop、次个 forward）、ackProduced=1（服务端恰一个 input.ack）。
      const io = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
      expect(io.writeCount, 'C2a rawInput 必须恰 1 次（首个丢、重试不重复写）').toBe(1)
      expect(controller.counts.sameMessageID, '重试必须同 MessageID（幂等）').toBe(true)
      expect(controller.counts.distinctRequestIDs, '各 attempt 必须 distinct RequestID').toBe(true)
      expect(controller.counts.inputFramesObserved, 'C2a attempts=2（两个 input 帧）').toBe(2)
      expect(controller.counts.ackFramesObserved, 'C2a ackProduced=1（服务端恰一个 input.ack）').toBe(1)
      await page.screenshot({ path: 'test-results/m3-int-c2a-input-drop-retry-settled.png', fullPage: false })
      expect(consoleErrors).toEqual([])
    } finally {
      await cleanup()
      await closeDevice(page)
    }
  })

  test('C6a. 4097 one-byte frame evict Seq1 → 新设备 late attach：earliest=2、marker [1,1] exhausted 且仍=1', async ({ browser }) => {
    const { info } = harness
    const consoleErrors: string[] = []
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    // 服务端先产出 4097 帧（>4096 回放环）→ origin 仅 evict Seq1，earliestSeq=2。
    const batch = await ctl<{ firstSeq: number; lastSeq: number; count: number }>(
      info,
      'POST',
      `/control/session/${created.sessionId}/output-many`,
      { count: 4097, prefix: 'ev' },
    )
    expect(batch.count).toBe(4097)
    expect(batch.firstSeq).toBe(1)

    // 新设备首 attach（无任何 prior 客户端）→ 真 WS attached。
    const page = await freshDevicePage(browser)
    consoleErrors.push(...watchConsole(page))
    await page.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    await pairBrowser(page, info, 'C6a·late-attach')
    await enterWorkspace(page, info, created.sessionId, created.title)

    // design §3：earliestSeq=2>1 → 先建立 [1,1] unavailable marker（exhausted，永久保留）再应用 tail。
    const gap = page.locator('[data-testid=gap-marker]')
    await expect(gap).toBeVisible({ timeout: 8_000 })
    await expect(gap).toHaveAttribute('data-gap-state', 'exhausted')
    await expect(gap).toHaveAttribute('data-from-seq', '1')
    await expect(gap).toHaveAttribute('data-to-seq', '1')
    // marker [1,1] exhausted 即 earliestSeq=2 的机器证据（客户端据 earliestSeq 建 [1,earliest-1] unavailable）。
    // TimelineView 虚拟化（useVirtualizer）只渲染可视窗口，且 4096 帧 tail 渲染/滚动不在本格 oracle；
    // marker exhausted 永久保留即 C6a 的核心断言（design §8 C6a）。evict 的 Seq1 全局不存在。
    await expect(page.locator('.mono-block', { hasText: 'ev-1' })).toHaveCount(0)
    // 历史层 warning（存在不可恢复缺口）。
    await expect(page.locator('.status-bar')).toContainText('历史')
    await page.screenshot({ path: 'test-results/m3-int-c6a-eviction-exhausted-marker.png', fullPage: false })
    expect(consoleErrors).toEqual([])
    await closeDevice(page)
  })

  test('断线-恢复-缺口-草稿全链（task 3）：草稿断连期间在途 → 恢复幂等结算 → 时间线只出现一次', async ({ browser }) => {
    const { info } = harness
    const consoleErrors: string[] = []
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    const page = await freshDevicePage(browser)
    consoleErrors.push(...watchConsole(page))
    await page.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    // relay：丢第 3 条 S→C 帧（制造 recoverable 缺口，与 R2 同序：#1 attached、#2 resize-forbidden error、#3 output→DROP）。
    const { controller, cleanup } = await installWsRelay(page, '**/ws/v1', {
      dropServerToClientAt: 3,
    })
    try {
      await pairBrowser(page, info, 'chain·草稿设备')
      await enterWorkspace(page, info, created.sessionId, created.title)
      // 先在 acquire 之前制造 recoverable 缺口（避免 acquire 的 control.state 帧推移 output 位置）。
      // barrier：resize-forbidden error（#2）已转发，后续注入的 seq1 必为 #3（drop 定点）。
      await expect.poll(() => controller.counts.serverToClientForwarded, { timeout: 8_000 }).toBeGreaterThanOrEqual(2)

      // 注入 seq1（被 relay 丢 → recoverable 缺口）+ seq2（到达 → 越洞缓冲不投影）。
      await ctl(info, 'POST', `/control/session/${created.sessionId}/output`, { text: 'chain-one\n' })
      await ctl(info, 'POST', `/control/session/${created.sessionId}/output`, { text: 'chain-two\n' })
      const gap = page.locator('[data-testid=gap-marker]')
      await expect(gap).toBeVisible({ timeout: 8_000 })
      await expect(gap).toHaveAttribute('data-gap-state', 'recoverable')

      // acquire 控制（缺口之后）→ 发送草稿（草稿在断连期间在途/pending）。
      await page.getByRole('button', { name: '获取控制权' }).click()
      await expect(page.locator('.control-bar')).toContainText('你正在控制')
      await page.locator('.composer-input').fill('chain-draft')
      await page.locator('.composer-send').click()
      // barrier：草稿被 accept（用户卡出现 = 入 outbox）。不查具体 delivery 态
      // （发送中/重试/已确认 均可能，取决于 input 是否在 gap 期间送达——非确定）。
      const draftCard = page.locator('.user-message', { hasText: 'chain-draft' })
      await expect(draftCard).toBeVisible({ timeout: 8_000 })

      // 断连（草稿 input 已到服务端，ACK 可能已到或在途）→ reconnecting → 恢复。
      await controller.disconnectAll(1011)
      const banner = page.locator('[data-testid=continuity-banner]')
      await expect(banner).toBeVisible({ timeout: 5_000 })
      await expect(banner).toHaveAttribute('data-state', 'reconnecting')
      await expect(banner).toHaveAttribute('data-state', 'restored', { timeout: 5_000 })

      // 恢复后：缺口原位消除（seq1/seq2 各一次）+ 草稿幂等结算（时间线恰一次）。
      await expect(gap).toHaveCount(0, { timeout: 5_000 })
      await expect(page.locator('.mono-block', { hasText: 'chain-one' })).toHaveCount(1)
      await expect(page.locator('.mono-block', { hasText: 'chain-two' })).toHaveCount(1)
      const userCard = page.locator('.user-message', { hasText: 'chain-draft' })
      await expect(userCard).toHaveCount(1) // 时间线只出现一次（不因重试/wire attempts>1 重复渲染）
      await expect(userCard.locator('.user-delivery')).toContainText('已确认', { timeout: 8_000 })

      // 幂等机器 oracle：rawInput=1（草稿 0 重复写，即便断连引发潜在重试）。
      const io = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
      expect(io.writeCount, '草稿 rawInput 必须恰 1 次（幂等结算）').toBe(1)
      await page.screenshot({ path: 'test-results/m3-int-chain-draft-idempotent-once.png', fullPage: false })
      expect(consoleErrors).toEqual([])
    } finally {
      await cleanup()
      await closeDevice(page)
    }
  })

  test('C2b. relay gate 队首 → accept 32 + 33rd reject(outbox.full) → 断 → FIFO drain（rawInput=32/wireFor33rd=0/draft33 不变/FIFO 顺序摘要）', async ({ browser }) => {
    test.setTimeout(180_000) // N=32 全量 drain：连发 32 + 33rd reject + 断重连 + FIFO 逐项结算需较长预算
    const { info } = harness
    const consoleErrors: string[] = []
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })
    // R3 冻结 oracle：N=32（INPUT_OUTBOX_MAX_ENTRIES）真 client→WS→gate→ledger→raw→ACK 全链。
    // 在真 product/harness 链实际输入第 33 条并观测 draft33 保留 + wireFor33rd=0；FIFO 顺序由
    // harness raw port rolling SHA-256 链权威锁定（不依赖客户端自报顺序）。32/33 + 256KiB±1 +
    // FIFO-drain-32 状态机边界仍由 inputOutbox.test.ts 单测互补覆盖。
    const N = 32

    const page = await freshDevicePage(browser)
    consoleErrors.push(...watchConsole(page))
    await page.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    // relay：hold 所有 input（gate 队首）→ single-flight 下后续 draft 仅 accept 不上 wire。
    const { controller, cleanup } = await installWsRelay(page, '**/ws/v1', {
      holdAllClientInputs: true,
    })
    try {
      await pairBrowser(page, info, 'C2b·FIFO 设备')
      await enterWorkspace(page, info, created.sessionId, created.title)
      await page.getByRole('button', { name: '获取控制权' }).click()
      await expect(page.locator('.control-bar')).toContainText('你正在控制')

      // 连发 N=32 个 draft：head（fifo-1）被 hold（无 ACK），2..N single-flight 排队 accept。
      // 虚拟化感知（TimelineView useVirtualizer 只渲染可视窗口）：不逐条断言 user-card 可见
      // （滚出视口即 not-in-DOM，toBeVisible 报 element-not-found，不可靠）。改以「composer-input
      // 清空」验证每次 accept 成功——sendDraft 仅在 outbox.accept 返回 accepted 时清空 draft，
      // 受控 textarea :value 随之变 ''；accept 失败（outbox.full/secure_id_unavailable 等）则 draft
      // 保留，此处立即暴露。32 条 accept 累积与 FIFO drain/不重复的权威 oracle 交给 harness
      // raw-io writeCount（冻结机器计数，不受前端虚拟化影响，见 drain 段）。
      const strip = page.locator('[data-testid=outbox-strip]')
      for (let i = 1; i <= N; i++) {
        await page.locator('.composer-input').fill(`fifo-${i}`)
        await page.locator('.composer-send').click()
        await expect(page.locator('.composer-input'), `fifo-${i} accept 必须成功（draft 清空）`).toHaveValue('', { timeout: 5_000 })
        await page.waitForTimeout(40)
      }
      // hold 期间服务端 rawInput 必须为 0（全部被 relay hold，未到 gate/ledger/raw）。
      let io = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
      expect(io.writeCount, 'C2b hold 期间服务端 rawInput 必须为 0').toBe(0)
      // 记录 33rd reject 前的 relay wire 计数（wireFor33rd=0 断言基线）。
      const wireBefore33rd = controller.counts.inputFramesObserved

      // C2b 冻结 oracle（R3）第 33 条 product reject：outbox 已满（32 entries），第 33 条 accept
      // 返回 outbox.full → sendDraft 不清空 draft（composer-input 保留 fifo-33）→ 不产生任何
      // wire 帧（wireFor33rd=0）。这是真 product 链（ComposerBar→InputOutbox.accept→满拒）
      // 的客户端边界证据，与 inputOutbox.test.ts「32 accepted, 33rd rejected」状态机单测互补。
      await page.locator('.composer-input').fill('fifo-33')
      await page.locator('.composer-send').click()
      // draft33 保留（accept 失败不清空）：composer-input 仍为 'fifo-33'。
      await expect(page.locator('.composer-input'), 'C2b 第 33 条 outbox.full reject：draft33 保留不清空').toHaveValue('fifo-33')
      // wireFor33rd=0：relay 未观察到任何新增 input 帧（33rd 未上 wire）。
      expect(controller.counts.inputFramesObserved, 'C2b wireFor33rd=0：第 33 条未产生 wire 帧').toBe(wireBefore33rd)
      // outbox-strip 仍在（32 pending 未清，未吞第 33）。
      await expect(strip, 'C2b outbox 仍 32 项（未吞第 33）').toBeVisible()

      // 断连（discard held frames）→ 释放 hold 使重连新 pair 透明 → FIFO drain。
      await controller.disconnectAll(1011)
      controller.releaseHolds()
      const banner = page.locator('[data-testid=continuity-banner]')
      await expect(banner).toBeVisible({ timeout: 5_000 })
      await expect(banner).toHaveAttribute('data-state', 'restored', { timeout: 8_000 })
      // FIFO drain：32 条全部 settled，outbox 清空（strip 随 pendingCount=0 消失）。
      // N=32 逐项结算（每项一轮 WS 往返）需较长预算；poll rawInput 到 32。
      await expect.poll(async () => (await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)).writeCount, {
        timeout: 60_000,
        message: `C2b FIFO drain 后 rawInput 应为 ${N}`,
      }).toBe(N)
      await expect(strip).toHaveCount(0, { timeout: 30_000 })
      // C2b 冻结 oracle（R3）—— 不可逆 FIFO 顺序摘要：raw port rolling SHA-256 链锁定
      // 32 项 drain 顺序（chain_i = sha256(chain_{i-1} || payload_i)，只存 hex 不存原文）。
      // 测试侧据已知 FIFO 入队序列（fifo-1\r .. fifo-32\r）独立复算同一链比对——证明 raw port
      // 见到的顺序与客户端 FIFO 一致（不乱序、不丢、不重复）。writeCount=32（恰 N，非 33）=
      // ledger 幂等去重 + 第 33 条 zero-wire（未到服务端）；outbox-strip toHaveCount(0) = 全结算。
      // DOM 逐条断言受 TimelineView 虚拟化限制（N=32 多数滚出视口），故以 raw port 服务端权威
      // 计数 + 顺序摘要为冻结 oracle（不受前端虚拟化影响），DOM 侧以首个可见 user-message 互证。
      let expectedChain = ''
      for (let i = 1; i <= N; i++) {
        expectedChain = createHash('sha256').update(expectedChain + `fifo-${i}\r`).digest('hex')
      }
      const ioDrained = await ctl<RawIO>(info, 'GET', `/control/session/${created.sessionId}/raw-io`)
      expect(ioDrained.writeCount, `C2b FIFO drain 后 rawInput 恰为 ${N}（wireFor33rd=0：第 33 条未到服务端）`).toBe(N)
      expect(ioDrained.writeOrderChain, 'C2b FIFO 顺序摘要匹配（raw port 见到 fifo-1..fifo-32 入队顺序）').toBe(expectedChain)
      await expect(page.locator('.user-message').first()).toBeVisible()
      await page.screenshot({ path: 'test-results/m3-int-c2b-fifo-drain-32-settled.png', fullPage: false })
      expect(consoleErrors).toEqual([])
    } finally {
      await cleanup()
      await closeDevice(page)
    }
  })

  test('C6b relay wire-lastSeq：drop S→C N、forward N+1 → 断 → reattach（wire lastSeq=N-1、Seq 各一次、marker 消除）', async ({ browser }) => {
    const { info } = harness
    const consoleErrors: string[] = []
    const created = await ctl<CreatedSession>(info, 'POST', '/control/session', { cliType: 'claudecode' })

    const page = await freshDevicePage(browser)
    consoleErrors.push(...watchConsole(page))
    const attaches = captureAttachFrames(page)
    await page.addInitScript((k: string) => localStorage.setItem(k, '1'), GUIDE_KEY)
    // relay：丢第 3 条 S→C（制造越洞：#1 attached、#2 resize-forbidden、#3 output(seq1)→DROP）。
    const { controller, cleanup } = await installWsRelay(page, '**/ws/v1', {
      dropServerToClientAt: 3,
    })
    try {
      await pairBrowser(page, info, 'C6b·wire-lastSeq 设备')
      await enterWorkspace(page, info, created.sessionId, created.title)
      // 缺口不需控制权（output 是服务端→客户端广播）；不 acquire，避免 control.state 帧推移 output 位置。
      await expect.poll(() => controller.counts.serverToClientForwarded, { timeout: 8_000 }).toBeGreaterThanOrEqual(2)

      // F=0（空流）；注入 seq1（被丢 #3）+ seq2（到达 #4 → 越洞缓冲不投影，触发 recoverable gap）。
      await ctl(info, 'POST', `/control/session/${created.sessionId}/output`, { text: 'wire-one\n' })
      await ctl(info, 'POST', `/control/session/${created.sessionId}/output`, { text: 'wire-two\n' })
      const gap = page.locator('[data-testid=gap-marker]')
      await expect(gap).toBeVisible({ timeout: 8_000 })
      await expect(gap).toHaveAttribute('data-gap-state', 'recoverable')
      await expect(page.locator('.mono-block', { hasText: 'wire-one' })).toHaveCount(0)

      // 断 → reattach：wire attach 的 lastSeq 必须 = F = N-1 = 0（design §3/C6b；首个 attach omit）。
      await controller.disconnectAll(1011)
      await expect.poll(() => attaches.length, { timeout: 8_000 }).toBeGreaterThanOrEqual(2)
      const reattach = attaches[attaches.length - 1]
      expect(reattach.lastSeq, 'reattach wire lastSeq 必须 = settledFrontier = 0').toBe(0)

      // reattach history 补齐：seq1/seq2 各恰一次、marker 消除。
      await expect(gap).toHaveCount(0, { timeout: 5_000 })
      await expect(page.locator('.mono-block', { hasText: 'wire-one' })).toHaveCount(1)
      await expect(page.locator('.mono-block', { hasText: 'wire-two' })).toHaveCount(1)
      await page.screenshot({ path: 'test-results/m3-int-c6b-relay-wire-lastseq-filled.png', fullPage: false })
      expect(consoleErrors).toEqual([])
    } finally {
      await cleanup()
      await closeDevice(page)
    }
  })
})
