<template>
  <div class="provider-detail">
    <div class="breadcrumb" v-if="showBreadcrumb">
      <a href="#" @click.prevent="$emit('back')" class="back-link">服务提供商</a>
      <span class="separator">/</span>
      <span class="current">{{ providerName }}</span>
    </div>

    <!-- Basic Info Card -->
    <div class="card provider-info">
      <div class="card-header">
        <h2>基本信息</h2>
        <button class="btn primary" @click="handleSaveProvider" :disabled="loading">
          {{ loading ? '保存中...' : '保存修改' }}
        </button>
      </div>
      <div class="card-body" v-if="provider">
        <div class="form-grid">
          <div class="form-group">
            <label>默认模型</label>
            <input type="text" v-model="provider.default_model" class="input-field" />
          </div>
          <div class="form-group">
            <label>当前有效基础 URL</label>
            <div class="effective-url-display">
              <template v-if="effectiveUrlSummary">
                <div v-for="item in effectiveUrlSummary" :key="item.label" class="url-line">
                  <span class="url-label">{{ item.label }}</span>
                  <span class="url-value">{{ item.url || '未设置' }}</span>
                </div>
              </template>
              <span v-else class="url-hint">请先在下方启用并配置格式卡片</span>
            </div>
          </div>
        </div>

        <!-- Unified API Key Section -->
        <div class="api-key-section">
          <div class="key-header">
            <label>API 密钥</label>
            <span :class="['status-badge', apiKeyState.hasKey ? 'status-configured' : 'status-unconfigured']">
              {{ apiKeyState.hasKey ? '已配置' : '未配置' }}
            </span>
          </div>
          <div v-if="apiKeyState.hasKey && !apiKeyState.isEditing" class="key-display">
            <span class="key-text">{{ apiKeyState.isVisible ? apiKeyState.actualKey : maskedApiKey }}</span>
            <div class="key-actions">
              <button class="btn secondary btn-small" @click="toggleKeyVisibility" :disabled="loading">
                {{ apiKeyState.isVisible ? '隐藏' : '显示' }}
              </button>
              <button class="btn secondary btn-small" @click="apiKeyState.isEditing = true; apiKeyState.inputVisible = true" :disabled="loading">
                编辑
              </button>
              <template v-if="apiKeyState.isConfirmingDelete">
                <span class="confirm-text">确认删除？</span>
                <button class="btn danger-outline btn-small" @click="deleteApiKey" :disabled="loading">确认</button>
                <button class="btn secondary btn-small" @click="apiKeyState.isConfirmingDelete = false">取消</button>
              </template>
              <button v-else class="btn danger-outline btn-small" @click="apiKeyState.isConfirmingDelete = true" :disabled="loading">
                删除
              </button>
            </div>
          </div>
          <div class="key-input-row" v-else>
            <input
              :type="apiKeyState.inputVisible ? 'text' : 'password'"
              v-model="apiKeyState.inputValue"
              class="input-field"
              placeholder="输入 API 密钥"
            />
            <button class="btn secondary btn-small" @click="apiKeyState.inputVisible = !apiKeyState.inputVisible">
              {{ apiKeyState.inputVisible ? '隐藏' : '明文' }}
            </button>
            <button class="btn primary" @click="saveApiKey" :disabled="!apiKeyState.inputValue || loading">
              {{ loading ? '...' : '保存' }}
            </button>
            <button v-if="apiKeyState.isEditing" class="btn secondary" @click="resetKeyState">取消</button>
          </div>
        </div>
      </div>
    </div>

    <!-- Anthropic Format Card -->
    <div class="card format-card" v-if="provider">
      <div class="card-header">
        <div class="card-title-group">
          <h2>Anthropic 格式</h2>
          <span class="format-badge anthropic-badge">A</span>
        </div>
        <button class="ios-toggle" :class="{ active: anthropicEnabled }" @click="anthropicEnabled = !anthropicEnabled"></button>
      </div>
      <div class="card-body" v-if="anthropicEnabled">
        <div class="form-group">
          <label>Base URL</label>
          <input type="text" v-model="anthropicBaseUrl" class="input-field" placeholder="https://api.anthropic.com" />
        </div>
      </div>
    </div>

    <!-- OpenAI Format Card -->
    <div class="card format-card" v-if="provider">
      <div class="card-header">
        <div class="card-title-group">
          <h2>OpenAI 格式</h2>
          <span class="format-badge openai-badge">O</span>
        </div>
        <button class="ios-toggle" :class="{ active: openaiEnabled }" @click="openaiEnabled = !openaiEnabled"></button>
      </div>
      <div class="card-body" v-if="openaiEnabled">
        <div class="form-group">
          <label>Base URL</label>
          <input type="text" v-model="openaiBaseUrl" class="input-field" placeholder="https://api.openai.com/v1" />
        </div>
        <div class="form-group">
          <label>Organization (可选)</label>
          <input type="text" v-model="openaiOrg" class="input-field" placeholder="org-..." />
        </div>
      </div>
    </div>

    <!-- JSON Editor Dialog -->
    <div class="dialog-overlay" v-if="showJsonEditor" @click.self="showJsonEditor = false">
      <div class="dialog card json-editor-dialog">
        <h2>JSON 编辑 - {{ providerName }}</h2>
        <div class="json-editor-body">
          <textarea
            v-model="jsonEditorContent"
            class="json-textarea"
            spellcheck="false"
            placeholder="加载中..."
          ></textarea>
          <div class="json-status" :class="{ error: !!jsonError, success: !jsonError && jsonEditorContent }">
            <span v-if="!jsonEditorContent"></span>
            <span v-else-if="jsonError">语法错误: {{ jsonError }}</span>
            <span v-else>JSON 语法正确</span>
          </div>
        </div>
        <div class="dialog-actions">
          <button class="btn secondary" @click="showJsonEditor = false" :disabled="loading">取消</button>
          <button class="btn primary" @click="handleSaveJson" :disabled="!!jsonError || !jsonEditorContent || loading">
            {{ loading ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>

    <!-- JSON Editor Toggle -->
    <div class="json-toggle-row" v-if="provider">
      <button class="btn secondary" @click="openJsonEditor">JSON 编辑</button>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, ref, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useToast } from '../composables/useToast'
import { GetProvider, SaveProvider } from '../../wailsjs/go/config/ConfigService'
import { GetProviderExportJSON, SaveProviderFromJSON } from '../../wailsjs/go/main/App'
import { HasAPIKey, GetAPIKey, SetAPIKey, DeleteAPIKey, Save as SaveSecrets } from '../../wailsjs/go/secrets/SecretsService'
import { config } from '../../wailsjs/go/models'

const props = withDefaults(defineProps<{
  providerName: string
  showBreadcrumb?: boolean
}>(), {
  showBreadcrumb: true
})

const emit = defineEmits<{
  (e: 'back'): void
  (e: 'saved'): void
}>()

const route = useRoute()
const { showSuccess, showError } = useToast()
const loading = ref(false)
const provider = ref<any>(null)

// Dual-format reactive accessors
const anthropicEnabled = computed({
  get: () => !!(provider.value?.anthropic?.enabled),
  set: (val: boolean) => {
    if (!provider.value) return
    if (!provider.value.anthropic) provider.value.anthropic = {}
    provider.value.anthropic.enabled = val
  }
})
const anthropicBaseUrl = computed({
  get: () => (provider.value?.anthropic?.base_url as string) || '',
  set: (val: string) => {
    if (!provider.value) return
    if (!provider.value.anthropic) provider.value.anthropic = {}
    provider.value.anthropic.base_url = val
  }
})

const openaiEnabled = computed({
  get: () => !!(provider.value?.openai?.enabled),
  set: (val: boolean) => {
    if (!provider.value) return
    if (!provider.value.openai) provider.value.openai = {}
    provider.value.openai.enabled = val
  }
})
const openaiBaseUrl = computed({
  get: () => (provider.value?.openai?.base_url as string) || '',
  set: (val: string) => {
    if (!provider.value) return
    if (!provider.value.openai) provider.value.openai = {}
    provider.value.openai.base_url = val
  }
})
const openaiOrg = computed({
  get: () => (provider.value?.openai?.organization as string) || '',
  set: (val: string) => {
    if (!provider.value) return
    if (!provider.value.openai) provider.value.openai = {}
    provider.value.openai.organization = val
  }
})

// Effective URL summary (read-only, derived from format cards)
const effectiveUrlSummary = computed(() => {
  if (!provider.value) return null
  const items: { label: string; url: string }[] = []
  const aOn = !!(provider.value.anthropic?.enabled)
  const oOn = !!(provider.value.openai?.enabled)
  if (aOn) items.push({ label: 'Anthropic', url: provider.value.anthropic?.base_url || '' })
  if (oOn) items.push({ label: 'OpenAI', url: provider.value.openai?.base_url || '' })
  return items.length > 0 ? items : null
})

// Unified API Key state (single key per provider)
function makeKeyState() {
  return {
    hasKey: false,
    isVisible: false,
    isEditing: false,
    isConfirmingDelete: false,
    inputVisible: false,
    actualKey: '',
    inputValue: '',
    storageKey: '', // actual key name used (providerName or legacy format)
  }
}
const apiKeyState = ref(makeKeyState())

const maskedApiKey = computed(() => {
  const k = apiKeyState.value.actualKey
  return k ? `••••••••${k.slice(-4)}` : '••••'
})

// JSON Editor State
const showJsonEditor = ref(false)
const jsonEditorContent = ref('')
const jsonError = ref('')

const loadProvider = async () => {
  try {
    provider.value = await GetProvider(props.providerName)
    // Auto-enable format based on existing type if dual-format not yet set
    const p = provider.value as any
    if (p.anthropic === undefined && p.openai === undefined) {
      // Legacy provider: infer format from type/auth_key
      const isOpenAI = (p.type || '').toLowerCase() === 'openai' || p.auth_key === 'OPENAI_API_KEY'
      if (isOpenAI) {
        if (!p.openai) p.openai = {}
        p.openai.enabled = true
        p.openai.base_url = p.base_url || ''
      } else {
        if (!p.anthropic) p.anthropic = {}
        p.anthropic.enabled = true
        p.anthropic.base_url = p.base_url || ''
      }
    }
    await loadKeys()
  } catch (err) {
    console.error('Failed to load provider:', err)
    showError('加载失败: ' + err)
    emit('back')
  }
}

const loadKeys = async () => {
  const name = props.providerName

  // Unified key: check providerName first, then fallback to legacy format-specific keys.
  // The fallback is purely for display compatibility -- it shows the legacy key so the user
  // is aware it exists, but saveApiKey/deleteApiKey will always converge to the unified key
  // and clean up any legacy entries.
  const kState = makeKeyState()
  const hasMainKey = await HasAPIKey(name)
  if (hasMainKey) {
    kState.hasKey = true
    kState.storageKey = name
    kState.actualKey = await GetAPIKey(name)
  } else {
    // Compatibility fallback: read legacy format-specific keys for display only.
    // storageKey records which legacy slot was found, so the user can see/delete it.
    const hasAk = await HasAPIKey(name + ':anthropic')
    if (hasAk) {
      kState.hasKey = true
      kState.storageKey = name + ':anthropic'
      kState.actualKey = await GetAPIKey(name + ':anthropic')
    } else {
      const hasOk = await HasAPIKey(name + ':openai')
      if (hasOk) {
        kState.hasKey = true
        kState.storageKey = name + ':openai'
        kState.actualKey = await GetAPIKey(name + ':openai')
      }
    }
  }
  apiKeyState.value = kState
}

// Key management helpers
function resetKeyState() {
  apiKeyState.value.inputValue = ''
  apiKeyState.value.inputVisible = false
  apiKeyState.value.isEditing = false
}

async function toggleKeyVisibility() {
  if (!apiKeyState.value.isVisible) {
    const sk = apiKeyState.value.storageKey || props.providerName
    try { apiKeyState.value.actualKey = await GetAPIKey(sk) } catch { /* ignore */ }
    apiKeyState.value.isVisible = true
  } else {
    apiKeyState.value.isVisible = false
  }
}

async function saveApiKey() {
  if (!apiKeyState.value.inputValue) return
  loading.value = true
  try {
    // Write unified key
    await SetAPIKey(props.providerName, apiKeyState.value.inputValue)
    await SaveSecrets()

    // Cleanup legacy format-specific keys (best-effort, do not block main flow)
    for (const suffix of [':anthropic', ':openai']) {
      try { await DeleteAPIKey(props.providerName + suffix) } catch { /* key may not exist */ }
    }
    try { await SaveSecrets() } catch { /* ignore */ }

    apiKeyState.value.hasKey = true
    apiKeyState.value.actualKey = apiKeyState.value.inputValue
    apiKeyState.value.storageKey = props.providerName
    resetKeyState()
    showSuccess('API 密钥已保存')
    emit('saved')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function deleteApiKey() {
  loading.value = true
  try {
    // Delete unified key + all legacy format-specific keys
    const keysToDelete = [
      props.providerName,
      props.providerName + ':anthropic',
      props.providerName + ':openai',
    ]
    for (const key of keysToDelete) {
      try { await DeleteAPIKey(key) } catch { /* key may not exist */ }
    }
    await SaveSecrets()
    apiKeyState.value = makeKeyState()
    showSuccess('API 密钥已删除')
    emit('saved')
  } catch (err) {
    showError('删除失败: ' + err)
  } finally {
    loading.value = false
  }
}

const handleSaveProvider = async () => {
  if (!provider.value) return
  loading.value = true
  try {
    await SaveProvider(props.providerName, provider.value)
    showSuccess('保存成功')
    emit('saved')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

// JSON Editor
const openJsonEditor = async () => {
  jsonEditorContent.value = ''
  jsonError.value = ''
  showJsonEditor.value = true
  try {
    const json = await GetProviderExportJSON(props.providerName)
    jsonEditorContent.value = json
  } catch (err) {
    showError('加载 JSON 失败: ' + err)
    showJsonEditor.value = false
  }
}

const validateJson = () => {
  if (!jsonEditorContent.value.trim()) { jsonError.value = ''; return }
  try { JSON.parse(jsonEditorContent.value); jsonError.value = '' }
  catch (err: any) { jsonError.value = err.message || '无效的 JSON' }
}

const handleSaveJson = async () => {
  validateJson()
  if (jsonError.value) return
  loading.value = true
  try {
    await SaveProviderFromJSON(props.providerName, jsonEditorContent.value)
    showJsonEditor.value = false
    await loadProvider()
    showSuccess('JSON 保存成功')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

watch(jsonEditorContent, () => validateJson())

onMounted(async () => {
  if (props.providerName) {
    await loadProvider()
  } else {
    emit('back')
  }
})

watch(() => props.providerName, async (newName) => {
  if (newName) {
    await loadProvider()
  }
})
</script>

<style scoped>
.provider-detail {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.breadcrumb {
  font-size: 16px;
  color: #8899aa;
  display: flex;
  align-items: center;
  gap: 8px;
}

.back-link {
  color: #4fc3f7;
  text-decoration: none;
  transition: color 0.15s ease;
  cursor: pointer;
}
.back-link:hover {
  color: #7bd4f9;
  text-decoration: underline;
}

.current {
  color: #e0e6ed;
  font-weight: 600;
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

.card-title-group {
  display: flex;
  align-items: center;
  gap: 12px;
}

.card-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.format-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 4px;
  font-size: 13px;
  font-weight: 700;
}

.anthropic-badge {
  background: rgba(230, 126, 34, 0.15);
  color: #e67e22;
}

.openai-badge {
  background: rgba(16, 163, 127, 0.15);
  color: #10a37f;
}

.form-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 20px;
}

.form-group {
  margin-bottom: 16px;
}

.form-group label {
  display: block;
  margin-bottom: 8px;
  color: #8899aa;
  font-size: 14px;
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

.effective-url-display {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  padding: 10px 12px;
  min-height: 38px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.url-line {
  display: flex;
  align-items: center;
  gap: 10px;
}

.url-label {
  font-size: 12px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 4px;
  white-space: nowrap;
}

.url-line:nth-child(1) .url-label {
  background: rgba(230, 126, 34, 0.15);
  color: #e67e22;
}

.url-line:nth-child(2) .url-label {
  background: rgba(16, 163, 127, 0.15);
  color: #10a37f;
}

.url-value {
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 14px;
  color: #c9e0f0;
  word-break: break-all;
}

.url-hint {
  font-size: 13px;
  color: #5a6a7a;
  font-style: italic;
}

.api-key-section {
  margin-top: 8px;
  padding-top: 12px;
  border-top: 1px solid #2a2f3e;
}

.key-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}
.key-header label {
  margin: 0;
  color: #8899aa;
  font-size: 14px;
}

.status-badge {
  font-size: 12px;
  padding: 4px 8px;
  border-radius: 4px;
  font-weight: 600;
}
.status-configured {
  background-color: rgba(102, 187, 106, 0.1);
  color: #66bb6a;
  border: 1px solid rgba(102, 187, 106, 0.2);
}
.status-unconfigured {
  background-color: rgba(255, 167, 38, 0.1);
  color: #ffa726;
  border: 1px solid rgba(255, 167, 38, 0.2);
}

.key-display {
  display: flex;
  align-items: center;
  gap: 12px;
  background-color: #0f1219;
  padding: 12px;
  border-radius: 6px;
  border: 1px solid #2a2f3e;
  flex-wrap: wrap;
}

.key-text {
  font-family: monospace;
  font-size: 14px;
  color: #e0e6ed;
  flex: 1;
  word-break: break-all;
}

.key-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  flex-wrap: wrap;
}

.confirm-text {
  font-size: 12px;
  color: #ffa726;
  font-weight: 600;
  white-space: nowrap;
}

.key-input-row {
  display: flex;
  gap: 12px;
  align-items: center;
}

.json-toggle-row {
  display: flex;
  justify-content: flex-end;
}

/* Buttons */
.btn {
  padding: 8px 16px;
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
.btn.danger-outline {
  background: transparent;
  color: #ef5350;
  border: 1px solid #ef5350;
}
.btn.danger-outline:hover:not(:disabled) {
  background-color: rgba(239, 83, 80, 0.1);
}
.btn-small {
  padding: 6px 12px;
  font-size: 12px;
}

/* Toggle */
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
  flex-shrink: 0;
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

/* JSON Editor Dialog */
.dialog-overlay {
  position: fixed;
  top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(15, 18, 25, 0.8);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
  backdrop-filter: blur(4px);
}
.dialog {
  width: 100%;
  max-width: 720px;
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
}
.json-editor-dialog h2 {
  margin: 0;
  padding: 20px;
  border-bottom: 1px solid #2a2f3e;
  color: #e0e6ed;
  font-size: 18px;
}
.json-editor-body {
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
  overflow: hidden;
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
.json-status.success { color: #66bb6a; }
.json-status.error { color: #ef5350; }
.dialog-actions {
  padding: 20px;
  border-top: 1px solid #2a2f3e;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
}

@media (max-width: 768px) {
  .key-input-row {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
