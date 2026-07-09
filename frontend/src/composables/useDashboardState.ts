import { reactive } from 'vue'
import * as settingsApi from '../api/settings'
import { usePlatformCapabilities } from './usePlatformCapabilities'

/**
 * 仪表盘状态——在 app 生命周期内跨路由保持
 * 不用 ref 而用 reactive 对象，整体更紧凑
 * 注意：shell 默认值 '' 会在 initDefaults 中由平台能力 defaultShellKey 覆盖
 */
const state = reactive({
  engine: 'claudecode' as 'claudecode' | 'opencode' | 'codex',
  provider: '',
  preset: '',
  openCodePresetKey: '',
  // Codex 独立选择（OpenAI 兼容 provider）
  codexProvider: '',
  codexModel: '',
  claudeMode: 'embedded',
  openCodeMode: 'embedded',
  codexMode: 'embedded',
  workDir: '',
  useProxy: false,
  useHeadroom: false,
  claudeShell: '',
  openCodeShell: '',
  codexShell: '',
  claudeCustomShellPath: '',
  openCodeCustomShellPath: '',
  codexCustomShellPath: '',
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
      state.claudeMode = d.claudeMode || d.mode || 'embedded'
      state.openCodeMode = d.openCodeMode || 'embedded'
      state.codexMode = d.codexMode || 'embedded'
      state.claudeShell = d.claudeShell || d.shell || shellFallback
      state.openCodeShell = d.openCodeShell || d.shell || shellFallback
      state.codexShell = d.codexShell || d.shell || shellFallback
      state.useProxy = d.useProxy || false
      state.useHeadroom = d.useHeadroom || false
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
        amagiCodePreset: '',
        amagiCodeMode: '',
        amagiCodeShell: '',
        useProxy: state.useProxy,
        useHeadroom: state.useHeadroom,
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
    state.workDir = ''
    state.useProxy = false
    state.useHeadroom = false
    state.initialized = false
  }

  return {
    state,
    initDefaults,
    persistDefaults,
    reset,
  }
}
