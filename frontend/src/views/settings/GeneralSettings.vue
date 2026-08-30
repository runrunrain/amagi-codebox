<template>
  <div class="set-card">
    <h2>仪表盘默认配置</h2>
    <p class="set-sub">应用启动时的默认引擎、提供商与启动参数</p>

    <div class="setting-list">
      <div class="setting-row">
        <label>默认服务提供商</label>
        <select class="sel" v-model="defaults.provider">
          <option value="">（不指定）</option>
          <option v-for="(p, name) in anthropicProviders" :key="name" :value="name">
            {{ name }}
          </option>
        </select>
      </div>

      <div class="setting-row">
        <label>默认预设配置</label>
        <select class="sel" v-model="defaults.preset" :disabled="!defaults.provider">
          <option value="">（不指定）</option>
          <option v-for="(preset, key) in availablePresets" :key="key" :value="key">
            {{ preset.name }}<span v-if="preset.model"> · {{ preset.model }}</span>
          </option>
        </select>
      </div>

      <div class="setting-row">
        <label>默认 OpenCode 预设</label>
        <select class="sel" v-model="defaults.openCodePresetKey">
          <option value="">本机默认（不启用受管预设）</option>
          <option v-for="p in openCodePresetList" :key="p.key" :value="p.key">
            {{ p.name }}<span v-if="p.bindingCount"> · {{ p.bindingCount }} 绑定</span>
          </option>
        </select>
      </div>

      <div class="setting-row">
        <label>引擎 Tab</label>
        <Segmented
          :model-value="activeEngineTab"
          @update:model-value="(v) => (activeEngineTab = v)"
          :options="engineOptions"
        />
      </div>

      <div class="setting-row">
        <label>启动模式</label>
        <Segmented
          :model-value="currentEngineMode"
          @update:model-value="(v) => (currentEngineMode = v)"
          :options="launchModeOptions"
        />
      </div>

      <div class="setting-row">
        <label>默认 Shell</label>
        <select class="sel" v-model="currentEngineShell">
          <option v-for="s in shellOptions" :key="s.value" :value="s.value">{{ s.label }}</option>
        </select>
      </div>

    </div>

    <div class="card-footer">
      <AppButton variant="primary" :disabled="saving" @click="saveDefaults">
        {{ saving ? '保存中...' : '保存默认配置' }}
      </AppButton>
      <span class="footer-hint">需重启应用生效</span>
    </div>
  </div>

  <div class="set-card">
    <h2>提交总结模型</h2>
    <p class="set-sub">终端页提交/推送弹窗中 AI 生成提交信息所用的模型，仅支持 OpenAI 兼容 Provider 的预设；若预设模型为空则使用该 Provider 默认模型</p>

    <div class="setting-list">
      <div class="setting-row">
        <label>模型预设</label>
        <select
          class="sel"
          v-model="commitSummaryPreset"
          :disabled="savingPreset || presetLoading"
          @change="saveCommitSummaryPreset"
        >
          <option value="">未设置（禁用 AI 生成）</option>
          <option v-for="opt in commitPresetOptions" :key="opt.value" :value="opt.value">
            {{ opt.label }}
          </option>
        </select>
      </div>
    </div>

    <div class="card-footer">
      <span class="footer-hint">选中即保存，立即生效</span>
    </div>
  </div>

  <div v-if="platformCaps.systemProxyControlSupported.value" class="set-card">
    <h2>系统显式代理</h2>
    <p class="set-sub">控制系统级 HTTP(S) 显式代理（Windows Internet Settings）。开启后浏览器等遵循系统代理的应用经下方端点出站；关闭仅摘除开关并保留地址，重新开启免重填。CLI 会话的代理注入不受此开关影响</p>

    <div class="setting-list">
      <div class="setting-row">
        <label>代理开关</label>
        <div class="proxy-toggle">
          <span v-if="proxyStatus.enabled" class="proxy-state on">
            已开启<template v-if="proxyStatus.host"> · {{ proxyStatus.host }}:{{ proxyStatus.port }}</template><template v-if="!proxyStatus.reachable"> · 代理进程未响应</template>
          </span>
          <span v-else class="proxy-state">已关闭</span>
          <Switch :model-value="proxyStatus.enabled" :disabled="proxyToggling" @update:model-value="onToggleSystemProxy" />
        </div>
      </div>

      <div class="setting-row">
        <label>代理主机</label>
        <TextInput v-model="proxyHost" placeholder="127.0.0.1" class="proxy-input" />
      </div>

      <div class="setting-row">
        <label>代理端口</label>
        <TextInput v-model="proxyPort" placeholder="5800" class="proxy-input port" />
      </div>
    </div>

    <div class="card-footer">
      <AppButton :disabled="savingProxyEndpoint" @click="saveProxyEndpoint">
        {{ savingProxyEndpoint ? '保存中...' : '保存代理地址' }}
      </AppButton>
      <span class="footer-hint">开关即时生效；地址保存后于下次开启时写入</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { GetProviders, GetOpenCodePresets } from '../../../wailsjs/go/config/ConfigService'
import { GetMergedTerminalPresets } from '../../../wailsjs/go/main/App'
import { config } from '../../../wailsjs/go/models'
import {
  getDashboardDefaults,
  setDashboardDefaults,
  getCommitSummaryPreset,
  setCommitSummaryPreset,
} from '../../api/settings'
import { useProviderStore } from '../../stores/provider'
import { useToast } from '../../composables/useToast'
import { usePlatformCapabilities } from '../../composables/usePlatformCapabilities'
import {
  getSystemProxyStatus,
  setSystemProxyEnabled,
  setSystemProxyEndpoint,
} from '../../api/systemProxy'
import Segmented from '../../components/ui/Segmented.vue'
import AppButton from '../../components/ui/AppButton.vue'
import Switch from '../../components/ui/Switch.vue'
import TextInput from '../../components/ui/TextInput.vue'

const { showSuccess, showError } = useToast()
const platformCaps = usePlatformCapabilities()

type Provider = config.Provider

interface MergedPresetEntry { key: string; label: string; provider: string; model: string }
interface OpenCodePresetSummary { key: string; name: string; description: string; bindingCount: number }

const providers = ref<Record<string, Provider>>({})
const settingsMergedPresets = ref<MergedPresetEntry[]>([])
const openCodePresetList = ref<OpenCodePresetSummary[]>([])
const saving = ref(false)

// ---- 提交总结模型（AI 生成提交信息；数据源：provider store openai 桶 mergedPresets）----
const providerStore = useProviderStore()
const commitSummaryPreset = ref('')
const savingPreset = ref(false)
const presetLoading = computed(() => providerStore.loadingPresets)

/** value 为 stable key（provider/preset 格式）；label 附模型名便于识别 */
const commitPresetOptions = computed(() =>
  providerStore.codexPresets.map((mp) => {
    const value = mp.key || `${mp.provider}/${mp.label}`
    return { value, label: mp.model ? `${value} · ${mp.model}` : value }
  }),
)

async function saveCommitSummaryPreset() {
  savingPreset.value = true
  try {
    await setCommitSummaryPreset(commitSummaryPreset.value)
    showSuccess(commitSummaryPreset.value ? '提交总结模型已保存' : '已禁用 AI 生成提交信息')
  } catch (err: any) {
    showError('保存提交总结模型失败: ' + (err?.message || err))
  } finally {
    savingPreset.value = false
  }
}

const activeEngineTab = ref<string>('claude')
const engineOptions = [
  { value: 'claude', label: 'ClaudeCode' },
  { value: 'opencode', label: 'OpenCode' },
  { value: 'codex', label: 'Codex' },
]

const defaults = reactive({
  provider: '',
  preset: '',
  openCodePresetKey: '',
  claudeMode: 'embedded',
  claudeShell: '',
  openCodeMode: 'embedded',
  openCodeShell: '',
  codexMode: 'embedded',
  codexShell: '',
  useHeadroom: false,
})

function isAnthropicCompatible(p: any): boolean {
  return !!(p?.anthropic?.enabled) || ((!p?.openai?.enabled) && (p?.type || 'anthropic') !== 'openai' && p?.auth_key !== 'OPENAI_API_KEY')
}

const anthropicProviders = computed(() => {
  const result: Record<string, Provider> = {}
  for (const [name, provider] of Object.entries(providers.value)) {
    if (isAnthropicCompatible(provider)) result[name] = provider
  }
  return result
})

const availablePresets = computed(() => {
  if (!defaults.provider) return {}
  const result: Record<string, { name: string; model: string }> = {}
  for (const mp of settingsMergedPresets.value) {
    if (mp.provider === defaults.provider) {
      result[mp.key] = { name: mp.label, model: mp.model }
    }
  }
  return result
})

const launchModeOptions = computed(() =>
  platformCaps.launchModes.value.map((m: any) => ({ value: m.value, label: m.label })),
)

const shellOptions = computed(() => [
  { value: '', label: '直接启动' },
  ...platformCaps.builtinShellOptions.value.map((s: any) => ({ value: s.value, label: s.label })),
])

const currentEngineMode = computed<string>({
  get: () => {
    if (activeEngineTab.value === 'claude') return defaults.claudeMode
    if (activeEngineTab.value === 'opencode') return defaults.openCodeMode
    return defaults.codexMode
  },
  set: (val: string) => {
    if (activeEngineTab.value === 'claude') defaults.claudeMode = val
    else if (activeEngineTab.value === 'opencode') defaults.openCodeMode = val
    else defaults.codexMode = val
  },
})

const currentEngineShell = computed<string>({
  get: () => {
    if (activeEngineTab.value === 'claude') return defaults.claudeShell
    if (activeEngineTab.value === 'opencode') return defaults.openCodeShell
    return defaults.codexShell
  },
  set: (val: string) => {
    if (activeEngineTab.value === 'claude') defaults.claudeShell = val
    else if (activeEngineTab.value === 'opencode') defaults.openCodeShell = val
    else defaults.codexShell = val
  },
})

watch(() => defaults.provider, (newVal) => {
  if (newVal) {
    const presetKeys = Object.keys(availablePresets.value)
    if (presetKeys.length > 0 && !presetKeys.includes(defaults.preset)) {
      defaults.preset = presetKeys[0]
    }
  } else {
    defaults.preset = ''
  }
})

async function loadData() {
  try {
    providers.value = await GetProviders()
  } catch (err) {
    console.error('load providers:', err)
  }
  try {
    const presets = await GetMergedTerminalPresets('anthropic')
    settingsMergedPresets.value = (presets || []) as unknown as MergedPresetEntry[]
  } catch (err) {
    console.error('load merged presets:', err)
  }
  try {
    const map = await GetOpenCodePresets()
    const list: OpenCodePresetSummary[] = []
    for (const [key, preset] of Object.entries(map || {})) {
      const p = preset as any
      list.push({
        key,
        name: p.name || key,
        description: p.description || '',
        bindingCount: p.bindings ? Object.keys(p.bindings).length : 0,
      })
    }
    openCodePresetList.value = list
  } catch (err) {
    console.error('load opencode presets:', err)
    openCodePresetList.value = []
  }
  // 提交总结模型：确保 provider store openai 预设已加载（已加载则跳过），并读取当前设置
  void providerStore.loadPresets('openai')
  try {
    commitSummaryPreset.value = await getCommitSummaryPreset()
  } catch (err) {
    console.error('load commit summary preset:', err)
  }
  try {
    const d = await getDashboardDefaults()
    const shellFallback = platformCaps.defaultShellKey.value || ''
    defaults.provider = d.provider || ''
    defaults.preset = d.preset || ''
    defaults.openCodePresetKey = d.openCodePresetKey || ''
    defaults.claudeMode = d.claudeMode || d.mode || 'embedded'
    defaults.claudeShell = d.claudeShell || d.shell || shellFallback
    defaults.openCodeMode = d.openCodeMode || 'embedded'
    defaults.openCodeShell = d.openCodeShell || d.shell || shellFallback
    defaults.codexMode = d.codexMode || 'embedded'
    defaults.codexShell = d.codexShell || d.shell || shellFallback
    defaults.useHeadroom = d.useHeadroom || false
  } catch (err) {
    console.error('load defaults:', err)
  }
}

async function saveDefaults() {
  saving.value = true
  try {
    await setDashboardDefaults({
      provider: defaults.provider,
      preset: defaults.preset,
      openCodePresetKey: defaults.openCodePresetKey,
      mode: defaults.claudeMode,
      shell: defaults.claudeShell,
      claudeMode: defaults.claudeMode,
      claudeShell: defaults.claudeShell,
      openCodeMode: defaults.openCodeMode,
      openCodeShell: defaults.openCodeShell,
      codexMode: defaults.codexMode,
      codexShell: defaults.codexShell,
      useHeadroom: defaults.useHeadroom,
    } as any)
    showSuccess('默认值已保存')
  } catch (err: any) {
    showError('保存失败: ' + (err?.message || err))
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  await platformCaps.ensure()
  await loadData()
  void loadSystemProxy()
})

// ---- 系统显式代理（Windows；capabilities 门控，卡片仅在支持时渲染）----
const proxyStatus = reactive({
  supported: false,
  enabled: false,
  host: '',
  port: 0,
  reachable: false,
  configuredHost: '',
  configuredPort: 0,
})
const proxyHost = ref('')
const proxyPort = ref('')
const proxyToggling = ref(false)
const savingProxyEndpoint = ref(false)

async function loadSystemProxy() {
  try {
    const status = await getSystemProxyStatus()
    Object.assign(proxyStatus, status)
    // 编辑框绑定持久化端点（后端已归一为非零默认）；已配置值与实时值解耦展示。
    if (!proxyHost.value && !proxyPort.value) {
      proxyHost.value = status.configuredHost || ''
      proxyPort.value = status.configuredPort ? String(status.configuredPort) : ''
    }
  } catch (err) {
    console.error('load system proxy status:', err)
  }
}

async function onToggleSystemProxy(enabled: boolean) {
  proxyToggling.value = true
  try {
    const status = await setSystemProxyEnabled(enabled)
    Object.assign(proxyStatus, status)
    showSuccess(enabled ? '系统代理已开启' : '系统代理已关闭')
  } catch (err: any) {
    showError('切换系统代理失败: ' + (err?.message || err))
    // 失败时回读真实状态，避免开关停留在乐观值。
    void loadSystemProxy()
  } finally {
    proxyToggling.value = false
  }
}

async function saveProxyEndpoint() {
  const port = parseInt(proxyPort.value, 10)
  if (!Number.isFinite(port)) {
    showError('代理端口必须是数字')
    return
  }
  savingProxyEndpoint.value = true
  try {
    await setSystemProxyEndpoint(proxyHost.value.trim(), port)
    showSuccess('代理地址已保存')
    await loadSystemProxy()
  } catch (err: any) {
    showError('保存代理地址失败: ' + (err?.message || err))
  } finally {
    savingProxyEndpoint.value = false
  }
}
</script>

<style scoped>
.set-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 14px;
  padding: 20px 24px;
  box-shadow: var(--shadow);
}

.set-card h2 {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin-bottom: 4px;
}

.set-sub {
  font-size: 12px;
  color: var(--tertiary);
  margin-bottom: 14px;
}

.setting-list {
  display: flex;
  flex-direction: column;
}

.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-top: 1px solid var(--separator);
}

.setting-row:first-child {
  border-top: none;
}

.setting-row label {
  font-size: 14px;
  color: var(--secondary);
}

.sel {
  appearance: none;
  -webkit-appearance: none;
  min-width: 220px;
  max-width: 320px;
  padding: 7px 30px 7px 12px;
  font-size: 13px;
  font-family: inherit;
  color: var(--label);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  background-image: linear-gradient(45deg, transparent 50%, var(--tertiary) 50%),
    linear-gradient(135deg, var(--tertiary) 50%, transparent 50%);
  background-position: calc(100% - 16px) center, calc(100% - 11px) center;
  background-size: 5px 5px, 5px 5px;
  background-repeat: no-repeat;
  cursor: pointer;
}

.sel:focus {
  outline: none;
  border-color: var(--accent);
}

.sel:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.card-footer {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 14px;
}

.footer-hint {
  font-size: 11px;
  color: var(--tertiary);
}

.proxy-toggle {
  display: flex;
  align-items: center;
  gap: 12px;
}

.proxy-state {
  font-size: 12px;
  color: var(--tertiary);
}

.proxy-state.on {
  color: var(--secondary);
}

.proxy-input {
  min-width: 220px;
  max-width: 320px;
}

.proxy-input.port {
  max-width: 120px;
}
</style>
