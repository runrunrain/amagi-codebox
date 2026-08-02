// e2e/network.spec.ts — M0-05 首审修复持久回归（Major-01 route 隔离 / Major-02 延迟语义）
// ---------------------------------------------------------------------------
// 目标：为首审 2 个 Major 修复补最小、持久、真实 Chromium 回归。
//   · Major-01：cleanup 不得移除调用方既有 **/* route（fix = page.unroute(pattern, exactHandler)）。
//   · Major-02：latency 均值只施加一次（jitter 激活时 CDP latency=0、route 接管 mean±jitter）。
//
// 设计原则（防"镜像假绿"）：
//   · 全部断言走公开 applyNetProfile + 真实 Chromium + 真实 Vite HTTP 行为，不复制 helper 算法、
//     不 grep 源码、不 mock server。
//   · 计时用 delta(baseline) 比较 + 恢复关系，避免脆弱的毫秒精确等值（见各 test 注释的阈值依据）。
//
// 视口说明：本 spec 为 helper 语义测试，与视口无关；config 中 mobile-360/mobile-320 经
//   testIgnore 排除，仅 desktop 运行一次（非 runtime skip / no-op）。
//
// 环境边界（如实，不掩盖）：
//   · latency-only 依赖 CDP 对 loopback origin 生效；Chromium 145/rev1208 本机生效（probe 实测 ~255ms）；
//     其他版本若豁免 loopback，latency-only 会如实失败（helper 边界 #5）。
//   · combined/jitter-only 经 seeded route 施加，不依赖 CDP loopback（稳定可复现）。
//   · 并发请求调度顺序非 URL 级确定；本 spec 每个请求串行 await，规避并发噪声。
// ---------------------------------------------------------------------------

import { expect, test, type Page } from '@playwright/test'
import { applyNetProfile } from './helpers/network'

// 阈值依据（统一说明，各 test 引用）：
//   LATENCY_MS = 250（首审复现值）。route 施加一次 → 墙钟 ≈ 250ms + 开销 + 调度噪声。
//   · 下界 LOWER=200：远高于"未施加延迟"（~baseline 2-10ms），留 50ms 余量；确保延迟真的施加。
//   · 上界 UPPER=400：远低于旧实现 ~511ms（2×250），留 111ms 余量击穿"均值被施加两次"；
//     同时给新实现 ~250ms 留 ~150ms 调度噪声余量。
//   · 恢复 RESTORE=100：cleanup 后应回到 baseline（~5ms），100 留足噪声余量，捕获"残留延迟"。
//   · 抖动上限 JITTER_MAX=80：jitter-only 不加均值（应远低于 250），80 覆盖 jitter=10 + 噪声。
const LOWER = 200
const UPPER = 400
const RESTORE = 100
const JITTER_MAX = 80
// 固定 seed：确定性、可复现（同 seed 同 rng 序列）；与 helper 默认不同以证明不依赖默认值。
const SEED = 42

/** 计时抓取真实 Vite 资源（经 page 网络栈，受 CDP + route 影响）。 */
async function fetchRealResource(
  page: Page,
  url: string,
): Promise<{ ok: boolean; status: number; body: string; elapsedMs: number }> {
  return page.evaluate(async (u: string) => {
    const t0 = performance.now()
    try {
      const r = await fetch(u, { cache: 'no-store' })
      const body = await r.text()
      return { ok: r.ok, status: r.status, body, elapsedMs: Math.round(performance.now() - t0) }
    } catch (e) {
      return { ok: false, status: 0, body: `ERR:${String(e)}`, elapsedMs: Math.round(performance.now() - t0) }
    }
  }, url)
}

test.describe('M0-05 network helper 回归（首审 Major-01 / Major-02）', () => {
  test('Major-01: cleanup 保留调用方 **/* route（route 隔离）', async ({ page, context, baseURL }) => {
    expect(baseURL, 'webServer baseURL 可用').toBeTruthy()
    await page.goto(baseURL!)

    // 调用方隔离探针 URL（合成探针，非业务数据）；真实 Vite 资源用于证"延迟已施加"。
    const probeUrl = `${baseURL}/m01-isolation-probe`
    const realUrl = `${baseURL}/src/main.ts`

    let callerHits = 0
    // 调用方 route 用 **/*（与 helper 同 pattern，是 unroute('**/*') 误删的必要复现条件）。
    // 探针 URL → fulfill CALLER（可观察 route 存在/移除）；其余 → fallback 让 jitter/网络继续。
    // route 执行顺序（Playwright 1.58.2 types.d.ts:20905）：多匹配 route 按【注册逆序】执行，
    // 故 helper 后注册的 jitter 先跑（施加延迟→fallback），再到先注册的 caller。探针请求因此
    // 同时承载"helper ~250ms 延迟"与"caller fulfill CALLER"——同一 probe 耦合举证（见 during）。
    await page.route('**/*', async (route) => {
      callerHits++
      if (route.request().url().includes('m01-isolation-probe')) {
        await route.fulfill({ status: 200, contentType: 'text/plain', body: 'CALLER' })
      } else {
        await route.fallback()
      }
    })

    // before：调用方 route 处理探针；真实资源取 baseline 计时。
    const beforeProbe = await fetchRealResource(page, probeUrl)
    const beforeReal = await fetchRealResource(page, realUrl)
    expect(beforeProbe.body, 'before: 调用方 route 处理探针').toBe('CALLER')
    expect(beforeReal.ok, 'before: 真实 Vite 资源可取').toBe(true)

    // 应用 profile {latencyMs:250, jitterMs:1, 固定 seed}（首审复现组合）。
    const cleanup = await applyNetProfile(context, page, { latencyMs: 250, jitterMs: 1, seed: SEED })

    // during：jitter（逆序先跑）对探针施加 ~250ms 延迟后 fallback 到 caller（fulfill CALLER）。
    // 同一 probe 同时证明：① caller route 仍处理响应（CALLER）；② helper 延迟已施加约 250ms；
    // ③ 均值未双施（delta < UPPER，旧实现 ~511）。真实 Vite URL 作真实 HTTP 补充证。
    const duringProbe = await fetchRealResource(page, probeUrl)
    const duringReal = await fetchRealResource(page, realUrl)
    expect(duringProbe.body, 'during: helper fallback 后 caller 仍处理探针').toBe('CALLER')
    const duringProbeDelta = duringProbe.elapsedMs - beforeProbe.elapsedMs
    expect(
      duringProbeDelta,
      `during: 同一 probe 承载 helper ~250ms 延迟（delta=${duringProbeDelta}ms，下界 ${LOWER}）`,
    ).toBeGreaterThan(LOWER)
    expect(
      duringProbeDelta,
      `during: 同一 probe 均值未双施（delta=${duringProbeDelta}ms，上界 ${UPPER}，旧实现 ~511）`,
    ).toBeLessThan(UPPER)
    const duringDelta = duringReal.elapsedMs - beforeReal.elapsedMs
    expect(
      duringDelta,
      `during: 真实 Vite 资源 jitter 生效（delta=${duringDelta}ms，下界 ${LOWER}）`,
    ).toBeGreaterThan(LOWER)

    // cleanup 幂等：连续两次不抛。
    await cleanup()
    await expect(cleanup(), 'cleanup 第二次（幂等）不抛').resolves.toBeUndefined()

    // after：调用方 route 仍存在（fix = 精确移除 jitterHandler；旧 broad unroute 会移除它）。
    const afterProbe = await fetchRealResource(page, probeUrl)
    expect(afterProbe.body, 'after: 调用方 route 经 cleanup 后仍存在').toBe('CALLER')
    // 调用方 route 在 before/during/after 三相均活跃。
    expect(callerHits, '调用方 route 三相均被命中').toBeGreaterThanOrEqual(3)
  })

  test('Major-02 latency-only: latencyMs=250 由 CDP 施加一次，cleanup 恢复', async ({ page, context, baseURL }) => {
    await page.goto(baseURL!)
    const url = `${baseURL}/src/main.ts`

    const baseline = await fetchRealResource(page, url)
    expect(baseline.ok).toBe(true)

    // 无 jitter → route 不挂载，latency 均值由 CDP emulate 施加一次。
    const cleanup = await applyNetProfile(context, page, { latencyMs: 250 })
    const during = await fetchRealResource(page, url)
    await cleanup()
    const restored = await fetchRealResource(page, url)

    const duringDelta = during.elapsedMs - baseline.elapsedMs
    expect(duringDelta, `latency 施加一次 ~250ms（delta=${duringDelta}，下界 ${LOWER}）`).toBeGreaterThan(LOWER)
    expect(duringDelta, `latency 未被加倍（delta=${duringDelta}，上界 ${UPPER}）`).toBeLessThan(UPPER)
    expect(
      restored.elapsedMs - baseline.elapsedMs,
      `cleanup 后恢复 baseline（delta=${restored.elapsedMs - baseline.elapsedMs}，上限 ${RESTORE}）`,
    ).toBeLessThan(RESTORE)
  })

  test('Major-02 jitter-only: jitterMs=10 + latencyMs=0 不加均值（负采样 clamp ≥0）', async ({ page, context, baseURL }) => {
    await page.goto(baseURL!)
    const url = `${baseURL}/src/main.ts`

    const baseline = await fetchRealResource(page, url)
    expect(baseline.ok).toBe(true)

    // jitter 激活 → CDP latency=0；route mean=0，delay=max(0, (rng*2-1)*jitter) ∈ [0,jitter)。
    const cleanup = await applyNetProfile(context, page, { latencyMs: 0, jitterMs: 10, seed: SEED })
    // 多次请求遍历 seeded rng 序列（含负采样），证"latency=0 不加均值"（见上 clamp 不可计时观测说明）。
    const deltas: number[] = []
    for (let i = 0; i < 4; i++) {
      const r = await fetchRealResource(page, url)
      deltas.push(r.elapsedMs - baseline.elapsedMs)
    }
    await cleanup()
    const restored = await fetchRealResource(page, url)

    for (const d of deltas) {
      // 仅断言"未加均值"（delta 远低于 250）。clamp-to-0 不可经计时单独观测：route 延迟恒
      // ≥0（setTimeout 负值即 0），但 baseline 首次 transform 开销 + 调度噪声可使 delta(baseline)
      // 略负（实测 -2ms）；"latency=0 是否仍被加均值"才是 Major-02 可观测、可复现的语义。
      expect(d, `jitter-only 未加 250ms 均值（delta=${d}，上限 ${JITTER_MAX}）`).toBeLessThan(JITTER_MAX)
    }
    expect(
      restored.elapsedMs - baseline.elapsedMs,
      `cleanup 后恢复 baseline（delta=${restored.elapsedMs - baseline.elapsedMs}，上限 ${RESTORE}）`,
    ).toBeLessThan(RESTORE)
  })

  test('Major-02 combined: latencyMs=250 + jitterMs=1 均值只施加一次（非 ~511ms）', async ({ page, context, baseURL }) => {
    await page.goto(baseURL!)
    const url = `${baseURL}/src/main.ts`

    const baseline = await fetchRealResource(page, url)
    expect(baseline.ok).toBe(true)

    // 首审复现组合：旧实现 CDP 250 + route 250 ≈ 511ms；fix 让 CDP latency=0，route 施加 250±1 一次。
    const cleanup = await applyNetProfile(context, page, { latencyMs: 250, jitterMs: 1, seed: SEED })
    const during = await fetchRealResource(page, url)
    await cleanup()
    const restored = await fetchRealResource(page, url)

    const duringDelta = during.elapsedMs - baseline.elapsedMs
    // 稳定击穿旧 ~511ms：上界 400 留 111ms 余量；下界 200 远高于"未施加"，留调度噪声余量。
    expect(duringDelta, `combined 施加 ~250ms 一次（delta=${duringDelta}，下界 ${LOWER}）`).toBeGreaterThan(LOWER)
    expect(
      duringDelta,
      `combined 均值未被施加两次（delta=${duringDelta}，上界 ${UPPER}，旧实现 ~511）`,
    ).toBeLessThan(UPPER)
    expect(
      restored.elapsedMs - baseline.elapsedMs,
      `cleanup 后恢复 baseline（delta=${restored.elapsedMs - baseline.elapsedMs}，上限 ${RESTORE}）`,
    ).toBeLessThan(RESTORE)
  })
})
