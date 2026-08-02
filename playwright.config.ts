// playwright.config.ts — M0-05 Playwright E2E 基建（Chromium-only）
// ---------------------------------------------------------------------------
// 冻结依据：P7 C-009 / M0-05；wukong preflight §3；wenqu 浏览器许可 §3 场景 A。
// 边界（C-009）：
//   · 仅 Chromium；不配置 WebKit / Firefox project（未使用即无登记义务）。
//   · video: 'off' → 不触发 FFmpeg 录屏路径（FFmpeg 不作为本轮"实际使用"依赖）。
//   · smoke 走真实 mobile Vite dev（webServer），无 mock server / 假数据。
//   · 浏览器二进制仅 CI/开发机运行、不随 release 分发（场景 A）。
// 不修改：package.json / lock / CI / 业务代码。
// ---------------------------------------------------------------------------

import { defineConfig } from '@playwright/test'

// 隔离端口；CI 每次干净启动，本地可复用。
const PORT = Number(process.env.E2E_PORT ?? 4317)
const isCI = !!process.env.CI

export default defineConfig({
  testDir: './e2e',
  // 网络整形 / 截图基线用例需隔离；CI 单 worker 保证可复现。
  workers: isCI ? 1 : undefined,
  fullyParallel: true,
  forbidOnly: isCI,
  retries: isCI ? 2 : 0,

  // M0-06：本地/CI 既有 reporter 保留，追加 JSON reporter 输出到已忽略的 test-results/。
  // 该 report 是 timing skeleton 管道证据（attachment 反解析），不是产品持久化或外部遥测。
  reporter: isCI
    ? [['github'], ['list'], ['json', { outputFile: 'test-results/playwright-results.json' }]]
    : [['list'], ['json', { outputFile: 'test-results/playwright-results.json' }]],
  outputDir: 'test-results/',

  // 移动 SPA 冷启动留余量；smoke 实测 <2s 起，但 mobile 懒加载 + 首次 vite transform 留 buffer。
  timeout: 30_000,
  expect: { timeout: 5_000 },

  // 截图基线目录：baselines/<platform>/<projectName>/<snapshot>.png
  // platform 取 OS（darwin/linux/win32）以隔离字体渲染差异；projectName 取视口名。
  snapshotPathTemplate: '{testDir}/baselines/{platform}/{projectName}/{arg}{ext}',

  use: {
    baseURL: process.env.E2E_BASE_URL ?? `http://localhost:${PORT}`,
    // 失败可复现：首重试留 trace；失败留截图；video 关（C-009：不引入 FFmpeg 录屏路径）。
    trace: 'on-first-retry',
    video: 'off',
    screenshot: 'only-on-failure',
  },

  // 仅 Chromium 三视口（C-009：Chromium-only；不配置 WebKit/Firefox）。
  // network.spec.ts 为 helper 语义回归，与视口无关；mobile 两视口经 testIgnore 排除
  // （非 runtime skip / no-op），仅 desktop 运行一次，避免三视口重复跑 helper-only spec。
  projects: [
    {
      name: 'mobile-360',
      // network.spec.ts / timing.spec.ts 为 helper/管道语义测试，与视口无关；
      // 经 testIgnore 排除（非 runtime skip），仅 desktop 运行一次（M0-05 / M0-06 原则一致）。
      testIgnore: ['**/network.spec.ts', '**/timing.spec.ts'],
      use: { viewport: { width: 360, height: 800 }, isMobile: true, hasTouch: true },
    },
    {
      name: 'mobile-320',
      // connect-pg01-real 每用例拉起真 harness，开销按设计只在 mobile-360 承担一次。
      testIgnore: ['**/network.spec.ts', '**/timing.spec.ts', '**/connect-pg01-real.spec.ts'],
      use: { viewport: { width: 320, height: 800 }, isMobile: true, hasTouch: true },
    },
    {
      name: 'desktop',
      // 真服务器配对 E2E 属移动 PG-01 场景（M1-D2），desktop 不重复跑。
      testIgnore: ['**/connect-pg01-real.spec.ts'],
      use: { viewport: { width: 1280, height: 720 } },
    },
  ],

  // 真实 mobile Vite dev 宿主；strictPort 防端口漂移导致 baseURL 错。
  // smoke 不需 Go 远控后端（标题/静态壳在纯 Vite 下渲染，preflight §2.4 已证）。
  webServer: {
    command: `npm --prefix mobile run dev -- --port ${PORT} --strictPort`,
    url: `http://localhost:${PORT}`,
    reuseExistingServer: !isCI,
    timeout: 60_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
})
