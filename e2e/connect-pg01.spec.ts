// e2e/connect-pg01.spec.ts — M1-D1 PG-01 连接与配对页（Chromium-only，route mock）
// ---------------------------------------------------------------------------
// 范围：PG-01 组件级浏览器证据。API 一律 Playwright route mock（非生产 mock）：
//   · 四类诊断（网络不可达 / 服务未开启或版本不兼容 / 未配对 / 已授权可进）
//   · 配对成功 201（Set-Cookie 由 mock 下发）与失败错误码（window_expired/unpaired）
//   · 配对窗口倒计时与过期自动回态（深链 expiresAt 真实路径，非测试钩子）
//   · 扫码降级（headless 无摄像头 → 分类文案 + 手动入口）
//   · 320px 回流（mobile-320 project 上无横向溢出）
// 真服务器 E2E（真 Go 宿主、真 Cookie、真窗口生命周期）归 D2，不在本 spec 伪证。
// 契约体字段对齐 mobile/src/lib/contract（M0-03 冻结）；本文件构造的是测试夹具，
// 不 import 前端实现（route mock 在浏览器外）。
// ---------------------------------------------------------------------------

import { expect, test, type Page, type Route } from '@playwright/test'

const BASE = '**/api/remote/v1'

const HOST_SUMMARY = {
  apiVersion: 'v1',
  serverVersion: '1.0.5-mock',
  cliAvailability: [
    { cliType: 'claudecode', available: true },
    { cliType: 'opencode', available: true },
    { cliType: 'codex', available: true },
    { cliType: 'pi', available: true },
  ],
}

function apiErrorBody(code: string, layer: string, message: string, actionHint: string, details?: unknown) {
  return { requestId: `req-${code}`, code, layer, message, actionHint, ...(details ? { details } : {}) }
}

function fulfillJson(route: Route, status: number, body: unknown, headers: Record<string, string> = {}) {
  return route.fulfill({
    status,
    contentType: 'application/json',
    headers,
    body: JSON.stringify(body),
  })
}

/** 收集 console error；每个用例结束断言为零（证据要求：console 无 error）。
 *  豁免：Chromium 对 4xx/5xx 响应自动打出的网络资源日志
 *  （"Failed to load resource: the server responded with a status of …"）——
 *  那是 route mock 有意构造的错误响应，不是应用 console.error。 */
function watchConsole(page: Page): string[] {
  const errors: string[] = []
  page.on('console', (msg) => {
    if (msg.type() !== 'error') return
    if (msg.text().startsWith('Failed to load resource')) return
    errors.push(msg.text())
  })
  page.on('pageerror', (err) => errors.push(String(err)))
  return errors
}

/** 未配对态：host/summary → 401 auth.unpaired（契约错误体）。 */
async function mockUnpaired(page: Page) {
  await page.route(`${BASE}/host/summary`, (route) =>
    fulfillJson(route, 401, apiErrorBody('auth.unpaired', 'auth', 'device not paired', 're-pair')),
  )
}

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => {
    const doc = document.documentElement
    return doc.scrollWidth - doc.clientWidth
  })
  expect(overflow).toBeLessThanOrEqual(0)
}

test.describe('M1-D1 PG-01 连接与配对页', () => {
  test('诊断分类：未配对 → 引导配对（风险条、扫码/手动入口、状态芯片三通道）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await mockUnpaired(page)
    await page.goto('/')

    await expect(page.locator('.diagnosis-title')).toHaveText('这台设备还没有配对')
    // 风险明示（NFR-18）：无"不再提示"。
    await expect(page.locator('.risk-banner')).toBeVisible()
    await expect(page.locator('.risk-banner')).toContainText('局域网明文 HTTP')
    // 主/次操作入口。
    await expect(page.getByRole('button', { name: '扫码配对' })).toBeVisible()
    await expect(page.getByRole('button', { name: '手动输入地址与配对码' })).toBeVisible()
    // 连接层/授权层芯片：文字通道（图标+颜色为辅）。
    await expect(page.locator('.status-chips')).toContainText('连接：正常')
    await expect(page.locator('.status-chips')).toContainText('授权：未配对')
    // 触控目标 ≥44px（主按钮）。
    const box = await page.getByRole('button', { name: '扫码配对' }).boundingBox()
    expect(box?.height).toBeGreaterThanOrEqual(44)

    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: 'test-results/pg01-unpaired.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('诊断分类：网络不可达 → 分类文案 + 重试动作（禁笼统失败 AC-23）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await page.route(`${BASE}/host/summary`, (route) => route.abort('connectionrefused'))
    await page.goto('/')

    await expect(page.locator('.diagnosis-title')).toHaveText('网络不可达')
    await expect(page.locator('.diagnosis-card')).toContainText('同一局域网')
    await expect(page.locator('.status-chips')).toContainText('连接：异常')
    await page.screenshot({ path: 'test-results/pg01-net-unreachable.png', fullPage: true })

    // 可执行动作：重试诊断（route 切换为未配对 → 分类迁移可见）。
    await mockUnpaired(page)
    await page.getByRole('button', { name: '重试诊断' }).click()
    await expect(page.locator('.diagnosis-title')).toHaveText('这台设备还没有配对')
    await page.screenshot({ path: 'test-results/pg01-net-unreachable-recovered.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('诊断分类：版本不兼容（bad_request + details）→ 服务未开启或版本不兼容', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await page.route(`${BASE}/host/summary`, (route) =>
      fulfillJson(
        route,
        400,
        apiErrorBody('bad_request', 'connection', 'unsupported api version', 'upgrade-client', {
          reason: 'unsupported_api_version',
          supportedApiVersions: ['v1'],
        }),
      ),
    )
    await page.goto('/')

    await expect(page.locator('.diagnosis-title')).toHaveText('远程服务未开启或版本不兼容')
    await expect(page.locator('.diagnosis-card')).toContainText('设置 › 远程访问')
    await page.screenshot({ path: 'test-results/pg01-service-down.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('诊断分类：已授权 → 进入会话大厅（PG-02，M2-B 本体）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await page.route(`${BASE}/host/summary`, (route) => fulfillJson(route, 200, HOST_SUMMARY))
    // PG-02 大厅会拉取真实会话列表（route mock，空列表为 []，design §5.3）。
    await page.route(`${BASE}/sessions`, (route) => fulfillJson(route, 200, []))
    await page.goto('/')

    await expect(page.locator('.diagnosis-title')).toHaveText('已授权，可以进入')
    await expect(page.locator('.status-chips')).toContainText('授权：已配对')
    await page.screenshot({ path: 'test-results/pg01-authorized.png', fullPage: true })

    await page.getByRole('button', { name: '进入会话大厅' }).click()
    await expect(page).toHaveURL(/#\/lobby$/)
    await expect(page.locator('.lobby-title')).toHaveText('会话大厅')
    // PG-02 本体：宿主投影行 + 空态（图标+说明），不再是 M2 占位卡。
    await expect(page.locator('.lobby-host-line')).toContainText('1.0.5-mock')
    await expect(page.locator('.empty-state')).toContainText('还没有会话')
    await page.screenshot({ path: 'test-results/pg01-lobby-pg02.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('手动路径配对成功 201 → 写投影 → 大厅占位（Cookie 由 mock Set-Cookie 下发）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    let pairCalls = 0
    await page.route(`${BASE}/pairing/complete`, async (route) => {
      pairCalls += 1
      const body = route.request().postDataJSON() as { code?: string; deviceName?: string }
      expect(body.code).toBe('PAIR-123')
      expect(body.deviceName).toBe('我的 Android 设备')
      await fulfillJson(
        route,
        201,
        {
          device: { id: 'dev-mock-1', name: '我的 Android 设备', pairedAt: '2026-08-02T08:00:00Z' },
          host: HOST_SUMMARY,
        },
        { 'Set-Cookie': 'amagi_remote_device=mock-opaque; Path=/; HttpOnly; SameSite=Strict' },
      )
    })
    await page.route(`${BASE}/host/summary`, (route) => {
      // 配对前 401 未配对；配对后（Cookie 生效投影）200。
      if (pairCalls === 0) {
        return fulfillJson(route, 401, apiErrorBody('auth.unpaired', 'auth', 'device not paired', 're-pair'))
      }
      return fulfillJson(route, 200, HOST_SUMMARY)
    })
    await page.route(`${BASE}/sessions`, (route) => fulfillJson(route, 200, []))

    await page.goto('/')
    await expect(page.locator('.diagnosis-title')).toHaveText('这台设备还没有配对')

    await page.getByRole('button', { name: '手动输入地址与配对码' }).click()
    await expect(page.locator('#pair-code')).toBeVisible()
    // 地址输入等宽字体通道（契约 §4 PG-01：地址输入等宽）。
    const fontFamily = await page.locator('#pair-address').evaluate((el) => getComputedStyle(el).fontFamily)
    expect(fontFamily.toLowerCase()).toMatch(/mono/)

    await page.locator('#pair-code').fill('PAIR-123')
    // deviceName 已按 UA 预填；显式确认值。
    await page.locator('#pair-device-name').fill('我的 Android 设备')
    await page.screenshot({ path: 'test-results/pg01-manual-form.png', fullPage: true })

    await page.getByRole('button', { name: '完成配对' }).click()
    await expect(page).toHaveURL(/#\/lobby$/)
    // PG-02 大厅：设备投影与宿主版本呈现于头部（非密投影）。
    await expect(page.locator('.lobby-host-line')).toContainText('我的 Android 设备')
    await expect(page.locator('.lobby-host-line')).toContainText('1.0.5-mock')
    await expect(page.locator('.empty-state')).toBeVisible()
    await page.screenshot({ path: 'test-results/pg01-pair-success-lobby.png', fullPage: true })

    // 配对材料不留地址栏历史（replace 进大厅，hash 无 code）。
    expect(page.url()).not.toContain('code=')
    expect(consoleErrors).toEqual([])
  })

  test('配对失败：auth.window_expired → E-02 窗口关闭回态，引导回桌面', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await mockUnpaired(page)
    await page.route(`${BASE}/pairing/complete`, (route) =>
      fulfillJson(route, 401, apiErrorBody('auth.window_expired', 'auth', 'pairing window closed', 're-pair')),
    )

    await page.goto('/')
    await page.getByRole('button', { name: '手动输入地址与配对码' }).click()
    await page.locator('#pair-code').fill('STALE-CODE')
    await page.getByRole('button', { name: '完成配对' }).click()

    await expect(page.locator('.window-closed-title')).toHaveText('配对窗口已关闭')
    await expect(page.locator('.window-closed')).toContainText('重新发起配对')
    // 配对材料已清。
    await expect(page.locator('#pair-code')).toHaveValue('')
    await page.screenshot({ path: 'test-results/pg01-window-expired.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('配对失败：auth.unpaired → 配对码不正确或已被使用（分类，非笼统失败）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await mockUnpaired(page)
    await page.route(`${BASE}/pairing/complete`, (route) =>
      fulfillJson(route, 401, apiErrorBody('auth.unpaired', 'auth', 'invalid pairing code', 're-pair')),
    )

    await page.goto('/')
    await page.getByRole('button', { name: '手动输入地址与配对码' }).click()
    await page.locator('#pair-code').fill('WRONG-CODE')
    await page.getByRole('button', { name: '完成配对' }).click()

    await expect(page.locator('.pair-error-title')).toHaveText('配对码不正确或已被使用')
    await page.screenshot({ path: 'test-results/pg01-pair-wrong-code.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('配对窗口倒计时：深链 expiresAt 真实路径 → 等宽倒计时 + 过期自动回态', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await mockUnpaired(page)
    // 90s 窗口：断言倒计时芯片与等宽数字；随后短窗口用例验证过期回态。
    const expiresAt = Date.now() + 90_000
    await page.goto(`/#/connect?code=DEEP-LINK-CODE&expiresAt=${expiresAt}`)

    const chip = page.locator('.countdown-chip')
    await expect(chip).toBeVisible()
    await expect(chip).toContainText('配对窗口剩余')
    // mm:ss 等宽数字（tabular-nums）。
    await expect(page.locator('.countdown-time')).toHaveText(/^0[01]:\d{2}$/)
    const variant = await page.locator('.countdown-time').evaluate((el) => getComputedStyle(el).fontVariantNumeric)
    expect(variant).toContain('tabular-nums')
    // 深链配对码已进入确认表单（诚实呈现，不自动提交）。
    await expect(page.locator('#pair-code')).toHaveValue('DEEP-LINK-CODE')
    await page.screenshot({ path: 'test-results/pg01-countdown.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('配对窗口倒计时：过期自动回态（3s 窗口 → E-02 面板 + 材料清除）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await mockUnpaired(page)
    const expiresAt = Date.now() + 3_000
    await page.goto(`/#/connect?code=SHORT-LIVED&expiresAt=${expiresAt}`)

    await expect(page.locator('.countdown-chip')).toBeVisible()
    // 等待过期（3s 窗口 + tick 余量）。
    await expect(page.locator('.window-closed-title')).toHaveText('配对窗口已关闭', { timeout: 10_000 })
    await expect(page.locator('.countdown-chip')).toHaveCount(0)
    await expect(page.locator('#pair-code')).toHaveValue('')
    await page.screenshot({ path: 'test-results/pg01-countdown-expired.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('扫码降级：headless 无摄像头 → 分类文案 + 手动入口（E-11 家族）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await mockUnpaired(page)
    await page.goto('/')

    await page.getByRole('button', { name: '扫码配对' }).click()
    // headless Chromium 无摄像头/无 fake media：启动失败分类呈现，降级手动路径可达。
    const failure = page.locator('.qr-failure')
    await expect(failure).toBeVisible({ timeout: 15_000 })
    await expect(failure).toContainText(/手动输入/)
    await page.screenshot({ path: 'test-results/pg01-scan-fallback.png', fullPage: true })
    await page.getByRole('button', { name: '取消扫码' }).click()
    await page.getByRole('button', { name: '手动输入地址与配对码' }).click()
    await expect(page.locator('#pair-code')).toBeVisible()
    // 相机启动失败由 html5-qrcode 以 console.error 记录属库内行为；断言无 pageerror，
    // console error 允许最多 1 条来自媒体设备探测。
    expect(consoleErrors.length).toBeLessThanOrEqual(1)
  })

  test('E-03/E-04 踢回：revoked 横幅 → 重新配对引导（清态呈现）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await page.route(`${BASE}/host/summary`, (route) =>
      fulfillJson(route, 401, apiErrorBody('auth.revoked', 'auth', 'device revoked', 're-pair')),
    )
    await page.goto('/#/?reason=revoked')

    await expect(page.locator('.kick-banner')).toContainText('授权已被桌面端撤销')
    await expect(page.locator('.diagnosis-title')).toHaveText('这台设备还没有配对')
    // Major-07：撤销必须落 E-03，绝不可误报为 service-down。
    await expect(page.locator('.diagnosis-title')).not.toHaveText('远程服务未开启或版本不兼容')
    await expect(page.locator('.diagnosis-card')).not.toContainText('服务未开启')
    await expect(page.locator('.status-chips')).toContainText('授权：已撤销')
    await page.screenshot({ path: 'test-results/pg01-kick-revoked.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  // Major-07：E-04 凭据失效场景为 route-mock 证据（如实声明）。
  // 真服务器注入 expired Cookie 态需要 internal/remote 提供凭据回溯 API
  // （超出本次前端修复范围，不动 internal/）；真实后端的错误码语义已由
  // routes_v1.go authExpired 分支（401 auth.window_expired + 清 Cookie）固定，
  // 此处 mock 的即是该真实响应形态。
  test('E-04 踢回：401 auth.window_expired（凭据过期）→ 失效横幅，不落 service-down', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    // 本机持有"曾配对"投影（授权事实由服务端判，投影仅作恢复入口）。
    await page.addInitScript(() => {
      localStorage.setItem(
        'remote.device.projection',
        JSON.stringify({ id: 'dev-exp-1', name: '过期凭据设备', pairedAt: '2026-07-01T08:00:00Z' }),
      )
    })
    await page.route(`${BASE}/host/summary`, (route) =>
      fulfillJson(route, 401, apiErrorBody('auth.window_expired', 'auth', 'device credential expired', 're-pair')),
    )
    await page.goto('/')

    // E-04：失效横幅 + 重新配对引导 + 授权芯片"已失效"，绝不出现 service-down。
    await expect(page.locator('.kick-banner')).toContainText('配对凭据已失效')
    await expect(page.locator('.diagnosis-title')).toHaveText('这台设备还没有配对')
    await expect(page.locator('.diagnosis-title')).not.toHaveText('远程服务未开启或版本不兼容')
    await expect(page.locator('.diagnosis-card')).not.toContainText('服务未开启')
    await expect(page.locator('.status-chips')).toContainText('授权：已失效')
    // E-04 保留"曾配对"恢复入口（投影不被清除，PR-10）。
    await expect(page.locator('.recovery-entry')).toContainText('过期凭据设备')
    await page.screenshot({ path: 'test-results/pg01-kick-expired.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('E-04 踢回：401 auth.unpaired + 本机凭据投影（凭据被清/畸形）→ 失效而非 service-down', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await page.addInitScript(() => {
      localStorage.setItem(
        'remote.device.projection',
        JSON.stringify({ id: 'dev-exp-2', name: '凭据被清设备', pairedAt: '2026-07-01T08:00:00Z' }),
      )
    })
    await page.route(`${BASE}/host/summary`, (route) =>
      fulfillJson(route, 401, apiErrorBody('auth.unpaired', 'auth', 'device not paired', 're-pair')),
    )
    await page.goto('/')

    await expect(page.locator('.kick-banner')).toContainText('配对凭据已失效')
    await expect(page.locator('.diagnosis-title')).not.toHaveText('远程服务未开启或版本不兼容')
    await expect(page.locator('.status-chips')).toContainText('授权：已失效')
    await page.screenshot({ path: 'test-results/pg01-kick-expired-unpaired-code.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('无凭据设备的 401 auth.unpaired 保持普通未配对分类（不升级 E-04）', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await mockUnpaired(page)
    await page.goto('/')

    await expect(page.locator('.diagnosis-title')).toHaveText('这台设备还没有配对')
    await expect(page.locator('.kick-banner')).toHaveCount(0)
    await expect(page.locator('.status-chips')).toContainText('授权：未配对')
    expect(consoleErrors).toEqual([])
  })

  test('320px 回流：手动表单与诊断卡无横向溢出', async ({ page }) => {
    const consoleErrors = watchConsole(page)
    await mockUnpaired(page)
    await page.goto('/')
    await page.getByRole('button', { name: '手动输入地址与配对码' }).click()
    await expect(page.locator('#pair-code')).toBeVisible()
    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: 'test-results/pg01-manual-320-flow.png', fullPage: true })
    expect(consoleErrors).toEqual([])
  })
})
