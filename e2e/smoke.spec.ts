// e2e/smoke.spec.ts — M0-05 smoke（Chromium-only，无后端）；M1-D1 起适配 PG-01 新页
// ---------------------------------------------------------------------------
// 目标（P7 M0-05 退出标准）：一个冒烟 E2E（打开页面断言标题）通过。
// M1-D1 变更：ConnectPage 已重写为 PG-01（VT 浅色壳、自动诊断、配对流程）。
//   · 默认路由挂载即自动诊断 GET /api/remote/v1/host/summary（真实 fetch，无 mock）；
//     纯 Vite dev 无 Go 远控后端时，该路径由 SPA fallback 返回 HTML → 前端如实分类为
//     「远程服务未开启或版本不兼容」（service.down，见 lib/api.ts 非 JSON 映射）。
//     这是确定性真实行为，不是假数据。
//   · legacy token 显隐用例随旧页移除（新模型无 token 输入，Cookie 为唯一凭据载体）。
//
// 视觉基线说明：baseline 于 M1-D1 重新生成（PG-01 VT 浅色壳），不再代表 M0 legacy
// dark shell；最终视觉仍以 P4/P5 为准，后续里程碑落地后须复核基线。
//
// 用例数：3（标题/静态壳+基线截图、诊断分类真实呈现、offline 断开/恢复）。
// ---------------------------------------------------------------------------

import { expect, test } from '@playwright/test'
import { setOffline } from './helpers/network'

test.describe('M0-05 smoke — PG-01 shell（M1-D1 适配）', () => {
  test('标题与 PG-01 静态壳渲染，无后端时诊断如实分类', async ({ page, baseURL }) => {
    await page.goto('/')
    await expect(page).toHaveTitle('Amagi CodeBox Mobile')
    // PG-01 品牌区与双层状态芯片（VT 浅色壳）。
    await expect(page.locator('.brand-title')).toHaveText('Amagi CodeBox')
    await expect(page.locator('.brand-subtitle')).toHaveText('连接与配对')
    await expect(page.locator('.status-chips')).toBeVisible()
    // 纯 Vite 无远控后端：诊断真实走到 service.down 分类（非笼统失败）。
    await expect(page.locator('.diagnosis-title')).toHaveText('远程服务未开启或版本不兼容')
    await expect(page.getByRole('button', { name: '重试诊断' })).toBeVisible()

    // 基线截图：禁动画、隐藏光标，避免 caret/动画造成像素抖动；
    // 不放宽 maxDiffPixelRatio/threshold（默认），不掩盖大变化。
    await expect(page).toHaveScreenshot('connect-shell.png', {
      animations: 'disabled',
      caret: 'hide',
    })

    // baseURL 真实可证（smoke 与 webServer 同一 Vite 实例）。
    expect(baseURL).toBeTruthy()
  })

  test('诊断重试动作真实发起 fetch（无后端仍如实分类）', async ({ page, baseURL }) => {
    await page.goto('/')
    await expect(page.locator('.diagnosis-title')).toHaveText('远程服务未开启或版本不兼容')

    // 重试 = 真实再发一次 host/summary 请求（可观测的 fetch 证据，非假交互）。
    const response = page.waitForResponse(
      (res) => res.url().includes('/api/remote/v1/host/summary'),
      { timeout: 10_000 },
    )
    await page.getByRole('button', { name: '重试诊断' }).first().click()
    await response
    // Vite SPA fallback 对该路径返回 200 HTML；前端如实分类为 service.down。
    await expect(page.locator('.diagnosis-title')).toHaveText('远程服务未开启或版本不兼容')
    expect(baseURL).toBeTruthy()
  })

  test('offline 断开后恢复（真实 Vite 资源，无假数据）', async ({ page, context, baseURL }) => {
    // 在线：真实 Vite 资源可取（mobile/src/main.ts 是真实模块，非 mock）。
    const fetchOk = async (): Promise<boolean> =>
      page.evaluate(async (url) => {
        try {
          const r = await fetch(`${url}/src/main.ts`, { cache: 'no-store' })
          return r.ok
        } catch {
          return false
        }
      }, baseURL)

    await page.goto('/')
    expect(await fetchOk()).toBe(true)

    // 断网：CDP 真断 transport（mobile 无 SW，无缓存绕过）。
    const cleanup = await setOffline(context, page, true)
    expect(await fetchOk()).toBe(false)

    // 恢复：同一真实资源再次可取。
    await cleanup()
    expect(await fetchOk()).toBe(true)
  })
})
