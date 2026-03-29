<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useConnection } from '../stores/connection'
import { apiClient, type ProviderSummary, type ProviderDetail } from '../api/client'

const router = useRouter()
const { isConnected } = useConnection()

const providers = ref<ProviderSummary[]>([])
const loading = ref(false)
const showEditDialog = ref(false)
const editMode = ref<'form' | 'json'>('form')
const editProvider = ref<ProviderDetail | null>(null)
const jsonText = ref('')
const saving = ref(false)
const saveError = ref('')

async function refresh() {
  loading.value = true
  try {
    providers.value = await apiClient.getProviders()
  } catch {
    providers.value = []
  } finally {
    loading.value = false
  }
}

const editingName = ref('')

async function openEdit(name: string) {
  try {
    editProvider.value = await apiClient.getProviderDetail(name)
    editingName.value = name
    jsonText.value = JSON.stringify(editProvider.value, null, 2)
    saveError.value = ''
    showEditDialog.value = true
  } catch (err) {
    alert(err instanceof Error ? err.message : 'Failed to load provider')
  }
}

async function saveProvider() {
  saving.value = true
  saveError.value = ''
  try {
    let data: ProviderDetail
    if (editMode.value === 'json') {
      data = JSON.parse(jsonText.value)
    } else {
      data = editProvider.value!
    }
    await apiClient.saveProvider(editingName.value, data)
    showEditDialog.value = false
    await refresh()
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : 'Save failed'
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  if (!isConnected.value) {
    router.replace('/')
    return
  }
  refresh()
})
</script>

<template>
  <div class="providers-page">
    <div class="page-header">
      <h2 class="page-title">Providers</h2>
      <button class="icon-btn" @click="refresh" :disabled="loading">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
          :class="{ spinning: loading }">
          <polyline points="23 4 23 10 17 10" />
          <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
        </svg>
      </button>
    </div>

    <div v-if="providers.length === 0 && !loading" class="empty-state">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#30363d" stroke-width="1.5">
        <path d="M12 2L2 7l10 5 10-5-10-5z" />
        <path d="M2 17l10 5 10-5" />
        <path d="M2 12l10 5 10-5" />
      </svg>
      <p>No providers configured</p>
    </div>

    <div class="provider-list">
      <div
        v-for="provider in providers"
        :key="provider.id"
        class="provider-card"
        @click="openEdit(provider.name)"
      >
        <div class="provider-top">
          <span class="provider-name">{{ provider.name }}</span>
          <span class="provider-type">{{ provider.type }}</span>
        </div>
        <div class="provider-details">
          <div class="detail-row">
            <span class="detail-label">URL</span>
            <span class="detail-value">{{ provider.baseURL || '-' }}</span>
          </div>
          <div class="detail-row">
            <span class="detail-label">Model</span>
            <span class="detail-value">{{ provider.model || '-' }}</span>
          </div>
        </div>
        <div class="provider-edit-hint">Tap to edit</div>
      </div>
    </div>

    <!-- Edit Dialog -->
    <Teleport to="body">
      <div v-if="showEditDialog && editProvider" class="dialog-overlay" @click.self="showEditDialog = false">
        <div class="dialog">
          <div class="dialog-header">
            <h3 class="dialog-title">Edit Provider</h3>
            <div class="mode-toggle">
              <button
                class="toggle-btn"
                :class="{ active: editMode === 'form' }"
                @click="editMode = 'form'"
              >Form</button>
              <button
                class="toggle-btn"
                :class="{ active: editMode === 'json' }"
                @click="editMode = 'json'; jsonText = JSON.stringify(editProvider, null, 2)"
              >JSON</button>
            </div>
          </div>

          <div v-if="editMode === 'form'" class="form-content">
            <div class="form-group">
              <label class="form-label">Name</label>
              <input v-model="editProvider.name" class="form-input" />
            </div>
            <div class="form-group">
              <label class="form-label">Type</label>
              <input v-model="editProvider.type" class="form-input" />
            </div>
            <div class="form-group">
              <label class="form-label">Base URL</label>
              <input v-model="editProvider.baseURL" class="form-input" />
            </div>
            <div class="form-group">
              <label class="form-label">API Key</label>
              <input v-model="editProvider.apiKey" type="password" class="form-input" />
            </div>
            <div class="form-group">
              <label class="form-label">Model</label>
              <input v-model="editProvider.model" class="form-input" />
            </div>
            <div class="form-group">
              <label class="form-label">Max Tokens</label>
              <input v-model.number="editProvider.maxTokens" type="number" class="form-input" />
            </div>
          </div>

          <div v-else class="json-content">
            <textarea
              v-model="jsonText"
              class="json-editor"
              spellcheck="false"
            ></textarea>
          </div>

          <div v-if="saveError" class="error-msg">{{ saveError }}</div>

          <div class="dialog-actions">
            <button class="btn btn--secondary" @click="showEditDialog = false">Cancel</button>
            <button class="btn btn--primary" @click="saveProvider" :disabled="saving">
              {{ saving ? 'Saving...' : 'Save' }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.providers-page {
  padding: 16px;
}

.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}

.page-title {
  font-size: 18px;
  font-weight: 600;
  color: #f0f6fc;
  margin: 0;
}

.icon-btn {
  background: none;
  border: none;
  color: #8b949e;
  cursor: pointer;
  padding: 6px;
  border-radius: 6px;
}

.icon-btn:active {
  background: #30363d;
}

.spinning {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 48px 24px;
  color: #8b949e;
}

.provider-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.provider-card {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 8px;
  padding: 12px;
  cursor: pointer;
}

.provider-card:active {
  border-color: #58a6ff;
}

.provider-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.provider-name {
  font-size: 15px;
  font-weight: 600;
  color: #f0f6fc;
}

.provider-type {
  font-size: 12px;
  color: #d2a8ff;
  background: rgba(210, 168, 255, 0.1);
  padding: 2px 8px;
  border-radius: 10px;
}

.provider-details {
  margin-bottom: 4px;
}

.detail-row {
  display: flex;
  gap: 8px;
  padding: 4px 0;
}

.detail-label {
  font-size: 12px;
  color: #8b949e;
  min-width: 48px;
}

.detail-value {
  font-size: 12px;
  color: #c9d1d9;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.provider-edit-hint {
  font-size: 11px;
  color: #484f58;
  text-align: right;
}

/* Dialog */
.dialog-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: flex-end;
  z-index: 200;
}

.dialog {
  width: 100%;
  background: #161b22;
  border-radius: 16px 16px 0 0;
  padding: 20px 16px;
  max-height: 85vh;
  overflow-y: auto;
  padding-bottom: calc(20px + env(safe-area-inset-bottom, 0));
}

.dialog-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}

.dialog-title {
  font-size: 18px;
  font-weight: 600;
  color: #f0f6fc;
  margin: 0;
}

.mode-toggle {
  display: flex;
  background: #0d1117;
  border-radius: 6px;
  overflow: hidden;
  border: 1px solid #30363d;
}

.toggle-btn {
  padding: 4px 12px;
  background: none;
  border: none;
  color: #8b949e;
  font-size: 12px;
  cursor: pointer;
}

.toggle-btn.active {
  background: #30363d;
  color: #f0f6fc;
}

.form-content {
  display: flex;
  flex-direction: column;
}

.form-group {
  margin-bottom: 12px;
}

.form-label {
  display: block;
  font-size: 13px;
  color: #8b949e;
  margin-bottom: 4px;
}

.form-input {
  width: 100%;
  padding: 10px 12px;
  background: #0d1117;
  border: 1px solid #30363d;
  border-radius: 6px;
  color: #c9d1d9;
  font-size: 14px;
  outline: none;
  box-sizing: border-box;
}

.form-input:focus {
  border-color: #58a6ff;
}

.json-content {
  margin-bottom: 12px;
}

.json-editor {
  width: 100%;
  min-height: 300px;
  padding: 12px;
  background: #0d1117;
  border: 1px solid #30363d;
  border-radius: 6px;
  color: #c9d1d9;
  font-family: "Cascadia Code", "Fira Code", monospace;
  font-size: 13px;
  line-height: 1.5;
  outline: none;
  resize: vertical;
  box-sizing: border-box;
}

.json-editor:focus {
  border-color: #58a6ff;
}

.error-msg {
  padding: 8px 12px;
  background: rgba(248, 81, 73, 0.1);
  border: 1px solid rgba(248, 81, 73, 0.3);
  border-radius: 6px;
  color: #f85149;
  font-size: 13px;
  margin-bottom: 12px;
}

.dialog-actions {
  display: flex;
  gap: 8px;
}

.btn {
  flex: 1;
  padding: 12px;
  border: none;
  border-radius: 6px;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
}

.btn--secondary {
  background: #21262d;
  color: #c9d1d9;
}

.btn--primary {
  background: #238636;
  color: #fff;
}

.btn:disabled {
  opacity: 0.5;
}

.btn:active {
  opacity: 0.8;
}
</style>
