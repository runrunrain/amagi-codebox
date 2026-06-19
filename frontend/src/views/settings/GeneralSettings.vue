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

      <div class="setting-row">
        <label>默认启用注入代理</label>
        <Switch :model-value="defaults.useProxy" @update:model-value="(v) => (defaults.useProxy = v)" />
      </div>
    </div>

    <div class="card-footer">
      <AppButton variant="primary" :disabled="saving" @click="saveDefaults">
        {{ saving ? '保存中...' : '保存默认配置' }}
      </AppButton>
      <span class="footer-hint">需重启应用生效</span>
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
} from '../../api/settings'
import { useToast } from '../../composables/useToast'
import { usePlatformCapabilities } from '../../composables/usePlatformCapabilities'
import Segmented from '../../components/ui/Segmented.vue'
import Switch from '../../components/ui/Switch.vue'
import AppButton from '../../components/ui/AppButton.vue'

const { showSuccess, showError } = useToast()
const platformCaps = usePlatformCapabilities()

type Provider = config.Provider

interface MergedPresetEntry { key: string; label: string; provider: string; model: string }
interface OpenCodePresetSummary { key: string; name: string; description: string; bindingCount: number }

const providers = ref<Record<string, Provider>>({})
const settingsMergedPresets = ref<MergedPresetEntry[]>([])
const openCodePresetList = ref<OpenCodePresetSummary[]>([])
const saving = ref(false)

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
  useProxy: false,
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
    const presets = await GetMergedTerminalPresets('claude_code')
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
    defaults.useProxy = d.useProxy || false
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
      useProxy: defaults.useProxy,
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
})
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
</style>
