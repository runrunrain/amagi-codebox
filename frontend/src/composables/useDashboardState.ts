import { reactive } from 'vue'
import * as settingsApi from '../api/settings'
import { usePlatformCapabilities } from './usePlatformCapabilities'

/**
 * 仪表盘状态——在 app 生命周期内跨路由保持
 * 不用 ref 而用 reactive 对象，整体更紧凑
 * 注意：shell 默认值 '' 会在 initDefaults 中由平台能力 defaultShellKey 覆盖
 */
const state = reactive({
  engine: 'claudecode' as 'claudecode' | 'opencode' | 'codex' | 'pi' | 'omp',
  provider: '',
  preset: '',
  openCodePresetKey: '',
  // Codex 独立选择（OpenAI 兼容 provider）
  codexProvider: '',
  codexModel: '',
  // Pi 独立选择（复用 amagi provider，映射到 Pi 内置 provider）
  piProvider: '',
  piModel: '',
  // R4：Pi 登录选择失效锁（会话页 reconcile 写入，启动链读取禁启）。
  // 瞬态运行时状态，不进 persistDefaults 载荷。
  piLoginStale: false,
  // Omp 独立选择（复用 amagi provider，映射到 Omp 内置 provider）
  ompProvider: '',
  ompModel: '',
  claudeMode: 'embedded',
  openCodeMode: 'embedded',
  codexMode: 'embedded',
  piMode: 'embedded',
  ompMode: 'embedded',
  workDir: '',
  claudeShell: '',
  openCodeShell: '',
  codexShell: '',
  piShell: '',
  ompShell: '',
  claudeCustomShellPath: '',
  openCodeCustomShellPath: '',
  codexCustomShellPath: '',
  piCustomShellPath: '',
  ompCustomShellPath: '',
  initialized: false,
})

export function useDashboardState() {
  const platformCaps = usePlatformCapabilities()

  /**
   * 从后端 GetDashboardDefaults 初始化默认值（仅一次）。
   * 需在平台能力 ensure() 完成后调用。
   */
  async function initDefaults() {
    if (state.initialized) return
    try {
      const d = await settingsApi.getDashboardDefaults()
      const shellFallback = platformCaps.defaultShellKey.value || ''
      if (d.provider) state.provider = d.provider
      if (d.preset) state.preset = d.preset
      state.openCodePresetKey = d.openCodePresetKey || ''
      state.codexProvider = state.codexProvider || ''
      state.codexModel = state.codexModel || ''
      state.piProvider = state.piProvider || ''
      state.piModel = state.piModel || ''
      state.piLoginStale = false
      state.ompProvider = state.ompProvider || ''
      state.ompModel = state.ompModel || ''
      state.claudeMode = d.claudeMode || d.mode || 'embedded'
      state.openCodeMode = d.openCodeMode || 'embedded'
      state.codexMode = d.codexMode || 'embedded'
      state.piMode = d.piMode || 'embedded'
      state.ompMode = d.ompMode || 'embedded'
      state.claudeShell = d.claudeShell || d.shell || shellFallback
      state.openCodeShell = d.openCodeShell || d.shell || shellFallback
      state.codexShell = d.codexShell || d.shell || shellFallback
      state.piShell = d.piShell || d.shell || shellFallback
      state.ompShell = d.ompShell || d.shell || shellFallback
    } catch (err) {
      console.error('Failed to load dashboard defaults:', err)
    }
    state.initialized = true
  }

  /**
   * 把当前仪表盘选择持久化到后端 SetDashboardDefaults。
   */
  async function persistDefaults() {
    try {
      await settingsApi.setDashboardDefaults({
        provider: state.provider,
        preset: state.preset,
        openCodeProvider: '',
        openCodePreset: '',
        openCodePresetKey: state.openCodePresetKey,
        mode: state.claudeMode,
        shell: state.claudeShell,
        claudeMode: state.claudeMode,
        claudeShell: state.claudeShell,
        openCodeMode: state.openCodeMode,
        openCodeShell: state.openCodeShell,
        codexMode: state.codexMode,
        codexShell: state.codexShell,
        piMode: state.piMode,
        piShell: state.piShell,
        ompMode: state.ompMode,
        ompShell: state.ompShell,
      })
    } catch (err) {
      console.error('Failed to persist dashboard defaults:', err)
    }
  }

  function reset() {
    state.engine = 'claudecode'
    state.provider = ''
    state.preset = ''
    state.openCodePresetKey = ''
    state.codexProvider = ''
    state.codexModel = ''
    state.piProvider = ''
    state.piModel = ''
    state.piLoginStale = false
    state.ompProvider = ''
    state.ompModel = ''
    state.workDir = ''
    state.initialized = false
  }

  return {
    state,
    initDefaults,
    persistDefaults,
    reset,
  }
}
