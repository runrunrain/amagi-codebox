// e2e/smoke.spec.ts — M0-05 smoke（Chromium-only，无后端）
// ---------------------------------------------------------------------------
// 目标（P7 M0-05 退出标准）：一个冒烟 E2E（打开页面断言标题）通过。
// 前置（wukong preflight §2.3/§2.4）：默认路由 ConnectPage 在"无 query 参数"时
//   onMounted→bootstrapFromLocation() 提前返回、不发任何 API 请求；故标题/静态壳
//   在纯 Vite dev、无 Go 远控后端下稳定渲染。受后端影响的是连接成功态/会话/WS，
//   不在本 smoke 断言范围。
//
// 视觉基线说明（任务背景）：M0-04 VT 令牌（浅色）已落地，但最终 cream/coral 迁移
//   尚未完成；本 baseline 只代表 M0 稳定 legacy shell，不冒充最终视觉。后续视觉迁移
//   里程碑（M2-C/D）落地后须重新生成基线。
//
// 用例数：3（标题/静态壳+基线截图、token 显隐交互、offline 断开/恢复）。
// ---------------------------------------------------------------------------

import { expect, test } from '@playwright/test'
import { setOffline } from './helpers/network'

test.describe('M0-05 smoke — legacy shell', () => {
  test('标题与静态壳渲染，无后端、无 query', async ({ page, baseURL }) => {
    // 无 query → bootstrapFromLocation 早退不发请求；不点 Connect。
    await page.goto('/')
    await expect(page).toHaveTitle('Amagi CodeBox Mobile')
    await expect(page.locator('.app-title')).toHaveText('Amagi CodeBox Mobile')
    await expect(page.locator('.app-subtitle')).toContainText('Remote terminal controller')
    await expect(page.locator('.connect-btn')).toBeVisible()

    // 基线截图：禁动画、隐藏光标，避免 caret/动画造成像素抖动；
    // 不放宽 maxDiffPixelRatio/threshold（默认），不掩盖大变化。
    await expect(page).toHaveScreenshot('connect-shell.png', {
      animations: 'disabled',
      caret: 'hide',
    })

    // baseURL 真实可证（smoke 与 webServer 同一 Vite 实例）。
    expect(baseURL).toBeTruthy()
  })

  test('token 显隐交互不触发后端', async ({ page }) => {
    await page.goto('/')
    const input = page.locator('input.form-input--with-toggle')
    await expect(input).toBeVisible()
    // 默认遮罩（password）。
    await expect(input).toHaveAttribute('type', 'password')
    // 切换为明文：纯前端 toggle，不调用任何 API。
    await page.locator('.token-toggle-btn').click()
    await expect(input).toHaveAttribute('type', 'text')
    // 再切回遮罩，验证可逆。
    await page.locator('.token-toggle-btn').click()
    await expect(input).toHaveAttribute('type', 'password')
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
    expect(await fetchOk(), '在线应能取到真实 Vite 资源').toBeTruthy()

    // 离线：CDP emulate offline（断 transport）；真实资源应取不到（不返回假数据）。
    const restore = await setOffline(context, page, true)
    // 给网络栈一小段时间生效。
    await page.waitForTimeout(150)
    expect(await fetchOk(), '离线后真实资源应失败').toBeFalsy()

    // 恢复：幂等 cleanup 还原在线；真实资源重新可取（证明恢复，非缓存假象）。
    await restore()
    await page.waitForTimeout(150)
    expect(await fetchOk(), '恢复在线后真实资源应再次可取').toBeTruthy()
  })
})
