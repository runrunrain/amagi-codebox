<template>
  <section class="view-settings">
    <PageHead title="会话设置" description="配置并启动一个新的 AI 编程会话" />

    <ConfigCard>
      <div class="card-head">
        <h2>快速启动</h2>
      </div>

      <Segmented
        :model-value="dashState.engine"
        :options="engineOptions"
        @update:model-value="handleEngineChange"
      />

      <div class="setting-list">
        <!-- 服务提供商 -->
        <div class="setting-row">
          <label>服务提供商</label>
          <select
            class="select"
            :value="currentProvider"
            :disabled="providerOptions.length === 0"
            @change="handleProviderChange(($event.target as HTMLSelectElement).value)"
          >
            <option value="" disabled v-if="providerOptions.length === 0">暂无可用提供商</option>
            <option
              v-for="opt in providerOptions"
              :key="opt.value"
              :value="opt.value"
            >{{ opt.label }}</option>
          </select>
        </div>

        <!-- 预设配置 -->
        <div class="setting-row">
          <label>预设配置</label>
          <select
            class="select"
            :value="currentPreset"
            :disabled="presetOptions.length === 0"
            @change="handlePresetChange(($event.target as HTMLSelectElement).value)"
          >
            <option value="" disabled v-if="presetOptions.length === 0">暂无可用预设</option>
            <option
              v-for="opt in presetOptions"
              :key="opt.value"
              :value="opt.value"
            >{{ opt.label }}</option>
          </select>
        </div>

        <!-- 启动模式 -->
        <div class="setting-row">
          <label>启动模式</label>
          <select
            class="select"
            :value="currentMode"
            @change="handleModeChange(($event.target as HTMLSelectElement).value)"
          >
            <option
              v-for="m in launchModes"
              :key="m.value"
              :value="m.value"
            >{{ m.label }}</option>
          </select>
        </div>

        <!-- 终端 Shell（仅内嵌模式可选） -->
        <div class="setting-row" v-if="currentMode === 'embedded'">
          <label>终端 Shell</label>
          <select
            class="select"
            :value="currentShell"
            @change="handleShellChange(($event.target as HTMLSelectElement).value)"
          >
            <option value="">直接启动</option>
            <option
              v-for="s in builtinShellOptions"
              :key="s.value"
              :value="s.value"
            >{{ s.label }}</option>
            <option value="__custom__">自定义路径</option>
          </select>
        </div>

        <!-- 自定义 Shell 路径 -->
        <div class="setting-row" v-if="currentMode === 'embedded' && currentShell === '__custom__'">
          <label>Shell 路径</label>
          <div class="input-group">
            <TextInput
              :model-value="currentCustomShellPath"
              placeholder="/bin/zsh"
              mono
              @update:model-value="handleCustomShellChange"
            />
          </div>
        </div>

        <!-- 工作目录 -->
        <div class="setting-row">
          <label>工作目录</label>
          <div class="input-group">
            <TextInput
              :model-value="dashState.workDir"
              placeholder="选择或输入工作目录"
              mono
              @update:model-value="dashState.workDir = $event"
            />
            <button class="btn btn-ghost browse-btn" @click="handleBrowse" :disabled="browsing">
              {{ browsing ? '…' : '浏览' }}
            </button>
          </div>
        </div>

        <!-- 启用注入代理 -->
        <div class="setting-row">
          <label>启用注入代理</label>
          <Switch
            :model-value="dashState.useProxy"
            @update:model-value="dashState.useProxy = $event"
          />
        </div>
      </div>
    </ConfigCard>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import PageHead from '../components/ui/PageHead.vue'
import ConfigCard from '../components/ui/ConfigCard.vue'
import Segmented from '../components/ui/Segmented.vue'
import Switch from '../components/ui/Switch.vue'
import TextInput from '../components/ui/TextInput.vue'

import { useDashboardState } from '../composables/useDashboardState'
import { usePlatformCapabilities } from '../composables/usePlatformCapabilities'
import { useToast } from '../composables/useToast'
import { useSessionList } from '../composables/useSessionList'
import { useSessionStore } from '../stores/session'

import * as providerApi from '../api/provider'
import * as sessionApi from '../api/session'
import { BrowseDirectory } from '../../wailsjs/go/main/App'
import { GetOpenCodePresets } from '../../wailsjs/go/config/ConfigService'
import { config } from '../../wailsjs/go/models'

type Provider = config.Provider
type MergedTerminalPreset = config.MergedTerminalPreset

const router = useRouter()
const { state: dashState, initDefaults, persistDefaults } = useDashboardState()
const platformCaps = usePlatformCapabilities()
const { showSuccess, showError } = useToast()
const sessionStore = useSessionStore()
const { refresh } = useSessionList()

const browsing = ref(false)

// --- 数据源 ---
// 后端 GetProvidersByType 已按 IsAnthropicCompatible / IsOpenAICompatible 过滤，
// 这里分别缓存两份，作为对应引擎下拉的真相源。
const anthropicProviders = ref<Record<string, Provider>>({})
const openaiProviders = ref<Record<string, Provider>>({})
const claudePresets = ref<MergedTerminalPreset[]>([])
const codexPresets = ref<MergedTerminalPreset[]>([])
const openCodePresetList = ref<Array<{ key: string; name: string; description: string; bindingCount: number }>>([])

// --- 引擎选项 ---
const engineOptions = [
  { value: 'claudecode', label: 'ClaudeCode' },
  { value: 'opencode', label: 'OpenCode' },
  { value: 'codex', label: 'Codex' },
]

// --- 引擎相关计算属性 ---
const currentMode = computed(() => {
  if (dashState.engine === 'claudecode') return dashState.claudeMode
  if (dashState.engine === 'opencode') return dashState.openCodeMode
  return dashState.codexMode
})
function setMode(v: string) {
  if (dashState.engine === 'claudecode') dashState.claudeMode = v
  else if (dashState.engine === 'opencode') dashState.openCodeMode = v
  else dashState.codexMode = v
}

const currentShell = computed(() => {
  if (dashState.engine === 'claudecode') return dashState.claudeShell
  if (dashState.engine === 'opencode') return dashState.openCodeShell
  return dashState.codexShell
})
function setShell(v: string) {
  if (dashState.engine === 'claudecode') dashState.claudeShell = v
  else if (dashState.engine === 'opencode') dashState.openCodeShell = v
  else dashState.codexShell = v
}

const currentCustomShellPath = computed(() => {
  if (dashState.engine === 'claudecode') return dashState.claudeCustomShellPath
  if (dashState.engine === 'opencode') return dashState.openCodeCustomShellPath
  return dashState.codexCustomShellPath
})
function setCustomShellPath(v: string) {
  if (dashState.engine === 'claudecode') dashState.claudeCustomShellPath = v
  else if (dashState.engine === 'opencode') dashState.openCodeCustomShellPath = v
  else dashState.codexCustomShellPath = v
}

const currentProvider = computed(() => {
  if (dashState.engine === 'codex') return dashState.codexProvider
  return dashState.provider
})

const currentPreset = computed(() => {
  if (dashState.engine === 'codex') return dashState.codexModel
  if (dashState.engine === 'opencode') return dashState.openCodePresetKey
  return dashState.preset
})

// --- 下拉选项 ---
const providerOptions = computed(() => {
  const map = dashState.engine === 'codex' ? openaiProviders.value : anthropicProviders.value
  return Object.keys(map).sort().map(name => ({ value: name, label: name }))
})

const presetOptions = computed(() => {
  if (dashState.engine === 'opencode') {
    // 开头添加"使用全局配置"选项
    const globalOption = { value: '', label: '使用全局配置' }
    const presetOptions = openCodePresetList.value.map(p => ({
      value: p.key,
      label: p.bindingCount > 0 ? `${p.name} (${p.bindingCount})` : p.name,
    }))
    return [globalOption, ...presetOptions]
  }
  const list = dashState.engine === 'codex' ? codexPresets.value : claudePresets.value
  const targetProvider = currentProvider.value
  return list
    .filter(p => !targetProvider || p.provider === targetProvider)
    .map(p => ({ value: p.key, label: p.label || p.key }))
})

const launchModes = computed(() => platformCaps.launchModes.value)
const builtinShellOptions = computed(() => platformCaps.builtinShellOptions.value)

// --- 校验与自动同步（照搬旧逻辑） ---
function validateClaudePreset() {
  if (dashState.engine !== 'claudecode') return
  if (dashState.preset) {
    const entry = claudePresets.value.find(p => p.key === dashState.preset)
    if (entry && entry.provider === dashState.provider) return
  }
  if (claudePresets.value.length > 0) {
    const match = claudePresets.value.find(p => p.provider === dashState.provider)
    if (match) {
      dashState.preset = match.key
    } else {
      const first = claudePresets.value[0]
      dashState.provider = first.provider
      dashState.preset = first.key
    }
  } else {
    dashState.preset = ''
  }
}

function validateCodexPreset() {
  if (dashState.engine !== 'codex') return
  if (dashState.codexModel) {
    const entry = codexPresets.value.find(p => p.key === dashState.codexModel)
    if (entry && entry.provider === dashState.codexProvider) return
  }
  if (codexPresets.value.length > 0) {
    const match = codexPresets.value.find(p => p.provider === dashState.codexProvider)
    if (match) {
      dashState.codexModel = match.key
    } else {
      const first = codexPresets.value[0]
      dashState.codexProvider = first.provider
      dashState.codexModel = first.key
    }
  } else {
    dashState.codexModel = ''
  }
}

const canLaunch = computed(() => {
  if (dashState.engine === 'claudecode') {
    if (!dashState.preset || !dashState.provider) return false
    return claudePresets.value.some(p => p.key === dashState.preset && p.provider === dashState.provider)
  }
  if (dashState.engine === 'codex') {
    if (!dashState.codexModel || !dashState.codexProvider) return false
    return codexPresets.value.some(p => p.key === dashState.codexModel && p.provider === dashState.codexProvider)
  }
  // OpenCode 只需要工作目录
  return !!dashState.workDir
})

// --- 事件处理 ---
function handleEngineChange(v: string) {
  dashState.engine = v as any
}

function handleProviderChange(v: string) {
  if (dashState.engine === 'codex') {
    dashState.codexProvider = v
    // 自动重置预设到该 provider 的第一个
    const first = codexPresets.value.find(p => p.provider === v)
    dashState.codexModel = first ? first.key : ''
  } else {
    dashState.provider = v
    const first = claudePresets.value.find(p => p.provider === v)
    dashState.preset = first ? first.key : ''
  }
}

function handlePresetChange(v: string) {
  if (dashState.engine === 'codex') {
    dashState.codexModel = v
    const entry = codexPresets.value.find(p => p.key === v)
    if (entry && entry.provider) dashState.codexProvider = entry.provider
  } else if (dashState.engine === 'opencode') {
    dashState.openCodePresetKey = v
  } else {
    dashState.preset = v
    const entry = claudePresets.value.find(p => p.key === v)
    if (entry && entry.provider) dashState.provider = entry.provider
  }
}

function handleModeChange(v: string) {
  setMode(v)
}

function handleShellChange(v: string) {
  setShell(v)
}

function handleCustomShellChange(v: string) {
  setCustomShellPath(v)
}

async function handleBrowse() {
  browsing.value = true
  try {
    const dir = await BrowseDirectory()
    if (dir) dashState.workDir = dir
  } catch (err) {
    showError('选择目录失败: ' + err)
  } finally {
    browsing.value = false
  }
}

function resolveShellPath(): string {
  const shell = currentShell.value
  const custom = currentCustomShellPath.value
  if (shell === '') return ''
  if (shell === '__custom__') return custom
  return platformCaps.resolveShellPath(shell, custom)
}

// --- 启动逻辑（3 引擎分支，对齐 wailsjs LaunchSession 签名） ---
async function handleLaunch() {
  if (!canLaunch.value) return
  try {
    let sessionId = ''
    if (dashState.engine === 'claudecode') {
      sessionId = await sessionApi.launchClaudeSession({
        providerName: dashState.provider,
        presetName: dashState.preset,
        mode: dashState.claudeMode,
        workDir: dashState.workDir,
        useProxy: dashState.useProxy,
        shellPath: dashState.claudeMode === 'embedded' ? resolveShellPath() : '',
      })
    } else if (dashState.engine === 'opencode') {
      sessionId = await sessionApi.launchOpenCodeSession({
        // 新模型：providerName 为空，presetName 传 presetKey
        providerName: '',
        presetName: dashState.openCodePresetKey,
        mode: dashState.openCodeMode,
        workDir: dashState.workDir,
        shellPath: dashState.openCodeMode === 'embedded' ? resolveShellPath() : '',
      })
    } else {
      sessionId = await sessionApi.launchCodexSession({
        modelName: dashState.codexModel,
        providerID: dashState.codexProvider,
        mode: dashState.codexMode,
        workDir: dashState.workDir,
        shellPath: dashState.codexMode === 'embedded' ? resolveShellPath() : '',
      })
    }

    await persistDefaults()
    await refresh()

    // 设为活动会话
    sessionStore.setActiveSession(sessionId)

    const engineLabel = dashState.engine === 'claudecode' ? 'ClaudeCode'
      : dashState.engine === 'opencode' ? 'OpenCode' : 'Codex'
    showSuccess(`${engineLabel} 会话启动成功`)

    // 内嵌模式自动跳转终端页
    if (currentMode.value === 'embedded') {
      router.push('/terminal')
    }
  } catch (err) {
    console.error('Launch failed:', err)
    showError('启动失败: ' + err)
  }
}

// --- 数据加载 ---
async function loadProviders() {
  try {
    const [anthropic, openai] = await Promise.all([
      providerApi.getProvidersByType('anthropic'),
      providerApi.getProvidersByType('openai'),
    ])
    anthropicProviders.value = anthropic || {}
    openaiProviders.value = openai || {}
  } catch (err) {
    console.error('Failed to load providers:', err)
  }
}

async function loadTerminalPresets() {
  try {
    const [claude, codex] = await Promise.all([
      providerApi.getMergedTerminalPresets('claude_code'),
      providerApi.getMergedTerminalPresets('codex'),
    ])
    claudePresets.value = claude || []
    codexPresets.value = codex || []
  } catch (err) {
    console.error('Failed to load terminal presets:', err)
  }
}

async function loadOpenCodePresets() {
  try {
    const map = await GetOpenCodePresets()
    const list: Array<{ key: string; name: string; description: string; bindingCount: number }> = []
    for (const [key, preset] of Object.entries(map || {})) {
      const p = preset as any
      list.push({
        key,
        name: p?.name || key,
        description: p?.description || '',
        bindingCount: p?.bindings ? Object.keys(p.bindings).length : 0,
      })
    }
    openCodePresetList.value = list
  } catch (err) {
    console.error('Failed to load OpenCode presets:', err)
    openCodePresetList.value = []
  }
}

// --- 监听 preset 列表变化时重新校验 ---
watch(claudePresets, () => { if (dashState.engine === 'claudecode') validateClaudePreset() })
watch(codexPresets, () => { if (dashState.engine === 'codex') validateCodexPreset() })

onMounted(async () => {
  await platformCaps.ensure()
  await Promise.all([
    loadProviders(),
    loadTerminalPresets(),
    loadOpenCodePresets(),
  ])
  await initDefaults()

  // 默认 provider 兜底
  if (!dashState.provider && Object.keys(anthropicProviders.value).length > 0) {
    dashState.provider = Object.keys(anthropicProviders.value)[0]
  }
  if (!dashState.codexProvider && Object.keys(openaiProviders.value).length > 0) {
    dashState.codexProvider = Object.keys(openaiProviders.value)[0]
  }
  validateClaudePreset()
  validateCodexPreset()
})
</script>

<style scoped>
.view-settings {
  padding: 32px 36px;
  gap: 22px;
  overflow: auto;
  display: flex;
  flex-direction: column;
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.card-head h2 {
  font-size: 17px;
  font-weight: 600;
}

.setting-list {
  display: flex;
  flex-direction: column;
}

.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 0;
  border-top: 1px solid var(--separator);
}

.setting-row:first-child {
  border-top: none;
}

.setting-row label {
  font-size: 14px;
  color: var(--secondary);
  flex-shrink: 0;
}

.select {
  appearance: none;
  -webkit-appearance: none;
  background: var(--control);
  border: 1px solid transparent;
  border-radius: 7px;
  padding: 6px 28px 6px 10px;
  font-size: 13px;
  color: var(--label);
  font-family: inherit;
  cursor: pointer;
  min-width: 180px;
  max-width: 320px;
  background-image: url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='10' height='10' viewBox='0 0 24 24' fill='none' stroke='%238E8E93' stroke-width='2.5' stroke-linecap='round'><polyline points='6 9 12 15 18 9'/></svg>");
  background-repeat: no-repeat;
  background-position: right 9px center;
  transition: background-color 0.12s, box-shadow 0.12s;
}

.select:hover:not(:disabled) {
  background-color: var(--controlHover);
}

.select:focus {
  outline: none;
  box-shadow: 0 0 0 2px rgba(0, 122, 255, 0.2);
  border-color: var(--accent);
}

.select:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.input-group {
  display: flex;
  align-items: center;
  gap: 8px;
}

.input-group :deep(.text-input) {
  min-width: 280px;
}

.browse-btn {
  padding: 6px 12px;
  font-size: 12px;
  flex-shrink: 0;
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  padding: 9px 16px;
  transition: background 0.15s, box-shadow 0.15s, opacity 0.15s;
  font-family: inherit;
}

.btn-primary {
  background: var(--accent);
  color: #fff;
}

.btn-primary:hover:not(:disabled) {
  background: var(--accentHover);
}

.btn-primary:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.btn-ghost {
  background: var(--control);
  color: var(--secondary);
}

.btn-ghost:hover:not(:disabled) {
  background: var(--controlHover);
}

.btn-ghost:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.launch-btn {
  padding: 8px 16px;
}

.spin {
  animation: luoshen-spin 0.8s linear infinite;
}

@keyframes luoshen-spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
