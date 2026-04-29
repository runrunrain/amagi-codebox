<template>
  <div class="envcheck-root">
    <!-- Hero Banner -->
    <div class="hero-banner" :class="heroBannerClass">
      <div class="hero-content">
        <h2 class="hero-title">环境检测</h2>
        <p class="hero-desc">检测和管理 CLI 编码工具的安装状态与版本</p>
        <div class="hero-meta" v-if="snapshot && snapshot.status">
          <span class="meta-item" v-if="formattedCheckedAt">
            <span class="meta-label">最后检测</span>
            <span class="meta-value">{{ formattedCheckedAt }}</span>
          </span>
          <span class="meta-item">
            <span class="meta-label">状态</span>
            <span class="meta-value" :class="snapshot.status.allOk ? 'text-success' : 'text-warn'">
              {{ snapshot.status.allOk ? '全部正常' : `${snapshot.status.issues?.length || 0} 项问题` }}
            </span>
          </span>
        </div>
        <div class="hero-meta" v-else>
          <span class="meta-item">
            <span class="meta-value text-muted">尚未执行检测</span>
          </span>
        </div>
      </div>
      <div class="hero-action">
        <button
          class="btn primary hero-btn"
          @click="runFullCheck"
          :disabled="checkingAll || !!runningOperation"
        >
          <span v-if="checkingAll" class="mini-spinner light"></span>
          {{ checkingAll ? '检测中...' : '一键检测' }}
        </button>
      </div>
      <!-- Global progress bar when operation running -->
      <div class="hero-progress" v-if="runningOperation">
        <div class="progress-track">
          <div class="progress-fill" :style="{ width: (runningOperation.progress || 0) + '%' }"></div>
        </div>
        <span class="progress-label">{{ operationLabel }}</span>
      </div>
      <!-- Operation result banner (persisted, not transient) -->
      <div class="hero-operation-result" v-if="lastOperationResult && !runningOperation">
        <el-alert
          :title="lastOperationResult.title"
          :description="lastOperationResult.description || ''"
          :type="lastOperationResult.type"
          show-icon
          :closable="true"
          @close="lastOperationResult = null"
          class="op-result-alert"
        />
      </div>
    </div>

    <!-- Summary Strip -->
    <div class="summary-strip" v-if="snapshot && snapshot.status && snapshot.status.items">
      <div class="summary-item summary-installed">
        <span class="summary-count">{{ installedCount }}</span>
        <span class="summary-label">已安装</span>
      </div>
      <div class="summary-item summary-issues">
        <span class="summary-count">{{ issuesCount }}</span>
        <span class="summary-label">有问题</span>
      </div>
      <div class="summary-item summary-updates">
        <span class="summary-count">{{ updatesCount }}</span>
        <span class="summary-label">有更新</span>
      </div>
    </div>

    <!-- Tool Cards Grid -->
    <div class="tool-grid">
      <div
        v-for="card in cardList"
        :key="card.meta.key"
        class="tool-card"
        :class="card.cardClass"
      >
        <!-- Card Header -->
        <div class="card-header">
          <div class="tool-icon" :style="{ background: card.meta.bgColor }">
            <span class="icon-char">{{ card.meta.iconChar }}</span>
          </div>
          <div class="card-title-area">
            <h3 class="card-title">{{ card.meta.displayName }}</h3>
            <el-tag
              :type="card.tagType"
              size="small"
              :effect="card.tagEffect"
              class="status-tag"
            >
              {{ card.tagLabel }}
            </el-tag>
          </div>
        </div>

        <!-- Card Body -->
        <div class="card-body" v-if="card.status">
          <div class="info-row" v-if="card.status.installed && card.status.installMethod">
            <span class="info-label">安装方式</span>
            <span class="info-value">
              <span class="method-badge">{{ formatInstallMethod(card.status.installMethod) }}</span>
            </span>
          </div>
          <div class="info-row" v-if="card.status.version">
            <span class="info-label">当前版本</span>
            <span class="info-value mono">{{ card.status.version }}</span>
          </div>
          <div class="info-row" v-if="card.status.hasUpdate && card.status.latestVersion">
            <span class="info-label">最新版本</span>
            <span class="info-value mono text-warn">{{ card.status.latestVersion }}</span>
          </div>
          <!-- PATH status based on pathState -->
          <div class="info-row" v-if="card.status.installed">
            <span class="info-label">PATH</span>
            <span class="info-value" :class="pathStateClass(card.status)">
              {{ pathStateLabel(card.status) }}
            </span>
          </div>
          <div class="info-row path-row" v-if="card.status.executablePath">
            <span class="info-label">路径</span>
            <span class="info-value mono path-value" :title="card.status.executablePath">
              {{ card.status.executablePath }}
            </span>
          </div>
        </div>

        <!-- Placeholder when no status -->
        <div class="card-body placeholder" v-if="!card.status && !checkingAll">
          <span class="placeholder-text">尚未检测此工具</span>
        </div>

        <!-- Operation overlay for this specific tool -->
        <div class="card-operation" v-if="card.isOperating">
          <div class="op-overlay">
            <span class="mini-spinner accent"></span>
            <span class="op-text">{{ card.operationLabel }}</span>
          </div>
          <!-- Per-card progress bar during operation -->
          <div class="card-progress" v-if="card.isOperating && runningOperation && runningOperation.progress > 0">
            <div class="progress-track">
              <div class="progress-fill" :style="{ width: runningOperation.progress + '%' }"></div>
            </div>
            <span class="progress-step-label">{{ formatStepLabel(runningOperation) }}</span>
          </div>
        </div>

        <!-- Issues / Solutions section -->
        <div class="card-issues" v-if="card.status && card.status.issues && card.status.issues.length > 0 && !card.isOperating">
          <div
            v-for="(issue, idx) in card.status.issues.slice(0, 2)"
            :key="'issue-' + idx"
            class="issue-block"
            :class="'issue-' + issue.severity"
          >
            <div class="issue-header">
              <span class="issue-severity-badge" :class="'severity-' + issue.severity">
                {{ severityLabel(issue.severity) }}
              </span>
              <span class="issue-message">{{ issue.message }}</span>
            </div>
            <div class="issue-detail" v-if="issue.detail">{{ issue.detail }}</div>
            <!-- Solutions for this issue -->
            <div class="issue-solutions" v-if="issue.solutions && issue.solutions.length > 0">
              <div
                v-for="(sol, sidx) in issue.solutions.slice(0, 2)"
                :key="'sol-' + sidx"
                class="solution-item"
              >
                <span class="solution-desc">{{ sol.description }}</span>
                <code class="solution-cmd" v-if="sol.command">{{ sol.command }}</code>
              </div>
            </div>
          </div>
          <!-- Top-level solutions (from status.solutions) -->
          <div class="issue-solutions" v-if="!card.status.issues?.[0]?.solutions?.length && card.status.solutions && card.status.solutions.length > 0">
            <div
              v-for="(sol, sidx) in card.status.solutions.slice(0, 2)"
              :key="'topsol-' + sidx"
              class="solution-item"
            >
              <span class="solution-desc">{{ sol.description }}</span>
              <code class="solution-cmd" v-if="sol.command">{{ sol.command }}</code>
            </div>
          </div>
        </div>

        <!-- Update hint -->
        <el-alert
          v-if="card.status && card.status.hasUpdate && !card.isOperating"
          :title="'有新版本 ' + (card.status.latestVersion || '') + ' 可用'"
          type="warning"
          :closable="false"
          show-icon
          class="card-alert"
        />

        <!-- Error hint (legacy, from status.error) -->
        <el-alert
          v-if="card.status && card.status.error && (!card.status.issues || card.status.issues.length === 0)"
          :title="card.status.error"
          type="error"
          :closable="false"
          show-icon
          class="card-alert"
        />

        <!-- PATH info hint for codebox_path / shell_fallback -->
        <el-alert
          v-if="card.status && card.status.installed && (card.status.pathState === 'codebox_path' || card.status.pathState === 'shell_fallback')"
          :title="pathStateHint(card.status)"
          :type="card.status.pathState === 'shell_fallback' ? 'warning' : 'info'"
          :closable="false"
          show-icon
          class="card-alert"
        />

        <!-- Install blocked hint -->
        <el-alert
          v-if="card.status && !card.status.installed && !card.status.canInstall && card.status.installBlockedReason"
          :title="'无法安装: ' + card.status.installBlockedReason"
          type="error"
          :closable="false"
          show-icon
          class="card-alert"
        />

        <!-- Actions -->
        <div class="card-actions">
          <button
            class="btn small action-btn check-action"
            @click="runSingleCheck(card.meta.key)"
            :disabled="card.isOperating || checkingAll"
          >
            <span v-if="checkingAll" class="mini-spinner"></span>
            {{ checkingAll ? '检测中...' : '检测' }}
          </button>
          <!-- Not installed: install button -->
          <button
            v-if="card.status && !card.status.installed"
            class="btn small action-btn install-action"
            @click="startInstall(card.meta.key, card.meta.displayName)"
            :disabled="card.isOperating || checkingAll || !!runningOperation || !card.status.canInstall"
            :title="!card.status.canInstall ? card.status.installBlockedReason : ''"
          >
            <span v-if="card.isOperating && card.operationKind === 'install'" class="mini-spinner"></span>
            {{ (card.isOperating && card.operationKind === 'install') ? '安装中...' : '安装' }}
          </button>
          <!-- Installed but has fixable issues: repair/reinstall -->
          <button
            v-if="card.status && card.status.installed && card.status.canInstall && hasFixableIssues(card.status)"
            class="btn small action-btn repair-action"
            @click="startInstall(card.meta.key, card.meta.displayName)"
            :disabled="card.isOperating || checkingAll || !!runningOperation"
          >
            <span v-if="card.isOperating && card.operationKind === 'install'" class="mini-spinner"></span>
            {{ (card.isOperating && card.operationKind === 'install') ? '修复中...' : '重装修复' }}
          </button>
          <!-- Installed without fixable issues: normal reinstall -->
          <button
            v-if="card.status && card.status.installed && !hasFixableIssues(card.status)"
            class="btn small action-btn reinstall-action"
            @click="startInstall(card.meta.key, card.meta.displayName)"
            :disabled="card.isOperating || checkingAll || !!runningOperation"
          >
            <span v-if="card.isOperating && card.operationKind === 'install'" class="mini-spinner"></span>
            {{ (card.isOperating && card.operationKind === 'install') ? '安装中...' : '重装' }}
          </button>
          <!-- Update button -->
          <button
            v-if="card.status && card.status.hasUpdate"
            class="btn small action-btn update-action"
            @click="startUpdate(card.meta.key, card.meta.displayName, card.status.latestVersion || '')"
            :disabled="card.isOperating || checkingAll || !!runningOperation"
          >
            <span v-if="card.isOperating && card.operationKind === 'update'" class="mini-spinner"></span>
            {{ (card.isOperating && card.operationKind === 'update') ? '更新中...' : '更新' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { ElTag, ElAlert, ElMessage, ElMessageBox } from 'element-plus'
import {
  RunEnvCheck,
  CheckTool,
  StartInstallToolAsync,
  StartUpdateToolAsync,
  GetEnvCheckSnapshot,
} from '../../wailsjs/go/main/App'
import type { envcheck } from '../../wailsjs/go/models'

// ---------- Types ----------

interface ToolMeta {
  key: string
  displayName: string
  iconChar: string
  bgColor: string
}

interface CardView {
  meta: ToolMeta
  status: envcheck.CheckStatus | null
  isOperating: boolean
  operationKind: string
  operationLabel: string
  cardClass: string
  tagType: 'success' | 'warning' | 'danger' | 'info' | 'primary'
  tagEffect: 'dark' | 'light' | 'plain'
  tagLabel: string
}

interface OperationResult {
  title: string
  description?: string
  type: 'success' | 'error' | 'warning' | 'info'
}

// ---------- Constants ----------

const TOOL_METAS: ToolMeta[] = [
  { key: 'claude_code', displayName: 'Claude Code', iconChar: 'C', bgColor: 'rgba(204,120,50,0.15)' },
  { key: 'opencode', displayName: 'OpenCode', iconChar: 'O', bgColor: 'rgba(79,195,247,0.15)' },
  { key: 'codex', displayName: 'Codex', iconChar: 'X', bgColor: 'rgba(102,187,106,0.15)' },
]

const POLL_INTERVAL = 1500

const PATH_STATE_MAP: Record<string, { label: string; cls: string }> = {
  system_path: { label: 'PATH 正常', cls: 'text-success' },
  codebox_path: { label: 'CodeBox 可启动', cls: 'text-info' },
  shell_fallback: { label: 'Shell 可解析', cls: 'text-warn' },
  missing: { label: '未找到', cls: 'text-error' },
  outside_path: { label: '未加入可用 PATH', cls: 'text-error' },
}

// ---------- State ----------

const snapshot = ref<envcheck.EnvCheckSnapshot | null>(null)
const checkingAll = ref(false)
const pollTimer = ref<ReturnType<typeof setInterval> | null>(null)
const mounted = ref(true)
const lastOperationResult = ref<OperationResult | null>(null)

// ---------- Computed ----------

const runningOperation = computed<envcheck.OperationState | null>(() => {
  const op = snapshot.value?.operation
  if (op && op.status === 'running') return op
  return null
})

const operationLabel = computed(() => {
  const op = runningOperation.value
  if (!op) return ''
  const toolName = TOOL_METAS.find(m => m.key === op.tool)?.displayName || op.tool
  const kind = op.kind === 'install' ? '安装' : '更新'
  const prog = op.progress > 0 ? ` (${op.progress}%)` : ''
  const msg = op.message ? ': ' + op.message : ''
  return `${toolName} ${kind}中${prog}${msg}`
})

const formattedCheckedAt = computed(() => {
  const at = snapshot.value?.status?.checkedAt
  if (!at) return ''
  try {
    const d = new Date(at)
    if (isNaN(d.getTime())) return ''
    return d.toLocaleString('zh-CN', {
      month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', second: '2-digit',
    })
  } catch {
    return ''
  }
})

const heroBannerClass = computed(() => {
  if (runningOperation.value) return 'hero-running'
  if (checkingAll.value) return 'hero-checking'
  if (!snapshot.value?.status) return 'hero-idle'
  return snapshot.value.status.allOk ? 'hero-ok' : 'hero-warn'
})

const installedCount = computed(() => {
  const items = snapshot.value?.status?.items
  if (!items) return 0
  return Object.values(items).filter(s => s.installed).length
})

const issuesCount = computed(() => {
  return snapshot.value?.status?.issues?.length || 0
})

const updatesCount = computed(() => {
  const items = snapshot.value?.status?.items
  if (!items) return 0
  return Object.values(items).filter(s => s.installed && s.hasUpdate).length
})

const cardList = computed((): CardView[] => {
  const op = runningOperation.value
  return TOOL_METAS.map(meta => {
    const status = snapshot.value?.status?.items?.[meta.key] || null
    const isOperatingThis = op != null && op.tool === meta.key
    const opKind = isOperatingThis ? (op.kind || '') : ''
    const opLabel = isOperatingThis ? (op.kind === 'install' ? '安装中...' : '更新中...') : ''

    let cardClass = ''
    let tagType: 'success' | 'warning' | 'danger' | 'info' | 'primary' = 'info'
    let tagEffect: 'dark' | 'light' | 'plain' = 'plain'
    let tagLabel = '待检测'

    if (isOperatingThis) {
      cardClass = 'card-operating'
      tagType = 'primary'
      tagEffect = 'dark'
      tagLabel = op.kind === 'install' ? '安装中' : '更新中'
    } else if (status) {
      if (!status.installed) {
        cardClass = 'card-missing'
        tagType = 'danger'
        tagEffect = 'dark'
        tagLabel = '未安装'
      } else if (status.hasUpdate) {
        cardClass = 'card-update'
        tagType = 'warning'
        tagEffect = 'dark'
        tagLabel = '有更新'
      } else if (status.error) {
        cardClass = 'card-error'
        tagType = 'danger'
        tagEffect = 'dark'
        tagLabel = '异常'
      } else {
        cardClass = 'card-installed'
        tagType = 'success'
        tagEffect = 'dark'
        tagLabel = '已安装'
      }
    }

    return {
      meta,
      status,
      isOperating: isOperatingThis,
      operationKind: opKind,
      operationLabel: opLabel,
      cardClass,
      tagType,
      tagEffect,
      tagLabel,
    }
  })
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

function pathStateLabel(status: envcheck.CheckStatus): string {
  if (status.pathState) {
    const mapping = PATH_STATE_MAP[status.pathState]
    if (mapping) return mapping.label
  }
  // Fallback for old backend without pathState
  return status.pathOk ? '正常' : '异常'
}

function pathStateClass(status: envcheck.CheckStatus): string {
  if (status.pathState) {
    const mapping = PATH_STATE_MAP[status.pathState]
    if (mapping) return mapping.cls
  }
  return status.pathOk ? 'text-success' : 'text-error'
}

function pathStateHint(status: envcheck.CheckStatus): string {
  if (status.pathState === 'codebox_path') {
    return '该工具不在系统 PATH 中，但 CodeBox 可正常启动。如需全局可用，建议重启终端或手动同步 PATH。'
  }
  if (status.pathState === 'shell_fallback') {
    return '该工具通过 Shell 环境回退找到，部分场景可能不稳定。建议检查 PATH 配置或重启应用。'
  }
  return ''
}

function severityLabel(sev: string): string {
  const map: Record<string, string> = {
    info: '信息',
    warning: '警告',
    error: '错误',
    critical: '严重',
  }
  return map[sev] || sev
}

function hasFixableIssues(status: envcheck.CheckStatus): boolean {
  if (!status.issues || status.issues.length === 0) return false
  return status.issues.some(issue =>
    issue.severity === 'warning' || issue.severity === 'error' || issue.severity === 'critical'
  )
}

function formatStepLabel(op: envcheck.OperationState): string {
  const stepMap: Record<string, string> = {
    precheck: '预检查',
    prepare: '准备中',
    run_command: '执行命令',
    verify: '验证中',
    refresh_cache: '刷新缓存',
    completed: '完成',
  }
  const stepLabel = stepMap[op.step] || op.step || ''
  const parts: string[] = []
  if (stepLabel) parts.push(stepLabel)
  if (op.message) parts.push(op.message)
  return parts.join(' - ')
}

// ---------- Snapshot / Polling ----------

async function fetchSnapshot(): Promise<void> {
  try {
    const s = await GetEnvCheckSnapshot() as unknown as envcheck.EnvCheckSnapshot
    if (mounted.value) {
      snapshot.value = s
    }
  } catch (err: any) {
    console.warn('[EnvCheck] fetchSnapshot failed:', err?.message || err)
  }
}

function startPolling(): void {
  stopPolling()
  pollTimer.value = setInterval(async () => {
    await fetchSnapshot()
    ensurePollingState()
  }, POLL_INTERVAL)
}

function stopPolling(): void {
  if (pollTimer.value !== null) {
    clearInterval(pollTimer.value)
    pollTimer.value = null
  }
}

function ensurePollingState(): void {
  if (runningOperation.value || checkingAll.value) {
    if (pollTimer.value === null) {
      startPolling()
    }
  } else {
    stopPolling()
  }
}

// ---------- Actions ----------

async function runFullCheck(): Promise<void> {
  checkingAll.value = true
  try {
    const status = await RunEnvCheck() as unknown as envcheck.OverallStatus
    if (mounted.value) {
      snapshot.value = {
        status,
        operation: snapshot.value?.operation || null,
      } as envcheck.EnvCheckSnapshot
    }
    ElMessage.success('环境检测完成')
  } catch (err: any) {
    ElMessage.error('检测失败: ' + (err?.message || err))
  } finally {
    if (mounted.value) {
      checkingAll.value = false
    }
  }
}

async function runSingleCheck(key: string): Promise<void> {
  try {
    const status = await CheckTool(key) as unknown as envcheck.CheckStatus
    if (!mounted.value) return
    const currentStatus = snapshot.value?.status
    if (currentStatus) {
      const items = { ...(currentStatus.items || {} as Record<string, envcheck.CheckStatus>) }
      items[key] = status
      const issues: string[] = []
      for (const m of TOOL_METAS) {
        const s = items[m.key]
        if (!s) continue
        if (s.error?.trim()) issues.push(`${s.tool}: ${s.error}`)
        else if (!s.installed) issues.push(`${s.tool}: not installed`)
        else if (!s.pathOk && s.pathState !== 'codebox_path' && s.pathState !== 'shell_fallback') {
          issues.push(`${s.tool}: executable not in PATH`)
        }
      }
      snapshot.value = {
        status: {
          allOk: issues.length === 0 && TOOL_METAS.every(m => !!items[m.key]),
          items,
          issues,
          checkedAt: status.checkedAt || currentStatus.checkedAt,
        },
        operation: snapshot.value?.operation || null,
      } as envcheck.EnvCheckSnapshot
    }
  } catch (err: any) {
    ElMessage.error('检测失败: ' + (err?.message || err))
  }
}

async function startInstall(key: string, displayName: string): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `确定要${snapshot.value?.status?.items?.[key]?.installed ? '重新' : ''}安装 ${displayName} 吗？`,
      '确认操作',
      { confirmButtonText: '确定', cancelButtonText: '取消', type: 'info' }
    )
  } catch {
    return // user cancelled
  }

  try {
    await StartInstallToolAsync(key) as any
    await fetchSnapshot()
    ensurePollingState()
  } catch (err: any) {
    lastOperationResult.value = {
      title: `安装 ${displayName} 失败`,
      description: err?.message || String(err),
      type: 'error',
    }
  }
}

async function startUpdate(key: string, displayName: string, latestVersion: string): Promise<void> {
  const verLabel = latestVersion ? 'v' + latestVersion : '最新版本'
  try {
    await ElMessageBox.confirm(
      `确定要将 ${displayName} 更新到 ${verLabel} 吗？`,
      '确认操作',
      { confirmButtonText: '确定', cancelButtonText: '取消', type: 'info' }
    )
  } catch {
    return // user cancelled
  }

  try {
    await StartUpdateToolAsync(key) as any
    await fetchSnapshot()
    ensurePollingState()
  } catch (err: any) {
    lastOperationResult.value = {
      title: `更新 ${displayName} 失败`,
      description: err?.message || String(err),
      type: 'error',
    }
  }
}

// ---------- Lifecycle ----------

// Watch runningOperation to detect operation completion and update the result banner.
watch(runningOperation, async (newVal, oldVal) => {
  if (oldVal && !newVal) {
    // Operation just completed -- fetch final snapshot
    await fetchSnapshot()

    // Build result from the snapshot's operation state (now completed)
    const completedOp = snapshot.value?.operation
    if (completedOp && completedOp.status !== 'running') {
      const toolName = TOOL_METAS.find(m => m.key === completedOp.tool)?.displayName || completedOp.tool
      const kind = completedOp.kind === 'install' ? '安装' : '更新'

      if (completedOp.status === 'succeeded') {
        const ver = completedOp.result?.version ? ` (v${completedOp.result.version})` : ''
        const msg = completedOp.result?.message || completedOp.message || ''
        lastOperationResult.value = {
          title: `${toolName} ${kind}成功${ver}`,
          description: msg || undefined,
          type: 'success',
        }
      } else if (completedOp.status === 'failed' || completedOp.status === 'timeout') {
        const errMsg = completedOp.error || completedOp.result?.error || ''
        const msg = completedOp.message || ''
        const parts = [errMsg, msg].filter(Boolean)
        lastOperationResult.value = {
          title: `${toolName} ${kind}失败`,
          description: parts.join(' - ') || undefined,
          type: 'error',
        }
      }
    }
  }
  ensurePollingState()
})

onMounted(async () => {
  mounted.value = true
  await fetchSnapshot()
  ensurePollingState()
})

onUnmounted(() => {
  mounted.value = false
  stopPolling()
})
</script>

<style scoped>
/* ===== Root ===== */
.envcheck-root {
  --bg: #0f1219;
  --surface: #1a1f2e;
  --elevated: #232a3b;
  --border: #2a2f3e;
  --border-hover: #3a4f5e;
  --text-primary: #e0e6ed;
  --text-secondary: #8899aa;
  --text-muted: #5a6a7a;
  --accent: #4fc3f7;
  --accent-hover: #7bd4f9;
  --success: #66bb6a;
  --error: #ef5350;
  --warn: #ffa726;
  --info: #4fc3f7;

  display: flex;
  flex-direction: column;
  gap: 24px;
  color: var(--text-primary);
}

/* ===== Hero Banner ===== */
.hero-banner {
  position: relative;
  padding: 28px 28px 20px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: var(--surface);
  overflow: hidden;
  transition: border-color 0.3s, background 0.3s;
}

.hero-banner.hero-ok {
  border-color: rgba(102, 187, 106, 0.3);
  background: linear-gradient(135deg, rgba(102, 187, 106, 0.06) 0%, var(--surface) 70%);
}

.hero-banner.hero-warn {
  border-color: rgba(255, 167, 38, 0.3);
  background: linear-gradient(135deg, rgba(255, 167, 38, 0.06) 0%, var(--surface) 70%);
}

.hero-banner.hero-checking {
  border-color: rgba(79, 195, 247, 0.3);
  background: linear-gradient(135deg, rgba(79, 195, 247, 0.06) 0%, var(--surface) 70%);
}

.hero-banner.hero-running {
  border-color: rgba(79, 195, 247, 0.35);
  background: linear-gradient(135deg, rgba(79, 195, 247, 0.08) 0%, var(--surface) 70%);
}

.hero-banner.hero-idle {
  border-color: var(--border);
}

.hero-content {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.hero-title {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  color: var(--text-primary);
}

.hero-desc {
  margin: 0;
  font-size: 14px;
  color: var(--text-secondary);
}

.hero-meta {
  display: flex;
  gap: 20px;
  margin-top: 8px;
}

.meta-item {
  display: flex;
  gap: 6px;
  align-items: center;
}

.meta-label {
  font-size: 12px;
  color: var(--text-muted);
}

.meta-value {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
}

.hero-action {
  position: absolute;
  top: 28px;
  right: 28px;
}

.hero-btn {
  min-width: 110px;
}

.hero-progress {
  margin-top: 16px;
  display: flex;
  align-items: center;
  gap: 12px;
}

.progress-track {
  flex: 1;
  height: 4px;
  background: var(--border);
  border-radius: 2px;
  overflow: hidden;
}

.progress-fill {
  height: 100%;
  background: var(--accent);
  border-radius: 2px;
  transition: width 0.4s ease;
}

.progress-label {
  font-size: 12px;
  color: var(--accent);
  white-space: nowrap;
  max-width: 320px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.hero-operation-result {
  margin-top: 14px;
}

.op-result-alert {
  border-radius: 8px;
}

/* ===== Summary Strip ===== */
.summary-strip {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16px;
}

.summary-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 16px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
  transition: border-color 0.2s;
}

.summary-count {
  font-size: 28px;
  font-weight: 700;
  line-height: 1;
  margin-bottom: 6px;
}

.summary-label {
  font-size: 12px;
  color: var(--text-muted);
  font-weight: 500;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.summary-installed .summary-count { color: var(--success); }
.summary-issues .summary-count { color: var(--error); }
.summary-updates .summary-count { color: var(--warn); }

/* ===== Tool Grid ===== */
.tool-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 20px;
}

@media (max-width: 1100px) {
  .tool-grid {
    grid-template-columns: 1fr;
  }
}

/* ===== Tool Card ===== */
.tool-card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 14px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 20px;
  transition: border-color 0.2s, box-shadow 0.2s;
}

.tool-card:hover {
  border-color: var(--border-hover);
}

.tool-card.card-missing {
  border-color: rgba(239, 83, 80, 0.25);
}

.tool-card.card-update {
  border-color: rgba(255, 167, 38, 0.25);
}

.tool-card.card-operating {
  border-color: rgba(79, 195, 247, 0.35);
  box-shadow: 0 0 0 1px rgba(79, 195, 247, 0.12);
}

/* Card Header */
.card-header {
  display: flex;
  align-items: center;
  gap: 14px;
}

.tool-icon {
  width: 44px;
  height: 44px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.icon-char {
  font-size: 18px;
  font-weight: 700;
  color: var(--text-primary);
}

.card-title-area {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.card-title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--text-primary);
}

.status-tag {
  width: fit-content;
}

/* Card Body */
.card-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
}

.card-body.placeholder {
  justify-content: center;
  align-items: center;
  padding: 8px 0;
}

.placeholder-text {
  color: var(--text-muted);
  font-size: 13px;
}

.info-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 13px;
  gap: 12px;
}

.info-label {
  color: var(--text-secondary);
  flex-shrink: 0;
  font-size: 12px;
}

.info-value {
  color: var(--text-primary);
  text-align: right;
  word-break: break-all;
  font-size: 13px;
}

.method-badge {
  background: rgba(79, 195, 247, 0.1);
  color: var(--accent);
  padding: 2px 10px;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 500;
}

.mono {
  font-family: monospace;
  font-size: 12px;
}

.path-row .path-value {
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
}

/* Card Alert */
.card-alert {
  border-radius: 6px;
}

/* Card Operation Overlay */
.card-operation {
  padding: 8px 0;
}

.op-overlay {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  background: rgba(79, 195, 247, 0.06);
  border: 1px solid rgba(79, 195, 247, 0.15);
  border-radius: 8px;
}

.op-text {
  font-size: 13px;
  color: var(--accent);
  font-weight: 500;
}

.card-progress {
  margin-top: 10px;
}

.progress-step-label {
  font-size: 11px;
  color: var(--accent);
  margin-top: 4px;
  display: block;
}

/* ===== Issues / Solutions ===== */
.card-issues {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.issue-block {
  padding: 10px 12px;
  border-radius: 8px;
  border: 1px solid var(--border);
}

.issue-block.issue-info {
  border-color: rgba(79, 195, 247, 0.2);
  background: rgba(79, 195, 247, 0.04);
}

.issue-block.issue-warning {
  border-color: rgba(255, 167, 38, 0.25);
  background: rgba(255, 167, 38, 0.04);
}

.issue-block.issue-error,
.issue-block.issue-critical {
  border-color: rgba(239, 83, 80, 0.25);
  background: rgba(239, 83, 80, 0.04);
}

.issue-header {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}

.issue-severity-badge {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 3px;
  font-size: 11px;
  font-weight: 600;
  flex-shrink: 0;
  line-height: 1.6;
}

.severity-info {
  background: rgba(79, 195, 247, 0.15);
  color: var(--info);
}

.severity-warning {
  background: rgba(255, 167, 38, 0.15);
  color: var(--warn);
}

.severity-error,
.severity-critical {
  background: rgba(239, 83, 80, 0.15);
  color: var(--error);
}

.issue-message {
  font-size: 13px;
  color: var(--text-primary);
  line-height: 1.5;
}

.issue-detail {
  margin-top: 4px;
  font-size: 12px;
  color: var(--text-secondary);
  line-height: 1.4;
}

.issue-solutions {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.solution-item {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding-left: 12px;
  border-left: 2px solid var(--border);
}

.solution-desc {
  font-size: 12px;
  color: var(--text-secondary);
}

.solution-cmd {
  font-family: monospace;
  font-size: 11px;
  background: var(--elevated);
  color: var(--accent);
  padding: 3px 8px;
  border-radius: 3px;
  word-break: break-all;
}

/* Card Actions */
.card-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  padding-top: 8px;
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

.check-action {
  background: var(--accent);
  color: var(--bg);
  border-color: transparent;
}

.check-action:hover:not(:disabled) {
  background: var(--accent-hover);
}

.install-action {
  background: rgba(102, 187, 106, 0.12);
  color: var(--success);
  border-color: rgba(102, 187, 106, 0.3);
}

.install-action:hover:not(:disabled) {
  background: rgba(102, 187, 106, 0.2);
}

.repair-action {
  background: rgba(255, 167, 38, 0.12);
  color: var(--warn);
  border-color: rgba(255, 167, 38, 0.3);
}

.repair-action:hover:not(:disabled) {
  background: rgba(255, 167, 38, 0.2);
}

.reinstall-action {
  background: var(--elevated);
  color: var(--text-secondary);
  border-color: var(--border);
}

.reinstall-action:hover:not(:disabled) {
  background: var(--border);
  color: var(--text-primary);
}

.update-action {
  background: rgba(255, 167, 38, 0.12);
  color: var(--warn);
  border-color: rgba(255, 167, 38, 0.3);
}

.update-action:hover:not(:disabled) {
  background: rgba(255, 167, 38, 0.2);
}

/* ===== Text Helpers ===== */
.text-success { color: var(--success); }
.text-info { color: var(--info); }
.text-error { color: var(--error); }
.text-warn { color: var(--warn); }
.text-muted { color: var(--text-muted); }

/* ===== Buttons ===== */
.btn {
  padding: 10px 20px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: transform 0.15s, box-shadow 0.15s, background 0.15s;
  border: 1px solid var(--border);
  outline: none;
  background: var(--surface);
  color: var(--text-primary);
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none !important;
  box-shadow: none !important;
}

.btn.small {
  padding: 6px 14px;
  font-size: 13px;
}

.btn.primary {
  background: var(--accent);
  color: var(--bg);
  border-color: transparent;
}

.btn.primary:hover:not(:disabled) {
  background: var(--accent-hover);
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(79, 195, 247, 0.2);
}

/* ===== Spinners ===== */
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

.mini-spinner.light {
  border-color: rgba(15, 18, 25, 0.3);
  border-top-color: transparent;
  opacity: 1;
}

.mini-spinner.accent {
  border-color: var(--accent);
  border-top-color: transparent;
  opacity: 1;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* ===== Element Plus overrides for dark theme ===== */
.envcheck-root :deep(.el-tag) {
  border-radius: 4px;
}

.envcheck-root :deep(.el-alert) {
  padding: 8px 12px;
}

.envcheck-root :deep(.el-alert .el-alert__title) {
  font-size: 12px;
  line-height: 1.4;
}

.envcheck-root :deep(.el-alert .el-alert__description) {
  font-size: 11px;
  line-height: 1.4;
  margin-top: 2px;
}

/* ===== Responsive ===== */
@media (max-width: 1100px) {
  .summary-strip {
    grid-template-columns: repeat(3, 1fr);
  }

  .hero-action {
    position: static;
    margin-top: 16px;
  }

  .hero-banner {
    display: flex;
    flex-direction: column;
  }
}

@media (max-width: 640px) {
  .summary-strip {
    grid-template-columns: 1fr;
  }

  .hero-meta {
    flex-direction: column;
    gap: 4px;
  }

  .tool-grid {
    grid-template-columns: 1fr;
  }
}
</style>
