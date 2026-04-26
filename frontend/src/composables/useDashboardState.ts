import { reactive } from 'vue'

// 仪表盘状态——在 app 生命周期内跨路由保持
// 不用 ref 而用 reactive 对象，整体更紧凑
// 注意：shell 默认值 '' 会在 initDefaults 中由平台能力 defaultShellKey 覆盖
const state = reactive({
    provider: '',
    preset: '',
    openCodePresetKey: '',
    claudeMode: 'embedded',
    openCodeMode: 'embedded',
    codexMode: 'embedded',
    amagiCodePreset: '',
    amagiCodeMode: 'embedded',
    amagiCodeShell: '',
    amagiCodeCustomShellPath: '',
    workDir: '',
    useProxy: false,
    claudeShell: '',
    openCodeShell: '',
    codexShell: '',
    claudeCustomShellPath: '',
    openCodeCustomShellPath: '',
    codexCustomShellPath: '',
    initialized: false,
})

export function useDashboardState() {
    return state
}
