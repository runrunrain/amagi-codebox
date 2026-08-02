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
    // 请求"在线"：直接应用空 profile（恢复）。
    return applyNetProfile(context, page, { offline: false })
  }
  return applyNetProfile(context, page, { offline: true })
}
