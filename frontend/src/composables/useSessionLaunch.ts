/**
 * Session Launch Composable
 *
 * 抽取自 SidebarNormal.vue 与 SessionSettingsView.vue 的共用会话启动逻辑，
 * 消除两处 launchFromSettings / resolveShellPath 的 ~60 行重复实现。
 *
 * 职责：
 *   - canLaunchFromSettings(dashState)：宽松启动前置校验（仅字段非空）
 *   - launchFromSettings(dashState, opts)：三引擎统一启动 + 按 mode 决定是否跳 /terminal
 *   - resolveShellPath(dashState, platformCaps)：按当前引擎解析 Shell 路径
 *
 * 行为契约：
 *   1. 启动成功后按 mode 决定跳转：
 *      - embedded 模式 → router.push('/terminal')（内嵌终端显示该会话）
 *      - external/webui/newWindow 等外部模式 → 不跳转，留在当前页（外部窗口/web 已开，
 *        跳 /terminal 会显示空终端，造成 UX 困惑）
 *   2. 启动失败仅 showError，不跳转
 *   3. OpenCode 必须有 workDir；preset 为空表示用全局 opencode.json 配置，给予友好提示
 *   4. 启动锁（launching）可选：调用方通过 opts.launchingRef 传入响应式 ref 即启用防重入
 *   5. canLaunchFromSettings 是"宽松校验"——仅检查字段非空，不校验 preset 是否在列表中
 *      （SessionSettingsView 的严格 canLaunch computed 是设置页特有逻辑，仍保留在组件内）
 *
 * 使用方式：
 *   const { canLaunchFromSettings, launchFromSettings, resolveShellPath } = useSessionLaunch()
 *
 *   // 调用方需自备：dashState（useDashboardState）、platformCaps、sessionStore、refresh、router、toast
 *   if (canLaunchFromSettings(dashState)) {
 *     await launchFromSettings(dashState, { launchingRef, platformCaps, sessionStore, refresh, router, showSuccess, showError, persistDefaults })
 *   }
 */

import type { Ref } from 'vue'
import type { Router } from 'vue-router'
import * as sessionApi from '../api/session'
import type { useDashboardState } from './useDashboardState'
import type { usePlatformCapabilities } from './usePlatformCapabilities'
import type { useSessionStore } from '../stores/session'

/** 仪表盘 state 类型（从 useDashboardState 推导，避免重复定义） */
type DashState = ReturnType<typeof useDashboardState>['state']

/** platformCaps 句柄类型 */
type PlatformCaps = ReturnType<typeof usePlatformCapabilities>

/** sessionStore 句柄类型 */
type SessionStore = ReturnType<typeof useSessionStore>

/** launchFromSettings 调用参数 */
export interface LaunchOptions {
  /** 可选启动锁 ref。传入则启用防重入（锁占用时直接 return）；不传则不锁 */
  launchingRef?: Ref<boolean>
  /** 平台能力句柄（用于 resolveShellPath） */
  platformCaps: PlatformCaps
  /** 会话 store（用于 setActiveSession） */
  sessionStore: SessionStore
  /** 会话列表刷新函数（来自 useSessionList.refresh） */
  refresh: () => Promise<void> | void
  /** 路由句柄（用于跳 /terminal） */
  router: Router
  /** 持久化默认值函数（来自 useDashboardState.persistDefaults） */
  persistDefaults: () => Promise<void> | void
  /** toast 成功提示 */
  showSuccess: (msg: string) => void
  /** toast 错误提示 */
  showError: (msg: string) => void
}

export function useSessionLaunch() {
  /**
   * 宽松启动前置校验：仅检查字段非空，不校验 preset 是否实际存在于后端列表。
   * 用于侧栏"新建会话"按钮做"直接启动 vs 跳设置页补全"决策。
   *
   * 引擎规则：
   *   - claudecode: provider + preset 均非空
   *   - codex: codexProvider + codexModel 均非空
   *   - opencode: workDir 非空（preset 空表示用全局配置，仍可启动）
   */
  function canLaunchFromSettings(dashState: DashState): boolean {
    if (dashState.engine === 'claudecode') {
      return !!(dashState.provider && dashState.preset)
    }
    if (dashState.engine === 'codex') {
      return !!(dashState.codexProvider && dashState.codexModel)
    }
    if (dashState.engine === 'pi') {
      return !!(dashState.piProvider && dashState.piModel)
    }
    // OpenCode: "使用全局配置"时 preset 为空（openCodePresetKey），仍可启动
    // 只要有工作目录即可启动（provider 可为空，用全局配置）
    return !!dashState.workDir
  }

  /**
   * 按当前引擎解析 Shell 路径。
   *
   * 规则：
   *   - shell === ''         → 直接启动（返回空，后端用默认）
   *   - shell === '__custom__' → 返回 customPath
   *   - 其他                  → 通过 platformCaps.resolveShellPath 解析内置 key
   */
  function resolveShellPath(
    dashState: DashState,
    platformCaps: PlatformCaps,
  ): string {
    const shell = dashState.engine === 'claudecode' ? dashState.claudeShell
      : dashState.engine === 'opencode' ? dashState.openCodeShell
      : dashState.engine === 'pi' ? dashState.piShell
      : dashState.codexShell
    const custom = dashState.engine === 'claudecode' ? dashState.claudeCustomShellPath
      : dashState.engine === 'opencode' ? dashState.openCodeCustomShellPath
      : dashState.engine === 'pi' ? dashState.piCustomShellPath
      : dashState.codexCustomShellPath

    if (shell === '') return ''
    if (shell === '__custom__') return custom
    return platformCaps.resolveShellPath(shell, custom)
  }

  /**
   * 三引擎统一启动会话。
   *
   * 流程：
   *   1. 启动锁检查（如传入 launchingRef 且占用中则 return）
   *   2. OpenCode workDir 必填校验
   *   3. 按 engine 调对应 sessionApi.launch*Session，embedded mode 传 shellPath
   *   4. persistDefaults + refresh + setActiveSession
   *   5. showSuccess（OpenCode 用全局配置时额外提示；外部 mode 额外提示会话已在外部启动）
   *   6. 按 mode 决定跳转：embedded 跳 /terminal；外部模式（external/webui/newWindow 等）留在当前页
   *   7. 异常仅 showError 不跳转；finally 释放锁
   */
  async function launchFromSettings(
    dashState: DashState,
    opts: LaunchOptions,
  ): Promise<void> {
    const {
      launchingRef,
      platformCaps,
      sessionStore,
      refresh,
      router,
      persistDefaults,
      showSuccess,
      showError,
    } = opts

    if (!canLaunchFromSettings(dashState)) return
    // 启动锁防重入（可选）
    if (launchingRef && launchingRef.value) return

    // OpenCode 必须有工作目录
    if (dashState.engine === 'opencode' && !dashState.workDir) {
      showError('请先设置工作目录')
      return
    }

    if (launchingRef) launchingRef.value = true
    try {
      let sessionId = ''
      if (dashState.engine === 'claudecode') {
        sessionId = await sessionApi.launchClaudeSession({
          providerName: dashState.provider,
          presetName: dashState.preset,
          mode: dashState.claudeMode,
          workDir: dashState.workDir,
          useProxy: dashState.useProxy,
          useHeadroom: dashState.useHeadroom,
          shellPath: dashState.claudeMode === 'embedded' ? resolveShellPath(dashState, platformCaps) : '',
        })
      } else if (dashState.engine === 'opencode') {
        // 空预设表示使用全局配置，给予友好提示
        if (!dashState.openCodePresetKey) {
          showSuccess('使用全局 opencode.json 配置启动')
        }
        sessionId = await sessionApi.launchOpenCodeSession({
          providerName: '',
          presetName: dashState.openCodePresetKey || '',
          mode: dashState.openCodeMode,
          workDir: dashState.workDir,
          shellPath: dashState.openCodeMode === 'embedded' ? resolveShellPath(dashState, platformCaps) : '',
        })
      } else if (dashState.engine === 'codex') {
        sessionId = await sessionApi.launchCodexSession({
          modelName: dashState.codexModel,
          providerID: dashState.codexProvider,
          mode: dashState.codexMode,
          workDir: dashState.workDir,
          shellPath: dashState.codexMode === 'embedded' ? resolveShellPath(dashState, platformCaps) : '',
        })
      } else if (dashState.engine === 'pi') {
        sessionId = await sessionApi.launchPiSession({
          modelName: dashState.piModel,
          providerID: dashState.piProvider,
          mode: dashState.piMode,
          workDir: dashState.workDir,
          shellPath: dashState.piMode === 'embedded' ? resolveShellPath(dashState, platformCaps) : '',
        })
      }

      await persistDefaults()
      await refresh()

      sessionStore.setActiveSession(sessionId)

      const engineLabel = dashState.engine === 'claudecode' ? 'ClaudeCode'
        : dashState.engine === 'opencode' ? 'OpenCode'
        : dashState.engine === 'pi' ? 'Pi' : 'Codex'

      // 按当前引擎取对应 mode，决定是否跳 /terminal：
      //   - embedded：内嵌终端显示该会话，跳 /terminal
      //   - external/webui/newWindow 等外部模式：外部窗口或 web 界面已开，
      //     跳 /terminal 会显示空终端，UX 困惑，故留在当前页并给提示
      const launchMode = dashState.engine === 'claudecode' ? dashState.claudeMode
        : dashState.engine === 'opencode' ? dashState.openCodeMode
        : dashState.engine === 'pi' ? dashState.piMode
        : dashState.codexMode

      if (launchMode === 'embedded') {
        showSuccess(`${engineLabel} 会话启动成功`)
        router.push('/terminal')
      } else {
        // 外部模式：外部窗口/web 已开，不跳终端页
        showSuccess(`${engineLabel} 会话已在外部启动`)
      }
    } catch (err) {
      console.error('Launch failed:', err)
      showError('启动失败: ' + err)
    } finally {
      if (launchingRef) launchingRef.value = false
    }
  }

  return {
    canLaunchFromSettings,
    launchFromSettings,
    resolveShellPath,
  }
}
