<template>
  <div class="env-vars-panel">
    <!-- Global Sync Status Card -->
    <div class="env-sync">
      <div class="env-sync-head">
        <div class="env-sync-title">
          <span class="status-dot" :class="globalSyncStatus.enabled ? 'on' : 'off'"></span>
          全局同步
          <span class="platform-badge">{{ globalSyncStatus.platform || 'System' }}</span>
        </div>
        <Switch
          v-if="globalSyncStatus.supported"
          :model-value="globalSyncStatus.enabled"
          :disabled="globalSyncLoading"
          @update:model-value="handleToggleGlobalSync"
        />
      </div>
      <div v-if="globalSyncStatus.message" class="env-sync-message">
        {{ globalSyncStatus.message }}
      </div>
      <div v-else-if="globalSyncStatus.enabled" class="env-sync-meta">
        已开启 · 管理 {{ globalSyncStatus.managedCount }} 个环境变量 · 写入 Shell 启动脚本（~/.zshrc）
      </div>
      <div v-else class="env-sync-meta">
        已关闭 · 环境变量仅在当前会话生效
      </div>
      <ul v-if="!globalSyncStatus.enabled && globalSyncStatus.managedKeys.length > 0" class="env-sync-notes">
        <li>开启后会将当前环境变量写入 Shell 配置文件</li>
        <li>关闭后会自动清理已写入的导出语句</li>
      </ul>
    </div>

    <!-- Toolbar -->
    <div class="env-toolbar">
      <div class="search-box">
        <input
          v-model="searchQuery"
          type="text"
          placeholder="按变量名过滤..."
          class="search-input"
        />
      </div>
      <div class="toolbar-actions">
        <AppButton variant="ghost" size="small" @click="handleImport">
          导入
        </AppButton>
        <AppButton variant="ghost" size="small" @click="handleExport">
          导出
        </AppButton>
        <AppButton variant="primary" size="small" @click="handleAddNew">
          新增
        </AppButton>
      </div>
    </div>

    <!-- View Mode Toggle -->
    <div class="view-mode-toggle">
      <Segmented
        v-model="viewMode"
        :options="viewModeOptions"
        variant="pill"
      />
    </div>

    <!-- Visual Table Mode -->
    <div v-if="viewMode === 'visual'" class="env-content">
      <!-- Empty State -->
      <div v-if="filteredVars.length === 0 && !loading" class="empty-state">
        <EmptyState
          icon="⌘"
          :title="searchQuery ? '未找到匹配的环境变量' : '暂无环境变量'"
          :description="searchQuery ? '尝试其他搜索词' : '点击「新增」添加第一个环境变量'"
        />
      </div>

      <!-- Variables Table -->
      <div v-else class="env-table-container">
        <table class="env-table">
          <thead>
            <tr>
              <th style="width: 35%">变量名</th>
              <th style="width: 50%">值</th>
              <th style="width: 15%">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="envVar in paginatedVars"
              :key="envVar.key"
              class="env-row"
            >
              <td class="env-key">{{ envVar.key }}</td>
              <td class="env-value">
                <MaskedValue
                  :value="envVar.value"
                  :toggleable="true"
                  @update:visible="handleValueVisibility(envVar.key, $event)"
                />
              </td>
              <td class="env-actions">
                <AppButton
                  variant="icon"
                  size="small"
                  title="显示/隐藏"
                  @click="toggleValueVisibility(envVar.key)"
                >
                  👁
                </AppButton>
                <AppButton
                  variant="icon"
                  size="small"
                  title="编辑"
                  @click="handleEdit(envVar)"
                >
                  ✎
                </AppButton>
                <AppButton
                  variant="icon"
                  size="small"
                  title="删除"
                  class="danger"
                  @click="handleDelete(envVar.key)"
                >
                  ✕
                </AppButton>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- JSON Mode -->
    <div v-else class="json-mode-container">
      <div class="json-header">
        <span class="json-title">JSON 配置</span>
        <span v-if="hasUnsavedJson" class="unsaved-indicator">未保存</span>
      </div>
      <div class="json-editor-wrapper">
        <textarea
          v-model="jsonContent"
          class="json-editor"
          spellcheck="false"
        />
      </div>
      <div class="json-actions">
        <AppButton variant="ghost" size="small" @click="handleResetJson">
          重置
        </AppButton>
        <AppButton
          variant="primary"
          size="small"
          :disabled="!hasUnsavedJson || jsonSaving"
          @click="handleSaveJson"
        >
          {{ jsonSaving ? '保存中...' : '保存' }}
        </AppButton>
      </div>
      <div v-if="jsonError" class="json-error">
        {{ jsonError }}
      </div>
    </div>

    <!-- Edit Dialog -->
    <Dialog
      v-model:open="showEditDialog"
      :title="editingVar.key ? '编辑环境变量' : '新增环境变量'"
    >
      <div class="edit-form">
        <div class="form-group">
          <label>变量名</label>
          <input
            v-model="editForm.key"
            type="text"
            class="form-input"
            placeholder="例如: API_KEY"
            :disabled="!!editingVar.key"
          />
        </div>
        <div class="form-group">
          <label>值</label>
          <textarea
            v-model="editForm.value"
            class="form-textarea"
            placeholder="输入变量值"
            rows="4"
          />
        </div>
      </div>
      <template #footer>
        <AppButton variant="ghost" @click="showEditDialog = false">
          取消
        </AppButton>
        <AppButton
          variant="primary"
          :disabled="!editForm.key.trim() || !editForm.value.trim() || saving"
          @click="handleSave"
        >
          {{ saving ? '保存中...' : '保存' }}
        </AppButton>
      </template>
    </Dialog>

    <!-- Import Dialog -->
    <Dialog
      v-model:open="showImportDialog"
      title="导入环境变量"
      description="粘贴 JSON 格式的环境变量配置"
    >
      <div class="import-form">
        <textarea
          v-model="importJson"
          class="import-textarea"
          placeholder='{"API_KEY": "sk-xxx", "DB_URL": "postgres://..."}'
          rows="10"
        />
      </div>
      <template #footer>
        <AppButton variant="ghost" @click="showImportDialog = false">
          取消
        </AppButton>
        <AppButton
          variant="primary"
          :disabled="!importJson.trim() || importing"
          @click="handleDoImport"
        >
          {{ importing ? '导入中...' : '导入' }}
        </AppButton>
      </template>
    </Dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue';
import * as envvarsApi from '../../api/envvars';
import AppButton from '../ui/AppButton.vue';
import Switch from '../ui/Switch.vue';
import MaskedValue from '../ui/MaskedValue.vue';
import Segmented from '../ui/Segmented.vue';
import EmptyState from '../ui/EmptyState.vue';
import Dialog from '../ui/Dialog.vue';

// Define types inline to avoid import issues
interface EnvVar {
  key: string;
  value: string;
}

interface GlobalSyncStatus {
  supported: boolean;
  platform: string;
  enabled: boolean;
  managedKeys: string[];
  managedCount: number;
  message?: string;
}

// State
const loading = ref(false);
const globalSyncLoading = ref(false);
const globalSyncStatus = ref<GlobalSyncStatus>({
  supported: false,
  platform: '',
  enabled: false,
  managedKeys: [],
  managedCount: 0,
  message: '',
});

const envVars = ref<EnvVar[]>([]);
const searchQuery = ref('');
const viewMode = ref<'visual' | 'json'>('visual');
const jsonContent = ref('');
const originalJsonContent = ref('');
const jsonSaving = ref(false);
const jsonError = ref('');

const showEditDialog = ref(false);
const showImportDialog = ref(false);
const saving = ref(false);
const importing = ref(false);

const editingVar = ref<EnvVar>({ key: '', value: '' });
const editForm = ref({ key: '', value: '' });
const importJson = ref('');

const visibleValues = ref<Record<string, boolean>>({});

// View mode options
const viewModeOptions = ref([
  { value: 'visual', label: '可视化' },
  { value: 'json', label: 'JSON' },
]);

// Computed
const filteredVars = computed(() => {
  if (!searchQuery.value.trim()) return envVars.value;
  const query = searchQuery.value.toLowerCase();
  return envVars.value.filter(v => v.key.toLowerCase().includes(query));
});

const paginatedVars = computed(() => {
  // For simplicity, show all filtered vars (pagination can be added later)
  return filteredVars.value;
});

const hasUnsavedJson = computed(() => {
  return jsonContent.value !== originalJsonContent.value;
});

// Methods
async function loadGlobalSyncStatus() {
  try {
    globalSyncLoading.value = true;
    const status = await envvarsApi.getEnvVarsGlobalSyncStatus();
    globalSyncStatus.value = status;
  } catch (error) {
    console.error('[EnvVarsPanel] Failed to load global sync status:', error);
  } finally {
    globalSyncLoading.value = false;
  }
}

async function loadEnvVars() {
  try {
    loading.value = true;
    const vars = await envvarsApi.getEnvVars();
    envVars.value = vars;
  } catch (error) {
    console.error('[EnvVarsPanel] Failed to load env vars:', error);
  } finally {
    loading.value = false;
  }
}

async function loadJsonContent() {
  try {
    const json = await envvarsApi.getEnvVarsJSON();
    jsonContent.value = json;
    originalJsonContent.value = json;
    jsonError.value = '';
  } catch (error) {
    console.error('[EnvVarsPanel] Failed to load JSON:', error);
    jsonError.value = '加载 JSON 失败';
  }
}

async function handleToggleGlobalSync(enabled: boolean) {
  try {
    const result = await envvarsApi.setEnvVarsGlobalSyncEnabled(enabled);
    globalSyncStatus.value = result;
  } catch (error) {
    console.error('[EnvVarsPanel] Failed to toggle global sync:', error);
  }
}

function toggleValueVisibility(key: string) {
  visibleValues.value[key] = !visibleValues.value[key];
}

function handleValueVisibility(key: string, visible: boolean) {
  visibleValues.value[key] = visible;
}

function handleEdit(envVar: EnvVar) {
  editingVar.value = { ...envVar };
  editForm.value = { key: envVar.key, value: envVar.value };
  showEditDialog.value = true;
}

function handleAddNew() {
  editingVar.value = { key: '', value: '' };
  editForm.value = { key: '', value: '' };
  showEditDialog.value = true;
}

async function handleSave() {
  const { key, value } = editForm.value;
  if (!key.trim() || !value.trim()) return;

  try {
    saving.value = true;
    await envvarsApi.setEnvVar(key.trim(), value.trim());

    // Refresh
    await Promise.all([loadEnvVars(), loadJsonContent()]);

    showEditDialog.value = false;
    editForm.value = { key: '', value: '' };
  } catch (error) {
    console.error('[EnvVarsPanel] Failed to save env var:', error);
  } finally {
    saving.value = false;
  }
}

async function handleDelete(key: string) {
  if (!confirm(`确定要删除环境变量「${key}」吗？`)) return;

  try {
    await envvarsApi.deleteEnvVar(key);
    await Promise.all([loadEnvVars(), loadJsonContent()]);
  } catch (error) {
    console.error('[EnvVarsPanel] Failed to delete env var:', error);
  }
}

function handleImport() {
  importJson.value = '';
  showImportDialog.value = true;
}

async function handleDoImport() {
  try {
    importing.value = true;
    await envvarsApi.importEnvVars(importJson.value.trim());
    await Promise.all([loadEnvVars(), loadJsonContent()]);
    showImportDialog.value = false;
  } catch (error) {
    console.error('[EnvVarsPanel] Failed to import:', error);
  } finally {
    importing.value = false;
  }
}

async function handleExport() {
  try {
    await envvarsApi.exportEnvVarsToFile();
  } catch (error) {
    console.error('[EnvVarsPanel] Failed to export:', error);
  }
}

async function handleSaveJson() {
  try {
    jsonSaving.value = true;
    jsonError.value = '';

    // Validate JSON
    try {
      JSON.parse(jsonContent.value);
    } catch (e) {
      jsonError.value = 'JSON 格式无效：' + (e as Error).message;
      return;
    }

    await envvarsApi.saveEnvVarsJSON(jsonContent.value);
    originalJsonContent.value = jsonContent.value;

    // Refresh visual view
    await loadEnvVars();
  } catch (error) {
    console.error('[EnvVarsPanel] Failed to save JSON:', error);
    jsonError.value = '保存失败';
  } finally {
    jsonSaving.value = false;
  }
}

function handleResetJson() {
  jsonContent.value = originalJsonContent.value;
  jsonError.value = '';
}

// Watch view mode to load JSON when switching
watch(viewMode, async (newMode) => {
  if (newMode === 'json') {
    await loadJsonContent();
  }
});

// Load data on mount
onMounted(async () => {
  await Promise.all([loadGlobalSyncStatus(), loadEnvVars()]);
});
</script>

<style scoped>
.env-vars-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* Global Sync Card */
.env-sync {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 14px 18px;
}

.env-sync-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.env-sync-title {
  font-size: 14px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--label);
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--tertiary);
}

.status-dot.on {
  background: var(--success);
}

.status-dot.off {
  background: var(--tertiary);
}

.platform-badge {
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 4px;
  background: var(--control);
  color: var(--secondary);
}

.env-sync-message,
.env-sync-meta {
  font-size: 12px;
  color: var(--secondary);
  margin-bottom: 8px;
}

.env-sync-notes {
  list-style: none;
  padding: 0;
  margin: 8px 0 0 0;
}

.env-sync-notes li {
  font-size: 11px;
  color: var(--tertiary);
  padding-left: 12px;
  position: relative;
  margin-bottom: 4px;
}

.env-sync-notes li::before {
  content: '•';
  position: absolute;
  left: 0;
}

/* Toolbar */
.env-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.search-box {
  flex: 1;
  min-width: 200px;
}

.search-input {
  width: 100%;
  padding: 7px 12px;
  font-size: 12px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  transition: border-color 0.15s;
}

.search-input:focus {
  outline: none;
  border-color: var(--accent);
}

.toolbar-actions {
  display: flex;
  gap: 8px;
}

/* View Mode Toggle */
.view-mode-toggle {
  display: flex;
  justify-content: flex-end;
}

/* Content */
.env-content {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  overflow: hidden;
  min-height: 200px;
}

.empty-state {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 200px;
}

/* Table */
.env-table-container {
  overflow-x: auto;
}

.env-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.env-table th {
  text-align: left;
  padding: 12px 16px;
  font-size: 11px;
  font-weight: 600;
  color: var(--tertiary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  border-bottom: 1px solid var(--separator);
  background: var(--control);
}

.env-table td {
  padding: 11px 16px;
  border-top: 1px solid var(--separator);
}

.env-row:hover td {
  background: var(--control);
}

.env-row:first-child td {
  border-top: none;
}

.env-key {
  font-family: var(--mono);
  color: var(--label);
  font-weight: 500;
}

.env-value {
  max-width: 400px;
}

.env-actions {
  display: flex;
  gap: 4px;
}

.env-actions .icon-btn.danger {
  color: var(--danger);
}

.env-actions .icon-btn.danger:hover {
  background: rgba(255, 59, 48, 0.1);
}

/* JSON Mode */
.json-mode-container {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.json-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.json-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
}

.unsaved-indicator {
  font-size: 11px;
  color: var(--warning);
  background: rgba(255, 149, 0, 0.1);
  padding: 2px 6px;
  border-radius: 4px;
}

.json-editor-wrapper {
  position: relative;
}

.json-editor {
  width: 100%;
  min-height: 300px;
  max-height: 500px;
  font-family: var(--mono);
  font-size: 11.5px;
  line-height: 1.7;
  color: #c7c7cc;
  background: #1B1B1F;
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 14px 16px;
  resize: vertical;
}

.json-editor:focus {
  outline: none;
  border-color: var(--accent);
}

.json-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.json-error {
  font-size: 12px;
  color: var(--danger);
  padding: 8px 12px;
  background: rgba(255, 59, 48, 0.05);
  border-radius: 8px;
}

/* Edit Dialog */
.edit-form {
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
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

.form-input,
.form-textarea {
  padding: 8px 12px;
  font-size: 13px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  transition: border-color 0.15s;
  font-family: inherit;
}

.form-input:focus,
.form-textarea:focus {
  outline: none;
  border-color: var(--accent);
}

.form-input:disabled {
  background: var(--control);
  color: var(--tertiary);
}

.form-textarea {
  resize: vertical;
  min-height: 80px;
}

/* Import Dialog */
.import-form {
  display: flex;
  flex-direction: column;
}

.import-textarea {
  width: 100%;
  padding: 12px;
  font-size: 12px;
  font-family: var(--mono);
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  resize: vertical;
  min-height: 150px;
}

.import-textarea:focus {
  outline: none;
  border-color: var(--accent);
}

/* Scrollbar */
.env-table-container::-webkit-scrollbar,
.json-editor::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}

.env-table-container::-webkit-scrollbar-track,
.json-editor::-webkit-scrollbar-track {
  background: transparent;
}

.env-table-container::-webkit-scrollbar-thumb,
.json-editor::-webkit-scrollbar-thumb {
  background: var(--separator);
  border-radius: 4px;
}

.env-table-container::-webkit-scrollbar-thumb:hover,
.json-editor::-webkit-scrollbar-thumb:hover {
  background: var(--tertiary);
}
</style>
