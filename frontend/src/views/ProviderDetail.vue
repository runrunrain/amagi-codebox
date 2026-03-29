<template>
  <div class="provider-detail">
    <div class="breadcrumb">
      <a href="#" @click.prevent="router.push('/providers')" class="back-link">服务提供商</a>
      <span class="separator">/</span>
      <span class="current">{{ name }}</span>
    </div>

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
            <label>基础 URL (Base URL)</label>
            <div class="url-input-group">
              <div class="url-autocomplete-wrapper">
                <el-autocomplete
                  v-model="provider.base_url"
                  :fetch-suggestions="queryUrlHistory"
                  placeholder="输入或选择 Base URL"
                  :loading="urlHistoryLoading"
                  class="url-autocomplete"
                  @select="onBaseUrlSelect"
                  @blur="onBaseUrlBlur"
                  :debounce="0"
                  popper-class="url-history-dropdown"
                  clearable
                >
                  <template #default="{ item }">
                    <div class="url-history-item">
                      <span class="url-text">{{ item.value }}</span>
                      <el-icon class="url-delete-btn" @click.stop="handleRemoveUrl(item.value)">
                        <Close />
                      </el-icon>
                    </div>
                  </template>
                  <template #empty>
                    <div class="url-empty-state">
                      <span>暂无历史 URL</span>
                    </div>
                  </template>
                </el-autocomplete>
              </div>
              <button class="btn-secondary url-save-btn" @click="handleSaveBaseUrl" :disabled="loading || !provider.base_url?.trim()">
                保存
              </button>
            </div>
          </div>
          <div class="form-group">
            <label>默认模型</label>
            <input type="text" v-model="provider.default_model" class="input-field" />
          </div>
          <div class="form-group">
            <label>认证密钥类型</label>
            <select v-model="provider.auth_key" class="input-field">
              <option value="ANTHROPIC_API_KEY" v-if="!provider.type || provider.type === 'anthropic'">ANTHROPIC_API_KEY</option>
              <option value="ANTHROPIC_AUTH_TOKEN" v-if="!provider.type || provider.type === 'anthropic'">ANTHROPIC_AUTH_TOKEN</option>
              <option value="OAUTH" v-if="!provider.type || provider.type === 'anthropic'">OAUTH（订阅认证）</option>
              <option value="OPENAI_API_KEY" v-if="provider.type === 'openai'">OPENAI_API_KEY</option>
            </select>
          </div>
        </div>
      </div>
    </div>

    <div class="card api-key-card" v-if="provider">
      <div class="card-header">
        <div class="card-title-group">
          <h2>API 密钥</h2>
          <span :class="['status-badge', apiKeyState.hasKey ? 'status-configured' : 'status-unconfigured']">
            {{ apiKeyState.hasKey ? '已配置' : '未配置' }}
          </span>
        </div>
      </div>
      <div class="card-body">
        <div v-if="provider.auth_key === 'OAUTH'" class="oauth-notice">
          此提供商使用 OAuth 认证，无需配置 API 密钥
        </div>

        <template v-else>
          <div class="key-display" v-if="apiKeyState.hasKey && !apiKeyState.isEditing">
            <span class="key-text">{{ maskedApiKey }}</span>
            <div class="key-actions">
              <button class="btn secondary btn-small" @click="toggleKeyVisibility" :disabled="loading">
                {{ apiKeyState.isVisible ? '隐藏' : '显示' }}
              </button>
              <button class="btn secondary btn-small" @click="startKeyEdit" :disabled="loading">
                编辑
              </button>
              <template v-if="apiKeyState.isConfirmingDelete">
                <span class="confirm-text">确认删除？</span>
                <button class="btn danger-outline btn-small" @click="deleteApiKey" :disabled="loading">
                  {{ loading ? '处理中...' : '确认' }}
                </button>
                <button class="btn secondary btn-small" @click="apiKeyState.isConfirmingDelete = false" :disabled="loading">
                  取消
                </button>
              </template>
              <button v-else class="btn danger-outline btn-small" @click="apiKeyState.isConfirmingDelete = true" :disabled="loading">
                删除
              </button>
            </div>
          </div>

          <div class="form-group api-key-input-group" v-else>
            <label>{{ apiKeyState.isEditing ? 'API 密钥' : '设置 API 密钥' }}</label>
            <div class="key-input-row">
              <input
                :type="apiKeyState.inputVisible ? 'text' : 'password'"
                v-model="apiKeyState.inputValue"
                class="input-field"
                :placeholder="apiKeyState.isEditing ? '输入新的 API 密钥' : '输入 API 密钥'"
              />
              <button class="btn secondary btn-small" @click="apiKeyState.inputVisible = !apiKeyState.inputVisible" :disabled="loading">
                {{ apiKeyState.inputVisible ? '隐藏' : '明文' }}
              </button>
              <button class="btn primary" @click="saveApiKey" :disabled="!apiKeyState.inputValue || loading">
                {{ loading ? '保存中...' : '保存' }}
              </button>
              <button v-if="apiKeyState.isEditing" class="btn secondary" @click="cancelKeyEdit" :disabled="loading">
                取消
              </button>
            </div>
          </div>
        </template>
      </div>
    </div>

    <div class="presets-section">
      <div class="section-header">
        <h2>预设配置</h2>
        <div class="section-header-actions">
          <button class="btn secondary" @click="openJsonEditor">JSON 编辑</button>
          <button class="btn secondary" @click="openAddPresetDialog">添加预设</button>
        </div>
      </div>

      <div class="presets-list">
        <div class="card preset-card" v-for="(preset, presetName) in provider?.presets" :key="presetName">
          <div class="preset-header">
            <h3 class="preset-name">{{ presetName }}</h3>
            <div class="preset-actions">
              <button class="btn-icon" @click="openEditPresetDialog(String(presetName), preset)" title="编辑">
                <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path>
                </svg>
              </button>
              <button class="btn-icon danger" @click="handleDeletePreset(String(presetName))" title="删除">
                <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                  <polyline points="3 6 5 6 21 6"></polyline>
                  <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
                </svg>
              </button>
            </div>
          </div>
          <div class="preset-body">
            <div class="info-row">
              <span class="label">模型:</span>
              <span class="value">{{ preset.model || '继承默认' }}</span>
            </div>
            <div class="params-summary">
              <span class="param-badge" v-if="preset.parameters?.temperature !== undefined">Temp: {{ preset.parameters.temperature }}</span>
              <span class="param-badge" v-if="preset.parameters?.top_p !== undefined">Top P: {{ preset.parameters.top_p }}</span>
              <span class="param-badge" v-if="preset.parameters?.max_tokens !== undefined">Max Tokens: {{ preset.parameters.max_tokens }}</span>
              <span class="param-badge" v-if="preset.parameters?.max_context_length !== undefined">CtxLen: {{ preset.parameters.max_context_length }}</span>
              <span class="param-badge" v-if="preset.parameters?.context_window?.model_context_window" title="Model Context Window">
                Window: {{ formatContextSize(preset.parameters.context_window.model_context_window) }}
              </span>
              <span class="param-badge" v-if="preset.parameters?.context_window?.model_auto_compact_token_limit" title="Auto Compact Limit">
                Compact@: {{ formatContextSize(preset.parameters.context_window.model_auto_compact_token_limit) }}
              </span>
              <span class="param-badge" v-if="preset.parameters?.stream">Stream</span>
              <span class="param-badge" v-if="preset.parameters?.thinking?.type === 'enabled'">Thinking ({{ preset.parameters.thinking.budgetTokens || 'auto' }})</span>
            </div>
          </div>
        </div>
        
        <div v-if="!provider?.presets || Object.keys(provider.presets).length === 0" class="empty-state">
          <p class="muted">暂无预设配置</p>
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

    <!-- Preset Dialog -->
    <div class="dialog-overlay" v-if="showPresetDialog" @click.self="showPresetDialog = false">
      <div class="dialog card preset-dialog">
        <h2>{{ isEditingPreset ? '编辑预设' : '添加预设' }}</h2>
        
        <div class="dialog-scroll-area">
          <div class="form-group" v-if="!isEditingPreset">
            <label>预设名称 (唯一标识)</label>
            <input type="text" v-model="editingPresetName" class="input-field" placeholder="例如: default, coding, writing" />
          </div>
          
          <div class="form-group">
            <label>模型 (留空则使用提供商默认模型)</label>
            <input type="text" v-model="editingPreset.model" class="input-field" placeholder="例如: claude-3-7-sonnet-20250219" />
          </div>

          <h3 class="section-subtitle">参数配置</h3>
          
          <div class="form-grid-2">
            <div class="form-group">
              <label>Temperature (温度)</label>
              <input type="number" v-model.number="editingPreset.parameters.temperature" class="input-field" step="0.1" min="0" max="1" placeholder="默认" />
            </div>
            <div class="form-group">
              <label>Top P</label>
              <input type="number" v-model.number="editingPreset.parameters.top_p" class="input-field" step="0.1" min="0" max="1" placeholder="默认" />
            </div>
            <div class="form-group">
              <label>Max Tokens (最大输出Token数)</label>
              <input type="number" v-model.number="editingPreset.parameters.max_tokens" class="input-field" step="1" min="1" placeholder="默认" />
            </div>
            <div class="form-group">
              <label>Context Window (上下文窗口大小)</label>
              <input type="number" v-model.number="editingPreset.parameters.max_context_length" class="input-field" step="1" min="1" placeholder="默认" />
              <p class="field-hint">设置 Claude Code 的上下文窗口容量（token 数），用于控制自动压缩的触发时机。</p>
            </div>
          </div>

          <h3 class="section-subtitle">上下文窗口高级配置 (Codex CLI 风格)</h3>
          <div class="context-window-config">
            <div class="form-group">
              <label>Model Context Window (上下文窗口大小)</label>
              <div class="input-with-presets">
                <input type="number" v-model.number="contextWindowModel" class="input-field" step="1" min="1" placeholder="默认" />
                <div class="preset-buttons">
                  <button type="button" class="btn-xs" @click="setContextWindow(200000)" title="200K">200K</button>
                  <button type="button" class="btn-xs" @click="setContextWindow(500000)" title="500K">500K</button>
                  <button type="button" class="btn-xs" @click="setContextWindow(1047576)" title="1M">1M</button>
                  <button type="button" class="btn-xs" @click="setContextWindow(2097152)" title="2M">2M</button>
                </div>
              </div>
              <p class="field-hint">设置模型的上下文窗口大小（如 GPT-5.4 的 1047576 = 1M token）</p>
            </div>
            <div class="form-group">
              <label>Auto Compact Token Limit (自动压缩阈值)</label>
              <div class="input-with-presets">
                <input type="number" v-model.number="contextWindowCompact" class="input-field" step="1" min="1" placeholder="默认" />
                <div class="preset-buttons">
                  <button type="button" class="btn-xs" @click="setCompactLimit(100000)" title="100K">100K</button>
                  <button type="button" class="btn-xs" @click="setCompactLimit(105197)" title="105K (GPT-5.4 推荐)">105K</button>
                  <button type="button" class="btn-xs" @click="setCompactLimit(180000)" title="180K">180K</button>
                  <button type="button" class="btn-xs" @click="setCompactLimit(200000)" title="200K">200K</button>
                </div>
              </div>
              <p class="field-hint">当历史上下文达到此 token 数时触发自动压缩（通常设置为窗口大小的 10%-20%）</p>
            </div>
          </div>

          <div class="checkbox-group-inline">
            <label class="checkbox-label">
              <input type="checkbox" v-model="editingPreset.parameters.do_sample" />
              <span class="checkbox-text">Do Sample</span>
            </label>
            <label class="checkbox-label">
              <input type="checkbox" v-model="editingPreset.parameters.stream" />
              <span class="checkbox-text">Stream (流式输出)</span>
            </label>
          </div>

          <h3 class="section-subtitle">思考配置 (Thinking)</h3>
          <div class="form-group">
            <label>思考模式</label>
            <select v-model="thinkingType" class="input-field">
              <option value="">默认 (不配置)</option>
              <option value="disabled">禁用 (Disabled)</option>
              <option value="enabled">启用 (Enabled)</option>
            </select>
          </div>
          
          <div class="form-group" v-if="thinkingType === 'enabled'">
            <label>思考预算 Tokens (Budget Tokens)</label>
            <input type="number" v-model.number="thinkingBudget" class="input-field" step="1" min="1024" placeholder="例如: 16384" />
          </div>
        </div>

        <div class="dialog-actions">
          <button class="btn secondary" @click="showPresetDialog = false" :disabled="loading">取消</button>
          <button class="btn primary" @click="handleSavePreset" :disabled="(!isEditingPreset && !editingPresetName) || loading">
            {{ loading ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, ref, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Close } from '@element-plus/icons-vue'
import { useToast } from '../composables/useToast'
import { GetProvider, SaveProvider, SavePreset, DeletePreset, GetUrlHistory, AddUrlToHistory, RemoveUrlFromHistory } from '../../wailsjs/go/config/ConfigService'
import { GetProviderExportJSON, SaveProviderFromJSON } from '../../wailsjs/go/main/App'
import { HasAPIKey, GetAPIKey, SetAPIKey, DeleteAPIKey, Save as SaveSecrets } from '../../wailsjs/go/secrets/SecretsService'
import { config } from '../../wailsjs/go/models'

const props = defineProps<{
  name: string
}>()

const route = useRoute()
const router = useRouter()
const { showSuccess, showError } = useToast()
const loading = ref(false)
const providerName = props.name || route.params.name as string

const provider = ref<config.Provider | null>(null)
const apiKeyState = ref({
  hasKey: false,
  isVisible: false,
  isEditing: false,
  isConfirmingDelete: false,
  inputVisible: false,
  actualKey: '',
  inputValue: ''
})

// URL History State
const urlHistory = ref<string[]>([])
const urlHistoryLoading = ref(false)
const previousBaseUrl = ref('')

// Preset Dialog State
const showPresetDialog = ref(false)
const isEditingPreset = ref(false)
const editingPresetName = ref('')
const editingPreset = ref(new config.Preset({
  name: '',
  model: '',
  parameters: new config.Parameters({})
}))
const thinkingType = ref('')
const thinkingBudget = ref<number | undefined>(undefined)

// Context Window Config State
const contextWindowModel = ref<number | undefined>(undefined)
const contextWindowCompact = ref<number | undefined>(undefined)

// JSON Editor State
const showJsonEditor = ref(false)
const jsonEditorContent = ref('')
const jsonError = ref('')

const maskedApiKey = computed(() => {
  if (apiKeyState.value.isVisible) {
    return apiKeyState.value.actualKey
  }

  const suffix = apiKeyState.value.actualKey ? apiKeyState.value.actualKey.slice(-4) : '****'
  return `••••••••${suffix}`
})

const loadProvider = async () => {
  try {
    provider.value = await GetProvider(providerName)
    previousBaseUrl.value = provider.value?.base_url || ''
    await loadUrlHistory()
  } catch (err) {
    console.error('Failed to load provider:', err)
    showError('加载失败: ' + err)
    router.push('/providers')
  }
}

const loadApiKey = async () => {
  apiKeyState.value = {
    hasKey: false,
    isVisible: false,
    isEditing: false,
    isConfirmingDelete: false,
    inputVisible: false,
    actualKey: '',
    inputValue: ''
  }

  if (!provider.value || provider.value.auth_key === 'OAUTH') {
    return
  }

  try {
    const hasKey = await HasAPIKey(providerName)
    apiKeyState.value.hasKey = hasKey
    if (hasKey) {
      apiKeyState.value.actualKey = await GetAPIKey(providerName)
    }
  } catch (err) {
    console.error('Failed to load API key:', err)
    showError('加载 API 密钥失败: ' + err)
  }
}

const toggleKeyVisibility = async () => {
  if (!apiKeyState.value.isVisible) {
    try {
      apiKeyState.value.actualKey = await GetAPIKey(providerName)
      apiKeyState.value.isVisible = true
    } catch (err) {
      console.error('Failed to get API key:', err)
      showError('读取 API 密钥失败: ' + err)
    }
    return
  }

  apiKeyState.value.isVisible = false
}

const startKeyEdit = () => {
  apiKeyState.value.inputValue = apiKeyState.value.actualKey
  apiKeyState.value.inputVisible = true
  apiKeyState.value.isEditing = true
  apiKeyState.value.isConfirmingDelete = false
}

const cancelKeyEdit = () => {
  apiKeyState.value.inputValue = ''
  apiKeyState.value.inputVisible = false
  apiKeyState.value.isEditing = false
}

const saveApiKey = async () => {
  if (!apiKeyState.value.inputValue) {
    return
  }

  const wasEditing = apiKeyState.value.isEditing
  loading.value = true
  try {
    await SetAPIKey(providerName, apiKeyState.value.inputValue)
    await SaveSecrets()
    apiKeyState.value.hasKey = true
    apiKeyState.value.actualKey = apiKeyState.value.inputValue
    apiKeyState.value.inputValue = ''
    apiKeyState.value.inputVisible = false
    apiKeyState.value.isEditing = false
    apiKeyState.value.isVisible = false
    apiKeyState.value.isConfirmingDelete = false
    showSuccess(wasEditing ? 'API 密钥已更新' : 'API 密钥已保存')
  } catch (err) {
    console.error('Failed to save API key:', err)
    showError('保存 API 密钥失败: ' + err)
  } finally {
    loading.value = false
  }
}

const deleteApiKey = async () => {
  loading.value = true
  try {
    await DeleteAPIKey(providerName)
    await SaveSecrets()
    apiKeyState.value = {
      hasKey: false,
      isVisible: false,
      isEditing: false,
      isConfirmingDelete: false,
      inputVisible: false,
      actualKey: '',
      inputValue: ''
    }
    showSuccess('API 密钥已删除')
  } catch (err) {
    console.error('Failed to delete API key:', err)
    showError('删除 API 密钥失败: ' + err)
  } finally {
    loading.value = false
  }
}

const loadUrlHistory = async () => {
  urlHistoryLoading.value = true
  try {
    if (typeof GetUrlHistory === 'function') {
      const history = await GetUrlHistory(providerName)
      urlHistory.value = history || []
    } else {
      console.warn('GetUrlHistory API not available yet. Please run wails dev to generate bindings.')
    }
  } catch (err) {
    console.error('Failed to load URL history:', err)
    // Non-critical error, don't show toast
  } finally {
    urlHistoryLoading.value = false
  }
}

const handleSaveProvider = async () => {
  if (!provider.value) return
  loading.value = true
  try {
    await SaveProvider(providerName, provider.value)
    // Add current URL to history if it changed and is not empty
    const currentUrl = provider.value.base_url || ''
    if (currentUrl && currentUrl !== previousBaseUrl.value) {
      try {
        if (typeof AddUrlToHistory === 'function') {
          await AddUrlToHistory(providerName, currentUrl)
          previousBaseUrl.value = currentUrl
          await loadUrlHistory()
        } else {
          console.warn('AddUrlToHistory API not available yet. Please run wails dev to generate bindings.')
        }
      } catch (historyErr) {
        console.error('Failed to add URL to history:', historyErr)
        // Non-critical error, don't fail the save
      }
    }
    await loadApiKey()
    showSuccess('保存成功')
  } catch (err) {
    console.error('Failed to save provider:', err)
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

const openAddPresetDialog = () => {
  isEditingPreset.value = false
  editingPresetName.value = ''
  editingPreset.value = new config.Preset({
    name: '',
    model: '',
    parameters: new config.Parameters({})
  })
  thinkingType.value = ''
  thinkingBudget.value = undefined
  contextWindowModel.value = undefined
  contextWindowCompact.value = undefined
  showPresetDialog.value = true
}

const openEditPresetDialog = (name: string, preset: config.Preset) => {
  isEditingPreset.value = true
  editingPresetName.value = name

  // Deep copy to avoid modifying original before save
  const presetCopy = JSON.parse(JSON.stringify(preset))
  editingPreset.value = new config.Preset(presetCopy)

  if (editingPreset.value.parameters?.thinking) {
    thinkingType.value = editingPreset.value.parameters.thinking.type || ''
    thinkingBudget.value = editingPreset.value.parameters.thinking.budgetTokens
  } else {
    thinkingType.value = ''
    thinkingBudget.value = undefined
  }

  // Load context window config
  if (editingPreset.value.parameters?.context_window) {
    contextWindowModel.value = editingPreset.value.parameters.context_window.model_context_window
    contextWindowCompact.value = editingPreset.value.parameters.context_window.model_auto_compact_token_limit
  } else {
    contextWindowModel.value = undefined
    contextWindowCompact.value = undefined
  }

  showPresetDialog.value = true
}

const handleSavePreset = async () => {
  const nameToSave = isEditingPreset.value ? editingPresetName.value : editingPresetName.value
  if (!nameToSave) return

  // Apply thinking config
  if (!editingPreset.value.parameters) {
    editingPreset.value.parameters = new config.Parameters({})
  }

  if (thinkingType.value) {
    editingPreset.value.parameters.thinking = new config.ThinkingConfig({
      type: thinkingType.value,
      budgetTokens: thinkingType.value === 'enabled' ? thinkingBudget.value : undefined
    })
  } else {
    editingPreset.value.parameters.thinking = undefined
  }

  // Apply context window config
  if (contextWindowModel.value !== undefined || contextWindowCompact.value !== undefined) {
    editingPreset.value.parameters.context_window = {
      model_context_window: contextWindowModel.value,
      model_auto_compact_token_limit: contextWindowCompact.value
    }
  } else {
    editingPreset.value.parameters.context_window = undefined
  }

  // Ensure name is set inside preset object
  editingPreset.value.name = nameToSave

  try {
    loading.value = true
    await SavePreset(providerName, nameToSave, editingPreset.value)
    showPresetDialog.value = false
    await loadProvider()
    showSuccess('保存预设成功')
  } catch (err) {
    console.error('Failed to save preset:', err)
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

const handleDeletePreset = async (presetName: string) => {
  if (confirm(`确定要删除预设 "${presetName}" 吗？`)) {
    try {
      loading.value = true
      await DeletePreset(providerName, presetName)
      await loadProvider()
      showSuccess('删除预设成功')
    } catch (err) {
      console.error('Failed to delete preset:', err)
      showError('删除失败: ' + err)
    } finally {
      loading.value = false
    }
  }
}

const queryUrlHistory = (queryString: string, cb: (results: { value: string }[]) => void) => {
  const results = queryString
    ? urlHistory.value.filter(url => url.toLowerCase().includes(queryString.toLowerCase()))
    : urlHistory.value
  cb(results.map(url => ({ value: url })))
}

const onBaseUrlSelect = (item: { value: string }) => {
  if (provider.value) {
    provider.value.base_url = item.value
  }
}

const onBaseUrlBlur = async () => {
  const currentUrl = provider.value?.base_url?.trim() || ''
  if (currentUrl && currentUrl !== previousBaseUrl.value) {
    try {
      if (typeof AddUrlToHistory === 'function') {
        await AddUrlToHistory(providerName, currentUrl)
        previousBaseUrl.value = currentUrl
        await loadUrlHistory()
      } else {
        console.warn('AddUrlToHistory API not available yet. Please run wails dev to generate bindings.')
      }
    } catch (historyErr) {
      console.error('Failed to add URL to history:', historyErr)
    }
  }
}

const handleBaseUrlChange = (value: string) => {
  // Store the previous value when URL changes
  previousBaseUrl.value = provider.value?.base_url || ''
}

const handleRemoveUrl = async (url: string) => {
  if (!confirm(`确定要从历史记录中删除 "${url}" 吗？`)) {
    return
  }
  try {
    if (typeof RemoveUrlFromHistory === 'function') {
      await RemoveUrlFromHistory(providerName, url)
      // Update local state
      urlHistory.value = urlHistory.value.filter(u => u !== url)
      showSuccess('删除成功')
    } else {
      console.warn('RemoveUrlFromHistory API not available yet. Please run wails dev to generate bindings.')
    }
  } catch (err) {
    console.error('Failed to remove URL from history:', err)
    showError('删除失败: ' + err)
  }
}

const handleSaveBaseUrl = async () => {
  const currentUrl = provider.value?.base_url?.trim() || ''
  if (!currentUrl) {
    showError('请输入 URL')
    return
  }
  try {
    if (typeof AddUrlToHistory === 'function') {
      await AddUrlToHistory(providerName, currentUrl)
      previousBaseUrl.value = currentUrl
      await loadUrlHistory()
      showSuccess('URL 已保存到历史记录')
    } else {
      console.warn('AddUrlToHistory API not available yet. Please run wails dev to generate bindings.')
    }
  } catch (err) {
    console.error('Failed to save URL to history:', err)
    showError('保存失败: ' + err)
  }
}

// Context Window quick preset methods
const setContextWindow = (value: number) => {
  contextWindowModel.value = value
  // Auto-set compact limit to ~10% of context window
  if (!contextWindowCompact.value) {
    contextWindowCompact.value = Math.floor(value * 0.1)
  }
}

const setCompactLimit = (value: number) => {
  contextWindowCompact.value = value
}

// Format context size for display (e.g., 1048576 -> "1M")
const formatContextSize = (tokens: number): string => {
  if (tokens >= 1000000) {
    return `${(tokens / 1000000).toFixed(1)}M`
  } else if (tokens >= 1000) {
    return `${(tokens / 1000).toFixed(0)}K`
  }
  return `${tokens}`
}

// JSON Editor methods
const openJsonEditor = async () => {
  jsonEditorContent.value = ''
  jsonError.value = ''
  showJsonEditor.value = true
  try {
    const json = await GetProviderExportJSON(providerName)
    jsonEditorContent.value = json
  } catch (err) {
    console.error('Failed to get provider JSON:', err)
    showError('加载 JSON 失败: ' + err)
    showJsonEditor.value = false
  }
}

const validateJson = () => {
  if (!jsonEditorContent.value.trim()) {
    jsonError.value = ''
    return
  }
  try {
    JSON.parse(jsonEditorContent.value)
    jsonError.value = ''
  } catch (err: any) {
    jsonError.value = err.message || '无效的 JSON'
  }
}

const handleSaveJson = async () => {
  validateJson()
  if (jsonError.value) return

  loading.value = true
  try {
    await SaveProviderFromJSON(providerName, jsonEditorContent.value)
    showJsonEditor.value = false
    await loadProvider()
    await loadApiKey()
    showSuccess('JSON 保存成功')
  } catch (err) {
    console.error('Failed to save provider from JSON:', err)
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

watch(jsonEditorContent, () => {
  validateJson()
})

onMounted(async () => {
  if (providerName) {
    await loadProvider()
    await loadApiKey()
  } else {
    router.push('/providers')
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

.card-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.card-title-group {
  display: flex;
  align-items: center;
  gap: 12px;
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

.field-hint {
  margin: 6px 0 0;
  color: #5a6a7a;
  font-size: 12px;
  line-height: 1.5;
}

.oauth-notice {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 18px;
  background: rgba(79, 195, 247, 0.06);
  border: 1px solid rgba(79, 195, 247, 0.2);
  border-radius: 8px;
  color: #a0d8ef;
  font-size: 14px;
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

.api-key-input-group {
  margin-bottom: 0;
}

.key-input-row {
  display: flex;
  gap: 12px;
  align-items: center;
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

/* URL Autocomplete Styles */
.url-input-group {
  display: flex;
  gap: 8px;
  align-items: flex-start;
}

.url-autocomplete-wrapper {
  position: relative;
  flex: 1;
}

.url-autocomplete {
  width: 100%;
}

.url-save-btn {
  margin-top: 1px;
  white-space: nowrap;
  padding: 8px 16px;
  height: 38px;
}

.url-autocomplete :deep(.el-input__wrapper) {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  padding: 8px 12px;
  box-shadow: none;
  transition: all 0.15s ease;
}

.url-autocomplete :deep(.el-input__wrapper:hover) {
  border-color: #4fc3f7;
}

.url-autocomplete :deep(.el-input__wrapper.is-focus) {
  border-color: #4fc3f7;
  box-shadow: 0 0 0 1px #4fc3f7;
}

.url-autocomplete :deep(.el-input__inner) {
  color: #e0e6ed;
  font-size: 14px;
  font-family: inherit;
}

.url-autocomplete :deep(.el-input__inner::placeholder) {
  color: #5a6a7a;
}

/* Dropdown Styles */
.url-history-dropdown {
  background-color: #1a1f2e !important;
  border: 1px solid #2a2f3e !important;
  border-radius: 8px !important;
  padding: 8px !important;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4) !important;
}

.url-history-dropdown .el-autocomplete-suggestion__wrap {
  max-height: 300px !important;
}

.url-history-dropdown .el-autocomplete-suggestion__list {
  padding: 0 !important;
}

.url-history-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-radius: 6px;
  transition: background-color 0.15s ease;
}

.url-history-item:hover {
  background-color: rgba(79, 195, 247, 0.1);
}

.url-history-item .url-text {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #e0e6ed;
  font-size: 14px;
  font-family: monospace;
  word-break: break-all;
}

.url-history-item .url-delete-btn {
  color: #5a6a7a;
  font-size: 16px;
  cursor: pointer;
  transition: color 0.15s ease;
  flex-shrink: 0;
  margin-left: 8px;
}

.url-history-item .url-delete-btn:hover {
  color: #ef5350;
}

.url-empty-state {
  padding: 12px 16px;
  color: #8899aa;
  font-size: 14px;
  text-align: center;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}

.section-header h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  color: #e0e6ed;
}

.presets-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(350px, 1fr));
  gap: 20px;
}

.preset-card {
  display: flex;
  flex-direction: column;
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
  font-size: 16px;
  font-weight: 600;
  color: #e0e6ed;
}

.preset-actions {
  display: flex;
  gap: 4px;
}

.info-row {
  display: flex;
  margin-bottom: 12px;
  font-size: 14px;
}

.label {
  color: #8899aa;
  min-width: 60px;
}

.value {
  color: #e0e6ed;
}

.params-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.param-badge {
  background: rgba(90, 106, 122, 0.2);
  color: #8899aa;
  padding: 4px 8px;
  border-radius: 4px;
  font-size: 12px;
  border: 1px solid #2a2f3e;
}

.empty-state {
  grid-column: 1 / -1;
  text-align: center;
  padding: 40px;
  background: #1a1f2e;
  border: 1px dashed #2a2f3e;
  border-radius: 8px;
}

.muted {
  color: #5a6a7a;
}

/* Dialog Styles */
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
  max-width: 600px;
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

.section-subtitle {
  margin: 24px 0 16px 0;
  font-size: 16px;
  color: #4fc3f7;
  border-bottom: 1px dashed #2a2f3e;
  padding-bottom: 8px;
}

.form-grid-2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

.checkbox-group-inline {
  display: flex;
  gap: 24px;
  margin: 16px 0;
}

.checkbox-label {
  display: flex;
  align-items: center;
  cursor: pointer;
  user-select: none;
}

.checkbox-label input {
  margin-right: 8px;
  width: 16px;
  height: 16px;
  accent-color: #4fc3f7;
}

.checkbox-text {
  color: #e0e6ed;
  font-size: 14px;
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

.btn-secondary {
  background-color: transparent;
  color: #e0e6ed;
  border: 1px solid #2a2f3e;
}

.btn-secondary:hover:not(:disabled) {
  border-color: #4fc3f7;
  color: #4fc3f7;
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

/* Context Window Config Styles */
.context-window-config {
  background: rgba(26, 31, 46, 0.5);
  border: 1px dashed #2a2f3e;
  border-radius: 8px;
  padding: 16px;
  margin: 16px 0;
}

.input-with-presets {
  display: flex;
  gap: 8px;
  align-items: center;
}

.input-with-presets .input-field {
  flex: 1;
}

.preset-buttons {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

.btn-xs {
  padding: 4px 8px;
  font-size: 12px;
  background: rgba(79, 195, 247, 0.1);
  border: 1px solid #2a2f3e;
  border-radius: 4px;
  color: #4fc3f7;
  cursor: pointer;
  transition: all 0.15s ease;
}

.btn-xs:hover {
  background: rgba(79, 195, 247, 0.2);
  border-color: #4fc3f7;
}

/* Section Header Actions */
.section-header-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}

@media (max-width: 768px) {
  .key-input-row {
    flex-direction: column;
    align-items: stretch;
  }
}

/* JSON Editor Dialog */
.json-editor-dialog {
  max-width: 720px;
  width: 100%;
  padding: 0;
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

.json-status.success {
  color: #66bb6a;
}

.json-status.error {
  color: #ef5350;
}
</style>
