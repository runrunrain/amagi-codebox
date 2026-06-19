<template>
  <div class="logs-page">
    <div class="page-header">
      <h1 class="page-title">系统日志</h1>
      <div class="header-actions">
        <button class="btn small" @click="handleExport" title="导出 JSON">导出</button>
        <button class="btn small danger" @click="handleClear" title="清除内存日志">清除</button>
      </div>
    </div>

    <!-- 过滤器 -->
    <div class="card filter-card">
      <div class="filter-row">
        <div class="filter-group">
          <label>级别</label>
          <select v-model="filterLevel" class="input-field">
            <option value="">全部</option>
            <option value="DEBUG">DEBUG</option>
            <option value="INFO">INFO</option>
            <option value="WARN">WARN</option>
            <option value="ERROR">ERROR</option>
          </select>
        </div>
        <div class="filter-group">
          <label>来源</label>
          <select v-model="filterSource" class="input-field">
            <option value="">全部</option>
            <option v-for="s in sources" :key="s" :value="s">{{ s }}</option>
          </select>
        </div>
        <div class="filter-group flex-1">
          <label>搜索</label>
          <input
            type="text"
            v-model="filterKeyword"
            class="input-field"
            placeholder="输入关键词..."
            @input="debouncedRefresh"
          />
        </div>
        <div class="filter-group">
          <label>条数</label>
          <select v-model="filterLimit" class="input-field">
            <option :value="100">100</option>
            <option :value="200">200</option>
            <option :value="500">500</option>
            <option :value="0">全部</option>
          </select>
        </div>
        <div class="filter-group auto-refresh-group">
          <label>&nbsp;</label>
          <label class="checkbox-label">
            <input type="checkbox" v-model="autoRefresh" />
            <span class="checkbox-text">自动刷新</span>
          </label>
        </div>
      </div>
    </div>

    <!-- 日志表格 -->
    <div class="card log-table-card">
      <div class="log-count">
        <span class="muted">{{ entries.length }} 条日志</span>
      </div>
      <div class="log-table-wrapper" ref="tableWrapper">
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
            <tr v-for="(entry, idx) in entries" :key="idx" :class="'row-' + entry.level.toLowerCase()">
              <td class="col-time mono">{{ formatTime(entry.time) }}</td>
              <td class="col-level">
                <span class="level-badge" :class="'badge-' + entry.level.toLowerCase()">
                  {{ entry.level }}
                </span>
              </td>
              <td class="col-source mono">{{ entry.source }}</td>
              <td class="col-message">{{ entry.message }}</td>
              <td class="col-detail mono" :title="entry.detail">{{ entry.detail || '-' }}</td>
            </tr>
            <tr v-if="entries.length === 0">
              <td colspan="5" class="empty-row">暂无日志</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 日志文件 -->
    <div class="card files-card">
      <h2>日志文件</h2>
      <div class="file-list" v-if="logFiles.length > 0">
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
      <div class="empty" v-else>
        <span class="muted">无日志文件</span>
      </div>

      <!-- 文件内容预览 -->
      <div class="file-preview" v-if="fileContent">
        <div class="file-preview-header">
          <span>{{ selectedFile }}</span>
          <button class="btn small" @click="fileContent = ''; selectedFile = ''">关闭</button>
        </div>
        <pre class="file-content">{{ fileContent }}</pre>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { GetLogs, GetLogSources, GetLogFiles, GetLogFileContent, ClearLogs, ExportLogs } from '../../wailsjs/go/main/App'
import { useToast } from '../composables/useToast'

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
const filterLimit = ref(200)
const autoRefresh = ref(true)

const tableWrapper = ref<HTMLElement | null>(null)

const { showSuccess, showError } = useToast()

let refreshTimer: number | null = null
let debounceTimer: number | null = null

const refreshLogs = async () => {
  try {
    entries.value = await GetLogs(filterLevel.value, filterSource.value, filterKeyword.value, filterLimit.value)
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
    // 创建下载
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
  // 只显示时:分:秒.毫秒
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
.logs-page {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.page-title {
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  color: #e0e6ed;
}

.header-actions {
  display: flex;
  gap: 8px;
}

.card {
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  padding: 16px;
}

.card h2 {
  margin: 0 0 12px 0;
  font-size: 16px;
  font-weight: 600;
  color: #e0e6ed;
}

/* Filter */
.filter-row {
  display: flex;
  gap: 12px;
  align-items: flex-end;
  flex-wrap: wrap;
}

.filter-group {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.filter-group label {
  font-size: 12px;
  color: #5a6a7a;
}

.flex-1 { flex: 1; min-width: 150px; }

.input-field {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  color: #e0e6ed;
  padding: 8px 10px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 13px;
  outline: none;
  transition: border-color 0.15s;
}

.input-field:focus { border-color: #4fc3f7; }

.auto-refresh-group { justify-content: flex-end; }

.checkbox-label {
  display: flex;
  align-items: center;
  cursor: pointer;
  user-select: none;
  gap: 6px;
}

.checkbox-label input {
  width: 14px;
  height: 14px;
  accent-color: #4fc3f7;
}

.checkbox-text { color: #ccd6e0; font-size: 13px; }

/* Log Table */
.log-count {
  margin-bottom: 8px;
}

.muted { color: #5a6a7a; font-size: 12px; }

.log-table-wrapper {
  max-height: 500px;
  overflow-y: auto;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
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
  background: #151a26;
  color: #5a6a7a;
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  padding: 8px 10px;
  text-align: left;
  border-bottom: 1px solid #2a2f3e;
}

.log-table td {
  padding: 6px 10px;
  border-bottom: 1px solid #1e2433;
  color: #ccd6e0;
  vertical-align: top;
}

.col-time { width: 110px; }
.col-level { width: 70px; }
.col-source { width: 90px; }
.col-message { min-width: 200px; }
.col-detail { max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.mono { font-family: 'Consolas', 'Courier New', monospace; font-size: 12px; }

/* Level badges */
.level-badge {
  display: inline-block;
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.3px;
}

.badge-debug { background: rgba(100, 181, 246, 0.15); color: #64b5f6; }
.badge-info { background: rgba(102, 187, 106, 0.15); color: #66bb6a; }
.badge-warn { background: rgba(255, 167, 38, 0.15); color: #ffa726; }
.badge-error { background: rgba(239, 83, 80, 0.15); color: #ef5350; }

/* Row highlights */
.row-error { background: rgba(239, 83, 80, 0.05); }
.row-warn { background: rgba(255, 167, 38, 0.03); }

.empty-row {
  text-align: center;
  color: #5a6a7a;
  padding: 24px !important;
}

/* Files */
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
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  color: #8899aa;
  cursor: pointer;
  font-size: 13px;
  transition: all 0.15s;
}

.file-item:hover { border-color: #3a4f5e; color: #ccd6e0; }
.file-item.active { border-color: #4fc3f7; color: #4fc3f7; }

.file-icon { font-size: 14px; }

.file-preview { margin-top: 12px; }

.file-preview-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
  color: #8899aa;
  font-size: 13px;
}

.file-content {
  background: #0a0e17;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  padding: 12px;
  font-family: 'Consolas', 'Courier New', monospace;
  font-size: 12px;
  color: #b0bec5;
  max-height: 400px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
}

.empty { display: flex; align-items: center; justify-content: center; height: 40px; }

/* Buttons */
.btn {
  padding: 8px 16px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s;
  border: none;
  outline: none;
  background: #2a2f3e;
  color: #ccd6e0;
}

.btn:hover { background: #353b4e; }
.btn.small { padding: 5px 12px; font-size: 12px; }
.btn.danger { color: #ef5350; }
.btn.danger:hover { background: rgba(239, 83, 80, 0.1); }
</style>
