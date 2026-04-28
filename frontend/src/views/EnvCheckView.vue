<template>
  <div class="envcheck-section">
    <div class="section-header">
      <h2>环境检测</h2>
      <p>检测和管理 CLI 编码工具的安装状态与版本。</p>
    </div>

    <!-- 顶部状态栏 -->
    <div class="envcheck-status-bar" :class="statusBarClass">
      <div class="status-bar-left">
        <span v-if="overallLoading" class="status-indicator loading-indicator">
          <span class="spinner"></span>
          <span>正在检测环境...</span>
        </span>
        <span v-else-if="overallStatus && overallStatus.allOk" class="status-indicator ok-indicator">
          <span class="status-dot-dot ok-dot"></span>
          <span>全部正常</span>
        </span>
        <span v-else-if="overallStatus && !overallStatus.allOk" class="status-indicator warn-indicator">
          <span class="status-dot-dot warn-dot"></span>
          <span>存在 {{ issueCount }} 项问题</span>
        </span>
        <span v-else class="status-indicator idle-indicator">
          <span class="status-dot-dot idle-dot"></span>
          <span>尚未检测</span>
        </span>
      </div>
      <div class="status-bar-right">
        <button class="btn primary small" @click="runFullCheck" :disabled="overallLoading">
          <span v-if="overallLoading" class="btn-spinner"></span>
          {{ overallLoading ? '检测中...' : '一键检测全部' }}
        </button>
        <span v-if="lastCheckedAt" class="last-check-time">
          最后检测：{{ lastCheckedAt }}
        </span>
      </div>
    </div>

    <!-- CLI 工具卡片 -->
    <div class="tool-cards">
      <div
        v-for="card in cardDataList"
        :key="card.meta.key"
        class="tool-card"
        :class="{ 'has-issue': card.status && !card.status.installed }"
      >
        <!-- 卡片头部：图标 + 名称 + 安装状态 -->
        <div class="tool-card-header">
          <div class="tool-icon-wrapper" :style="{ background: card.meta.bgColor }">
            <span class="tool-icon-text">{{ card.meta.iconChar }}</span>
          </div>
          <div class="tool-title-area">
            <h3 class="tool-name">{{ card.meta.displayName }}</h3>
            <span v-if="card.loading" class="install-badge loading-badge">
              <span class="mini-spinner"></span> 检测中...
            </span>
            <span v-else-if="card.status && card.status.installed" class="install-badge badge-installed">
              已安装
            </span>
            <span v-else-if="card.status && !card.status.installed" class="install-badge badge-missing">
              未安装
            </span>
            <span v-else class="install-badge idle-badge">
              待检测
            </span>
          </div>
        </div>

        <!-- 卡片详情 -->
        <div class="tool-card-body" v-if="card.status">
          <!-- 安装方式 -->
          <div class="detail-row" v-if="card.status.installed">
            <span class="detail-label">安装方式</span>
            <span class="detail-value install-method-tag">{{ formatInstallMethod(card.status.installMethod) }}</span>
          </div>

          <!-- 版本号 -->
          <div class="detail-row" v-if="card.status.version">
            <span class="detail-label">版本</span>
            <span class="detail-value monospace">{{ card.status.version }}</span>
          </div>

          <!-- PATH 状态 -->
          <div class="detail-row" v-if="card.status.installed">
            <span class="detail-label">PATH</span>
            <span class="detail-value" :class="card.status.pathOk ? 'path-ok' : 'path-warn'">
              {{ card.status.pathOk ? '正常' : '异常' }}
            </span>
          </div>

          <!-- 可执行路径 -->
          <div class="detail-row" v-if="card.status.executablePath">
            <span class="detail-label">路径</span>
            <span class="detail-value monospace path-text" :title="card.status.executablePath">
              {{ card.status.executablePath }}
            </span>
          </div>

          <!-- 更新提示 -->
          <div class="update-hint" v-if="card.status.hasUpdate && card.status.latestVersion">
            <span class="hint-icon">&#x2191;</span>
            <span>有新版本 <strong>{{ card.status.latestVersion }}</strong> 可用</span>
          </div>

          <!-- 错误信息 -->
          <div class="error-hint" v-if="card.status.error">
            {{ card.status.error }}
          </div>
        </div>

        <!-- 未检测时的占位 -->
        <div class="tool-card-body placeholder-body" v-if="!card.status && !card.loading">
          <span class="placeholder-text">点击下方按钮开始检测</span>
        </div>

        <!-- 操作按钮 -->
        <div class="tool-card-actions">
          <button
            class="btn small action-btn check-btn"
            @click="runSingleCheck(card.meta.key)"
            :disabled="card.loading || !!card.operating"
          >
            <span v-if="card.loading" class="mini-spinner"></span>
            {{ card.loading ? '检测中...' : '检测安装情况' }}
          </button>

          <button
            v-if="card.status?.installed"
            class="btn small action-btn reinstall-btn"
            @click="reinstallTool(card.meta.key, card.meta.displayName)"
            :disabled="card.loading || !!card.operating"
          >
            <span v-if="card.operating === 'install'" class="mini-spinner"></span>
            {{ card.operating === 'install' ? '安装中...' : '重装' }}
          </button>

          <button
            v-if="card.status?.hasUpdate"
            class="btn small action-btn update-btn"
            @click="updateTool(card.meta.key, card.meta.displayName, card.status?.latestVersion || '')"
            :disabled="card.loading || !!card.operating"
          >
            <span v-if="card.operating === 'update'" class="mini-spinner"></span>
            {{ card.operating === 'update' ? '更新中...' : '更新到最新' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { GetEnvCheckStatus, RunEnvCheck, CheckTool, InstallTool, UpdateTool } from '../../wailsjs/go/main/App'
import { useToast } from '../composables/useToast'

const { showSuccess, showError } = useToast()

// ---------- Types ----------

interface CheckStatus {
  tool: string
  installed: boolean
  installMethod: string
  version: string
  hasUpdate: boolean
  latestVersion: string
  pathOk: boolean
  executablePath: string
  error: string
  checkedAt: string
}

interface OverallStatus {
  allOk: boolean
  items: Record<string, CheckStatus>
  issues: string[]
  checkedAt: string
}

interface ToolMeta {
  key: string
  displayName: string
  iconChar: string
  bgColor: string
}

interface CardData {
  meta: ToolMeta
  status: CheckStatus | null
  loading: boolean
  operating: string | false
}

// ---------- Tool metadata ----------

const toolMetaList: ToolMeta[] = [
  { key: 'claude_code', displayName: 'Claude Code', iconChar: 'C', bgColor: 'rgba(204,120,50,0.15)' },
  { key: 'opencode',   displayName: 'OpenCode',    iconChar: 'O', bgColor: 'rgba(79,195,247,0.15)' },
  { key: 'codex',      displayName: 'Codex',       iconChar: 'X', bgColor: 'rgba(102,187,106,0.15)' },
]

// ---------- State ----------

const overallStatus = ref<OverallStatus | null>(null)
const loadingTools = reactive<Record<string, boolean>>({})
const operating = reactive<Record<string, string | false>>({
  claude_code: false,
  opencode: false,
  codex: false,
})
const overallLoading = ref(false)

// ---------- Computed ----------

const cardDataList = computed<CardData[]>(() => {
  return toolMetaList.map(meta => ({
    meta,
    status: overallStatus.value?.items?.[meta.key] || null,
    loading: !!loadingTools[meta.key],
    operating: operating[meta.key] || false,
  }))
})

const issueCount = computed(() => {
  if (!overallStatus.value) return 0
  return overallStatus.value.issues?.length || 0
})

const lastCheckedAt = computed(() => {
  if (!overallStatus.value?.checkedAt) return ''
  try {
    const d = new Date(overallStatus.value.checkedAt)
    if (isNaN(d.getTime())) return ''
    return d.toLocaleString('zh-CN', {
      month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', second: '2-digit',
    })
  } catch {
    return ''
  }
})

const statusBarClass = computed(() => {
  if (overallLoading.value) return 'bar-loading'
  if (!overallStatus.value) return 'bar-idle'
  return overallStatus.value.allOk ? 'bar-ok' : 'bar-warn'
})

// ---------- Helpers ----------

function formatInstallMethod(m: string): string {
  const map: Record<string, string> = {
    native: 'native',
    winget: 'winget',
    npm: 'npm',
    unknown: 'unknown',
  }
  return map[m] || m
}

function issueForStatus(status: CheckStatus): string | null {
  if (status.error?.trim()) return `${status.tool}: ${status.error}`
  if (!status.installed) return `${status.tool}: not installed`
  if (!status.pathOk) return `${status.tool}: executable is not available in PATH`
  return null
}

function rebuildOverallStatus(items: Record<string, CheckStatus>, checkedAt: string): OverallStatus {
  const issues = toolMetaList
    .map(meta => items[meta.key])
    .filter((status): status is CheckStatus => !!status)
    .map(issueForStatus)
    .filter((issue): issue is string => !!issue)

  return {
    allOk: issues.length === 0 && toolMetaList.every(meta => !!items[meta.key]),
    items,
    issues,
    checkedAt,
  }
}

// ---------- Actions ----------

async function loadCachedStatus() {
  try {
    const status = await GetEnvCheckStatus()
    if (status) {
      overallStatus.value = status as unknown as OverallStatus
    }
  } catch {
    // no cache yet, silently ignore
  }
}

async function runFullCheck() {
  overallLoading.value = true
  for (const meta of toolMetaList) {
    loadingTools[meta.key] = true
  }
  try {
    const status = await RunEnvCheck()
    overallStatus.value = status as unknown as OverallStatus
    showSuccess('环境检测完成')
  } catch (err) {
    showError('检测失败: ' + err)
  } finally {
    overallLoading.value = false
    for (const meta of toolMetaList) {
      loadingTools[meta.key] = false
    }
  }
}

async function runSingleCheck(key: string) {
  loadingTools[key] = true
  try {
    const status = await CheckTool(key) as unknown as CheckStatus
    const items = { ...(overallStatus.value?.items || {}) }
    items[key] = status
    overallStatus.value = rebuildOverallStatus(items, status.checkedAt || new Date().toISOString())
  } catch (err) {
    showError('检测失败: ' + err)
  } finally {
    loadingTools[key] = false
  }
}

async function reinstallTool(key: string, displayName: string) {
  const confirmed = window.confirm(`确定要重新安装 ${displayName} 吗？`)
  if (!confirmed) return

  operating[key] = 'install'
  try {
    const result = await InstallTool(key)
    if ((result as any)?.success) {
      showSuccess(`${displayName} 安装成功` + ((result as any)?.version ? ` (${(result as any).version})` : ''))
      await runSingleCheck(key)
    } else {
      showError(`安装失败: ${(result as any)?.error || '未知错误'}`)
    }
  } catch (err) {
    showError('安装失败: ' + err)
  } finally {
    operating[key] = false
  }
}

async function updateTool(key: string, displayName: string, latestVersion: string) {
  const verLabel = latestVersion ? 'v' + latestVersion : '最新版本'
  const confirmed = window.confirm(`确定要将 ${displayName} 更新到 ${verLabel} 吗？`)
  if (!confirmed) return

  operating[key] = 'update'
  try {
    const result = await UpdateTool(key)
    if ((result as any)?.success) {
      showSuccess(`${displayName} 更新成功` + ((result as any)?.version ? ` (${(result as any).version})` : ''))
      await runSingleCheck(key)
    } else {
      showError(`更新失败: ${(result as any)?.error || '未知错误'}`)
    }
  } catch (err) {
    showError('更新失败: ' + err)
  } finally {
    operating[key] = false
  }
}

// ---------- Lifecycle ----------

onMounted(() => {
  loadCachedStatus()
})
</script>

<style scoped>
/* Status Bar */
.envcheck-status-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-radius: 10px;
  border: 1px solid var(--border);
  margin-bottom: 28px;
  transition: background 0.3s, border-color 0.3s;
}

.envcheck-status-bar.bar-ok {
  background: rgba(102, 187, 106, 0.06);
  border-color: rgba(102, 187, 106, 0.25);
}

.envcheck-status-bar.bar-warn {
  background: rgba(255, 167, 38, 0.06);
  border-color: rgba(255, 167, 38, 0.25);
}

.envcheck-status-bar.bar-loading {
  background: rgba(79, 195, 247, 0.06);
  border-color: rgba(79, 195, 247, 0.25);
}

.envcheck-status-bar.bar-idle {
  background: var(--surface);
}

.status-bar-left {
  display: flex;
  align-items: center;
  gap: 10px;
}

.status-indicator {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 500;
}

.ok-indicator { color: var(--success); }
.warn-indicator { color: #ffa726; }
.loading-indicator { color: var(--accent); }
.idle-indicator { color: var(--text-muted); }

.status-dot-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.ok-dot {
  background: var(--success);
  box-shadow: 0 0 6px rgba(102, 187, 106, 0.4);
}

.warn-dot {
  background: #ffa726;
  box-shadow: 0 0 6px rgba(255, 167, 38, 0.4);
}

.idle-dot {
  background: var(--text-muted);
}

.status-bar-right {
  display: flex;
  align-items: center;
  gap: 16px;
}

.last-check-time {
  font-size: 12px;
  color: var(--text-muted);
  white-space: nowrap;
}

/* Tool Cards Grid */
.tool-cards {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 20px;
}

@media (max-width: 960px) {
  .tool-cards {
    grid-template-columns: 1fr;
  }
}

.tool-card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  transition: border-color 0.2s, box-shadow 0.2s;
}

.tool-card:hover {
  border-color: var(--border-hover);
}

.tool-card.has-issue {
  border-color: rgba(239, 83, 80, 0.3);
}

/* Card Header */
.tool-card-header {
  display: flex;
  align-items: center;
  gap: 14px;
}

.tool-icon-wrapper {
  width: 42px;
  height: 42px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.tool-icon-text {
  font-size: 18px;
  font-weight: 700;
  color: var(--text-primary);
}

.tool-title-area {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.tool-name {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--text-primary);
}

.install-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  font-weight: 500;
  width: fit-content;
}

.badge-installed { color: var(--success); }
.badge-missing { color: var(--error); }
.loading-badge { color: var(--accent); }
.idle-badge { color: var(--text-muted); }

/* Card Body */
.tool-card-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
  flex: 1;
}

.placeholder-body {
  justify-content: center;
}

.placeholder-text {
  color: var(--text-muted);
  font-size: 13px;
  text-align: center;
  padding: 12px 0;
}

.detail-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 13px;
}

.detail-label {
  color: var(--text-secondary);
  flex-shrink: 0;
}

.detail-value {
  color: var(--text-primary);
  text-align: right;
  word-break: break-all;
}

.install-method-tag {
  background: rgba(79, 195, 247, 0.1);
  color: var(--accent);
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 500;
}

.path-ok { color: var(--success); }
.path-warn { color: #ffa726; }

.path-text {
  font-size: 11px;
  max-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Update Hint */
.update-hint {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  background: rgba(255, 167, 38, 0.08);
  border: 1px solid rgba(255, 167, 38, 0.2);
  border-radius: 6px;
  font-size: 13px;
  color: #ffa726;
}

.update-hint strong {
  color: #ffb74d;
}

.hint-icon {
  font-size: 14px;
  font-weight: 700;
}

/* Error Hint */
.error-hint {
  font-size: 12px;
  color: var(--error);
  padding: 6px 10px;
  background: rgba(239, 83, 80, 0.06);
  border-radius: 4px;
}

/* Card Actions */
.tool-card-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  padding-top: 4px;
  border-top: 1px solid var(--border);
}

.action-btn {
  flex: 1;
  min-width: 0;
  text-align: center;
  justify-content: center;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.check-btn {
  background: var(--accent);
  color: var(--bg);
  border-color: transparent;
}

.check-btn:hover:not(:disabled) {
  background: var(--accent-hover);
}

.reinstall-btn {
  /* default btn style */
}

.update-btn {
  background: rgba(255, 167, 38, 0.12);
  color: #ffa726;
  border-color: rgba(255, 167, 38, 0.3);
}

.update-btn:hover:not(:disabled) {
  background: rgba(255, 167, 38, 0.2);
}

/* Spinners */
.spinner {
  width: 14px;
  height: 14px;
  border: 2px solid rgba(79, 195, 247, 0.3);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
  flex-shrink: 0;
}

.btn-spinner {
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 2px solid rgba(15, 18, 25, 0.3);
  border-top-color: var(--bg);
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
  margin-right: 4px;
  vertical-align: middle;
}

.mini-spinner {
  display: inline-block;
  width: 10px;
  height: 10px;
  border: 2px solid currentColor;
  border-top-color: transparent;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
  opacity: 0.7;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}
</style>
