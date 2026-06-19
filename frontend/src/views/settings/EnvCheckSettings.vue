<template>
  <div class="set-card envcheck-hero">
    <div class="hero-summary">
      <h2>环境检测</h2>
      <p class="set-sub">CLI 工具安装状态、版本与 PATH 校验</p>
      <div class="hero-meta">
        <span class="dot" :class="heroDotClass" />
        <span class="hero-text">{{ heroText }}</span>
        <span v-if="checkedAtText" class="hero-time">最近检测：{{ checkedAtText }}</span>
      </div>
    </div>
    <div class="hero-actions">
      <AppButton
        variant="primary"
        size="small"
        :disabled="checking || !!runningOperation"
        @click="runFullCheck"
      >
        {{ checking ? '检测中...' : '全部检测' }}
      </AppButton>
    </div>
  </div>

  <div v-if="runningOperation" class="set-card op-progress-card">
    <ProgressBar :percent="runningOperation.progress || 0" />
    <div class="op-progress-meta">
      <span class="op-text">{{ operationLabel }}</span>
    </div>
  </div>

  <div v-if="lastResult" class="set-card op-result" :class="resultClass">
    <div class="op-result-title">{{ lastResult.title }}</div>
    <div v-if="lastResult.description" class="op-result-desc">{{ lastResult.description }}</div>
    <AppButton variant="ghost" size="small" @click="lastResult = null">关闭</AppButton>
  </div>

  <div v-for="card in cardList" :key="card.key" class="set-card envcheck-card" :class="card.cardClass">
    <div class="card-head">
      <div class="card-title-wrap">
        <span class="card-icon" :style="{ background: card.bgColor }">{{ card.iconChar }}</span>
        <div class="card-title-text">
          <div class="card-title">{{ card.displayName }}</div>
          <div class="card-tag" :class="card.tagClass">{{ card.tagLabel }}</div>
        </div>
      </div>
    </div>

    <div class="card-body">
      <div v-if="card.status" class="info-grid">
        <div class="info-item">
          <span class="info-label">版本</span>
          <span class="info-value mono">{{ card.status.version || '—' }}</span>
        </div>
        <div v-if="card.status.hasUpdate" class="info-item">
          <span class="info-label">最新版本</span>
          <span class="info-value mono">{{ card.status.latestVersion || '—' }}</span>
        </div>
        <div class="info-item">
          <span class="info-label">PATH</span>
          <span class="info-value" :class="pathStateClass(card.status)">{{ pathStateLabel(card.status) }}</span>
        </div>
        <div v-if="card.status.executablePath" class="info-item info-item-path">
          <span class="info-label">路径</span>
          <span class="info-value mono path-text">{{ card.status.executablePath }}</span>
        </div>
      </div>
      <div v-else class="info-empty">尚未检测</div>

      <div v-if="card.isOperating" class="card-progress">
        <ProgressBar :percent="runningOperation?.progress || 0" />
        <span class="card-progress-step">{{ formatStepLabel(runningOperation) }}</span>
      </div>

      <div v-if="card.status?.error" class="card-error">{{ card.status.error }}</div>
    </div>

    <div class="card-actions">
      <AppButton
        variant="ghost"
        size="small"
        :disabled="card.isOperating || checking || !!runningOperation"
        @click="runSingleCheck(card.key)"
      >
        检测
      </AppButton>
      <AppButton
        v-if="!card.status?.installed"
        variant="primary"
        size="small"
        :disabled="card.isOperating || checking || !!runningOperation"
        @click="startInstall(card.key, card.displayName)"
      >
        {{ card.isInstalling ? '安装中...' : '安装' }}
      </AppButton>
      <AppButton
        v-else-if="card.status?.hasUpdate"
        variant="primary"
        size="small"
        :disabled="card.isOperating || checking || !!runningOperation"
        @click="startUpdate(card.key, card.displayName, card.status?.latestVersion || '')"
      >
        {{ card.isUpdating ? '更新中...' : '更新' }}
      </AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { envcheck } from '../../../wailsjs/go/models'
import {
  runEnvCheck,
  checkTool,
  startInstallToolAsync,
  startUpdateToolAsync,
  getEnvCheckSnapshot,
} from '../../api/envcheck'
import { useToast } from '../../composables/useToast'
import AppButton from '../../components/ui/AppButton.vue'
import ProgressBar from '../../components/ui/ProgressBar.vue'

const { showSuccess, showError } = useToast()

interface ToolMeta {
  key: string
  displayName: string
  iconChar: string
  bgColor: string
}

interface OperationResult {
  title: string
  description?: string
  type: 'success' | 'error' | 'warning' | 'info'
}

interface CardView {
  key: string
  displayName: string
  iconChar: string
  bgColor: string
  status: envcheck.CheckStatus | null
  isOperating: boolean
  isInstalling: boolean
  isUpdating: boolean
  cardClass: string
  tagClass: string
  tagLabel: string
}

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

const snapshot = ref<envcheck.EnvCheckSnapshot | null>(null)
const checking = ref(false)
const pollTimer = ref<ReturnType<typeof setInterval> | null>(null)
const mounted = ref(true)
const lastResult = ref<OperationResult | null>(null)

const runningOperation = computed<envcheck.OperationState | null>(() => {
  const op = snapshot.value?.operation
  if (op && op.status === 'running') return op
  return null
})

function operationKindLabel(kind: string): string {
  if (kind === 'install') return '安装'
  if (kind === 'update') return '更新'
  if (kind === 'uninstall') return '卸载'
  return kind || '操作'
}

const operationLabel = computed(() => {
  const op = runningOperation.value
  if (!op) return ''
  const toolName = TOOL_METAS.find((m) => m.key === op.tool)?.displayName || op.tool
  const kind = operationKindLabel(op.kind)
  const prog = op.progress > 0 ? ` (${op.progress}%)` : ''
  const msg = op.message ? ': ' + op.message : ''
  return `${toolName} ${kind}中${prog}${msg}`
})

function formatStepLabel(op: envcheck.OperationState | null): string {
  if (!op) return ''
  const kind = operationKindLabel(op.kind)
  const step = op.step ? ` · ${op.step}` : ''
  return `${kind}中${step}`
}

const checkedAtText = computed(() => {
  const at = snapshot.value?.status?.checkedAt as any
  if (!at) return ''
  try {
    const d = new Date(at)
    if (isNaN(d.getTime())) return ''
    return d.toLocaleString('zh-CN', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    return ''
  }
})

const heroDotClass = computed(() => {
  if (runningOperation.value || checking.value) return 'dot-running'
  if (!snapshot.value?.status) return 'dot-idle'
  return snapshot.value.status.allOk ? 'dot-ok' : 'dot-warn'
})

const heroText = computed(() => {
  if (checking.value) return '正在检测所有工具...'
  if (runningOperation.value) return operationLabel.value
  if (!snapshot.value?.status) return '尚未检测，点击「全部检测」开始'
  const items = snapshot.value.status.items || {}
  const total = TOOL_METAS.length
  const installed = Object.values(items).filter((s) => s.installed).length
  if (snapshot.value.status.allOk) return `全部正常 · ${installed}/${total} 已安装`
  return `存在异常 · ${installed}/${total} 已安装`
})

const resultClass = computed(() => `result-${lastResult.value?.type || 'info'}`)

const cardList = computed<CardView[]>(() => {
  const op = runningOperation.value
  return TOOL_METAS.map((meta) => {
    const status = snapshot.value?.status?.items?.[meta.key] || null
    const isOperatingThis = op != null && op.tool === meta.key
    const isInstalling = isOperatingThis && op?.kind === 'install'
    const isUpdating = isOperatingThis && op?.kind === 'update'

    let cardClass = ''
    let tagClass = 'tag-info'
    let tagLabel = '待检测'

    if (isOperatingThis) {
      cardClass = 'card-operating'
      tagClass = 'tag-primary'
      tagLabel = `${operationKindLabel(op!.kind)}中`
    } else if (status) {
      if (!status.installed) {
        cardClass = 'card-missing'
        tagClass = 'tag-danger'
        tagLabel = '未安装'
      } else if (status.hasUpdate) {
        cardClass = 'card-update'
        tagClass = 'tag-warn'
        tagLabel = '有更新'
      } else if (status.error) {
        cardClass = 'card-error'
        tagClass = 'tag-danger'
        tagLabel = '异常'
      } else {
        cardClass = 'card-ok'
        tagClass = 'tag-success'
        tagLabel = '已安装'
      }
    }

    return {
      key: meta.key,
      displayName: meta.displayName,
      iconChar: meta.iconChar,
      bgColor: meta.bgColor,
      status,
      isOperating: isOperatingThis,
      isInstalling,
      isUpdating,
      cardClass,
      tagClass,
      tagLabel,
    }
  })
})

function pathStateLabel(status: envcheck.CheckStatus): string {
  if (status.pathState && PATH_STATE_MAP[status.pathState]) {
    return PATH_STATE_MAP[status.pathState].label
  }
  return status.pathOk ? 'PATH 正常' : '未加入 PATH'
}

function pathStateClass(status: envcheck.CheckStatus): string {
  if (status.pathState && PATH_STATE_MAP[status.pathState]) {
    return PATH_STATE_MAP[status.pathState].cls
  }
  return status.pathOk ? 'text-success' : 'text-error'
}

// ---------- Snapshot / Polling ----------

async function fetchSnapshot(): Promise<void> {
  try {
    const s = await getEnvCheckSnapshot()
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
  if (runningOperation.value || checking.value) {
    if (pollTimer.value === null) {
      startPolling()
    }
  } else {
    stopPolling()
  }
}

// ---------- Actions ----------

async function runFullCheck(): Promise<void> {
  if (checking.value || runningOperation.value) return
  checking.value = true
  try {
    const status = await runEnvCheck()
    if (mounted.value) {
      snapshot.value = {
        status,
        operation: snapshot.value?.operation || null,
      } as envcheck.EnvCheckSnapshot
    }
    showSuccess('环境检测完成')
  } catch (err: any) {
    showError('检测失败: ' + (err?.message || err))
  } finally {
    if (mounted.value) checking.value = false
  }
}

async function runSingleCheck(key: string): Promise<void> {
  try {
    const status = await checkTool(key)
    if (!mounted.value) return
    const currentStatus = snapshot.value?.status
    if (currentStatus) {
      const items = { ...(currentStatus.items || ({} as Record<string, envcheck.CheckStatus>)) }
      items[key] = status
      const issues: string[] = []
      for (const m of TOOL_METAS) {
        const s = items[m.key]
        if (!s) continue
        if (s.error?.trim()) issues.push(`${s.tool}: ${s.error}`)
        else if (!s.installed) issues.push(`${s.tool}: 未安装`)
        else if (!s.pathOk && s.pathState !== 'codebox_path' && s.pathState !== 'shell_fallback') {
          issues.push(`${s.tool}: 可执行文件未加入 PATH`)
        }
      }
      snapshot.value = {
        status: {
          allOk: issues.length === 0 && TOOL_METAS.every((m) => !!items[m.key]),
          items,
          issues,
          checkedAt: new Date().toISOString() as any,
        } as envcheck.OverallStatus,
        operation: snapshot.value?.operation || null,
      } as envcheck.EnvCheckSnapshot
    }
  } catch (err: any) {
    showError(`检测 ${key} 失败: ` + (err?.message || err))
  }
}

async function startInstall(key: string, displayName: string): Promise<void> {
  try {
    await startInstallToolAsync(key)
    await fetchSnapshot()
    ensurePollingState()
  } catch (err: any) {
    lastResult.value = {
      title: `安装 ${displayName} 失败`,
      description: err?.message || String(err),
      type: 'error',
    }
  }
}

async function startUpdate(key: string, displayName: string, latestVersion: string): Promise<void> {
  const verLabel = latestVersion ? 'v' + latestVersion : '最新版本'
  try {
    await startUpdateToolAsync(key)
    await fetchSnapshot()
    ensurePollingState()
  } catch (err: any) {
    lastResult.value = {
      title: `更新 ${displayName} 到 ${verLabel} 失败`,
      description: err?.message || String(err),
      type: 'error',
    }
  }
}

// Watch runningOperation to detect completion and surface a result banner.
watch(runningOperation, async (newVal, oldVal) => {
  if (oldVal && !newVal) {
    await fetchSnapshot()
    const completedOp = snapshot.value?.operation
    if (completedOp && completedOp.status !== 'running') {
      const toolName =
        TOOL_METAS.find((m) => m.key === completedOp.tool)?.displayName || completedOp.tool
      const kind = operationKindLabel(completedOp.kind)
      if (completedOp.status === 'succeeded') {
        const ver = completedOp.result?.version ? ` (v${completedOp.result.version})` : ''
        const msg = completedOp.result?.message || completedOp.message || ''
        lastResult.value = {
          title: `${toolName} ${kind}成功${ver}`,
          description: msg || undefined,
          type: 'success',
        }
      } else if (completedOp.status === 'failed' || completedOp.status === 'timeout') {
        const errMsg = completedOp.error || completedOp.result?.error || ''
        const msg = completedOp.message || ''
        const parts = [errMsg, msg].filter(Boolean)
        lastResult.value = {
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
.set-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 14px;
  padding: 20px 24px;
  box-shadow: var(--shadow);
}

.set-card h2 {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin-bottom: 4px;
}

.set-sub {
  font-size: 12px;
  color: var(--tertiary);
  margin-bottom: 8px;
}

/* hero */
.envcheck-hero {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 16px;
}

.hero-summary {
  flex: 1;
}

.hero-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 6px;
  font-size: 12px;
  color: var(--secondary);
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.dot-ok {
  background: var(--success, #34c759);
}

.dot-warn {
  background: #ff9f0a;
}

.dot-running {
  background: var(--accent, #007aff);
}

.dot-idle {
  background: var(--tertiary, #8e8e93);
}

.hero-time {
  color: var(--tertiary);
}

/* operation progress */
.op-progress-card {
  padding: 14px 24px;
}

.op-progress-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 4px;
}

.op-text {
  font-size: 12px;
  color: var(--secondary);
}

/* result banner */
.op-result {
  display: flex;
  flex-direction: column;
  gap: 6px;
  align-items: flex-start;
}

.op-result.result-success {
  border-left: 3px solid var(--success, #34c759);
}

.op-result.result-error {
  border-left: 3px solid var(--error, #ff3b30);
}

.op-result-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
}

.op-result-desc {
  font-size: 12px;
  color: var(--secondary);
  line-height: 1.5;
}

/* cards */
.envcheck-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.envcheck-card.card-operating {
  border-color: var(--accent, #007aff);
}

.envcheck-card.card-missing {
  border-left: 3px solid var(--error, #ff3b30);
}

.envcheck-card.card-update {
  border-left: 3px solid #ff9f0a;
}

.envcheck-card.card-error {
  border-left: 3px solid var(--error, #ff3b30);
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.card-title-wrap {
  display: flex;
  align-items: center;
  gap: 12px;
}

.card-icon {
  width: 36px;
  height: 36px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  font-weight: 600;
  color: var(--label);
}

.card-title-text {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.card-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--label);
}

.card-tag {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 999px;
  display: inline-block;
  width: fit-content;
}

.tag-success {
  color: #1d6a3a;
  background: rgba(52, 199, 89, 0.16);
}

.tag-warn {
  color: #b25000;
  background: rgba(255, 159, 10, 0.16);
}

.tag-danger {
  color: #b3261e;
  background: rgba(255, 59, 48, 0.14);
}

.tag-primary {
  color: #0a5cb8;
  background: rgba(0, 122, 255, 0.14);
}

.tag-info {
  color: var(--secondary);
  background: var(--control);
}

.info-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px 16px;
}

.info-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.info-item-path {
  grid-column: 1 / -1;
}

.info-label {
  font-size: 11px;
  color: var(--tertiary);
}

.info-value {
  font-size: 13px;
  color: var(--secondary);
  word-break: break-all;
}

.info-value.mono {
  font-family: var(--mono);
}

.info-value.text-success {
  color: var(--success, #34c759);
}

.info-value.text-error {
  color: var(--error, #ff3b30);
}

.info-value.text-warn {
  color: #b25000;
}

.info-value.text-info {
  color: var(--accent, #007aff);
}

.path-text {
  font-size: 11px;
  line-height: 1.4;
}

.info-empty {
  font-size: 12px;
  color: var(--tertiary);
}

.card-progress {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.card-progress-step {
  font-size: 11px;
  color: var(--tertiary);
}

.card-error {
  font-size: 12px;
  color: var(--error, #ff3b30);
  line-height: 1.5;
}

.card-actions {
  display: flex;
  gap: 8px;
  padding-top: 4px;
  border-top: 1px solid var(--separator);
}

.mono {
  font-family: var(--mono);
}
</style>
