<template>
  <section class="view-logs">
    <PageHead title="系统日志" description="查看应用运行日志与调试信息" />

    <!-- Filters Card -->
    <ConfigCard>
      <div class="filter-row">
        <div class="filter-group">
          <label>级别</label>
          <select v-model="filterLevel" class="filter-select">
            <option value="">全部</option>
            <option value="ERROR">ERROR</option>
            <option value="WARN">WARN</option>
            <option value="INFO">INFO</option>
            <option value="DEBUG">DEBUG</option>
          </select>
        </div>
        <div class="filter-group">
          <label>来源</label>
          <select v-model="filterSource" class="filter-select">
            <option value="">全部</option>
            <option v-for="s in sources" :key="s" :value="s">{{ s }}</option>
          </select>
        </div>
        <div class="filter-group flex-1">
          <label>搜索</label>
          <TextInput
            v-model="filterKeyword"
            placeholder="输入关键词..."
            @update:model-value="debouncedRefresh"
          />
        </div>
        <div class="filter-group">
          <label>条数</label>
          <select v-model="filterLimit" class="filter-select">
            <option :value="50">50</option>
            <option :value="100">100</option>
            <option :value="200">200</option>
            <option :value="500">500</option>
            <option :value="0">全部</option>
          </select>
        </div>
        <div class="filter-group">
          <label>&nbsp;</label>
          <label class="checkbox-label">
            <input type="checkbox" v-model="autoRefresh" />
            <span>自动刷新</span>
          </label>
        </div>
      </div>
    </ConfigCard>

    <!-- Logs Table -->
    <ConfigCard>
      <div class="card-head">
        <h2>日志记录</h2>
        <div class="head-actions">
          <span class="log-count">{{ entries.length }} 条</span>
          <AppButton variant="ghost" size="small" @click="handleExport">导出</AppButton>
          <AppButton variant="danger" size="small" @click="handleClear">清除</AppButton>
        </div>
      </div>

      <div v-if="entries.length === 0" class="empty-container">
        <EmptyState icon="" title="暂无日志" description="当前筛选条件下无日志记录" />
      </div>

      <div v-else class="log-table-wrapper">
        <table class="log-table">
          <thead>
            <tr>
              <th class="col-time">时间</th>
              <th class="col-level">级别</th>
              <th class="col-source">来源</th>
              <th class="col-message">消息</th>
              <th class="col-detail">详情</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(entry, idx) in entries"
              :key="idx"
              :class="['log-row', 'row-' + entry.level.toLowerCase()]"
            >
              <td class="col-time mono">{{ formatTime(entry.time) }}</td>
              <td class="col-level">
                <span :class="['level-badge', 'badge-' + entry.level.toLowerCase()]">
                  {{ entry.level }}
                </span>
              </td>
              <td class="col-source mono">{{ entry.source }}</td>
              <td class="col-message">{{ entry.message }}</td>
              <td class="col-detail mono" :title="entry.detail">
                {{ entry.detail || '-' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </ConfigCard>

    <!-- Log Files -->
    <ConfigCard>
      <div class="card-head">
        <h2>日志文件</h2>
      </div>

      <div v-if="logFiles.length === 0" class="empty-container">
        <EmptyState icon="" title="无日志文件" description="当前无持久化日志文件" />
      </div>

      <div v-else>
        <div class="file-list">
          <div
            v-for="f in logFiles"
            :key="f"
            class="file-item"
            :class="{ active: selectedFile === f }"
            @click="loadFileContent(f)"
          >
            <span class="file-icon">📄</span>
            <span class="file-name">{{ f }}</span>
          </div>
        </div>

        <div v-if="fileContent" class="file-preview">
          <div class="file-preview-header">
            <span>{{ selectedFile }}</span>
            <AppButton variant="ghost" size="small" @click="fileContent = ''; selectedFile = ''">
              关闭
            </AppButton>
          </div>
          <pre class="file-content">{{ fileContent }}</pre>
        </div>
      </div>
    </ConfigCard>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue'
import PageHead from '../components/ui/PageHead.vue'
import ConfigCard from '../components/ui/ConfigCard.vue'
import TextInput from '../components/ui/TextInput.vue'
import AppButton from '../components/ui/AppButton.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import { useToast } from '../composables/useToast'
import {
  GetLogs,
  GetLogSources,
  GetLogFiles,
  GetLogFileContent,
  ClearLogs,
  ExportLogs,
} from '../../wailsjs/go/main/App'

interface LogEntry {
  time: string
  level: string
  source: string
  message: string
  detail?: string
}

const entries = ref<LogEntry[]>([])
const sources = ref<string[]>([])
const logFiles = ref<string[]>([])
const fileContent = ref('')
const selectedFile = ref('')

const filterLevel = ref('')
const filterSource = ref('')
const filterKeyword = ref('')
const filterLimit = ref(100)
const autoRefresh = ref(true)

const { showSuccess, showError } = useToast()

let refreshTimer: number | null = null
let debounceTimer: number | null = null

const refreshLogs = async () => {
  try {
    entries.value = await GetLogs(
      filterLevel.value,
      filterSource.value,
      filterKeyword.value,
      filterLimit.value
    )
  } catch (err) {
    console.error('Failed to load logs:', err)
  }
}

const refreshSources = async () => {
  try {
    sources.value = await GetLogSources()
  } catch (err) {
    console.error('Failed to load sources:', err)
  }
}

const refreshFiles = async () => {
  try {
    logFiles.value = await GetLogFiles()
  } catch (err) {
    console.error('Failed to load log files:', err)
  }
}

const debouncedRefresh = () => {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = window.setTimeout(refreshLogs, 300)
}

const loadFileContent = async (filename: string) => {
  try {
    selectedFile.value = filename
    fileContent.value = await GetLogFileContent(filename)
  } catch (err) {
    showError('加载文件失败: ' + err)
  }
}

const handleClear = async () => {
  if (!confirm('确定要清除内存中的日志吗？')) return
  try {
    await ClearLogs()
    await refreshLogs()
    showSuccess('日志已清除')
  } catch (err) {
    showError('清除失败: ' + err)
  }
}

const handleExport = async () => {
  try {
    const json = await ExportLogs()
    const blob = new Blob([json], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `amagi-codebox-logs-${new Date().toISOString().replace(/[:.]/g, '-')}.json`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
    showSuccess('日志已导出')
  } catch (err) {
    showError('导出失败: ' + err)
  }
}

function formatTime(t: string): string {
  const parts = t.split(' ')
  return parts.length > 1 ? parts[1] : t
}

watch([filterLevel, filterSource, filterLimit], () => {
  refreshLogs()
})

watch(autoRefresh, (val) => {
  if (val) {
    refreshTimer = window.setInterval(() => {
      refreshLogs()
      refreshSources()
    }, 2000)
  } else if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
})

onMounted(async () => {
  await refreshLogs()
  await refreshSources()
  await refreshFiles()
  if (autoRefresh.value) {
    refreshTimer = window.setInterval(() => {
      refreshLogs()
      refreshSources()
    }, 2000)
  }
})

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  if (debounceTimer) clearTimeout(debounceTimer)
})
</script>

<style scoped>
.view-logs {
  padding: 32px 36px;
  gap: 22px;
  overflow: auto;
  display: flex;
  flex-direction: column;
}

.filter-row {
  display: flex;
  gap: 12px;
  align-items: flex-end;
  flex-wrap: wrap;
}

.filter-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.filter-group label {
  font-size: 12px;
  color: var(--secondary);
  font-weight: 500;
}

.flex-1 {
  flex: 1;
  min-width: 150px;
}

.filter-select {
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
  min-width: 100px;
  background-image: url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='10' height='10' viewBox='0 0 24 24' fill='none' stroke='%238E8E93' stroke-width='2.5' stroke-linecap='round'><polyline points='6 9 12 15 18 9'/></svg>");
  background-repeat: no-repeat;
  background-position: right 9px center;
  transition: background-color 0.12s;
}

.filter-select:hover {
  background-color: var(--controlHover);
}

.checkbox-label {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  font-size: 13px;
  color: var(--label);
  user-select: none;
}

.checkbox-label input[type="checkbox"] {
  width: 14px;
  height: 14px;
  accent-color: var(--accent);
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 4px;
}

.card-head h2 {
  font-size: 17px;
  font-weight: 600;
}

.head-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.log-count {
  font-size: 12px;
  color: var(--tertiary);
  margin-right: 4px;
}

.empty-container {
  padding: 20px 0;
}

.log-table-wrapper {
  max-height: 500px;
  overflow-y: auto;
  border: 1px solid var(--separator);
  border-radius: 10px;
}

.log-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.log-table thead {
  position: sticky;
  top: 0;
  z-index: 1;
}

.log-table th {
  background: var(--sidebar);
  color: var(--secondary);
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  padding: 10px 14px;
  text-align: left;
  border-bottom: 1px solid var(--separator);
}

.log-table td {
  padding: 8px 14px;
  border-bottom: 1px solid var(--separator);
  color: var(--label);
  vertical-align: top;
}

.log-table tr:last-child td {
  border-bottom: none;
}

.col-time {
  width: 90px;
}

.col-level {
  width: 70px;
}

.col-source {
  width: 90px;
}

.col-message {
  min-width: 200px;
}

.col-detail {
  max-width: 300px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mono {
  font-family: var(--mono);
  font-size: 12px;
}

.level-badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.3px;
}

.badge-error {
  background: rgba(255, 59, 48, 0.12);
  color: var(--danger);
}

.badge-warn {
  background: rgba(255, 149, 0, 0.12);
  color: var(--warning);
}

.badge-info {
  background: rgba(0, 122, 255, 0.12);
  color: var(--accent);
}

.badge-debug {
  background: var(--control);
  color: var(--tertiary);
}

.log-row.row-error {
  background: rgba(255, 59, 48, 0.04);
}

.log-row.row-warn {
  background: rgba(255, 149, 0, 0.03);
}

.file-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 12px;
}

.file-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  color: var(--secondary);
  cursor: pointer;
  font-size: 13px;
  transition: all 0.15s;
}

.file-item:hover {
  border-color: var(--accent);
  color: var(--label);
}

.file-item.active {
  border-color: var(--accent);
  color: var(--accent);
  background: rgba(0, 122, 255, 0.06);
}

.file-icon {
  font-size: 14px;
}

.file-preview {
  margin-top: 12px;
}

.file-preview-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
  color: var(--secondary);
  font-size: 13px;
}

.file-content {
  background: var(--sidebar);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 12px;
  font-family: var(--mono);
  font-size: 12px;
  color: var(--label);
  max-height: 400px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
}
</style>
