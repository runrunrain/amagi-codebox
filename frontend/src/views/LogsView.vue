<template>
  <section class="view-logs">
    <!-- Loading state -->
    <LoadingState v-if="loading" message="加载日志中..." />

    <!-- Error state -->
    <ErrorState
      v-else-if="error"
      :message="error"
      :on-retry="handleRetry"
    />

    <!-- Main content -->
    <template v-else>
    <PageHead title="系统日志" description="查看应用运行日志与调试信息" />

    <!-- Headroom 压缩统计（累计 · 全局 ledger） -->
    <ConfigCard class="headroom-card">
      <div class="headroom-head">
        <div class="headroom-title">
          <h2>Headroom 上下文压缩统计</h2>
          <span class="headroom-sub">累计统计 · 全局 ledger · 每 10 秒自动刷新</span>
        </div>
        <span
          v-if="!headroomLoading && !headroomError && headroomReport && headroomReport.lifetime.calls > 0"
          class="headroom-live"
        >
          <span class="live-dot" />数据已同步
        </span>
      </div>

      <!-- 加载态：仅首次拉取显示，避免每 10s 闪烁 -->
      <div v-if="headroomLoading" class="headroom-inline-state">
        <div class="spinner-sm" />
        <span>正在读取压缩统计...</span>
      </div>

      <!-- 空态 / 错误态：友好提示，绝不刷屏弹 toast -->
      <div
        v-else-if="headroomError || !headroomReport || headroomReport.lifetime.calls === 0"
        class="headroom-inline-state"
      >
        <span class="state-dot" />
        <span>{{ headroomEmptyMessage }}</span>
      </div>

      <!-- 成功态 -->
      <div v-else class="headroom-stats">
        <div class="metric metric-primary">
          <span class="metric-label">累计压缩次数</span>
          <span class="metric-value mono">{{ formatNumber(headroomReport.lifetime.calls) }}</span>
        </div>
        <div class="metric metric-primary">
          <span class="metric-label">累计节省 Token</span>
          <span class="metric-value mono accent">{{ formatNumber(headroomReport.lifetime.tokens_saved) }}</span>
        </div>
        <div class="metric">
          <span class="metric-label">累计节省比例</span>
          <span class="metric-value mono">{{ formatPercent(headroomReport.lifetime.savings_percent) }}</span>
        </div>
        <div class="metric">
          <span class="metric-label">累计避免成本</span>
          <span class="metric-value mono">{{ formatCost(headroomReport.lifetime.cost_usd) }}</span>
        </div>

        <div
          v-if="headroomReport.by_client && headroomReport.by_client.length"
          class="client-breakdown"
        >
          <span class="client-label">来源客户端</span>
          <span
            v-for="c in headroomReport.by_client"
            :key="c.client"
            class="client-chip"
            :title="`${c.client}：${formatNumber(c.calls)} 次压缩，节省 ${formatNumber(c.tokens_saved)} token`"
          >
            <span class="client-name mono">{{ c.client }}</span>
            <span class="client-calls">{{ formatNumber(c.calls) }} 次</span>
          </span>
        </div>
      </div>
    </ConfigCard>

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
            <svg class="file-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
  <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
  <polyline points="14 2 14 8 20 8"/>
  <line x1="12" y1="18" x2="12" y2="12"/>
  <line x1="9" y1="15" x2="15" y2="15"/>
</svg>
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
    </template>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, computed } from 'vue'
import PageHead from '../components/ui/PageHead.vue'
import ConfigCard from '../components/ui/ConfigCard.vue'
import TextInput from '../components/ui/TextInput.vue'
import AppButton from '../components/ui/AppButton.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import LoadingState from '../components/ui/LoadingState.vue'
import ErrorState from '../components/ui/ErrorState.vue'
import { useToast } from '../composables/useToast'
import {
  GetLogs,
  GetLogSources,
  GetLogFiles,
  GetLogFileContent,
  ClearLogs,
  ExportLogs,
} from '../../wailsjs/go/main/App'
import { headroom } from '../../wailsjs/go/models'
import { getHeadroomSavings } from '../api/headroom'

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

// Loading and error states
const loading = ref(true)
const error = ref('')

const filterLevel = ref('')
const filterSource = ref('')
const filterKeyword = ref('')
const filterLimit = ref(100)
const autoRefresh = ref(true)

const { showSuccess, showError } = useToast()

let refreshTimer: number | null = null
let debounceTimer: number | null = null

// --- Headroom 压缩统计（独立 10s 定时器，与日志 2s 刷新解耦） ---
// 每次拉取会触发 headroom 子进程，故降频至 10s；autoRefresh 开关与日志共享。
const HEADROOM_REFRESH_INTERVAL = 10000
let headroomTimer: number | null = null

const headroomLoading = ref(true)
const headroomReport = ref<headroom.SavingsReport | null>(null)
const headroomError = ref(false)

const headroomEmptyMessage = computed(() => {
  // 区分两种空态文案：调用失败（未安装/未启用） vs 已就绪但无数据（calls===0）
  if (headroomError.value) return 'Headroom 未安装或未启用，暂无压缩数据'
  return 'Headroom 已就绪，暂无压缩数据'
})

function formatNumber(n: number): string {
  // 千分位格式化（en-US 逗号分组，与 mono 字体一致）
  return Number(n || 0).toLocaleString('en-US')
}

function formatPercent(p: number): string {
  return `${Number(p || 0).toFixed(1)}%`
}

function formatCost(usd: number): string {
  return `$${Number(usd || 0).toFixed(2)}`
}

/**
 * 拉取 Headroom 压缩统计。
 * @param showLoading 是否显示加载态（仅首次拉取传 true，10s 定时刷新传 false 避免闪烁）。
 * 错误处理：GetHeadroomSavings 在 headroom 未安装/子进程失败/JSON 解析失败时 reject。
 * 静默置空态，绝不每 10s 弹 toast 刷屏；瞬时失败时保留已有数据，避免数字闪烁清空。
 */
const refreshHeadroomSavings = async (showLoading = false) => {
  if (showLoading) headroomLoading.value = true
  try {
    const report = await getHeadroomSavings()
    headroomReport.value = report
    headroomError.value = false
  } catch (err) {
    // 仅在从未拿到过数据时进入空态；已有数据则保留（略陈旧但有信息量），避免瞬时失败清空。
    if (!headroomReport.value) {
      headroomError.value = true
    }
    if (showLoading) {
      // 仅首次加载打印一次诊断日志，便于排查；后续失败静默
      console.warn('[LogsView] GetHeadroomSavings failed:', err)
    }
  } finally {
    if (showLoading) headroomLoading.value = false
  }
}


// Retry function for ErrorState
const handleRetry = async () => {
  loading.value = true
  error.value = ''
  try {
    await Promise.all([refreshLogs(), refreshSources(), refreshFiles()])
  } catch (err) {
    error.value = String(err)
  } finally {
    loading.value = false
  }
}

const refreshLogs = async () => {
  try {
    entries.value = await GetLogs(
      filterLevel.value,
      filterSource.value,
      filterKeyword.value,
      filterLimit.value
    )
  } catch (err) {
    error.value = String(err)
  }
}

const refreshSources = async () => {
  try {
    sources.value = await GetLogSources()
  } catch (err) {
    error.value = String(err)
  }
}

const refreshFiles = async () => {
  try {
    logFiles.value = await GetLogFiles()
  } catch (err) {
    error.value = String(err)
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
    // Headroom 统计独立 10s 定时器（每次调用会起子进程，不能跟日志共用 2s）
    if (!headroomTimer) {
      headroomTimer = window.setInterval(() => {
        refreshHeadroomSavings(false)
      }, HEADROOM_REFRESH_INTERVAL)
    }
  } else {
    if (refreshTimer) {
      clearInterval(refreshTimer)
      refreshTimer = null
    }
    if (headroomTimer) {
      clearInterval(headroomTimer)
      headroomTimer = null
    }
  }
})

onMounted(async () => {
  loading.value = true
  error.value = ''
  try {
    await Promise.all([refreshLogs(), refreshSources(), refreshFiles()])
  } finally {
    loading.value = false
  }
  if (autoRefresh.value) {
    refreshTimer = window.setInterval(() => {
      refreshLogs()
      refreshSources()
    }, 2000)
  }
  // Headroom 统计：首拉 + 启动独立 10s 定时器（autoRefresh 关时不启）
  refreshHeadroomSavings(true)
  if (autoRefresh.value && !headroomTimer) {
    headroomTimer = window.setInterval(() => {
      refreshHeadroomSavings(false)
    }, HEADROOM_REFRESH_INTERVAL)
  }
})

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  if (debounceTimer) clearTimeout(debounceTimer)
  if (headroomTimer) clearInterval(headroomTimer)
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
  width: 14px;
  height: 14px;
  stroke: var(--secondary);
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

/* --- Headroom 压缩统计卡片 --- */
.headroom-card {
  /* 复用 ConfigCard 容器，仅追加内部布局样式 */
  gap: 14px;
}

.headroom-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.headroom-title {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.headroom-title h2 {
  font-size: 16px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
  letter-spacing: -0.2px;
}

.headroom-sub {
  font-size: 12px;
  color: var(--tertiary);
}

.headroom-live {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--secondary);
}

.live-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 18%, transparent);
}

/* 加载 / 空态 / 错误态共用内联条（紧凑，避免占据过多纵向空间） */
.headroom-inline-state {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 4px;
  font-size: 13px;
  color: var(--secondary);
}

.state-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--tertiary);
  flex-shrink: 0;
}

.spinner-sm {
  width: 14px;
  height: 14px;
  border: 2px solid var(--separator);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: headroom-spin 0.8s linear infinite;
  flex-shrink: 0;
}

@keyframes headroom-spin {
  to { transform: rotate(360deg); }
}

/* 成功态：指标仪表盘布局 */
.headroom-stats {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.metric {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 2px 0;
}

.metric-label {
  font-size: 12px;
  color: var(--tertiary);
  letter-spacing: 0.2px;
}

.metric-value {
  font-size: 20px;
  font-weight: 600;
  color: var(--label);
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
}

.metric-primary .metric-value {
  font-size: 26px;
}

.metric-value.accent {
  color: var(--accent);
}

.mono {
  font-family: var(--mono);
}

/* 客户端来源明细 */
.client-breakdown {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 12px;
  border-top: 1px solid var(--separator);
}

.client-label {
  font-size: 12px;
  color: var(--tertiary);
  margin-right: 4px;
}

.client-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--accent) 8%, var(--card));
  border: 1px solid var(--separator);
  font-size: 12px;
  color: var(--secondary);
}

.client-name {
  color: var(--label);
  font-weight: 600;
}

.client-calls {
  color: var(--tertiary);
}

/* 响应式：桌面 4 列指标仪表盘，平板 2 列，移动端单列 */
@media (min-width: 720px) {
  .headroom-stats {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    align-items: start;
    gap: 14px 20px;
  }

  /* 指标列之间加细分隔线，强化"仪表盘"感而非堆叠卡片 */
  .metric + .metric {
    border-left: 1px solid var(--separator);
    padding-left: 20px;
  }

  /* client-breakdown 跨满 4 列 */
  .client-breakdown {
    grid-column: 1 / -1;
  }
}

@media (max-width: 719px) {
  .metric-value,
  .metric-primary .metric-value {
    font-size: 22px;
  }
}
</style>
