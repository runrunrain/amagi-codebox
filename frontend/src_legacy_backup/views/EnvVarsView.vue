<script lang="ts" setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useToast } from '../composables/useToast'
import {
  GetEnvVars,
  SetEnvVar,
  DeleteEnvVar,
  ExportEnvVars,
  ImportEnvVars,
  ExportEnvVarsToFile,
  ImportEnvVarsFromFile,
  GetEnvVarsJSON,
  SaveEnvVarsJSON,
  GetEnvVarsGlobalSyncStatus,
  SetEnvVarsGlobalSyncEnabled,
} from '../../wailsjs/go/main/App'
import { envvars } from '../../wailsjs/go/models'

const { showSuccess, showError } = useToast()

const loading = ref(false)
const searchQuery = ref('')
const currentPage = ref(1)
const pageSize = 15

const envVarsList = ref<envvars.EnvVar[]>([])

// --- Global sync state ---
const globalSyncStatus = ref<envvars.GlobalSyncStatus | null>(null)
const globalSyncLoading = ref(false)
const globalSyncInitialized = ref(false)
const globalSyncError = ref('')

// View mode: 'table' or 'json'
const viewMode = ref<'table' | 'json'>('table')

// Add/Edit dialog
const showDialog = ref(false)
const dialogMode = ref<'add' | 'edit'>('add')
const editOriginalKey = ref('')
const formKey = ref('')
const formValue = ref('')
const formValueVisible = ref(false)

// Delete confirm
const confirmingDeleteKey = ref<string | null>(null)

// JSON editor (inline, not dialog)
const jsonContent = ref('')
const jsonError = ref('')
const jsonDirty = ref(false)

// Value visibility per row
const visibleValues = ref<Set<string>>(new Set())

// Loading initial state
const initialized = ref(false)

const totalCount = computed(() => envVarsList.value.length)

const filteredVars = computed(() => {
  if (!searchQuery.value) return envVarsList.value
  const q = searchQuery.value.toLowerCase()
  return envVarsList.value.filter(
    v => v.key.toLowerCase().includes(q) || v.value.toLowerCase().includes(q)
  )
})

const totalPages = computed(() => Math.max(1, Math.ceil(filteredVars.value.length / pageSize)))

const paginatedVars = computed(() => {
  const start = (currentPage.value - 1) * pageSize
  return filteredVars.value.slice(start, start + pageSize)
})

watch(searchQuery, () => { currentPage.value = 1 })

// --- Data loading ---
async function loadEnvVars() {
  loading.value = true
  try {
    const result = await GetEnvVars()
    envVarsList.value = result || []
    initialized.value = true
  } catch (err) {
    showError('加载环境变量失败: ' + err)
  } finally {
    loading.value = false
  }
}

// --- Add ---
function openAddDialog() {
  dialogMode.value = 'add'
  editOriginalKey.value = ''
  formKey.value = ''
  formValue.value = ''
  formValueVisible.value = false
  showDialog.value = true
}

// --- Edit ---
function openEditDialog(item: envvars.EnvVar) {
  dialogMode.value = 'edit'
  editOriginalKey.value = item.key
  formKey.value = item.key
  formValue.value = item.value
  formValueVisible.value = true
  showDialog.value = true
}

function closeDialog() {
  showDialog.value = false
}

async function saveDialog() {
  const key = formKey.value.trim()
  const value = formValue.value
  if (!key) {
    showError('变量名不能为空')
    return
  }

  loading.value = true
  try {
    if (dialogMode.value === 'edit' && editOriginalKey.value !== key) {
      await DeleteEnvVar(editOriginalKey.value)
    }
    await SetEnvVar(key, value)
    await loadEnvVars()
    await loadGlobalSyncStatus()
    closeDialog()
    showSuccess(dialogMode.value === 'add' ? '环境变量已添加' : '环境变量已更新')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

// --- Delete ---
function requestDelete(key: string) {
  confirmingDeleteKey.value = key
}

function cancelDelete() {
  confirmingDeleteKey.value = null
}

async function confirmDelete(key: string) {
  loading.value = true
  try {
    await DeleteEnvVar(key)
    await loadEnvVars()
    await loadGlobalSyncStatus()
    confirmingDeleteKey.value = null
    showSuccess('环境变量已删除')
  } catch (err) {
    showError('删除失败: ' + err)
  } finally {
    loading.value = false
  }
}

// --- Value visibility ---
function toggleValueVisibility(key: string) {
  if (visibleValues.value.has(key)) {
    visibleValues.value.delete(key)
  } else {
    visibleValues.value.add(key)
  }
}

function isValueVisible(key: string): boolean {
  return visibleValues.value.has(key)
}

function maskedValue(value: string): string {
  if (value.length <= 4) return '****'
  return '****' + value.slice(-4)
}

// --- Export (file dialog) ---
async function handleExport() {
  loading.value = true
  try {
    await ExportEnvVarsToFile()
    showSuccess('环境变量已导出到文件')
  } catch (err) {
    const msg = String(err)
    if (msg.includes('cancel') || msg.includes('Cancel')) return
    showError('导出失败: ' + err)
  } finally {
    loading.value = false
  }
}

// --- Import (file dialog) ---
async function handleImport() {
  loading.value = true
  try {
    await ImportEnvVarsFromFile()
    await loadEnvVars()
    await loadGlobalSyncStatus()
    showSuccess('导入成功')
  } catch (err) {
    const msg = String(err)
    if (msg.includes('cancel') || msg.includes('Cancel')) return
    showError('导入失败: ' + err)
  } finally {
    loading.value = false
  }
}

// --- View mode switching ---
async function switchToJsonMode() {
  loading.value = true
  try {
    const json = await GetEnvVarsJSON()
    jsonContent.value = JSON.stringify(JSON.parse(json), null, 2)
    jsonError.value = ''
    jsonDirty.value = false
    viewMode.value = 'json'
  } catch (err) {
    showError('加载 JSON 失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function switchToTableMode() {
  if (jsonDirty.value) {
    const discard = confirm('JSON 编辑内容尚未保存，切换将丢失修改。确定切换？')
    if (!discard) return
  }
  viewMode.value = 'table'
  await loadEnvVars()
}

function onJsonInput() {
  jsonDirty.value = true
  jsonError.value = ''
}

async function saveJson() {
  try {
    JSON.parse(jsonContent.value)
    jsonError.value = ''
  } catch {
    jsonError.value = 'JSON 格式无效，请检查语法'
    return
  }

  loading.value = true
  try {
    await SaveEnvVarsJSON(jsonContent.value)
    await loadGlobalSyncStatus()
    jsonDirty.value = false
    showSuccess('JSON 保存成功')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function resetJson() {
  loading.value = true
  try {
    const json = await GetEnvVarsJSON()
    jsonContent.value = JSON.stringify(JSON.parse(json), null, 2)
    jsonError.value = ''
    jsonDirty.value = false
    showSuccess('已重置为当前数据')
  } catch (err) {
    showError('重置失败: ' + err)
  } finally {
    loading.value = false
  }
}

// --- Pagination ---
function prevPage() {
  if (currentPage.value > 1) currentPage.value--
}
function nextPage() {
  if (currentPage.value < totalPages.value) currentPage.value++
}

onMounted(() => {
  loadEnvVars()
  loadGlobalSyncStatus()
})

// --- Global sync: load ---
async function loadGlobalSyncStatus() {
  globalSyncLoading.value = true
  globalSyncError.value = ''
  try {
    const result = await GetEnvVarsGlobalSyncStatus()
    globalSyncStatus.value = result
    globalSyncInitialized.value = true
  } catch (err) {
    globalSyncError.value = String(err)
  } finally {
    globalSyncLoading.value = false
  }
}

// --- Global sync: UI computed ---
const canToggleGlobalSync = computed(() => {
  const s = globalSyncStatus.value
  return !!s && s.supported === true && !globalSyncLoading.value
})

const globalSyncCardState = computed(() => {
  const s = globalSyncStatus.value
  if (!s) return 'state-loading'
  if (!s.supported) return 'state-unsupported'
  return s.enabled ? 'state-enabled' : 'state-disabled'
})

const gscDotClass = computed(() => {
  const s = globalSyncStatus.value
  if (!s) return 'dot-loading'
  if (!s.supported) return 'dot-unsupported'
  return s.enabled ? 'dot-enabled' : 'dot-disabled'
})

const gscBadgeClass = computed(() => {
  const s = globalSyncStatus.value
  if (!s) return 'badge-loading'
  if (!s.supported) return 'badge-unsupported'
  return s.enabled ? 'badge-enabled' : 'badge-disabled'
})

const gscBadgeText = computed(() => {
  const s = globalSyncStatus.value
  if (!s) return '加载中'
  if (!s.supported) return '当前平台不支持'
  return s.enabled ? '已开启' : '已关闭'
})

const gscMetaText = computed(() => {
  const s = globalSyncStatus.value
  if (globalSyncLoading.value && !s) return '正在加载全局同步状态…'
  if (globalSyncError.value) return `状态加载失败：${globalSyncError.value}`
  if (!s) return '尚未加载全局同步状态'
  if (!s.supported) return s.message || '当前平台不支持全局环境变量同步，仅 amagi-codebox 内部终端注入仍生效。'
  if (s.enabled) {
    const n = s.managedCount
    return `正在将 ${n} 个变量同步到当前用户级全局环境（已广播变更通知）。`
  }
  return '关闭状态 · 变量仅注入 amagi-codebox 内部终端，不影响系统环境。'
})

// --- Global sync: toggle with confirm ---
async function requestToggleGlobalSync(event: Event) {
  const target = event.target as HTMLInputElement
  const desired = !!target.checked
  const current = globalSyncStatus.value?.enabled === true
  // Optimistic UI: revert the checkbox; real state will be applied after backend response
  target.checked = current
  if (desired === current) return
  if (!canToggleGlobalSync.value) {
    showError('当前平台不支持全局环境变量同步')
    return
  }
  if (!desired) {
    const ok = window.confirm(
      '关闭后，将恢复开启前已存在的系统环境变量，或删除由 amagi-codebox 创建的全局变量。\n' +
      'amagi-codebox 内部终端注入仍会继续生效，外部桌面端是否受影响由其自身读取时机决定。\n\n' +
      '确定要关闭全局同步吗？'
    )
    if (!ok) return
  }
  await performToggleGlobalSync(desired)
}

async function performToggleGlobalSync(enabled: boolean) {
  globalSyncLoading.value = true
  globalSyncError.value = ''
  try {
    const result = await SetEnvVarsGlobalSyncEnabled(enabled)
    globalSyncStatus.value = result
    if (enabled) {
      showSuccess(
        result.managedCount > 0
          ? `已开启全局同步，当前共管理 ${result.managedCount} 个变量`
          : '已开启全局同步（暂无变量需要同步）'
      )
    } else {
      showSuccess('已关闭全局同步，托管变量已恢复或清理')
    }
  } catch (err) {
    const msg = String(err)
    globalSyncError.value = msg
    showError('操作失败: ' + msg)
  } finally {
    globalSyncLoading.value = false
  }
}
</script>

<template>
  <div class="envvars-view">
    <!-- Loading bar -->
    <div class="loading-bar" v-if="loading"></div>

    <!-- Toolbar -->
    <div class="toolbar">
      <div class="toolbar-left">
        <div class="search-box" v-if="viewMode === 'table'">
          <span class="search-icon">&#x2315;</span>
          <input
            type="text"
            v-model="searchQuery"
            placeholder="搜索环境变量..."
            class="search-input"
          />
          <span class="count-badge" v-if="totalCount > 0">{{ totalCount }}</span>
        </div>
        <div class="view-toggle" v-else>
          <span class="view-label">JSON 编辑模式</span>
          <span v-if="jsonDirty" class="dirty-indicator">*未保存</span>
        </div>
      </div>
      <div class="toolbar-right">
        <template v-if="viewMode === 'table'">
          <button class="btn secondary" @click="openAddDialog" :disabled="loading">新增</button>
          <button class="btn secondary" @click="handleImport" :disabled="loading">导入</button>
          <button class="btn secondary" @click="handleExport" :disabled="loading" :class="{ hidden: totalCount === 0 }">导出</button>
          <button class="btn primary" @click="switchToJsonMode" :disabled="loading">JSON 模式</button>
        </template>
        <template v-else>
          <button class="btn secondary" @click="resetJson" :disabled="loading">重置</button>
          <button class="btn primary" @click="saveJson" :disabled="loading || !jsonDirty">
            {{ loading ? '保存中...' : '保存' }}
          </button>
          <button class="btn secondary" @click="switchToTableMode" :disabled="loading">表格模式</button>
        </template>
      </div>
    </div>

    <!-- Global Sync Card -->
    <div class="gsc" :class="globalSyncCardState" :data-state="globalSyncCardState">
      <div class="gsc-stripe" aria-hidden="true"></div>
      <div class="gsc-body">
        <div class="gsc-head">
          <div class="gsc-title-block">
            <div class="gsc-title">
              <span class="gsc-dot" :class="gscDotClass" aria-hidden="true"></span>
              <span class="gsc-name">同步到设备全局环境</span>
              <span class="gsc-platform-tag">{{ globalSyncStatus?.platform || '—' }}</span>
              <span class="gsc-badge" :class="gscBadgeClass">{{ gscBadgeText }}</span>
            </div>
            <p class="gsc-meta">{{ gscMetaText }}</p>
          </div>
          <div class="gsc-control">
            <label
              class="switch gsc-switch"
              :class="{ 'is-disabled': !canToggleGlobalSync, 'is-busy': globalSyncLoading && canToggleGlobalSync }"
              :title="canToggleGlobalSync ? '点击切换全局同步' : '当前平台不支持全局同步'"
            >
              <input
                type="checkbox"
                :checked="globalSyncStatus?.enabled === true"
                :disabled="!canToggleGlobalSync"
                @change="requestToggleGlobalSync"
                aria-label="同步到设备全局环境"
              />
              <span class="slider"></span>
            </label>
            <span class="gsc-control-hint" v-if="globalSyncLoading">处理中…</span>
          </div>
        </div>
        <ul class="gsc-notes">
          <li>
            <span class="gsc-num">1</span>
            作用于当前用户级全局环境（<code>HKCU\Environment</code>），不会写入机器级系统环境变量，也无需管理员权限。
          </li>
          <li>
            <span class="gsc-num">2</span>
            开启后，配置的环境变量会同步到后续启动的 Codex / OpenCode 等独立桌面端进程。
          </li>
          <li>
            <span class="gsc-num">3</span>
            已运行的外部桌面端通常需要重启后才能读取到新值；个别进程可能仍使用启动时缓存。
          </li>
          <li>
            <span class="gsc-num">4</span>
            关闭后，仅恢复 / 清理本应用曾经管理过的全局变量，<strong>amagi-codebox 内部终端注入仍会继续生效</strong>。
          </li>
        </ul>
      </div>
    </div>

    <!-- JSON Editor (inline) -->
    <div class="json-editor-container" v-if="viewMode === 'json'">
      <div class="json-editor-hint">直接编辑环境变量的 JSON 数据，保存后将覆盖现有配置。</div>
      <textarea
        v-model="jsonContent"
        class="json-editor-textarea"
        spellcheck="false"
        @input="onJsonInput"
      ></textarea>
      <p v-if="jsonError" class="json-error">{{ jsonError }}</p>
    </div>

    <!-- Table -->
    <div class="table-container" v-if="viewMode === 'table'">
      <table class="env-table">
        <thead>
          <tr>
            <th class="col-key">变量名</th>
            <th class="col-value">值</th>
            <th class="col-actions">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="paginatedVars.length === 0 && initialized">
            <td colspan="3">
              <div class="empty-state">
                <div class="empty-icon">
                  <svg viewBox="0 0 24 24" width="40" height="40" stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round">
                    <rect x="2" y="3" width="20" height="14" rx="2" ry="2"></rect>
                    <line x1="8" y1="21" x2="16" y2="21"></line>
                    <line x1="12" y1="17" x2="12" y2="21"></line>
                  </svg>
                </div>
                <p class="empty-text" v-if="searchQuery">没有匹配"{{ searchQuery }}"的环境变量</p>
                <p class="empty-text" v-else>暂无环境变量</p>
                <p class="empty-hint" v-if="!searchQuery">点击"新增"添加变量，或使用"导入"从剪贴板加载 JSON</p>
                <button class="btn secondary empty-action" v-if="!searchQuery" @click="openAddDialog">添加第一个变量</button>
              </div>
            </td>
          </tr>
          <tr v-for="item in paginatedVars" :key="item.key" class="env-row">
            <td class="col-key">
              <span class="var-key">{{ item.key }}</span>
            </td>
            <td class="col-value">
              <div class="value-cell">
                <span class="var-value" :class="{ masked: !isValueVisible(item.key) }">
                  {{ isValueVisible(item.key) ? item.value : maskedValue(item.value) }}
                </span>
                <button class="btn-icon btn-toggle-vis" @click="toggleValueVisibility(item.key)" :title="isValueVisible(item.key) ? '隐藏' : '显示'">
                  <svg v-if="isValueVisible(item.key)" viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
                    <circle cx="12" cy="12" r="3"></circle>
                  </svg>
                  <svg v-else viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"></path>
                    <line x1="1" y1="1" x2="23" y2="23"></line>
                  </svg>
                </button>
              </div>
            </td>
            <td class="col-actions">
              <div class="actions-cell">
                <template v-if="confirmingDeleteKey === item.key">
                  <span class="confirm-text">确认删除？</span>
                  <button class="btn-sm danger" @click="confirmDelete(item.key)" :disabled="loading">确认</button>
                  <button class="btn-sm secondary" @click="cancelDelete">取消</button>
                </template>
                <template v-else>
                  <button class="btn-icon" title="编辑" @click="openEditDialog(item)">
                    <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                      <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path>
                      <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path>
                    </svg>
                  </button>
                  <button class="btn-icon danger" title="删除" @click="requestDelete(item.key)">
                    <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                      <polyline points="3 6 5 6 21 6"></polyline>
                      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
                    </svg>
                  </button>
                </template>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Pagination -->
    <div class="pagination" v-if="viewMode === 'table' && filteredVars.length > 0">
      <span class="pagination-info">
        共 {{ filteredVars.length }} 项，第 {{ currentPage }} / {{ totalPages }} 页
      </span>
      <div class="pagination-controls">
        <button class="page-btn" :disabled="currentPage <= 1" @click="prevPage">上一页</button>
        <button class="page-btn" :disabled="currentPage >= totalPages" @click="nextPage">下一页</button>
      </div>
    </div>

    <!-- Add/Edit Dialog -->
    <transition name="dialog-fade">
    <div class="dialog-overlay" v-if="showDialog" @click.self="closeDialog">
      <div class="dialog">
        <h2 class="dialog-title">{{ dialogMode === 'add' ? '新增环境变量' : '编辑环境变量' }}</h2>
        <div class="form-group">
          <label>变量名</label>
          <input
            type="text"
            v-model="formKey"
            class="input-field"
            placeholder="例如: ANTHROPIC_API_KEY"
            :disabled="dialogMode === 'edit'"
          />
        </div>
        <div class="form-group">
          <label>值</label>
          <div class="value-input-group">
            <input
              :type="formValueVisible ? 'text' : 'password'"
              v-model="formValue"
              class="input-field"
              placeholder="输入变量值"
            />
            <button class="btn-sm secondary" @click="formValueVisible = !formValueVisible">
              {{ formValueVisible ? '隐藏' : '明文' }}
            </button>
          </div>
        </div>
        <div class="dialog-actions">
          <button class="btn secondary" @click="closeDialog" :disabled="loading">取消</button>
          <button class="btn primary" @click="saveDialog" :disabled="!formKey.trim() || loading">
            {{ loading ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>
    </transition>

  </div>
</template>

<style scoped>
.envvars-view {
  display: flex;
  flex-direction: column;
  gap: 20px;
  position: relative;
}

/* Loading bar */
.loading-bar {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 2px;
  background: linear-gradient(90deg, transparent, #4fc3f7, transparent);
  animation: loading-slide 1.2s ease-in-out infinite;
  z-index: 10;
}

@keyframes loading-slide {
  0% { transform: translateX(-100%); }
  100% { transform: translateX(100%); }
}

/* Toolbar */
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}

.toolbar-left {
  flex: 1;
  min-width: 200px;
  max-width: 400px;
}

.toolbar-right {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

.search-box {
  display: flex;
  align-items: center;
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  padding: 0 12px;
  transition: all 0.15s ease;
}

.search-box:focus-within {
  border-color: #4fc3f7;
}

.search-icon {
  color: #5a6a7a;
  font-size: 16px;
  margin-right: 8px;
  flex-shrink: 0;
}

.search-input {
  flex: 1;
  background: transparent;
  border: none;
  outline: none;
  color: #e0e6ed;
  font-size: 14px;
  font-family: inherit;
  padding: 8px 0;
}

.search-input::placeholder {
  color: #5a6a7a;
}

.count-badge {
  background: rgba(79, 195, 247, 0.15);
  color: #4fc3f7;
  font-size: 11px;
  font-weight: 700;
  padding: 2px 7px;
  border-radius: 10px;
  flex-shrink: 0;
  min-width: 18px;
  text-align: center;
}

.hidden {
  visibility: hidden;
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
  white-space: nowrap;
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
  border-color: #4fc3f7;
  color: #4fc3f7;
}

.btn-sm {
  padding: 4px 12px;
  border-radius: 4px;
  font-family: inherit;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  border: none;
  outline: none;
  white-space: nowrap;
}

.btn-sm.secondary {
  background: transparent;
  color: #e0e6ed;
  border: 1px solid #2a2f3e;
}

.btn-sm.secondary:hover:not(:disabled) {
  border-color: #4fc3f7;
  color: #4fc3f7;
}

.btn-sm.danger {
  background: transparent;
  color: #ef5350;
  border: 1px solid #ef5350;
}

.btn-sm.danger:hover:not(:disabled) {
  background: rgba(239, 83, 80, 0.1);
}

.btn-sm:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Table */
.table-container {
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  overflow: hidden;
}

.env-table {
  width: 100%;
  border-collapse: collapse;
  text-align: left;
  font-size: 14px;
}

.env-table th {
  padding: 12px 16px;
  background: rgba(42, 47, 62, 0.5);
  color: #8899aa;
  font-weight: 600;
  border-bottom: 1px solid #2a2f3e;
  white-space: nowrap;
}

.env-table td {
  padding: 12px 16px;
  border-bottom: 1px solid #2a2f3e;
  color: #e0e6ed;
}

.env-table tr:last-child td {
  border-bottom: none;
}

.env-row {
  transition: background 0.1s ease;
}

.env-row:hover td {
  background: rgba(42, 47, 62, 0.3);
}

.env-row:hover td:first-child {
  box-shadow: inset 3px 0 0 #4fc3f7;
}

.col-key {
  width: 30%;
}

.col-value {
  width: 45%;
}

.col-actions {
  width: 25%;
  text-align: right;
}

.var-key {
  font-family: monospace;
  font-weight: 600;
  color: #4fc3f7;
}

.value-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}

.var-value {
  font-family: monospace;
  color: #e0e6ed;
  word-break: break-all;
  flex: 1;
}

.var-value.masked {
  color: #5a6a7a;
}

.btn-toggle-vis {
  flex-shrink: 0;
}

.actions-cell {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
}

.confirm-text {
  font-size: 12px;
  color: #ffa726;
  font-weight: 600;
  white-space: nowrap;
}

/* Icon buttons */
.btn-icon {
  background: transparent;
  border: none;
  cursor: pointer;
  padding: 4px;
  border-radius: 4px;
  display: inline-flex;
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

/* Empty state */
.empty-state {
  padding: 56px 24px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}

.empty-icon {
  color: #3a4f5e;
  margin-bottom: 4px;
}

.empty-text {
  color: #8899aa;
  font-size: 16px;
  margin: 0;
}

.empty-hint {
  color: #5a6a7a;
  font-size: 13px;
  margin: 0;
}

.empty-action {
  margin-top: 8px;
}

/* Pagination */
.pagination {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.pagination-info {
  font-size: 13px;
  color: #8899aa;
}

.pagination-controls {
  display: flex;
  gap: 8px;
}

.page-btn {
  padding: 6px 14px;
  background: transparent;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  color: #e0e6ed;
  font-size: 13px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: all 0.15s ease;
}

.page-btn:hover:not(:disabled) {
  border-color: #4fc3f7;
  color: #4fc3f7;
}

.page-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
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
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  padding: 24px;
  width: 100%;
  max-width: 480px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* View Toggle */
.view-toggle {
  display: flex;
  align-items: center;
  gap: 12px;
}

.view-label {
  font-size: 14px;
  font-weight: 600;
  color: #4fc3f7;
}

.dirty-indicator {
  font-size: 12px;
  color: #ffa726;
  font-weight: 600;
  background: rgba(255, 167, 38, 0.1);
  padding: 2px 8px;
  border-radius: 4px;
  border: 1px solid rgba(255, 167, 38, 0.2);
}

/* JSON Editor (inline) */
.json-editor-container {
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
}

.json-editor-hint {
  font-size: 13px;
  color: #8899aa;
}

.json-editor-textarea {
  flex: 1;
  min-height: 400px;
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  color: #e0e6ed;
  font-family: monospace;
  font-size: 13px;
  line-height: 1.6;
  padding: 16px;
  outline: none;
  resize: vertical;
  transition: border-color 0.15s ease;
  box-sizing: border-box;
  width: 100%;
  tab-size: 2;
}

.json-editor-textarea:focus {
  border-color: #4fc3f7;
}

.dialog-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: #e0e6ed;
  padding-bottom: 12px;
  border-bottom: 1px solid #2a2f3e;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.form-group label {
  font-size: 14px;
  color: #8899aa;
  font-weight: 600;
}

.input-field {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  color: #e0e6ed;
  padding: 10px 12px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  outline: none;
  transition: all 0.15s ease;
  box-sizing: border-box;
  width: 100%;
}

.input-field:focus {
  border-color: #4fc3f7;
}

.input-field:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.value-input-group {
  display: flex;
  gap: 8px;
  align-items: center;
}

.value-input-group .input-field {
  flex: 1;
}

.json-error {
  margin: 0;
  font-size: 12px;
  color: #ef5350;
  font-weight: 600;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 8px;
  padding-top: 12px;
  border-top: 1px solid #2a2f3e;
}

/* Dialog transition */
.dialog-fade-enter-active,
.dialog-fade-leave-active {
  transition: opacity 0.15s ease;
}

.dialog-fade-enter-active .dialog,
.dialog-fade-leave-active .dialog {
  transition: transform 0.15s ease;
}

.dialog-fade-enter-from,
.dialog-fade-leave-to {
  opacity: 0;
}

.dialog-fade-enter-from .dialog,
.dialog-fade-leave-to .dialog {
  transform: scale(0.95) translateY(-8px);
}

/* Responsive */
@media (max-width: 640px) {
  .toolbar {
    flex-direction: column;
    align-items: stretch;
  }

  .toolbar-left {
    max-width: none;
  }

  .toolbar-right {
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .col-value {
    width: 35%;
  }

  .col-actions {
    width: 30%;
  }

  .dialog {
    margin: 16px;
    max-width: none;
  }
}

/* === Global Sync Card === */
.gsc {
  position: relative;
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  overflow: hidden;
  transition: border-color 0.18s ease;
}
.gsc:hover {
  border-color: #3a4156;
}
.gsc-stripe {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 2px;
  background: #5a6a7a;
  transition: background 0.18s ease;
}
.gsc.state-enabled .gsc-stripe { background: #4caf50; }
.gsc.state-disabled .gsc-stripe { background: #5a6a7a; }
.gsc.state-unsupported .gsc-stripe { background: #ef5350; }
.gsc.state-loading .gsc-stripe { background: #ffa726; }

.gsc-body {
  padding: 16px 18px 14px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.gsc-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.gsc-title-block {
  flex: 1;
  min-width: 240px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.gsc-title {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.gsc-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #5a6a7a;
  display: inline-block;
  flex-shrink: 0;
  transition: background 0.18s ease;
}
.gsc-dot.dot-enabled { background: #4caf50; box-shadow: 0 0 0 3px rgba(76, 175, 80, 0.15); }
.gsc-dot.dot-disabled { background: #5a6a7a; }
.gsc-dot.dot-unsupported { background: #ef5350; box-shadow: 0 0 0 3px rgba(239, 83, 80, 0.15); }
.gsc-dot.dot-loading { background: #ffa726; animation: gsc-pulse 1.2s ease-in-out infinite; }

@keyframes gsc-pulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.4; transform: scale(0.85); }
}

.gsc-name {
  font-size: 14px;
  font-weight: 600;
  color: #e0e6ed;
  letter-spacing: 0.01em;
}

.gsc-platform-tag {
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 11px;
  font-weight: 600;
  color: #5a6a7a;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid #2a2f3e;
  padding: 1px 7px;
  border-radius: 4px;
  text-transform: lowercase;
}

.gsc-badge {
  font-size: 11px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 4px;
  border: 1px solid #2a2f3e;
  background: rgba(255, 255, 255, 0.02);
  color: #8899aa;
  letter-spacing: 0.02em;
}
.gsc-badge.badge-enabled { color: #4caf50; border-color: rgba(76, 175, 80, 0.35); background: rgba(76, 175, 80, 0.08); }
.gsc-badge.badge-disabled { color: #8899aa; }
.gsc-badge.badge-unsupported { color: #ef5350; border-color: rgba(239, 83, 80, 0.35); background: rgba(239, 83, 80, 0.08); }
.gsc-badge.badge-loading { color: #ffa726; border-color: rgba(255, 167, 38, 0.35); background: rgba(255, 167, 38, 0.08); }

.gsc-meta {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  color: #8899aa;
}

.gsc-control {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.gsc-control-hint {
  font-size: 12px;
  color: #ffa726;
  font-weight: 600;
}

.gsc-switch {
  position: relative;
  display: inline-block;
  width: 44px;
  height: 22px;
  flex-shrink: 0;
}
.gsc-switch input {
  opacity: 0;
  width: 0;
  height: 0;
}
.gsc-switch .slider {
  position: absolute;
  cursor: pointer;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: #2a2f3e;
  border: 1px solid #2a2f3e;
  transition: background 0.15s ease, border-color 0.15s ease, opacity 0.15s ease;
  border-radius: 22px;
}
.gsc-switch .slider::before {
  position: absolute;
  content: "";
  height: 16px;
  width: 16px;
  left: 2px;
  bottom: 2px;
  background-color: #8899aa;
  transition: transform 0.15s ease, background-color 0.15s ease;
  border-radius: 50%;
}
.gsc-switch input:checked + .slider {
  background-color: rgba(79, 195, 247, 0.15);
  border-color: #4fc3f7;
}
.gsc-switch input:checked + .slider::before {
  transform: translateX(22px);
  background-color: #4fc3f7;
}
.gsc-switch.is-disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
.gsc-switch.is-disabled .slider {
  cursor: not-allowed;
}
.gsc-switch.is-busy .slider {
  cursor: wait;
}
.gsc-switch.state-enabled input:checked + .slider {
  background-color: rgba(76, 175, 80, 0.18);
  border-color: #4caf50;
}
.gsc-switch.state-enabled input:checked + .slider::before {
  background-color: #4caf50;
}

.gsc-notes {
  list-style: none;
  margin: 0;
  padding: 12px 14px 4px;
  background: rgba(255, 255, 255, 0.02);
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.gsc-notes li {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  font-size: 12.5px;
  line-height: 1.55;
  color: #8899aa;
}
.gsc-notes li code {
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 12px;
  color: #4fc3f7;
  background: rgba(79, 195, 247, 0.08);
  padding: 0 4px;
  border-radius: 3px;
}
.gsc-notes li strong {
  color: #e0e6ed;
  font-weight: 600;
}
.gsc-num {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: rgba(79, 195, 247, 0.1);
  color: #4fc3f7;
  font-size: 11px;
  font-weight: 700;
  flex-shrink: 0;
  font-family: 'Consolas', 'Monaco', monospace;
}
.gsc.state-enabled .gsc-num { background: rgba(76, 175, 80, 0.12); color: #4caf50; }
.gsc.state-unsupported .gsc-num { background: rgba(239, 83, 80, 0.12); color: #ef5350; }

@media (max-width: 640px) {
  .gsc-head {
    flex-direction: column;
    align-items: stretch;
  }
  .gsc-control {
    align-self: flex-end;
  }
  .gsc-notes {
    padding: 10px 12px 2px;
  }
}
</style>
