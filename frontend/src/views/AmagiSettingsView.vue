<template>
  <div class="amagi-settings-page">
    <div class="page-header">
      <div class="header-left">
        <h1 class="page-title">AmagiCode 设置</h1>
        <p class="page-description">配置 AmagiCode 特有的功能，包括模型预设、思考模式和努力级别</p>
      </div>
      <div class="header-actions">
        <button class="btn primary" @click="saveAllSettings" :disabled="loading">
          {{ loading ? '保存中...' : '保存所有设置' }}
        </button>
      </div>
    </div>

    <div class="view-tabs">
      <button
        v-for="tab in viewTabs"
        :key="tab.key"
        :class="['view-tab', { active: activeView === tab.key }]"
        @click="activeView = tab.key"
      >
        {{ tab.label }}
      </button>
    </div>

    <!-- Form View -->
    <div v-if="activeView === 'form'" class="form-view">
      <!-- Basic Settings -->
      <div class="card">
        <div class="card-header">
          <h2>基础设置</h2>
        </div>
        <div class="card-body">
          <div class="form-group">
            <label>默认模型</label>
            <div class="model-input-group">
              <input
                type="text"
                v-model="settings.model"
                class="input-field"
                placeholder="例如: claude-3-7-sonnet-20250219"
              />
              <select v-model="selectedAvailableModel" @change="applyAvailableModel" class="input-field model-select">
                <option value="">选择可用模型...</option>
                <option v-for="model in settings.availableModels" :key="model" :value="model">
                  {{ model }}
                </option>
              </select>
            </div>
          </div>

          <div class="form-row">
            <div class="form-group flex-1">
              <label>思考模式</label>
              <button
                class="ios-toggle"
                :class="{ active: settings.alwaysThinkingEnabled }"
                @click="settings.alwaysThinkingEnabled = !settings.alwaysThinkingEnabled"
              ></button>
            </div>
            <div class="form-group flex-1">
              <label>努力级别</label>
              <select v-model="settings.effortLevel" class="input-field">
                <option value="low">Low (低)</option>
                <option value="medium">Medium (中)</option>
                <option value="high">High (高)</option>
                <option value="max">Max (最大)</option>
              </select>
            </div>
          </div>

          <div class="form-group">
            <label>顾问模型 (Advisor Model)</label>
            <input
              type="text"
              v-model="settings.advisorModel"
              class="input-field"
              placeholder="用于代码审查和建议的模型"
            />
          </div>
        </div>
      </div>

      <!-- Providers Settings -->
      <div class="card">
        <div class="card-header">
          <h2>服务提供商配置</h2>
          <button class="btn secondary" @click="addProvider">添加提供商</button>
        </div>
        <div class="card-body">
          <div v-if="Object.keys(settings.providers).length === 0" class="empty-state">
            <p class="muted">暂无提供商配置</p>
          </div>
          <div class="provider-list">
            <div
              v-for="(provider, key) in settings.providers"
              :key="key"
              class="provider-card"
            >
              <div class="provider-header">
                <input
                  type="text"
                  v-model="provider.name"
                  class="input-field provider-name-input"
                  placeholder="提供商名称"
                />
                <div class="provider-actions">
                  <select v-model="provider.protocol" class="input-field protocol-select">
                    <option value="anthropic">Anthropic</option>
                    <option value="openai">OpenAI</option>
                  </select>
                  <button class="btn-icon danger" @click="removeProvider(key)" title="删除">
                    <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                      <polyline points="3 6 5 6 21 6"></polyline>
                      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
                    </svg>
                  </button>
                </div>
              </div>
              <div class="provider-body">
                <div class="form-group">
                  <label>Base URL</label>
                  <input
                    type="text"
                    v-model="provider.baseURL"
                    class="input-field"
                    placeholder="https://api.anthropic.com"
                  />
                </div>
                <div class="api-key-notice">
                  <span class="notice-text">使用服务提供商管理的 API Key</span>
                  <a href="#" @click.prevent="router.push('/providers')" class="link-text">前往配置</a>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Model Presets -->
      <div class="card">
        <div class="card-header">
          <h2>模型预设</h2>
          <button class="btn secondary" @click="openAddPresetDialog">添加预设</button>
        </div>
        <div class="card-body">
          <div v-if="Object.keys(settings.modelPresets).length === 0" class="empty-state">
            <p class="muted">暂无预设配置</p>
          </div>
          <div class="presets-grid">
            <div
              v-for="(preset, presetName) in settings.modelPresets"
              :key="presetName"
              class="preset-card"
              @click="openEditPresetDialog(presetName, preset)"
            >
              <div class="preset-header">
                <h3 class="preset-name">{{ presetName }}</h3>
                <div class="preset-actions" @click.stop>
                  <button class="btn-icon" @click="openEditPresetDialog(presetName, preset)" title="编辑">
                    <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                      <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path>
                      <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path>
                    </svg>
                  </button>
                  <button class="btn-icon danger" @click="deletePreset(presetName)" title="删除">
                    <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                      <polyline points="3 6 5 6 21 6"></polyline>
                      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
                    </svg>
                  </button>
                </div>
              </div>
              <div class="preset-body">
                <div class="info-row">
                  <span class="label">Provider:</span>
                  <span class="value">{{ preset.provider || '未指定' }}</span>
                </div>
                <div class="info-row">
                  <span class="label">Model:</span>
                  <span class="value">{{ preset.model || '继承默认' }}</span>
                </div>
                <div class="params-summary">
                  <span class="param-badge" v-if="preset.temperature !== undefined">Temp: {{ preset.temperature }}</span>
                  <span class="param-badge" v-if="preset.maxTokens !== undefined">Max: {{ preset.maxTokens }}</span>
                  <span class="param-badge" v-if="preset.thinking?.type === 'enabled'">
                    Thinking ({{ preset.thinking.budgetTokens || 'auto' }})
                  </span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- JSON View -->
    <div v-else class="json-view">
      <div class="card">
        <div class="card-header">
          <h2>JSON 编辑器</h2>
        </div>
        <div class="card-body">
          <div class="json-editor-wrapper">
            <textarea
              v-model="jsonContent"
              class="json-textarea"
              spellcheck="false"
              placeholder="加载中..."
            ></textarea>
            <div class="json-status" :class="{ error: !!jsonError, success: !jsonError && jsonContent }">
              <span v-if="!jsonContent"></span>
              <span v-else-if="jsonError">语法错误: {{ jsonError }}</span>
              <span v-else>JSON 语法正确</span>
            </div>
          </div>
          <div v-if="jsonWarning" class="json-warning">
            <span class="warning-icon">⚠</span>
            <span>{{ jsonWarning }}</span>
          </div>
          <div class="json-actions">
            <button class="btn secondary" @click="loadJsonData">重新加载</button>
            <button class="btn primary" @click="saveJsonData" :disabled="!!jsonError || !jsonContent || loading">
              {{ loading ? '保存中...' : '保存 JSON' }}
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Preset Dialog -->
    <div class="dialog-overlay" v-if="showPresetDialog" @click.self="showPresetDialog = false">
      <div class="dialog card preset-dialog">
        <h2>{{ isEditingPreset ? '编辑预设' : '添加预设' }}</h2>

        <div class="dialog-scroll-area">
          <div class="form-group" v-if="!isEditingPreset">
            <label>预设名称</label>
            <input type="text" v-model="editingPreset.name" class="input-field" placeholder="例如: coding, writing" />
          </div>

          <div class="form-group">
            <label>Provider</label>
            <select v-model="editingPreset.provider" class="input-field">
              <option value="">未指定</option>
              <option v-for="(provider, key) in settings.providers" :key="key" :value="key">
                {{ provider.name || key }}
              </option>
            </select>
          </div>

          <div class="form-group">
            <label>模型</label>
            <input type="text" v-model="editingPreset.model" class="input-field" placeholder="留空则使用默认模型" />
          </div>

          <div class="form-grid-2">
            <div class="form-group">
              <label>Temperature</label>
              <input type="number" v-model.number="editingPreset.temperature" class="input-field" step="0.1" min="0" max="1" placeholder="0.7" />
            </div>
            <div class="form-group">
              <label>Max Tokens</label>
              <input type="number" v-model.number="editingPreset.maxTokens" class="input-field" step="1" min="1" placeholder="4096" />
            </div>
          </div>

          <div class="section-subtitle">思考配置</div>
          <div class="form-group">
            <label>思考模式</label>
            <select v-model="editingThinkingType" class="input-field">
              <option value="">默认 (不配置)</option>
              <option value="disabled">禁用 (Disabled)</option>
              <option value="enabled">启用 (Enabled)</option>
            </select>
          </div>

          <div class="form-group" v-if="editingThinkingType === 'enabled'">
            <label>思考预算 Tokens</label>
            <input type="number" v-model.number="editingThinkingBudget" class="input-field" step="1024" min="1024" placeholder="16384" />
          </div>
        </div>

        <div class="dialog-actions">
          <button class="btn secondary" @click="showPresetDialog = false" :disabled="loading">取消</button>
          <button class="btn primary" @click="savePreset" :disabled="(!isEditingPreset && !editingPreset.name) || loading">
            {{ loading ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, reactive, watch, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useToast } from '../composables/useToast'
import { GetAmagiSettings, SaveAmagiModelPreset, DeleteAmagiModelPreset, GetAmagiSettingsJSON, SaveAmagiSettingsJSON, SetAmagiModel, SetAmagiEffortLevel } from '../../wailsjs/go/main/App'
import { amagi } from '../../wailsjs/go/models'

const router = useRouter()
const { showSuccess, showError } = useToast()
const loading = ref(false)

// View tabs
const viewTabs = [
  { key: 'form', label: '表单视图' },
  { key: 'json', label: 'JSON 视图' }
]
const activeView = ref('form')

// Settings data - using local interface with camelCase for Vue template binding
interface LocalProviderConfig {
  name: string
  protocol: 'anthropic' | 'openai'
  baseURL: string
}

interface LocalThinkingConfig {
  type: 'disabled' | 'enabled' | ''
  budgetTokens?: number
}

interface LocalModelPreset {
  name?: string
  provider?: string
  model?: string
  temperature?: number
  maxTokens?: number
  thinking?: LocalThinkingConfig
}

interface LocalAmagiSettings {
  model: string
  providers: Record<string, LocalProviderConfig>
  availableModels: string[]
  modelPresets: Record<string, LocalModelPreset>
  alwaysThinkingEnabled: boolean
  effortLevel: 'low' | 'medium' | 'high' | 'max'
  advisorModel: string
}

const settings = reactive<LocalAmagiSettings>({
  model: '',
  providers: {},
  availableModels: [],
  modelPresets: {},
  alwaysThinkingEnabled: false,
  effortLevel: 'medium',
  advisorModel: ''
})

// Helper: Convert amagi.AmagiSettings to LocalAmagiSettings
function fromBackendSettings(backend: amagi.AmagiSettings): LocalAmagiSettings {
  return {
    model: backend.model || '',
    providers: Object.fromEntries(
      Object.entries(backend.providers || {}).map(([key, p]) => [
        key,
        { name: key, protocol: p.protocol as any, base_url: p.base_url || '', baseURL: p.base_url || '' }
      ])
    ),
    availableModels: backend.available_models || [],
    modelPresets: Object.fromEntries(
      Object.entries(backend.model_presets || {}).map(([key, p]) => [
        key,
        {
          provider: p.provider,
          model: p.model,
          temperature: p.temperature,
          maxTokens: p.max_tokens,
          thinking: p.thinking ? {
            type: p.thinking.type as any,
            budgetTokens: p.thinking.budget_tokens
          } : undefined
        }
      ])
    ),
    alwaysThinkingEnabled: backend.always_thinking_enabled || false,
    effortLevel: (backend.effort_level || 'medium') as any,
    advisorModel: backend.advisor_model || ''
  }
}

// Helper: Convert LocalModelPreset to amagi.AmagiModelPreset
function toBackendPreset(local: LocalModelPreset): amagi.AmagiModelPreset {
  return new amagi.AmagiModelPreset({
    provider: local.provider || '',
    model: local.model || '',
    temperature: local.temperature,
    max_tokens: local.maxTokens,
    thinking: local.thinking?.type ? new amagi.AmagiThinking({
      type: local.thinking.type,
      budget_tokens: local.thinking.budgetTokens
    }) : undefined
  })
}

const selectedAvailableModel = ref('')

// JSON Editor
const jsonContent = ref('')
const jsonError = ref('')
const jsonWarning = ref('')

// Preset Dialog
const showPresetDialog = ref(false)
const isEditingPreset = ref(false)
const editingPreset = ref<LocalModelPreset & { name?: string }>({})
const editingPresetName = ref('')
const editingThinkingType = ref('')
const editingThinkingBudget = ref<number | undefined>(undefined)

// Whitelist fields for JSON validation
const whitelistFields = new Set([
  'model', 'providers', 'availableModels', 'modelOverrides',
  'modelCapabilityOverrides', 'modelPresets', 'alwaysThinkingEnabled',
  'effortLevel', 'advisorModel'
])

// Apply selected model from available models
function applyAvailableModel() {
  if (selectedAvailableModel.value) {
    settings.model = selectedAvailableModel.value
  }
}

// Add new provider
function addProvider() {
  const key = `provider_${Date.now()}`
  settings.providers[key] = {
    name: `Provider ${Object.keys(settings.providers).length + 1}`,
    protocol: 'anthropic',
    baseURL: ''
  }
}

// Remove provider
function removeProvider(key: string) {
  if (confirm('确定要删除此提供商吗？')) {
    delete settings.providers[key]
  }
}

// Open add preset dialog
function openAddPresetDialog() {
  isEditingPreset.value = false
  editingPreset.value = {}
  editingPresetName.value = ''
  editingThinkingType.value = ''
  editingThinkingBudget.value = undefined
  showPresetDialog.value = true
}

// Open edit preset dialog
function openEditPresetDialog(name: string, preset: LocalModelPreset) {
  isEditingPreset.value = true
  editingPresetName.value = name
  editingPreset.value = { ...preset }
  editingThinkingType.value = preset.thinking?.type || ''
  editingThinkingBudget.value = preset.thinking?.budgetTokens
  showPresetDialog.value = true
}

// Save preset
async function savePreset() {
  const nameToSave = isEditingPreset.value ? editingPresetName.value : editingPreset.value.name
  if (!nameToSave) {
    showError('请输入预设名称')
    return
  }

  // Apply thinking config
  if (editingThinkingType.value) {
    editingPreset.value.thinking = {
      type: editingThinkingType.value as 'disabled' | 'enabled',
      budgetTokens: editingThinkingType.value === 'enabled' ? editingThinkingBudget.value : undefined
    }
  } else {
    editingPreset.value.thinking = undefined
  }

  try {
    loading.value = true

    await SaveAmagiModelPreset(nameToSave, toBackendPreset(editingPreset.value))

    // Update local state
    settings.modelPresets[nameToSave] = { ...editingPreset.value }
    delete settings.modelPresets[nameToSave].name

    showPresetDialog.value = false
    showSuccess(isEditingPreset.value ? '预设已更新' : '预设已添加')
  } catch (err) {
    console.error('Failed to save preset:', err)
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

// Delete preset
async function deletePreset(name: string) {
  if (confirm(`确定要删除预设 "${name}" 吗？`)) {
    try {
      loading.value = true

      await DeleteAmagiModelPreset(name)

      delete settings.modelPresets[name]
      showSuccess('预设已删除')
    } catch (err) {
      console.error('Failed to delete preset:', err)
      showError('删除失败: ' + err)
    } finally {
      loading.value = false
    }
  }
}

// Load JSON data
async function loadJsonData() {
  try {
    jsonContent.value = await GetAmagiSettingsJSON()
    validateJson()
  } catch (err) {
    console.error('Failed to load JSON:', err)
    showError('加载失败: ' + err)
  }
}

// Validate JSON
function validateJson() {
  if (!jsonContent.value.trim()) {
    jsonError.value = ''
    jsonWarning.value = ''
    return
  }

  try {
    const parsed = JSON.parse(jsonContent.value)
    jsonError.value = ''

    // Check for non-whitelisted fields
    const fields = Object.keys(parsed)
    const nonWhitelist = fields.filter(f => !whitelistFields.has(f))
    if (nonWhitelist.length > 0) {
      jsonWarning.value = `检测到非白名单字段: ${nonWhitelist.join(', ')}。这些字段将被忽略。`
    } else {
      jsonWarning.value = ''
    }
  } catch (err: any) {
    jsonError.value = err.message || '无效的 JSON'
    jsonWarning.value = ''
  }
}

// Save JSON data
async function saveJsonData() {
  validateJson()
  if (jsonError.value) return

  try {
    loading.value = true

    await SaveAmagiSettingsJSON(jsonContent.value)

    // Reload settings
    await loadSettings()
    showSuccess('JSON 已保存')
  } catch (err) {
    console.error('Failed to save JSON:', err)
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

// Save all settings
async function saveAllSettings() {
  try {
    loading.value = true

    await SetAmagiModel(settings.model)
    await SetAmagiEffortLevel(settings.effortLevel)

    showSuccess('设置已保存')
  } catch (err) {
    console.error('Failed to save settings:', err)
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

// Load settings from backend
async function loadSettings() {
  try {
    const data = await GetAmagiSettings()
    const local = fromBackendSettings(data)
    Object.assign(settings, local)
  } catch (err) {
    console.error('Failed to load settings:', err)
    showError('加载设置失败: ' + err)
  }
}

// Watch json content changes
watch(jsonContent, () => {
  validateJson()
})

// Watch active view changes
watch(activeView, (newView) => {
  if (newView === 'json') {
    loadJsonData()
  }
})

onMounted(async () => {
  await loadSettings()
})
</script>

<style scoped>
.amagi-settings-page {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 8px;
}

.header-left {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.page-title {
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  color: #e0e6ed;
}

.page-description {
  margin: 0;
  font-size: 14px;
  color: #8899aa;
}

.header-actions {
  display: flex;
  gap: 12px;
}

.view-tabs {
  display: flex;
  gap: 8px;
  border-bottom: 1px solid #2a2f3e;
  padding-bottom: 0;
  margin-bottom: 24px;
}

.view-tab {
  padding: 10px 20px;
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  color: #8899aa;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  font-family: inherit;
}

.view-tab:hover {
  color: #ccd6e0;
  background: rgba(255, 255, 255, 0.03);
}

.view-tab.active {
  color: #4fc3f7;
  border-bottom-color: #4fc3f7;
}

.form-view {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.card {
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  padding: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.card-header h2 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: #e0e6ed;
}

.card-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.form-group label {
  color: #8899aa;
  font-size: 14px;
  font-weight: 500;
}

.form-row {
  display: flex;
  gap: 24px;
}

.flex-1 {
  flex: 1;
}

.input-field {
  width: 100%;
  background: #0f1219;
  border: 1px solid #2a2f3e;
  color: #e0e6ed;
  padding: 10px 12px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  transition: all 0.15s ease;
  outline: none;
  box-sizing: border-box;
}

.input-field:focus {
  border-color: #4fc3f7;
}

.model-input-group {
  display: flex;
  gap: 8px;
}

.model-select {
  max-width: 300px;
}

.ios-toggle {
  position: relative;
  width: 44px;
  height: 24px;
  background: #2a2f3e;
  border-radius: 24px;
  cursor: pointer;
  transition: background 0.2s ease;
  border: none;
  outline: none;
  align-self: flex-start;
}

.ios-toggle.active {
  background: #4fc3f7;
}

.ios-toggle::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 20px;
  height: 20px;
  background: #fff;
  border-radius: 50%;
  transition: transform 0.2s cubic-bezier(0.25, 0.8, 0.25, 1);
  box-shadow: 0 2px 4px rgba(0,0,0,0.2);
}

.ios-toggle.active::after {
  transform: translateX(20px);
}

.provider-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.provider-card {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  padding: 16px;
}

.provider-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}

.provider-name-input {
  flex: 1;
}

.provider-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.protocol-select {
  width: 140px;
}

.provider-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.api-key-notice {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 14px;
  background: rgba(79, 195, 247, 0.06);
  border: 1px solid rgba(79, 195, 247, 0.2);
  border-radius: 6px;
}

.notice-text {
  color: #a0d8ef;
  font-size: 13px;
}

.link-text {
  color: #4fc3f7;
  text-decoration: none;
  font-size: 13px;
  font-weight: 600;
}

.link-text:hover {
  text-decoration: underline;
}

.presets-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 16px;
}

.preset-card {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  padding: 16px;
  cursor: pointer;
  transition: all 0.15s ease;
}

.preset-card:hover {
  border-color: #3a4f5e;
  background: #141822;
}

.preset-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 12px;
  padding-bottom: 12px;
  border-bottom: 1px solid #2a2f3e;
}

.preset-name {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: #e0e6ed;
}

.preset-actions {
  display: flex;
  gap: 4px;
}

.preset-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.info-row {
  display: flex;
  font-size: 13px;
}

.info-row .label {
  color: #8899aa;
  min-width: 60px;
}

.info-row .value {
  color: #e0e6ed;
}

.params-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.param-badge {
  background: rgba(90, 106, 122, 0.2);
  color: #8899aa;
  padding: 3px 8px;
  border-radius: 4px;
  font-size: 11px;
  border: 1px solid #2a2f3e;
}

.empty-state {
  text-align: center;
  padding: 32px;
  background: #0f1219;
  border: 1px dashed #2a2f3e;
  border-radius: 8px;
}

.muted {
  color: #5a6a7a;
  font-size: 14px;
}

/* JSON View */
.json-view {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.json-editor-wrapper {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.json-textarea {
  width: 100%;
  min-height: 400px;
  max-height: 55vh;
  background: #0a0d14;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  color: #c9e0f0;
  font-family: 'Consolas', 'Monaco', 'Courier New', monospace;
  font-size: 13px;
  line-height: 1.6;
  padding: 12px;
  resize: vertical;
  outline: none;
  box-sizing: border-box;
  transition: border-color 0.15s ease;
  tab-size: 2;
}

.json-textarea:focus {
  border-color: #4fc3f7;
}

.json-status {
  font-size: 13px;
  padding: 4px 0;
  min-height: 20px;
  color: #5a6a7a;
}

.json-status.success {
  color: #66bb6a;
}

.json-status.error {
  color: #ef5350;
}

.json-warning {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  background: rgba(255, 167, 38, 0.1);
  border: 1px solid rgba(255, 167, 38, 0.3);
  border-radius: 6px;
  color: #ffb74d;
  font-size: 13px;
}

.warning-icon {
  font-size: 16px;
}

.json-actions {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 8px;
}

/* Dialog */
.dialog-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(15, 18, 25, 0.8);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
  backdrop-filter: blur(4px);
}

.dialog {
  width: 100%;
  max-width: 520px;
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
}

.preset-dialog {
  padding: 0;
}

.preset-dialog h2 {
  margin: 0;
  padding: 20px;
  border-bottom: 1px solid #2a2f3e;
  color: #e0e6ed;
}

.dialog-scroll-area {
  padding: 20px;
  overflow-y: auto;
  flex: 1;
}

.form-grid-2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

.section-subtitle {
  margin: 20px 0 12px 0;
  font-size: 14px;
  color: #4fc3f7;
  border-bottom: 1px dashed #2a2f3e;
  padding-bottom: 6px;
}

.dialog-actions {
  padding: 20px;
  border-top: 1px solid #2a2f3e;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
}

/* Buttons */
.btn {
  padding: 10px 20px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  border: none;
  outline: none;
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn.primary {
  background: #4fc3f7;
  color: #0f1219;
}

.btn.primary:hover:not(:disabled) {
  background: #7bd4f9;
}

.btn.secondary {
  background: transparent;
  color: #e0e6ed;
  border: 1px solid #2a2f3e;
}

.btn.secondary:hover:not(:disabled) {
  border-color: #5a6a7a;
  background: rgba(255, 255, 255, 0.05);
}

.btn-icon {
  background: transparent;
  border: none;
  cursor: pointer;
  padding: 6px;
  border-radius: 4px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.15s ease;
  color: #8899aa;
}

.btn-icon:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #e0e6ed;
}

.btn-icon.danger:hover {
  background: rgba(239, 83, 80, 0.1);
  color: #ef5350;
}
</style>
