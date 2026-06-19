<template>
  <div class="providers-page">
    <div class="page-header">
      <h1 class="page-title">服务提供商</h1>
      <div class="header-actions">
        <button class="btn secondary" @click="handleExportConfig" :disabled="loading || exporting">
          {{ exporting ? '导出中...' : '导出完整配置' }}
        </button>
        <button class="btn secondary" @click="handleImportConfig" :disabled="loading">JSON 导入</button>
        <button class="btn primary" @click="showAddDialog = true">添加提供商</button>
      </div>
    </div>

    <!-- Type Filter Tabs -->
    <div class="provider-filter-tabs">
      <button
        class="filter-tab"
        :class="{ active: filterType === 'all' }"
        @click="filterType = 'all'"
      >
        全部
      </button>
      <button
        class="filter-tab"
        :class="{ active: filterType === 'anthropic' }"
        @click="filterType = 'anthropic'"
      >
        Anthropic
      </button>
      <button
        class="filter-tab"
        :class="{ active: filterType === 'openai' }"
        @click="filterType = 'openai'"
      >
        OpenAI
      </button>
    </div>

    <!-- Add Provider Dialog -->
    <div class="dialog-overlay" v-if="showAddDialog" @click.self="showAddDialog = false">
      <div class="dialog card">
        <h2>添加提供商</h2>
        <div class="form-group">
          <label>类型</label>
          <div class="type-selector">
            <button
              class="type-btn"
              :class="{ active: newProvider.type === 'anthropic' }"
              @click="switchNewProviderType('anthropic')"
            >
              Anthropic
            </button>
            <button
              class="type-btn"
              :class="{ active: newProvider.type === 'openai' }"
              @click="switchNewProviderType('openai')"
            >
              OpenAI
            </button>
          </div>
        </div>
        <div class="form-group">
          <label>名称 (唯一标识)</label>
          <input type="text" v-model="newProviderName" class="input-field" placeholder="例如: anthropic, openai" />
        </div>
        <div class="form-group">
          <label>基础 URL (Base URL)</label>
          <input type="text" v-model="newProvider.base_url" class="input-field" :placeholder="newProvider.type === 'openai' ? 'https://api.openai.com/v1' : 'https://api.anthropic.com'" />
        </div>
        <div class="form-group">
          <label>默认模型</label>
          <input type="text" v-model="newProvider.default_model" class="input-field" :placeholder="newProvider.type === 'openai' ? 'o3' : 'claude-3-7-sonnet-20250219'" />
        </div>
        <div class="form-group">
          <label>认证密钥类型</label>
          <select v-model="newProvider.auth_key" class="input-field">
            <option value="ANTHROPIC_API_KEY" v-if="newProvider.type === 'anthropic'">ANTHROPIC_API_KEY</option>
            <option value="ANTHROPIC_AUTH_TOKEN" v-if="newProvider.type === 'anthropic'">ANTHROPIC_AUTH_TOKEN</option>
            <option value="OPENAI_API_KEY" v-if="newProvider.type === 'openai'">OPENAI_API_KEY</option>
          </select>
        </div>
        <div class="dialog-actions">
          <button class="btn secondary" @click="showAddDialog = false" :disabled="loading">取消</button>
          <button class="btn primary" @click="handleAddProvider" :disabled="!newProviderName || loading">
            {{ loading ? '处理中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>

    <!-- Provider Cards -->
    <div class="provider-grid">
      <div class="card provider-card" v-for="(provider, name) in filteredProviders" :key="name" @click="goToDetail(String(name))">
        <div class="card-header">
          <div class="provider-title-group">
            <h2 class="provider-name">{{ name }}</h2>
            <div class="key-status-indicator">
              <span :class="['status-dot', apiKeyStatus[String(name)] ? 'configured' : 'unconfigured']"></span>
              <span class="status-text">{{ apiKeyStatus[String(name)] ? '已配置密钥' : '未配置密钥' }}</span>
            </div>
          </div>
          <button class="btn-icon danger" @click.stop="handleDeleteProvider(String(name))" title="删除" :disabled="loading">
            <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="3 6 5 6 21 6"></polyline>
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
            </svg>
          </button>
        </div>
        <div class="card-body">
          <div class="info-row">
            <span class="label">Base URL:</span>
            <span class="value truncate" :title="provider.base_url">{{ provider.base_url || '-' }}</span>
          </div>
          <div class="info-row">
            <span class="label">默认模型:</span>
            <span class="value truncate" :title="provider.default_model">{{ provider.default_model || '-' }}</span>
          </div>
          <div class="badge-row">
            <span class="badge">{{ Object.keys(provider.presets || {}).length }} 个预设</span>
            <span class="badge type-badge" :class="'type-' + (provider.type || 'anthropic')">{{ (provider.type || 'anthropic').toUpperCase() }}</span>
          </div>
        </div>
      </div>

      <div v-if="Object.keys(filteredProviders).length === 0" class="empty-state">
        <p class="muted" v-if="Object.keys(providers).length === 0">暂无服务提供商，请点击右上角添加</p>
        <p class="muted" v-else>当前筛选条件下无匹配的服务提供商</p>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
// DEPRECATED: This page has been superseded by ProviderCenter.vue.
// The route /providers/${name} below may not exist in the current router.
// Kept for reference only; do not add new features here.
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { GetProviders, SaveProvider, DeleteProvider } from '../../wailsjs/go/config/ConfigService'
import { ImportConfigFromFile, ExportConfigToFile } from '../../wailsjs/go/main/App'
import { HasAPIKey } from '../../wailsjs/go/secrets/SecretsService'
import { config } from '../../wailsjs/go/models'
import { useToast } from '../composables/useToast'
const router = useRouter()
const providers = ref<Record<string, config.Provider>>({})
const apiKeyStatus = ref<Record<string, boolean>>({})

const showAddDialog = ref(false)
const loading = ref(false)
const exporting = ref(false)
const { showSuccess, showError } = useToast()
const filterType = ref<'all' | 'anthropic' | 'openai'>('all')
const newProviderName = ref('')
const newProvider = ref(new config.Provider({
  type: 'anthropic',
  base_url: '',
  default_model: '',
  auth_key: 'ANTHROPIC_API_KEY',
  presets: {}
}))

const filteredProviders = computed(() => {
  if (filterType.value === 'all') return providers.value
  const result: Record<string, config.Provider> = {}
  for (const [name, provider] of Object.entries(providers.value)) {
    if ((provider.type || 'anthropic') === filterType.value) {
      result[name] = provider
    }
  }
  return result
})

const switchNewProviderType = (type: 'anthropic' | 'openai') => {
  newProvider.value.type = type
  newProvider.value.auth_key = type === 'openai' ? 'OPENAI_API_KEY' : 'ANTHROPIC_API_KEY'
}

const loadProviders = async () => {
  loading.value = true
  try {
    const providerRecords = await GetProviders()
    const statusEntries = await Promise.all(
      Object.keys(providerRecords).map(async (name) => [name, await HasAPIKey(name)] as const)
    )

    providers.value = providerRecords
    apiKeyStatus.value = Object.fromEntries(statusEntries)
  } catch (err) {
    console.error('Failed to load providers:', err)
    showError('加载提供商失败: ' + err)
  } finally {
    loading.value = false
  }
}

const handleExportConfig = async () => {
  exporting.value = true
  try {
    const savedPath = await ExportConfigToFile()
    if (savedPath) {
      showSuccess('配置已导出到: ' + savedPath)
    }
  } catch (err) {
    console.error('Failed to export config:', err)
    showError('导出失败: ' + err)
  } finally {
    exporting.value = false
  }
}

const handleAddProvider = async () => {
  if (!newProviderName.value) return
  loading.value = true
  try {
    await SaveProvider(newProviderName.value, newProvider.value)
    showAddDialog.value = false
    newProviderName.value = ''
    newProvider.value = new config.Provider({
      type: 'anthropic',
      base_url: '',
      default_model: '',
      auth_key: 'ANTHROPIC_API_KEY',
      presets: {}
    })
    await loadProviders()
    showSuccess('添加提供商成功')
  } catch (err) {
    console.error('Failed to save provider:', err)
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

const handleDeleteProvider = async (name: string) => {
  if (confirm(`确定要删除提供商 "${name}" 吗？此操作不可恢复。`)) {
    loading.value = true
    try {
      await DeleteProvider(name)
      await loadProviders()
      showSuccess('删除提供商成功')
    } catch (err) {
      console.error('Failed to delete provider:', err)
      showError('删除失败: ' + err)
    } finally {
      loading.value = false
    }
  }
}

const handleImportConfig = async () => {
  loading.value = true
  try {
    const result = await ImportConfigFromFile()
    if (result) {
      await loadProviders()
      showSuccess(result)
    }
  } catch (err) {
    console.error('Failed to import config:', err)
    showError('导入失败: ' + err)
  } finally {
    loading.value = false
  }
}

const goToDetail = (name: string) => {
  router.push(`/providers/${name}`)
}

onMounted(() => {
  loadProviders()
})
</script>

<style scoped>
.providers-page {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

/* Filter Tabs */
.provider-filter-tabs {
  display: flex;
  gap: 4px;
  background: #0f1219;
  border-radius: 8px;
  padding: 4px;
  width: fit-content;
}

.filter-tab {
  padding: 8px 16px;
  background: transparent;
  border: none;
  border-radius: 6px;
  color: #8899aa;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  font-family: inherit;
}

.filter-tab:hover {
  color: #ccd6e0;
  background: rgba(255, 255, 255, 0.05);
}

.filter-tab.active {
  color: #4fc3f7;
  background: #1a1f2e;
}

/* Type Selector in Add Dialog */
.type-selector {
  display: flex;
  gap: 8px;
}

.type-btn {
  flex: 1;
  padding: 10px 16px;
  background: #0f1219;
  border: 2px solid #2a2f3e;
  border-radius: 6px;
  color: #8899aa;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  font-family: inherit;
}

.type-btn:hover {
  border-color: #3a4f5e;
  color: #ccd6e0;
}

.type-btn.active {
  border-color: #4fc3f7;
  color: #4fc3f7;
  background: rgba(79, 195, 247, 0.08);
}

/* Type Badge */
.type-badge {
  margin-left: 8px;
}

.type-badge.type-anthropic {
  background: rgba(230, 126, 34, 0.1);
  color: #e67e22;
}

.type-badge.type-openai {
  background: rgba(16, 163, 127, 0.1);
  color: #10a37f;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.header-actions {
  display: flex;
  gap: 8px;
}

.provider-title-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.page-title {
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  color: #e0e6ed;
}

.provider-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 20px;
}

.card {
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  padding: 20px;
}

.provider-card {
  cursor: pointer;
  transition: all 0.2s ease;
  position: relative;
  overflow: hidden;
}

.provider-card:hover {
  border-color: #4fc3f7;
  transform: translateY(-2px);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 16px;
}

.provider-name {
  margin: 0;
  font-size: 18px;
  font-weight: 700;
  color: #4fc3f7;
}

.key-status-indicator {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: #8899aa;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.status-dot.configured {
  background: #66bb6a;
  box-shadow: 0 0 0 3px rgba(102, 187, 106, 0.12);
}

.status-dot.unconfigured {
  background: #ffa726;
  box-shadow: 0 0 0 3px rgba(255, 167, 38, 0.12);
}

.status-text {
  line-height: 1;
}

.info-row {
  display: flex;
  margin-bottom: 8px;
  font-size: 14px;
}

.label {
  color: #8899aa;
  min-width: 80px;
}

.value {
  color: #e0e6ed;
  flex: 1;
}

.truncate {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.badge-row {
  margin-top: 16px;
  display: flex;
}

.badge {
  background: rgba(79, 195, 247, 0.1);
  color: #4fc3f7;
  padding: 4px 10px;
  border-radius: 12px;
  font-size: 12px;
  font-weight: 600;
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
  max-width: 480px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
}

.dialog h2 {
  margin: 0 0 20px 0;
  color: #e0e6ed;
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

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 24px;
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

.btn-icon {
  background: transparent;
  border: none;
  cursor: pointer;
  padding: 4px;
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
