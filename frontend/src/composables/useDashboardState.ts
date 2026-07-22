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
  // Codex 全局 headroom 开关由独立的设置入口（App.SetCodexGlobalHeadroom）管理，
  // 不属于本仪表盘表单。这里仅缓存后端持久化值，在 persistDefaults 时原样回写，
  // 避免仪表盘保存把该开关重置为 false（透传保护，不在本表单编辑）。
  codexGlobalHeadroom: false,
  codexGlobalHeadroomTarget: '',
  codexGlobalHeadroomPort: 0,
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
      state.codexGlobalHeadroom = d.codexGlobalHeadroom || false
      state.codexGlobalHeadroomTarget = d.codexGlobalHeadroomTarget || ''
      state.codexGlobalHeadroomPort = d.codexGlobalHeadroomPort || 0
    } catch (err) {
      console.error('Failed to load dashboard defaults:', err)
    }
    state.initialized = true
  }

  /**
   * 把当前仪表盘选择持久化到后端 SetDashboardDefaults。
   *
   * codexGlobalHeadroom / codexGlobalHeadroomTarget / codexGlobalHeadroomPort
   * 是透传占位：后端 SetDashboardDefaults 会忽略这三字段（始终钉住现有值，
   * 真实状态由独立的 App.SetCodexGlobalHeadroom 管理）。这里仅原样回写缓存值，
   * 既不重置该开关，也避免遗漏字段导致 schema 不一致。发送与否功能等价。
   *
   * codexGlobalHeadroom / codexGlobalHeadroomTarget / codexGlobalHeadroomPort
   * are pass-through placeholders: the backend SetDashboardDefaults ignores
   * these three fields (always pinning the existing values; the real state is
   * owned by App.SetCodexGlobalHeadroom). They are echoed verbatim here so the
   * dashboard save neither resets the toggle nor drops fields needed to keep
   * the payload schema consistent. Sending them or not is functionally equivalent.
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
        // Pass-through placeholders (see function doc above) — backend ignores.
        codexGlobalHeadroom: state.codexGlobalHeadroom,
        codexGlobalHeadroomTarget: state.codexGlobalHeadroomTarget,
        codexGlobalHeadroomPort: state.codexGlobalHeadroomPort,
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
