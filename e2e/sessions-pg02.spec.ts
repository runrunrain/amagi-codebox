// e2e/sessions-pg02.spec.ts — M2-B PG-02 会话大厅 + PG-06 危险操作（Chromium-only，route mock）
// ---------------------------------------------------------------------------
// 范围：PG-02 大厅组件级浏览器证据。API 一律 Playwright route mock（测试夹具，
// 非生产 mock）；mock 形状逐字段对齐 M2-A 真实 mapper 输出：
//   · 错误体 = 顶层 ApiError {requestId, code, layer, message, actionHint, details?}
//     （无 {error:{}} 信封；internal/remote/v1_error_mapper.go + m2a_error_mapper_test.go 断言）；
//   · list = 顶层数组（空为 []）；create/stop/restart 成功 = SessionDetail；
//   · remove = 204 无 body；acquire/release = ControlSnapshot 对象；
//   · 启动失败四类（AC-25，design §8.3 冻结文案）；control.busy 409 / control.forbidden 403。
// 真实服务器 E2E 属 M2-INT（harness 装配 session adapter 后），本 spec 不伪证。
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

interface SessionFixture {
  id: string
  title: string
  cliType: string
  state: string
  control: { state: string; deviceName?: string }
  lastActivityAt: string
}

function makeSession(over: Partial<SessionFixture>): SessionFixture {
  return {
    id: 'sess-1',
    title: 'Claude Code · amagi-codebox',
    cliType: 'claudecode',
    state: 'running',
    control: { state: 'none' },
    lastActivityAt: new Date().toISOString(),
    ...over,
  }
}

/** SessionDetail = summary + detail 字段（design §5.3：earliestSeq/latestSeq 即使 0 也必填）。 */
function makeDetail(summary: SessionFixture) {
  return {
    ...summary,
    workdir: '/users/dev/project',
    startedAt: new Date(Date.now() - 3_600_000).toISOString(),
    earliestSeq: 0,
    latestSeq: 0,
  }
}

function apiErrorBody(code: string, layer: string, message: string, actionHint: string, details?: unknown) {
  return { requestId: `req-${code}`, code, layer, message, actionHint, ...(details ? { details } : {}) }
}

function fulfillJson(route: Route, status: number, body: unknown) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

function watchConsole(page: Page): string[] {
  const errors: string[] = []
  page.on('console', (msg) => {
    if (msg.type() !== 'error') return
    // 豁免 Chromium 对 route-mock 4xx/5xx 自动打出的资源日志（非应用 console.error）。
    if (msg.text().startsWith('Failed to load resource')) return
    errors.push(msg.text())
  })
  page.on('pageerror', (err) => errors.push(String(err)))
  return errors
}

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
}

/** 默认 mock：host/summary 已授权 + 空会话列表。返回会话列表可变引用供用例改写。 */
async function mockAuthorized(page: Page, sessions: SessionFixture[] = []) {
  await page.route(`${BASE}/host/summary`, (route) => fulfillJson(route, 200, HOST_SUMMARY))
  await page.route(`${BASE}/sessions`, (route) => {
    if (route.request().method() === 'GET') return fulfillJson(route, 200, sessions)
    return route.fallback()
  })
}

/** 经 PG-01 真实路径进入大厅（诊断 → 已授权 → 点击进入）。 */
async function enterLobby(page: Page) {
  await page.goto('/')
  await expect(page.locator('.diagnosis-title')).toHaveText('已授权，可以进入')
  await page.getByRole('button', { name: '进入会话大厅' }).click()
  await expect(page).toHaveURL(/#\/lobby$/)
  await expect(page.locator('.lobby-title')).toHaveText('会话大厅')
}

function shotName(testInfo: { project: { name: string } }, name: string) {
  return `test-results/pg02-${testInfo.project.name}-${name}.png`
}

test.describe('M2-B PG-02 会话大厅', () => {
  test('大厅加载：五层芯片 + 会话卡片字段（名称/CLI/状态/控制者/最后活动）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    const sessions = [
      makeSession({ id: 'sess-1', title: 'Claude Code · amagi-codebox', cliType: 'claudecode', state: 'running', control: { state: 'you' } }),
      makeSession({ id: 'sess-2', title: 'Codex · demo', cliType: 'codex', state: 'stopped', control: { state: 'desktop' } }),
    ]
    await mockAuthorized(page, sessions)
    await enterLobby(page)

    // StatusBar 五层（连接/授权/会话/控制/历史），图标+文字+颜色三通道。
    const chips = page.locator('.status-bar .status-chips .chip')
    await expect(chips).toHaveCount(5)
    await expect(page.locator('.status-bar')).toContainText('连接：正常')
    await expect(page.locator('.status-bar')).toContainText('授权：已配对')
    await expect(page.locator('.status-bar')).toContainText('会话：2 个（1 运行中）')
    await expect(page.locator('.status-bar')).toContainText('控制：你控制 1 个')
    await expect(page.locator('.status-bar')).toContainText('历史：附着后可见')

    // 卡片字段：名称 / CLI 类型 / 运行状态 / 控制者投影 / 最后活动。
    const card1 = page.locator('.session-card', { hasText: 'amagi-codebox' })
    await expect(card1.locator('.session-title')).toHaveText('Claude Code · amagi-codebox')
    await expect(card1.locator('.cli-badge')).toHaveText('Claude Code')
    await expect(card1.locator('.state-chip')).toContainText('运行中')
    await expect(card1.locator('.control-chip')).toContainText('你正在控制')
    await expect(card1.locator('.activity-time')).toContainText('最后活动')
    const card2 = page.locator('.session-card', { hasText: 'Codex · demo' })
    await expect(card2.locator('.state-chip')).toContainText('已停止')
    await expect(card2.locator('.control-chip')).toContainText('桌面端控制中')

    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: shotName(testInfo, 'list'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('空态：图标 + 说明 + 启动器主操作可用', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await mockAuthorized(page, [])
    await enterLobby(page)

    await expect(page.locator('.empty-state')).toContainText('还没有会话')
    await expect(page.locator('.empty-icon')).toBeVisible()
    await expect(page.locator('.cli-launcher')).toBeVisible()
    await expect(page.locator('.status-bar')).toContainText('会话：无会话')
    await page.screenshot({ path: shotName(testInfo, 'empty'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('错误态：503 service.down → 分类 + 重试恢复', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await mockAuthorized(page, [])
    // 覆盖列表为 503（M2-A §8.1：宿主 unhealthy → service.down/check-desktop）。
    let failList = true
    await page.route(`${BASE}/sessions`, (route) => {
      if (route.request().method() !== 'GET') return route.fallback()
      if (failList) {
        return fulfillJson(route, 503, apiErrorBody('service.down', 'connection', 'service unavailable', 'check-desktop'))
      }
      return fulfillJson(route, 200, [makeSession({ id: 'sess-r', title: '恢复后的会话', control: { state: 'you' } })])
    })
    await enterLobby(page)

    await expect(page.locator('.lobby-error-card')).toContainText('宿主会话服务不可用')
    await expect(page.locator('.lobby-error-card')).toContainText('service.down')
    // 异常自动展开：StatusBar 呈现明细列表。
    await expect(page.locator('.status-bar .layer-details')).toBeVisible()
    await expect(page.locator('.status-bar')).toContainText('会话：列表不可用')
    await page.screenshot({ path: shotName(testInfo, 'list-error'), fullPage: true })

    failList = false
    await page.locator('.lobby-error-card').getByRole('button', { name: '重试' }).click()
    await expect(page.locator('.session-card')).toContainText('恢复后的会话')
    await page.screenshot({ path: shotName(testInfo, 'list-recovered'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('四类 CLI 启动：POST {cliType} → 201 → 卡片出现（逐类断言请求体）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    const sessions: SessionFixture[] = []
    await mockAuthorized(page, sessions)
    const launches: string[] = []
    await page.route(`${BASE}/sessions`, (route) => {
      const req = route.request()
      if (req.method() === 'POST') {
        const body = req.postDataJSON() as { cliType: string }
        launches.push(body.cliType)
        const created = makeSession({ id: `sess-${body.cliType}`, title: `${body.cliType} 会话`, cliType: body.cliType, control: { state: 'none' } })
        sessions.push(created)
        // design §5.2 index 4：201 SessionDetail，初始 control=none（不自动占权）。
        return fulfillJson(route, 201, makeDetail(created))
      }
      return fulfillJson(route, 200, sessions)
    })
    await enterLobby(page)

    for (const label of ['Claude Code', 'OpenCode', 'Codex', 'Pi']) {
      await page.locator('.cli-card', { hasText: label }).click()
      await expect(page.locator('.session-card', { hasText: `${label === 'Claude Code' ? 'claudecode' : label.toLowerCase()} 会话` })).toBeVisible()
    }
    expect(launches).toEqual(['claudecode', 'opencode', 'codex', 'pi'])
    await page.screenshot({ path: shotName(testInfo, 'launch-four-cli'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('启动失败四分类（AC-25）：workdir 400 / capability / context / effect 422', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await mockAuthorized(page, [])
    // design §8.3 冻结文案（逐字段对齐 M2-A v1ErrorMapper 输出）。
    const failures: Array<{ status: number; body: unknown; expectTitle: string; expectDetail: string }> = [
      {
        status: 400,
        body: apiErrorBody('bad_request', 'session', 'Working directory is invalid', 'retry'),
        expectTitle: '工作目录无效',
        expectDetail: 'Working directory is invalid',
      },
      {
        status: 422,
        body: apiErrorBody('session.launch_failed', 'session', 'CLI is unavailable on this host', 'check-desktop', { cliType: 'claudecode' }),
        expectTitle: '启动失败',
        expectDetail: 'CLI is unavailable on this host',
      },
      {
        status: 422,
        body: apiErrorBody('session.launch_failed', 'session', 'Host launch configuration is unavailable', 'check-desktop', { cliType: 'claudecode' }),
        expectTitle: '启动失败',
        expectDetail: 'Host launch configuration is unavailable',
      },
      {
        status: 422,
        body: apiErrorBody('session.launch_failed', 'session', 'CLI session could not be started', 'check-desktop', { cliType: 'claudecode' }),
        expectTitle: '启动失败',
        expectDetail: 'CLI session could not be started',
      },
    ]
    let idx = 0
    await page.route(`${BASE}/sessions`, (route) => {
      const req = route.request()
      if (req.method() === 'POST') {
        const f = failures[idx]
        return fulfillJson(route, f.status, f.body)
      }
      return fulfillJson(route, 200, [])
    })
    await enterLobby(page)

    for (let i = 0; i < failures.length; i++) {
      idx = i
      await page.locator('.cli-card', { hasText: 'Claude Code' }).click()
      const panel = page.locator('.launch-error')
      await expect(panel.locator('.launch-error-title')).toHaveText(failures[i].expectTitle)
      await expect(panel.locator('.launch-error-detail')).toHaveText(failures[i].expectDetail)
      // 分类指引存在，且不是笼统失败（带错误码小字）。
      await expect(panel.locator('.launch-error-guidance')).not.toBeEmpty()
      await expect(panel.locator('.launch-error-code')).toContainText(i === 0 ? 'bad_request' : 'session.launch_failed')
      if (i === 0) await page.screenshot({ path: shotName(testInfo, 'launch-fail-workdir'), fullPage: true })
      if (i === 1) await page.screenshot({ path: shotName(testInfo, 'launch-fail-capability'), fullPage: true })
      await panel.getByRole('button', { name: '关闭错误提示' }).click()
      await expect(panel).toHaveCount(0)
    }
    expect(consoleErrors).toEqual([])
  })

  test('CLI available=false → 卡片禁用 + 原因（不伪装可点）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await page.route(`${BASE}/host/summary`, (route) =>
      fulfillJson(route, 200, {
        ...HOST_SUMMARY,
        cliAvailability: [
          { cliType: 'claudecode', available: true },
          { cliType: 'opencode', available: false },
          { cliType: 'codex', available: true },
          { cliType: 'pi', available: false },
        ],
      }),
    )
    await page.route(`${BASE}/sessions`, (route) => fulfillJson(route, 200, []))
    await enterLobby(page)

    const piCard = page.locator('.cli-card', { hasText: 'Pi' })
    await expect(piCard).toBeDisabled()
    await expect(piCard).toContainText('宿主不可用：未安装或未配置')
    await expect(page.locator('.cli-card', { hasText: 'OpenCode' })).toBeDisabled()
    await expect(page.locator('.cli-card', { hasText: 'Codex' })).toBeEnabled()
    await page.screenshot({ path: shotName(testInfo, 'cli-unavailable'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('控制者投影四变体：you/desktop/other+deviceName/none', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await mockAuthorized(page, [
      makeSession({ id: 's-you', title: '你控制的会话', control: { state: 'you' } }),
      makeSession({ id: 's-desktop', title: '桌面控制的会话', control: { state: 'desktop' } }),
      makeSession({ id: 's-other', title: '他人控制的会话', control: { state: 'other', deviceName: '家里的 iPad' } }),
      makeSession({ id: 's-none', title: '空闲的会话', control: { state: 'none' } }),
    ])
    await enterLobby(page)

    await expect(page.locator('.session-card', { hasText: '你控制的会话' }).locator('.control-chip')).toContainText('你正在控制')
    await expect(page.locator('.session-card', { hasText: '桌面控制的会话' }).locator('.control-chip')).toContainText('桌面端控制中')
    await expect(page.locator('.session-card', { hasText: '他人控制的会话' }).locator('.control-chip')).toContainText('由 家里的 iPad 控制')
    await expect(page.locator('.session-card', { hasText: '空闲的会话' }).locator('.control-chip')).toContainText('无人控制')
    await page.screenshot({ path: shotName(testInfo, 'control-variants'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('观察者语义：desktop 控制的会话写操作禁用并说明原因', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await mockAuthorized(page, [makeSession({ id: 's-obs', title: '仅观察会话', control: { state: 'desktop' } })])
    await enterLobby(page)

    await page.getByRole('button', { name: '会话 仅观察会话 的操作菜单' }).click()
    const menu = page.locator('.menu-pop')
    await expect(menu.getByRole('menuitem', { name: '停止会话…' })).toBeDisabled()
    await expect(menu.getByRole('menuitem', { name: '重启会话…' })).toBeDisabled()
    await expect(menu.getByRole('menuitem', { name: '移除会话…' })).toBeDisabled()
    // 占用中不提供获取控制权（诚实说明而非可点失败）。
    await expect(menu).toContainText('控制权被占用，无法获取')
    await expect(menu.locator('.menu-reason')).toContainText('桌面端正在控制，你可观察但无法操作')
    await page.screenshot({ path: shotName(testInfo, 'observer-disabled'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('获取/释放控制权：none → acquire 200 {state:you} → release → {state:none}', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    const session = makeSession({ id: 's-ctl', title: '控制权流转会话', control: { state: 'none' } })
    await mockAuthorized(page, [session])
    const calls: string[] = []
    await page.route(`${BASE}/sessions/s-ctl/control/*`, (route) => {
      const url = route.request().url()
      if (url.endsWith('/acquire')) {
        calls.push('acquire')
        session.control = { state: 'you' }
        return fulfillJson(route, 200, { state: 'you' })
      }
      calls.push('release')
      session.control = { state: 'none' }
      return fulfillJson(route, 200, { state: 'none' })
    })
    await enterLobby(page)

    const card = page.locator('.session-card', { hasText: '控制权流转会话' })
    await expect(card.locator('.control-chip')).toContainText('无人控制')

    await card.getByRole('button', { name: '会话 控制权流转会话 的操作菜单' }).click()
    await page.locator('.menu-pop').getByRole('menuitem', { name: '获取控制权' }).click()
    await expect(card.locator('.control-chip')).toContainText('你正在控制')

    await card.getByRole('button', { name: '会话 控制权流转会话 的操作菜单' }).click()
    await page.locator('.menu-pop').getByRole('menuitem', { name: '释放控制权' }).click()
    await expect(card.locator('.control-chip')).toContainText('无人控制')

    expect(calls).toEqual(['acquire', 'release'])
    await page.screenshot({ path: shotName(testInfo, 'control-flow'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('控制权冲突：acquire → 409 control.busy → 冲突反馈，不静默覆盖', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    const session = makeSession({ id: 's-busy', title: '冲突会话', control: { state: 'none' } })
    await mockAuthorized(page, [session])
    await page.route(`${BASE}/sessions/s-busy/control/acquire`, (route) => {
      // 409 后控制者已变为其他设备：列表刷新应投影最新状态（不静默覆盖）。
      session.control = { state: 'other', deviceName: '另一台手机' }
      return fulfillJson(route, 409, apiErrorBody('control.busy', 'control', 'session control is held by another controller', 'request-control'))
    })
    await enterLobby(page)

    const card = page.locator('.session-card', { hasText: '冲突会话' })
    await card.getByRole('button', { name: '会话 冲突会话 的操作菜单' }).click()
    await page.locator('.menu-pop').getByRole('menuitem', { name: '获取控制权' }).click()

    await expect(page.locator('.conflict-banner')).toContainText('控制权刚被其他设备或桌面端取得')
    // 投影如实刷新为新控制者（不保留“无人控制”假象）。
    await expect(card.locator('.control-chip')).toContainText('由 另一台手机 控制')
    await page.screenshot({ path: shotName(testInfo, 'control-conflict'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('PG-06 stop 确认流：动词按钮/后果/焦点安全默认/Esc 无请求/防连点', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    const session = makeSession({ id: 's-stop', title: '待停止会话', control: { state: 'you' } })
    await mockAuthorized(page, [session])
    let stopCalls = 0
    await page.route(`${BASE}/sessions/s-stop/stop`, async (route) => {
      stopCalls += 1
      const body = route.request().postDataJSON() as { confirm?: boolean }
      expect(body).toEqual({ confirm: true }) // PG-06 协议级 confirm 字段
      session.state = 'stopped'
      await new Promise((r) => setTimeout(r, 300)) // 拉长提交窗口验证防连点
      return fulfillJson(route, 200, makeDetail(session))
    })
    await enterLobby(page)

    const card = page.locator('.session-card', { hasText: '待停止会话' })
    await card.getByRole('button', { name: '会话 待停止会话 的操作菜单' }).click()
    await page.locator('.menu-pop').getByRole('menuitem', { name: '停止会话…' }).click()

    // 对话呈现：动词化按钮 + 后果说明。
    const dialog = page.locator('.confirm-dialog')
    await expect(dialog.locator('.confirm-title')).toHaveText('停止会话「待停止会话」？')
    await expect(dialog.getByRole('button', { name: '停止会话' })).toBeVisible()
    await expect(dialog).toContainText('会话进程将被终止')
    // 安全默认：焦点在取消按钮（非危险动词）。
    await expect(dialog.getByRole('button', { name: '取消' })).toBeFocused()
    // 焦点圈闭：Tab 在按钮间循环（不逃逸到页面）。
    await page.keyboard.press('Tab')
    await expect(dialog.getByRole('button', { name: '停止会话' })).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(dialog.getByRole('button', { name: '取消' })).toBeFocused()
    // Esc 取消：零副作用（无请求发出）。
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    expect(stopCalls).toBe(0)

    // 重新打开并确认；提交中双击动词不再发第二次请求（防连点）。
    await card.getByRole('button', { name: '会话 待停止会话 的操作菜单' }).click()
    await page.locator('.menu-pop').getByRole('menuitem', { name: '停止会话…' }).click()
    const verbBtn = dialog.getByRole('button', { name: '停止会话' })
    await verbBtn.click()
    // 提交中：动词与取消均禁用（防连点的 UI 层证据）；完成后请求数恰为 1。
    await expect(dialog.getByRole('button', { name: '执行中…' })).toBeDisabled()
    await expect(dialog.getByRole('button', { name: '取消' })).toBeDisabled()
    await expect(dialog).toHaveCount(0)
    expect(stopCalls).toBe(1)

    // 回执：动词化结果 + 记录占位说明（journal 查询面 M2-C 预留）。
    await expect(page.locator('.receipt')).toContainText('已停止会话「待停止会话」')
    await expect(page.locator('.receipt')).toContainText('远程查询入口将随 M2-C 交付')
    await expect(card.locator('.state-chip')).toContainText('已停止')
    await page.screenshot({ path: shotName(testInfo, 'stop-flow'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('PG-06 restart 确认流：同 ID 重启 → 回执', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    const session = makeSession({ id: 's-restart', title: '待重启会话', control: { state: 'you' }, state: 'stopped' })
    await mockAuthorized(page, [session])
    let restartCalls = 0
    await page.route(`${BASE}/sessions/s-restart/restart`, (route) => {
      restartCalls += 1
      const body = route.request().postDataJSON() as { confirm?: boolean }
      expect(body).toEqual({ confirm: true })
      session.state = 'running'
      return fulfillJson(route, 200, makeDetail(session))
    })
    await enterLobby(page)

    const card = page.locator('.session-card', { hasText: '待重启会话' })
    await card.getByRole('button', { name: '会话 待重启会话 的操作菜单' }).click()
    await page.locator('.menu-pop').getByRole('menuitem', { name: '重启会话…' }).click()

    const dialog = page.locator('.confirm-dialog')
    await expect(dialog.locator('.confirm-title')).toHaveText('重启会话「待重启会话」？')
    await expect(dialog).toContainText('会话标识保持不变')
    await dialog.getByRole('button', { name: '重启会话' }).click()

    await expect(page.locator('.receipt')).toContainText('已重启会话「待重启会话」')
    await expect(card.locator('.state-chip')).toContainText('运行中')
    expect(restartCalls).toBe(1)
    await page.screenshot({ path: shotName(testInfo, 'restart-flow'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('PG-06 remove 确认流：不可逆说明 → DELETE {confirm:true} → 204 → 卡片消失', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    const sessions = [makeSession({ id: 's-remove', title: '待移除会话', control: { state: 'you' } })]
    await mockAuthorized(page, sessions)
    let removeCalls = 0
    await page.route(`${BASE}/sessions/s-remove`, (route) => {
      if (route.request().method() !== 'DELETE') return route.fallback()
      removeCalls += 1
      const body = route.request().postDataJSON() as { confirm?: boolean }
      expect(body).toEqual({ confirm: true })
      sessions.splice(0, sessions.length)
      // M2-A：DELETE 成功 204 无 body。
      return route.fulfill({ status: 204 })
    })
    await enterLobby(page)

    const card = page.locator('.session-card', { hasText: '待移除会话' })
    await card.getByRole('button', { name: '会话 待移除会话 的操作菜单' }).click()
    await page.locator('.menu-pop').getByRole('menuitem', { name: '移除会话…' }).click()

    const dialog = page.locator('.confirm-dialog')
    await expect(dialog.locator('.confirm-title')).toHaveText('移除会话「待移除会话」？')
    await expect(dialog.locator('.irreversible-note')).toContainText('此操作不可撤销')
    await page.screenshot({ path: shotName(testInfo, 'remove-confirm'), fullPage: true })
    await dialog.getByRole('button', { name: '移除会话' }).click()

    expect(removeCalls).toBe(1)
    await expect(page.locator('.receipt')).toContainText('已移除会话「待移除会话」')
    await expect(page.locator('.session-card')).toHaveCount(0)
    await expect(page.locator('.empty-state')).toContainText('还没有会话')
    await page.screenshot({ path: shotName(testInfo, 'remove-done'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('授权失效：list → 401 auth.revoked → 清态踢回 PG-01（E-03）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await page.route(`${BASE}/host/summary`, (route) => fulfillJson(route, 200, HOST_SUMMARY))
    await page.route(`${BASE}/sessions`, (route) =>
      fulfillJson(route, 401, apiErrorBody('auth.revoked', 'auth', 'device revoked', 're-pair')),
    )
    await page.goto('/')
    await expect(page.locator('.diagnosis-title')).toHaveText('已授权，可以进入')
    await page.getByRole('button', { name: '进入会话大厅' }).click()

    // 大厅首个列表请求即遇真 401 revoked：清态踢回 PG-01 撤销横幅，绝不停留大厅。
    //（大厅 URL 仅瞬时经过，故直接断言最终落点。）
    await expect(page).toHaveURL(/#\/connect/)
    await expect(page.locator('.kick-banner')).toContainText('授权已被桌面端撤销')
    await page.screenshot({ path: shotName(testInfo, 'auth-revoked-kick'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })

  test('导航：点卡片 → PG-03 工作区（M2-C 本体；WS route-mock）', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await mockAuthorized(page, [makeSession({ id: 's-nav', title: '导航会话', control: { state: 'you' } })])
    // PG-03 本体已交付（M2-C）：detail + WS attach 走 route mock（形状对齐契约）。
    await page.route(`${BASE}/sessions/s-nav`, (route) => {
      if (route.request().method() !== 'GET') return route.fallback()
      return fulfillJson(route, 200, {
        ...makeDetail(makeSession({ id: 's-nav', title: '导航会话', control: { state: 'you' } })),
      })
    })
    await page.routeWebSocket('**/ws/v1', (ws) => {
      ws.onMessage((msg) => {
        const frame = JSON.parse(String(msg)) as { type?: string }
        if (frame.type === 'attach') {
          ws.send(
            JSON.stringify({
              type: 'session.attached',
              requestId: 'req-nav',
              apiVersion: 'v1',
              sessionId: 's-nav',
              history: [],
              earliestSeq: 0,
              latestSeq: 0,
              snapshot: {
                connection: { state: 'connected' },
                auth: { state: 'authorized' },
                session: { state: 'running' },
                control: { state: 'you' },
                history: { state: 'continuous' },
              },
            }),
          )
        }
      })
    })
    await enterLobby(page)

    await page.locator('.session-card', { hasText: '导航会话' }).click()
    await expect(page).toHaveURL(/#\/workspace\/s-nav$/)
    await expect(page.locator('.workspace-title')).toHaveText('导航会话')
    await expect(page.locator('.status-bar')).toContainText('连接：已连接')
    await page.screenshot({ path: shotName(testInfo, 'workspace-navigation'), fullPage: true })

    // 返回大厅。
    await page.getByRole('button', { name: '返回会话大厅' }).click()
    await expect(page).toHaveURL(/#\/lobby$/)
    expect(consoleErrors).toEqual([])
  })

  test('320px 回流：大厅与卡片无横向溢出', async ({ page }, testInfo) => {
    const consoleErrors = watchConsole(page)
    await mockAuthorized(page, [
      makeSession({ id: 's-long', title: '一个标题相当长的会话名称用于验证窄屏回流表现', control: { state: 'other', deviceName: '名字很长的其他设备' } }),
    ])
    await enterLobby(page)
    await expectNoHorizontalOverflow(page)
    await page.screenshot({ path: shotName(testInfo, 'reflow'), fullPage: true })
    expect(consoleErrors).toEqual([])
  })
})
