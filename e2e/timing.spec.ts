// e2e/timing.spec.ts — M0-06 timing skeleton contract report（Chromium-only，desktop）
// ---------------------------------------------------------------------------
// 目标（设计 §9.5）：证明 timing 字段真实进入 Playwright 测试报告（attachment +
// JSON reporter），而非只在测试进程内对象断言。这是骨架 API probe（默认
// performance.now），不是页面 lifecycle performance，更不是真实 AC-01/02 性能结果。
//
// 视口说明：本 spec 为 timing 字段管道测试，与视口无关；config 中 mobile-360/
// mobile-320 经 testIgnore 排除，仅 desktop 运行一次（同 network.spec.ts 原则）。
//
// 边界（如实）：
//   · 只用 public API（createTimingRecorder / mark / report）；不复制状态机算法。
//   · duration 用默认 performance.now 真实读数；只断言 finite/nonnegative/预算值，
//     不强制 within/over，避免调度 flaky；绝不宣称真实 3s/5s 达标。
//   · 全程不调用 console reporter。
// ---------------------------------------------------------------------------

import { Buffer } from 'node:buffer'
import { expect, test } from '@playwright/test'

const ATTACHMENT_NAME = 'timing-skeleton-contract-report-v1'

// 报告顶层 + measurement 允许 key（exact allowlist，供反解析断言复用）。
const TOP_KEYS = ['measurements', 'schemaVersion', 'unit']
const MEASUREMENT_KEYS = ['budgetMs', 'budgetStatus', 'durationMs', 'invalidReason', 'status']

test.describe('M0-06 timing skeleton contract', () => {
  test('two-lane mark/report via default performance.now → attachment', async ({ page }, testInfo) => {
    // 捕获 console，验证全程无 TIMING_REPORT_V1 输出（reportToConsole 未调用）。
    const consoleTexts: string[] = []
    page.on('console', (msg) => consoleTexts.push(msg.text()))

    // 真实 mobile Vite 页面（同 smoke webServer）。
    await page.goto('/')

    // 在 browser realm 动态 import 真实 production module；默认 performance.now。
    const report = await page.evaluate(async () => {
      const mod = await import('/src/lib/timing.ts')
      const recorder = mod.createTimingRecorder()
      recorder.mark('T0')
      recorder.mark('T1')
      recorder.mark('R0')
      recorder.mark('R1')
      // 用 report(callback) 捕获 safe report（reportToConsole 不调用）。
      let captured: unknown = null
      recorder.report((r: unknown) => {
        captured = r
      })
      return captured
    })

    // --- schema envelope ---
    expect(report).toBeTruthy()
    const r = report as Record<string, unknown>
    expect(r.schemaVersion).toBe(1)
    expect(r.unit).toBe('ms')
    expect(Object.keys(r).sort()).toEqual(TOP_KEYS)

    // --- measurements：两 lane keys 精确 ---
    const measurements = r.measurements as Record<string, Record<string, unknown>>
    expect(Object.keys(measurements).sort()).toEqual(['R0_R1', 'T0_T1'])

    for (const key of ['T0_T1', 'R0_R1'] as const) {
      const m = measurements[key]
      expect(Object.keys(m).sort()).toEqual(MEASUREMENT_KEYS)
      expect(m.status).toBe('observed')
      expect(m.invalidReason).toBeNull()
      // duration：真实 performance.now 读数，finite 且非负；不强制 within/over（避免 flaky）。
      expect(typeof m.durationMs).toBe('number')
      const d = m.durationMs as number
      expect(Number.isFinite(d)).toBe(true)
      expect(d).toBeGreaterThanOrEqual(0)
      expect(['within_budget', 'over_budget']).toContain(m.budgetStatus)
    }
    // 预算值精确（固定 3000/5000）。
    expect(measurements.T0_T1.budgetMs).toBe(3000)
    expect(measurements.R0_R1.budgetMs).toBe(5000)

    // --- 全程无 console reporter 输出 ---
    expect(
      consoleTexts.every((t) => !t.startsWith('TIMING_REPORT_V1')),
      'reportToConsole must not be called',
    ).toBe(true)

    // --- attachment 落盘到测试报告（管道证据，非 AC-01/02）---
    const body = Buffer.from(JSON.stringify(report))
    await testInfo.attach(ATTACHMENT_NAME, {
      body,
      contentType: 'application/json',
    })

    // 断言 attachment 已登记。
    const att = testInfo.attachments.find((a) => a.name === ATTACHMENT_NAME)
    expect(att, 'attachment registered').toBeTruthy()
    expect(att!.contentType).toBe('application/json')
    expect(att!.body).toBeTruthy()
  })
})
