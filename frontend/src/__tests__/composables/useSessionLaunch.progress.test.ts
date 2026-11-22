import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { EventsOn } from '../../../wailsjs/runtime/runtime'
import * as sessionApi from '../../api/session'
import { useSessionLaunch, LAUNCH_STAGE_TEXT, type LaunchOptions } from '../../composables/useSessionLaunch'
import { useDashboardState } from '../../composables/useDashboardState'

// wailsjs runtime 可 mock（对照 remoteClient.test.ts 模式）：EventsOn 默认返回
// 一个可断言的 dispose fn；api/session 全量 mock 供 launchFromSettings 驱动。
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn(() => vi.fn()) }))
vi.mock('../../api/session', () => ({
  launchClaudeSession: vi.fn(),
  launchOpenCodeSession: vi.fn(),
  launchCodexSession: vi.fn(),
  launchPiSession: vi.fn(),
  launchOmpSession: vi.fn(),
}))

type LaunchStateInput = Parameters<ReturnType<typeof useSessionLaunch>['canLaunchFromSettings']>[0]

/** 构造最小 dashState（opencode 引擎：preset 空仍可启动，链路最短） */
function makeState(overrides: Partial<ReturnType<typeof useDashboardState>['state']> = {}): LaunchStateInput {
  const { state } = useDashboardState()
  Object.assign(state, { engine: 'opencode', workDir: '/tmp/demo', openCodeMode: 'embedded' }, overrides)
  return state as unknown as LaunchStateInput
}

/** 最小 LaunchOptions（platformCaps/sessionStore 等仅覆盖 composable 实际触碰的面） */
function makeOpts(): LaunchOptions {
  return {
    platformCaps: { resolveShellPath: () => '' },
    sessionStore: { setActiveSession: vi.fn() },
    refresh: vi.fn(),
    router: { push: vi.fn() },
    persistDefaults: vi.fn(),
    showSuccess: vi.fn(),
    showError: vi.fn(),
  } as unknown as LaunchOptions
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((r, fail) => { resolve = r; reject = fail })
  return { promise, resolve, reject }
}

/** 取第 n 次 EventsOn 订阅的 (回调, dispose) */
function nthSubscription(n: number): [(payload: unknown) => void, () => void] {
  const call = vi.mocked(EventsOn).mock.calls[n]
  const dispose = vi.mocked(EventsOn).mock.results[n].value as () => void
  return [call[1] as (payload: unknown) => void, dispose]
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('LAUNCH_STAGE_TEXT 进度文案映射', () => {
  it('三阶段枚举映射到既定文案，键集合与后端 stage 枚举对齐', () => {
    expect(LAUNCH_STAGE_TEXT.preflight).toBe('正在唤醒 WSL 发行版…')
    expect(LAUNCH_STAGE_TEXT.supply).toBe('正在准备共享配置…')
    expect(LAUNCH_STAGE_TEXT.pty).toBe('正在启动终端…')
    expect(Object.keys(LAUNCH_STAGE_TEXT).sort()).toEqual(['preflight', 'pty', 'supply'])
  })
})

describe('launchFromSettings 进度订阅生命周期', () => {
  it('launching 期间订阅 launch:progress，阶段事件更新响应式文案；未知阶段忽略；结束后注销', async () => {
    const d = deferred<string>()
    vi.mocked(sessionApi.launchOpenCodeSession).mockReturnValueOnce(d.promise)
    const { launchFromSettings, launchStageText } = useSessionLaunch()
    const launching = ref(false)

    const pending = launchFromSettings(makeState(), { ...makeOpts(), launchingRef: launching })
    // 启动挂起中：已订阅、未注销、文案可被事件驱动
    expect(EventsOn).toHaveBeenCalledWith('launch:progress', expect.any(Function))
    const [onProgress, dispose] = nthSubscription(0)
    expect(dispose).not.toHaveBeenCalled()
    expect(launchStageText.value).toBeNull()

    onProgress({ stage: 'preflight', message: 'WSL 预检开始', appType: 'claudecode' })
    expect(launchStageText.value).toBe('正在唤醒 WSL 发行版…')
    onProgress({ stage: 'supply', message: '共享配置供给完成', appType: 'claudecode' })
    expect(launchStageText.value).toBe('正在准备共享配置…')
    onProgress({ stage: 'pty', message: 'PTY 已启动', appType: 'claudecode' })
    expect(launchStageText.value).toBe('正在启动终端…')
    // 未知阶段 / 空负载：不映射，维持当前文案
    onProgress({ stage: 'unknown-stage', message: '', appType: 'claudecode' })
    expect(launchStageText.value).toBe('正在启动终端…')
    onProgress(null)
    expect(launchStageText.value).toBe('正在启动终端…')

    // 结束挂起的启动：resolve 后 finally 注销订阅并清空文案
    d.resolve('sess-9')
    await pending
    expect(dispose).toHaveBeenCalled()
    expect(launchStageText.value).toBeNull()
    expect(launching.value).toBe(false)
  })

  it('启动成功后注销订阅并清空文案（不泄漏）', async () => {
    vi.mocked(sessionApi.launchOpenCodeSession).mockResolvedValueOnce('sess-1')
    const { launchFromSettings, launchStageText } = useSessionLaunch()

    await launchFromSettings(makeState(), makeOpts())

    expect(EventsOn).toHaveBeenCalledTimes(1)
    const [, dispose] = nthSubscription(0)
    expect(dispose).toHaveBeenCalled()
    expect(launchStageText.value).toBeNull()
  })

  it('启动失败（API reject）后同样注销订阅并清空文案', async () => {
    vi.mocked(sessionApi.launchOpenCodeSession).mockRejectedValueOnce(new Error('boom'))
    const { launchFromSettings, launchStageText } = useSessionLaunch()
    const opts = makeOpts()

    await launchFromSettings(makeState(), opts)

    const [, dispose] = nthSubscription(0)
    expect(dispose).toHaveBeenCalled()
    expect(launchStageText.value).toBeNull()
    expect(opts.showError).toHaveBeenCalled()
  })

  it('EventsOn 不可用（非 Wails 环境）时降级：启动照常完成，文案保持 null', async () => {
    vi.mocked(EventsOn).mockImplementationOnce(() => {
      throw new Error('wails runtime unavailable')
    })
    vi.mocked(sessionApi.launchOpenCodeSession).mockResolvedValueOnce('sess-2')
    const { launchFromSettings, launchStageText } = useSessionLaunch()
    const opts = makeOpts()

    await expect(launchFromSettings(makeState(), opts)).resolves.toBeUndefined()

    expect(opts.showError).not.toHaveBeenCalled()
    expect(launchStageText.value).toBeNull()
  })
})

describe('embedded Pi launch route', () => {
  function setupPi() {
    const { launchFromSettings } = useSessionLaunch()
    const launch = deferred<string>()
    vi.mocked(sessionApi.launchPiSession).mockReturnValueOnce(launch.promise)
    const route = ref({ path: '/', fullPath: '/', query: {} as Record<string, string> })
    const opts = makeOpts()
    Object.assign(opts.sessionStore, { piLaunchMessage: null })
    Object.assign(opts.router, {
      currentRoute: route,
      push: vi.fn(async () => { route.value = { path: '/terminal', fullPath: '/terminal?starting=pi', query: { starting: 'pi' } } }),
      replace: vi.fn(async (path: string) => { route.value = { path, fullPath: path, query: {} } }),
    })
    const state = makeState({ engine: 'pi', piProvider: 'provider', piModel: 'model', piMode: 'embedded' })
    return { launch, route, opts, start: () => launchFromSettings(state, opts) }
  }

  it('shows progress before backend activation and does not wait for preference persistence', async () => {
    const { launch, route, opts, start } = setupPi()
    const save = deferred<void>()
    vi.mocked(opts.persistDefaults).mockReturnValueOnce(save.promise)
    const pending = start()
    expect(route.value.query.starting).toBe('pi')
    expect(opts.sessionStore.piLaunchMessage).toBeTruthy()
    expect(opts.sessionStore.setActiveSession).not.toHaveBeenCalled()
    const [onProgress] = nthSubscription(0)
    onProgress({ appType: 'pi', stage: 'preflight' })
    expect(opts.sessionStore.piLaunchMessage).toBe(LAUNCH_STAGE_TEXT.preflight)

    launch.resolve('real-pi-session')
    await pending
    expect(opts.sessionStore.setActiveSession).toHaveBeenCalledWith('real-pi-session')
    expect(opts.persistDefaults).toHaveBeenCalled()
    expect(route.value.fullPath).toBe('/terminal')
    expect(opts.sessionStore.piLaunchMessage).toBeNull()
    save.resolve()
  })

  it('restores the previous route on failed launch without selecting a session', async () => {
    const { launch, route, opts, start } = setupPi()
    const pending = start()
    launch.reject(new Error('WSL preflight failed'))
    await pending
    expect(opts.sessionStore.setActiveSession).not.toHaveBeenCalled()
    expect(opts.showSuccess).not.toHaveBeenCalled()
    expect(opts.showError).toHaveBeenCalledWith(expect.stringContaining('WSL preflight failed'))
    expect(route.value.fullPath).toBe('/')
    expect(opts.sessionStore.piLaunchMessage).toBeNull()
  })

  it('does not steal selection if the user leaves the progress route', async () => {
    const { launch, route, opts, start } = setupPi()
    const pending = start()
    route.value = { fullPath: '/terminal', path: '/terminal', query: {} }
    launch.resolve('real-pi-session')
    await pending
    expect(opts.sessionStore.setActiveSession).not.toHaveBeenCalled()
    expect(opts.router.replace).not.toHaveBeenCalled()
    expect(opts.sessionStore.piLaunchMessage).toBeNull()
  })
})
