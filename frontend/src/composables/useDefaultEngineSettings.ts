import { computed, ref } from 'vue'

export const defaultEngineOptions = [
  { value: 'claude', label: 'ClaudeCode' },
  { value: 'opencode', label: 'OpenCode' },
  { value: 'codex', label: 'Codex' },
  { value: 'pi', label: 'Pi' },
  { value: 'omp', label: 'Oh My Pi' },
]

const engineFields = {
  claude: { mode: 'claudeMode', shell: 'claudeShell' },
  opencode: { mode: 'openCodeMode', shell: 'openCodeShell' },
  codex: { mode: 'codexMode', shell: 'codexShell' },
  pi: { mode: 'piMode', shell: 'piShell' },
  omp: { mode: 'ompMode', shell: 'ompShell' },
} as const

type Engine = keyof typeof engineFields
type EngineField = (typeof engineFields)[Engine]['mode' | 'shell']

/** 设置页的引擎 Tab 只选择编辑对象，不改变其他引擎的启动默认值。 */
export function useDefaultEngineSettings(defaults: Record<EngineField, string>) {
  const selectedEngine = ref<Engine>('claude')
  const activeEngineTab = computed<string>({
    get: () => selectedEngine.value,
    set: (value) => {
      if (Object.prototype.hasOwnProperty.call(engineFields, value)) selectedEngine.value = value as Engine
    },
  })
  const currentEngineMode = computed({
    get: () => defaults[engineFields[selectedEngine.value].mode],
    set: (value: string) => { defaults[engineFields[selectedEngine.value].mode] = value },
  })
  const currentEngineShell = computed({
    get: () => defaults[engineFields[selectedEngine.value].shell],
    set: (value: string) => { defaults[engineFields[selectedEngine.value].shell] = value },
  })
  return { activeEngineTab, currentEngineMode, currentEngineShell }
}
