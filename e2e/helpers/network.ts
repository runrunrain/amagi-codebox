// e2e/helpers/network.ts — 网络整形辅助（Chromium CDP + seeded route jitter）
// ---------------------------------------------------------------------------
// 用途（P6 §11.1/§11.2、wukong preflight §5）：为 E2E 提供确定性的
//   · 离线（offline）
//   · 吞吐 / 延迟（throughput / latency，Chromium CDP Network.emulateNetworkConditions）
//   · 确定性抖动（seeded deterministic route jitter，mulberry32）
// 并返回幂等 cleanup（重复调用安全、恢复后不影响后续用例）。
//
// 延迟分层（latency 均值只施加一次，Major-02 修复）：
//   · 无抖动（jitterMs 未设/0）→ latency 均值由 CDP emulate 施加，route 不挂载；
//   · 有抖动（jitterMs>0）→ latency 均值 ± jitter 由 seeded route 施加，CDP latency 置 0，
//     避免均值被 CDP 与 route 重复叠加。CDP 始终负责 offline 与 throughput（与 route 不冲突）。
//
// route 隔离（Major-01 修复）：helper 以具名稳定 handler 注册 jitter route，cleanup 只
//   以 page.unroute(pattern, exactHandler) 精确移除自身 handler，不用 broad unroute；
//   handler 延迟后调用 route.fallback()，让调用方既有 route（鉴权/观测/资源守卫）继续匹配，
//   不被静默跳过；无后续 handler 时 fallback 等价于 continue（请求落到网络）。
//   请求并发顺序仍由浏览器调度决定，本 helper 不夸大为 URL 级并发确定性（见下方边界披露）。
//
// 边界披露（不伪称覆盖，详见注释）：
//   1. WebSocket 已建帧：CDP emulate 影响 transport 层；对"已建立 WS 的逐帧延迟/丢包"
//      覆盖不确定。WS 的重连 / seq / gap / backfill 是应用层（M3-B）行为，
//      本 helper 只造网络条件，不验证应用恢复 —— 后者在 M3 整形 E2E 用真实宿主验证。
//   2. ServiceWorker：mobile 当前无 SW（grep serviceWorker/workbox/registerSW 零命中），
//      故 setOffline(true) 即真离线（无 SW 缓存绕过）。若未来注册 SW（Capacitor 壳/离线
//      缓存），需重评（先断 SW 再断网，或启动期禁用 SW）。
//   3. 真机网络（iOS Safari / Android Chrome）：不在 helper —— P6 §11.2 用系统网络
//      conditioner（macOS Network Link Conditioner / Android emulator）手测，归 M4-C。
//   4. 浏览器覆盖：CDP Network.emulateNetworkConditions 为 Chromium-first；本 config
//      仅 Chromium（C-009），WebKit/Firefox 不配置，故 helper 不做跨浏览器降级。
//   5. loopback（localhost）offline：部分 Chromium 版本对 loopback 网络条件豁免；
//      本 helper 以 CDP emulate 为准，smoke 用例据实断言真实 fetch 行为（失败/恢复），
//      不用假数据掩盖。
// ---------------------------------------------------------------------------

import { randomBytes, createHmac } from 'node:crypto'
import type { BrowserContext, Page, Route } from '@playwright/test'

export interface NetProfile {
  /** 完全离线（断 transport，CDP） */
  offline?: boolean
  /** 单向额外延迟均值（ms）。
   *  无抖动时由 CDP emulate 施加；有抖动时作为 seeded route 的均值（± jitter），
   *  且此时 CDP latency 置 0（均值只施加一次，不重复）。 */
  latencyMs?: number
  /** 确定性抖动幅度（ms，seeded route 层）。
   *  >0 激活 route：route 施加 latencyMs ± [-jitter,+jitter] 延迟（负值 clamp 为 0），
   *  并接管 latency 均值（CDP latency 置 0）。0/未设 = 不加抖动，latency 仅由 CDP 施加。 */
  jitterMs?: number
  /** 下载吞吐（Kbps，0/未设 = 不限） */
  downloadKbps?: number
  /** 上行吞吐（Kbps，0/未设 = 不限） */
  uploadKbps?: number
  /** seeded RNG 种子（确定性，防 flaky）；默认固定 0xC0FFEE */
  seed?: number
}

/** mulberry32 — 确定性 PRNG，同 seed 同序列（防 flaky 随机）。 */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0
  return function () {
    a |= 0
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

const DEFAULT_SEED = 0xc0ffee

/** CDP 吞吐换算：Kbps → bytes/s（emulateNetworkConditions 以 bytes/s 计）。 */
function kbpsToBytesPerSec(kbps: number | undefined): number {
  // 未设或 0 表示不限（CDP 传 -1）。
  if (!kbps || kbps <= 0) return -1
  return Math.round((kbps * 1024) / 8)
}

interface ActiveHandle {
  client: import('@playwright/test').CDPSession
  routes: Set<Route>
  cleanup: () => Promise<void>
}

const active = new WeakMap<BrowserContext, ActiveHandle>()

/**
 * 应用一个网络整形 profile（Chromium CDP throughput/latency/offline + seeded route 抖动）。
 *
 * 返回幂等 cleanup：恢复为在线/无限速/零延迟并移除 route、detach CDP session。
 * cleanup 可安全重复调用（再调用为 no-op）。
 *
 * 同一 context 重复调用会先回收上一个 handle 再应用新 profile（幂等语义）。
 */
export async function applyNetProfile(
  context: BrowserContext,
  page: Page,
  profile: NetProfile = {},
): Promise<() => Promise<void>> {
  // 先回收既有 handle（幂等：同 context 不叠加多个 emulate/route）。
  const prev = active.get(context)
  if (prev) await prev.cleanup()

  // 延迟均值只施加一次（Major-02 修复）：
  //   · 无抖动（jitter 未激活）→ latency 均值由 CDP emulate 施加，route 不挂载；
  //   · 有抖动（jitter 激活）→ latency 均值 ± jitter 由 seeded route 施加，CDP latency 置 0，
  //     避免均值被 CDP 与 route 重复叠加。
  // CDP 始终负责 offline 与 throughput（不与 route 冲突）。
  const jitterActive = !!(profile.jitterMs && profile.jitterMs > 0)
  const client = await context.newCDPSession(page)
  await client.send('Network.enable')
  await client.send('Network.emulateNetworkConditions', {
    offline: profile.offline ?? false,
    latency: jitterActive ? 0 : profile.latencyMs ?? 0,
    downloadThroughput: kbpsToBytesPerSec(profile.downloadKbps),
    uploadThroughput: kbpsToBytesPerSec(profile.uploadKbps),
  })

  // 确定性 route 抖动：仅当 jitter 激活时挂载（避免无抖动场景的 route 开销）。
  const routes = new Set<Route>()
  let removeRoute: (() => Promise<void>) | null = null
  if (jitterActive) {
    const rng = mulberry32(profile.seed ?? DEFAULT_SEED)
    const mean = profile.latencyMs ?? 0
    const jitter = profile.jitterMs as number
    // Major-01 修复：具名稳定 handler，cleanup 以 page.unroute(pattern, exactHandler) 精确移除。
    const jitterHandler = async (route: Route) => {
      routes.add(route)
      // 均匀分布 [-jitter, +jitter] 围绕 mean（确定性，同 seed 同序列）；
      // 负值 clamp 为 0（jitter-only 时 mean=0，rng 偏负 → delay=0）。
      const delay = Math.max(0, Math.round(mean + (rng() * 2 - 1) * jitter))
      await new Promise((r) => setTimeout(r, delay))
      try {
        // fallback：让后续匹配 handler（调用方鉴权/观测/资源守卫等）继续处理；
        // 无后续 handler 时 fallback 等价于 continue（请求落到网络），与 smoke 既有行为一致。
        await route.fallback()
      } finally {
        routes.delete(route)
      }
    }
    await page.route('**/*', jitterHandler)
    removeRoute = async () => {
      // 仅移除本 helper 的 jitterHandler，保留调用方其他 route。
      // 重复调用幂等：unroute 对已移除的 handler 为 no-op。
      await page.unroute('**/*', jitterHandler)
      // 进行中的 route 让其自然完成；不强制 abort（避免污染断言）。
    }
  }

  let cleaned = false
  const cleanup = async () => {
    if (cleaned) return
    cleaned = true
    active.delete(context)
    try {
      if (removeRoute) await removeRoute()
    } catch {
      /* 忽略：route 已移除 */
    }
    try {
      // 恢复为在线/无限速/零延迟（幂等恢复）。
      await client.send('Network.emulateNetworkConditions', {
        offline: false,
        latency: 0,
        downloadThroughput: -1,
        uploadThroughput: -1,
      })
    } catch {
      /* 忽略：session 可能已断 */
    }
    try {
      await client.detach()
    } catch {
      /* 忽略 */
    }
  }

  active.set(context, { client, routes, cleanup })
  return cleanup
}

/**
 * 便捷：纯离线切换（断 transport）。返回 cleanup（恢复在线）。
 * 等价于 applyNetProfile({ offline }) 的特化；单独提供以表达"仅离线"语义。
 */
export async function setOffline(
  context: BrowserContext,
  page: Page,
  offline: boolean,
): Promise<() => Promise<void>> {
  if (!offline) {
    // 请求“在线”：直接应用空 profile（恢复）。
    return applyNetProfile(context, page, { offline: false })
  }
  return applyNetProfile(context, page, { offline: true })
}

// ===========================================================================
// CG-03 / M3-B WS relay 整形（design §8）。
// ---------------------------------------------------------------------------
// 用途：为已建立的 WS 提供应用帧级确定性整形——分方向 seeded delay（mean±jitter）、
// 一次性 drop/close fault。handler 在 socket 创建前注册，调用真实 connectToServer()；
// client→server 与 server→client 各自手工 forward。
//
// 证据分层（design §8）：本 relay 是「relay + 真 harness」层（协议/ledger/stream 真实，
// 故障是应用帧级确定性注入），不是 TCP packet/带宽模型，也不冒充真机 conditioner。
//
// 隐私（design §6/§8；M3-010 + R2-002 修复）：relay 只统计固定 typed 计数 + 派生布尔
// （sameMessageID / distinctRequestIDs），绝不留 raw frame、input data、ID 值或 URL。
// 输入帧在途解析后立即丢弃原始字段——ID 比较只存 **HMAC-SHA-256 摘要**（进程内随机
// 32-byte key + 全 256-bit hex digest，R2-002：FNV-1a 32-bit 无密钥可碰撞，实测 79822
// 次 canonical 形状输入即出现 32-bit 碰撞，削弱 exact oracle；HMAC-SHA-256 256-bit
// 摘要在测试规模下碰撞概率可忽略，且 key 进程内随机、cleanup 销毁 key 与 digest）。
// 不存原始 MessageID/requestId 字符串；cleanup 销毁 key 与全部派生状态。
//
// 边界（design §8）：
//   · 双向 delay 不得与 CDP latency 重复叠加——relay 场景调用方须将 CDP latency 置 0。
//   · Playwright 无 unrouteWebSocket：shaped 用例须独占新 page/context；cleanup 关闭 active
//     pair 后不在该 page 继续作行为证明。
//   · relay 不夸大为真实网络；外部 TCP/系统 conditioner 仍属 M4-C。
// ===========================================================================

export interface WsRelayProfile {
  /** client→server 单向延迟均值（ms）。 */
  clientToServerLatencyMs?: number
  /** server→client 单向延迟均值（ms）。 */
  serverToClientLatencyMs?: number
  /** client→server 抖动幅度（ms，±，seeded）。 */
  clientToServerJitterMs?: number
  /** server→client 抖动幅度（ms，±，seeded）。 */
  serverToClientJitterMs?: number
  /** 一次性 fault：丢第 N 条 client→server 消息（occurrence，1-based）。 */
  dropClientToServerAt?: number
  /** 一次性 fault：丢第 N 条 server→client 消息（occurrence，1-based）。 */
  dropServerToClientAt?: number
  /** 一次性 fault：收到第 N 条 client→server 消息后 close（occurrence，1-based）。 */
  closeOnClientToServerAt?: { occurrence: number; code: number }
  /** 一次性 fault：丢第 N 条 input 类型 client→server 帧（type=input，1-based）。
   *  与 dropClientToServerAt（全类型 occurrence）正交：首个 C→S 帧是 attach，不是 input。 */
  dropClientToServerInputN?: number
  /** 配合 dropClientToServerInputN：丢该 input 后立即以 code 关闭 active pair（C2a「丢 1 并断」）。 */
  closeAfterDropInput?: number
  /** 持续 fault：hold 所有 input 类型 client→server 帧（不转发，直到 cleanup/disconnect）。
   *  C2b 用于 gate 队首：single-flight 下后续 draft 仅 accept 不上 wire，验证 outbox 容量与重连后 FIFO drain。 */
  holdAllClientInputs?: boolean
  /** 持续 fault：defer（排队不转发）所有 resize 类型 client→server 帧，直到 releaseHolds()
   *  flush。C5b 用于「A resize 停 checkpoint」：A 的 resize 在 relay 排队（未到 server），
   *  desktop take 先 commit，releaseHolds 后 A 的 resize 到达 server → checkpoint 1 失败（A
   *  已非 holder）→ 不 commit；最终 rawResize 仅 [desktop-dims]。 */
  holdClientResize?: boolean
  /** seeded RNG 种子（确定性，防 flaky）；默认 0x5EED。 */
  seed?: number
}

export interface WsRelayCounts {
  connections: number
  clientToServerForwarded: number
  serverToClientForwarded: number
  clientToServerDropped: number
  serverToClientDropped: number
  closes: number
  /** 派生布尔（design [R2/M-05]）：canonical input 重试是否同 MessageID（由不可逆摘要比较）。 */
  sameMessageID: boolean
  /** 派生布尔：canonical input 各 attempt 是否 distinct RequestID（由不可逆摘要比较）。 */
  distinctRequestIDs: boolean
  /** typed 计数（design §8 C2a/C3 oracle）：relay 观察到的 C→S input 帧总数（=wire attempts）。 */
  inputFramesObserved: number
  /** typed 计数（design §8 C2a/C3 oracle）：relay 观察到的 S→C input.ack 帧总数（=ackProduced）。 */
  ackFramesObserved: number
  /** typed 计数（design §8 C3 oracle）：被 relay 丢弃的 S→C input.ack 帧数（=ackDropped）。 */
  ackFramesDropped: number
}

export interface WsRelayController {
  counts: WsRelayCounts
  /** 关闭所有 active relay pair（触发客户端重连/检测）。 */
  disconnectAll(code?: number): Promise<void>
  /** 停止后续 pair 的 input hold（使重连后的新连接透明，用于 C2b FIFO drain）。 */
  releaseHolds(): void
  /** 迟后 arm resize defer（holdClientResize）：用于 C5b 在 attach/acquire 初始 resize
   *  commit 之后再 arm，仅 defer 测试触发的 resize（避免误持初始尺寸上报）。 */
  armHoldClientResize(): void
}

interface ActivePair {
  clientSide: import('@playwright/test').WebSocketRoute
  serverSide: import('@playwright/test').WebSocketRoute
}

/**
 * 安装一个 WS relay：拦截 pattern 匹配的 WS，连接真实服务器，分方向施加 seeded
 * delay（mean±jitter）与一次性 drop/close fault。返回 controller（typed 计数 +
 * disconnectAll）与 cleanup。
 *
 * 注意：Playwright 无 unrouteWebSocket。shaped 用例须独占新 page/context；cleanup 关闭
 * active pair 后不在该 page 继续作行为证明（design §8 边界）。
 */
export async function installWsRelay(
  page: Page,
  pattern: string,
  profile: WsRelayProfile = {},
): Promise<{ controller: WsRelayController; cleanup: () => Promise<void> }> {
  const baseSeed = profile.seed ?? 0x5eed
  // 分方向独立 seeded RNG（design：均值只在一个层施加）。
  const csRng = mulberry32(baseSeed)
  const scRng = mulberry32(baseSeed ^ 0xa5a5a5a5)
  const activePairs: ActivePair[] = []
  let closed = false
  // relay 级全局状态（跨所有连接对）：dropInputN 只生效一次；hold 可被 release。

  // 派生 input 指标（M3-010 + R2-002 隐私修复）：只存 HMAC-SHA-256 hex 摘要（64 字符），
  // 不存原始 MessageID/requestId 字符串。HMAC key 为进程内随机 32 字节，per-relay 独立；
  // 摘要用于比较/去重。cleanup 销毁 key 与 digest 集合。
  // （R2-002：原 FNV-1a 32-bit 无密钥、碰撞概率高，已废弃；HMAC-SHA-256 256-bit 摘要在
  // 测试规模下碰撞概率 ≈ N²/2^257，可忽略，且 key 不出 relay、cleanup 清零。）
  const relayKey: Buffer = randomBytes(32)
  let firstInputIdDigest: string | null = null
  let inputIdSame = true
  const seenRequestIdDigests = new Set<string>()
  let requestIdsDistinct = true
  // input 帧全局计数（跨连接对）：dropClientToServerInputN 只匹配一次。
  let relayInputSeq = 0
  // input hold 开关：holdAllClientInputs 初始开启，releaseHolds 后关闭（使重连透明）。
  let holdInputs = !!profile.holdAllClientInputs
  // resize defer 开关：holdClientResize 初始开启，releaseHolds 后 flush 排队帧并关闭。
  let holdResizes = !!profile.holdClientResize
  // C5b resize defer 队列：存「发送闭包」，releaseHolds 时 flush（发送到原 pair）。
  const deferredResizeSenders: Array<() => void> = []

  const counts: WsRelayCounts = {
    connections: 0,
    clientToServerForwarded: 0,
    serverToClientForwarded: 0,
    clientToServerDropped: 0,
    serverToClientDropped: 0,
    closes: 0,
    sameMessageID: true,
    distinctRequestIDs: true,
    inputFramesObserved: 0,
    ackFramesObserved: 0,
    ackFramesDropped: 0,
  }

  /** HMAC-SHA-256 摘要（R2-002）：以 per-relay 随机 key 对原始 ID 求 256-bit hex 摘要。
   *  用于比较/去重而不保留原文。256-bit 摘要在测试规模下碰撞概率可忽略（≈N²/2^257）；
   *  key 进程内随机、cleanup 清零，不出 relay 边界。不声称「不可逆」——HMAC 在已知 key
   *  下可复算，但 key 不留存、原始 ID 字符串relay 内不留存，故无法从摘要反推原文。 */
  function idDigest(s: string): string {
    return createHmac('sha256', relayKey).update(s).digest('hex')
  }

  function derivedDelay(rng: () => number, mean: number | undefined, jitter: number | undefined): number {
    const m = mean ?? 0
    const j = jitter ?? 0
    if (j <= 0) return m
    return Math.max(0, Math.round(m + (rng() * 2 - 1) * j))
  }

  // 在途解析 input 帧，更新派生布尔（不保留 raw——只存 HMAC-SHA-256 摘要）。返回是否为 input 帧。
  function observeClientFrame(raw: string | Buffer): boolean {
    try {
      const o = JSON.parse(String(raw)) as Record<string, unknown>
      if (o && o.type === 'input' && typeof o.id === 'string' && typeof o.requestId === 'string') {
        const idDig = idDigest(o.id)
        const ridDig = idDigest(o.requestId)
        if (firstInputIdDigest === null) firstInputIdDigest = idDig
        else if (idDig !== firstInputIdDigest) inputIdSame = false
        if (seenRequestIdDigests.has(ridDig)) requestIdsDistinct = false
        else seenRequestIdDigests.add(ridDig)
        counts.sameMessageID = inputIdSame
        counts.distinctRequestIDs = requestIdsDistinct
        counts.inputFramesObserved += 1
        return true
      }
    } catch {
      // 非 JSON / 非 input：不解析（relay 不依赖帧内容做转发）。
    }
    return false
  }

  // 在途解析 S→C 帧，更新 typed ack 计数（不保留 raw——只按 type 计数）。
  // 返回是否为 input.ack 帧（供 drop 路径统计 ackFramesDropped）。
  function observeServerFrame(raw: string | Buffer): boolean {
    try {
      const o = JSON.parse(String(raw)) as Record<string, unknown>
      if (o && o.type === 'input.ack') {
        counts.ackFramesObserved += 1
        return true
      }
    } catch {
      // 非 JSON：不解析。
    }
    return false
  }

  await page.routeWebSocket(pattern, (ws) => {
    counts.connections += 1
    const server = ws.connectToServer()
    activePairs.push({ clientSide: ws, serverSide: server })
    let csSeq = 0
    let scSeq = 0
    let pairClosed = false

    const closePair = async (code?: number) => {
      if (pairClosed) return
      pairClosed = true
      counts.closes += 1
      try {
        await ws.close({ code })
      } catch {
        /* 忽略 */
      }
      try {
        await server.close({ code })
      } catch {
        /* 忽略 */
      }
    }

    // client → server：onMessage 关闭自动转发，手工 forward + delay/fault。
    ws.onMessage((msg) => {
      csSeq += 1
      const isInput = observeClientFrame(msg)
      // input 类型专属 fault（drop/hold/close）：与全类型 occurrence fault 正交。
      // dropInputN/hole 使用 relay 级全局计数，确保只生效一次 / 可被释放。
      if (isInput) {
        relayInputSeq += 1
        if (profile.dropClientToServerInputN === relayInputSeq) {
          counts.clientToServerDropped += 1
          if (profile.closeAfterDropInput) {
            void closePair(profile.closeAfterDropInput)
          }
          return // 丢弃，不转发
        }
        if (holdInputs) {
          counts.clientToServerDropped += 1
          return // 持续 hold（gate 队首），不转发直到 releaseHolds/cleanup
        }
      }
      if (profile.dropClientToServerAt === csSeq) {
        counts.clientToServerDropped += 1
        return // 丢弃，不转发
      }
      if (profile.closeOnClientToServerAt && profile.closeOnClientToServerAt.occurrence === csSeq) {
        counts.clientToServerDropped += 1
        void closePair(profile.closeOnClientToServerAt.code)
        return
      }
      // C5b resize defer：holdClientResize 时排队 resize 帧（不转发），releaseHolds 后 flush。
      // flush 发送到原 pair；pair 已关闭则静默丢失（发送闭包自检 pairClosed）。
      if (holdResizes) {
        let isResize = false
        try {
          const o = JSON.parse(String(msg)) as Record<string, unknown>
          isResize = !!(o && o.type === 'resize')
        } catch {
          /* 非 JSON：不 defer */
        }
        if (isResize) {
          deferredResizeSenders.push(() => {
            if (pairClosed) return // pair 已关闭：排队帧丢失（不计 dropped/forwarded）
            server.send(msg)
            counts.clientToServerForwarded += 1
          })
          return // 排队，暂不转发；flush 时才计 forwarded
        }
      }
      const delay = derivedDelay(csRng, profile.clientToServerLatencyMs, profile.clientToServerJitterMs)
      setTimeout(() => {
        if (pairClosed) return
        server.send(msg)
        counts.clientToServerForwarded += 1
      }, delay)
    })

    // server → client：onMessage 关闭自动转发，手工 forward + delay/fault。
    server.onMessage((msg) => {
      scSeq += 1
      const isAck = observeServerFrame(msg)
      if (profile.dropServerToClientAt === scSeq) {
        counts.serverToClientDropped += 1
        if (isAck) counts.ackFramesDropped += 1
        return
      }
      const delay = derivedDelay(scRng, profile.serverToClientLatencyMs, profile.serverToClientJitterMs)
      setTimeout(() => {
        if (pairClosed) return
        ws.send(msg)
        counts.serverToClientForwarded += 1
      }, delay)
    })

    ws.onClose(() => {
      void closePair()
    })
    server.onClose(() => {
      void closePair()
    })
  })

  const controller: WsRelayController = {
    counts,
    async disconnectAll(code?: number): Promise<void> {
      const pairs = activePairs.splice(0)
      await Promise.all(pairs.map((p) => p.clientSide.close({ code }).catch(() => {})))
    },
    releaseHolds(): void {
      holdInputs = false
      holdResizes = false
      // flush 排队的 resize 帧（C5b）：此时 desktop take 已 commit，A 的 resize 到达
      // server 会被 production gate 拒（createDevicePTYPermit owner-check：A 已非 holder）
      // → 不 commit raw（DenyNotController，不依赖 fake raw ctx 行为）。
      const senders = deferredResizeSenders.splice(0)
      for (const send of senders) send()
    },
    armHoldClientResize(): void {
      // C5b：迟后 arm，使 attach/acquire 初始 resize 已 commit 后才 defer 测试 resize。
      holdResizes = true
    },
  }

  const cleanup = async (): Promise<void> => {
    if (closed) return
    closed = true
    // 无 unrouteWebSocket：只能关闭 active pair。调用方须独占 page/context。
    await controller.disconnectAll()
    // M3-010 + R2-002 隐私：销毁全部派生状态（HMAC key 清零、digest 集合、defer 队列），
    // 不留可重构原文的痕迹。relayKey 清零使遗留 digest 也无法复算原文。
    relayKey.fill(0)
    seenRequestIdDigests.clear()
    firstInputIdDigest = null
    inputIdSame = true
    requestIdsDistinct = true
    deferredResizeSenders.length = 0
  }

  return { controller, cleanup }
}
