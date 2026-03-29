import { reactive } from 'vue'

// 仪表盘状态——在 app 生命周期内跨路由保持
// 不用 ref 而用 reactive 对象，整体更紧凑
const state = reactive({
    provider: '',
    preset: '',
    openCodeProvider: '',
    claudeMode: 'embedded',
    openCodeMode: 'embedded',
    codexMode: 'embedded',
    workDir: '',
    useProxy: false,
    claudeShell: 'pwsh',
    openCodeShell: 'pwsh',
    codexShell: 'pwsh',
    claudeCustomShellPath: '',
    openCodeCustomShellPath: '',
    codexCustomShellPath: '',
    initialized: false,
})

export function useDashboardState() {
    return state
}
