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

  <!--
    R5 首屏竞态 loading 占位（必做）。
    app.go Startup goroutine 异步跑 CheckAll，前端 onMounted 可能在 CheckAll
    写 cache 之前拉到空 snapshot，此时渲染卡片会误显示「未安装/损坏」。
    解决：在首次 snapshot 拿到但 checkedAt 仍为空（表示 CheckAll 尚未跑完）
    时，显示克制的 loading 占位而非卡片列表。轮询会持续拉 snapshot，
    一旦 checkedAt 出现（或超时兜底）即切换到正常视图。
  -->
  <div v-if="initialLoading" class="set-card envcheck-initial-loading">
    <div class="initial-loading-copy">
      <span class="initial-loading-title">正在检测环境</span>
      <span class="initial-loading-sub">首次启动需要一点时间扫描 CLI 工具与 PATH，请稍候...</span>
    </div>
    <ProgressBar :percent="initialLoadingPercent" />
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

  <template v-if="!initialLoading">
  <div class="envcheck-layout">
    <!-- 左侧栏：工具列表 -->
    <aside class="envcheck-sidebar">
      <button
        v-for="card in cardList"
        :key="card.key"
        type="button"
        class="envcheck-tool-item"
        :class="[card.cardClass, { 'tool-active': card.key === activeToolKey }]"
        @click="activeToolKey = card.key"
      >
        <span class="tool-icon" :style="{ background: card.bgColor }">{{ card.iconChar }}</span>
        <span class="tool-name">{{ card.displayName }}</span>
        <span class="tool-status-dot" :class="card.tagClass" :title="card.tagLabel" />
        <span class="tool-tag" :class="card.tagClass">{{ card.tagLabel }}</span>
      </button>
    </aside>

    <!-- 右侧详情：当前选中工具 -->
    <div v-if="activeCard" class="set-card envcheck-card" :class="activeCard.cardClass">
    <div class="card-head">
      <div class="card-title-wrap">
        <span class="card-icon" :style="{ background: activeCard.bgColor }">{{ activeCard.iconChar }}</span>
        <div class="card-title-text">
          <div class="card-title">{{ activeCard.displayName }}</div>
          <div class="card-tag" :class="activeCard.tagClass">{{ activeCard.tagLabel }}</div>
        </div>
      </div>
    </div>

    <div class="card-body">
      <div v-if="activeCard.status" class="info-grid">
        <div class="info-item">
          <span class="info-label">版本</span>
          <span class="info-value mono">{{ activeCard.status.version || '—' }}</span>
        </div>
        <div v-if="activeCard.status.hasUpdate" class="info-item">
          <span class="info-label">最新版本</span>
          <span class="info-value mono">{{ activeCard.status.latestVersion || '—' }}</span>
        </div>
        <div v-if="activeCard.status.installed" class="info-item">
          <span class="info-label">来源</span>
          <span class="info-value">{{ installMethodLabel(activeCard.status.installMethod) }}</span>
        </div>
        <div class="info-item">
          <span class="info-label">PATH</span>
          <span class="info-value" :class="pathStateClass(activeCard.status)">{{ pathStateLabel(activeCard.status) }}</span>
        </div>
        <div v-if="activeCard.status.executablePath" class="info-item info-item-path">
          <span class="info-label">路径</span>
          <span class="info-value mono path-text">{{ activeCard.status.executablePath }}</span>
        </div>
      </div>
      <div v-else class="info-empty">尚未检测</div>

      <div v-if="activeCard.isOperating" class="card-progress">
        <ProgressBar :percent="runningOperation?.progress || 0" />
        <span class="card-progress-step">{{ formatStepLabel(runningOperation) }}</span>
      </div>

      <div v-if="activeCard.status?.error" class="card-error">{{ activeCard.status.error }}</div>

      <!--
        Structured issue + solution entries.
        The backend attaches Issues[] and Solutions[] to CheckStatus. Each
        Solution has a type (fix_path / install_tool / install_node / retry)
        that maps to a RunEnvFixAction handler on the backend. Rendering them
        here restores the "通用环境修复" entry that was dropped during the HIG
        redesign without introducing new decorative cards.
      -->
      <div
        v-if="activeCard.status?.issues && activeCard.status.issues.length > 0"
        class="issue-list"
      >
        <div
          v-for="(issue, idx) in activeCard.status.issues"
          :key="activeCard.key + '-issue-' + idx"
          class="issue-row"
        >
          <div class="issue-copy">
            <span class="issue-sev" :class="issueSeverityClass(issue.severity)">{{ issueSeverityLabel(issue.severity) }}</span>
            <span class="issue-msg">{{ issue.message || '' }}</span>
          </div>
          <div v-if="issue.detail" class="issue-detail">{{ issue.detail }}</div>
          <div v-if="issue.solutions && issue.solutions.length" class="issue-solutions">
            <AppButton
              v-for="(sol, sIdx) in issue.solutions"
              :key="solutionKey(sol, activeCard.key, sIdx)"
              variant="ghost"
              size="small"
              :disabled="activeCard.isOperating || checking || !!runningOperation || fixLoadingKey === solutionKey(sol, activeCard.key, sIdx)"
              @click="executeSolution(sol, activeCard.key, activeCard.displayName)"
            >
              {{ fixLoadingKey === solutionKey(sol, activeCard.key, sIdx) ? '处理中...' : solutionLabel(sol.type) }}
            </AppButton>
          </div>
        </div>
      </div>

      <!--
        Claude Code configuration panel.
        CheckStatus.config is populated by the backend for claude_code only;
        we render it inline (HIG: keep related info inside the owning card
        instead of spawning a sibling card). Each unconfigured item shows a
        "一键配置" button that calls FixClaudeConfig(key, defaultValue, filePath).
      -->
      <div
        v-if="activeCard.key === 'claude_code' && claudeConfigItems(activeCard.status).length > 0"
        class="claude-config"
      >
        <div class="claude-config-head">
          <span class="claude-config-title">Claude Code 配置检测</span>
          <span class="claude-config-summary" :class="claudeConfigAllConfigured(activeCard.status) ? 'tag-success' : 'tag-warn'">
            {{ claudeConfigConfiguredCount(activeCard.status) }}/{{ claudeConfigItems(activeCard.status).length }} 项已配置
          </span>
          <button
            type="button"
            class="claude-config-toggle"
            :aria-expanded="claudeConfigExpanded[activeCard.key] || false"
            @click="toggleClaudeConfig(activeCard.key)"
          >
            {{ claudeConfigExpanded[activeCard.key] ? '收起' : '展开' }}
          </button>
        </div>
        <div v-show="claudeConfigExpanded[activeCard.key]" class="claude-config-list">
          <div
            v-for="item in claudeConfigItems(activeCard.status)"
            :key="activeCard.key + '-cfg-' + item.key"
            class="claude-config-item"
          >
            <div class="claude-config-copy">
              <div class="claude-config-key mono">{{ item.key }}</div>
              <div v-if="item.description" class="claude-config-desc">{{ item.description }}</div>
              <div v-if="item.configured && item.currentValue" class="claude-config-current">
                当前值：<span class="mono">{{ item.currentValue }}</span>
              </div>
            </div>
            <div class="claude-config-state">
              <span v-if="item.configured" class="mini-tag tag-success">已配置</span>
              <span v-else class="mini-tag tag-danger">未配置</span>
              <AppButton
                v-if="!item.configured"
                variant="primary"
                size="small"
                :disabled="!!configFixing[item.key] || activeCard.isOperating || checking || !!runningOperation"
                @click="handleFixClaudeConfig(activeCard.key, item)"
              >
                {{ configFixing[item.key] ? '写入中...' : '一键配置' }}
              </AppButton>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="card-actions">
      <AppButton
        variant="ghost"
        size="small"
        :disabled="activeCard.isOperating || checking || !!runningOperation"
        @click="runSingleCheck(activeCard.key)"
      >
        检测
      </AppButton>

      <!--
        Claude Code: explicit native / npm method picker.
        Backend supports both methods (StartInstallClaudeWithMethodAsync);
        the generic StartInstallToolAsync would silently pick one via
        ClaudeInstallAuto, hiding the choice from the user.
      -->
      <template v-if="activeCard.key === 'claude_code'">
        <select
          class="method-select"
          :value="claudeInstallMethod"
          :disabled="activeCard.isOperating || checking || !!runningOperation || claudeBusy"
          @change="onClaudeMethodChange(($event.target as HTMLSelectElement).value)"
        >
          <option value="" disabled>选择安装方式</option>
          <option
            v-for="opt in claudeInstallMethodOptions"
            :key="opt.value"
            :value="opt.value"
          >
            {{ opt.label }}
          </option>
        </select>
        <AppButton
          v-if="!activeCard.status?.installed"
          variant="primary"
          size="small"
          :disabled="activeCard.isOperating || checking || !!runningOperation || claudeBusy || !claudeInstallMethod"
          @click="startClaudeInstallWithMethod(activeCard.displayName, false)"
        >
          {{ activeCard.isInstalling || claudeInstalling ? '安装中...' : '安装' }}
        </AppButton>
        <template v-else>
          <!--
            已安装且有更新：显示「更新」按钮（首要操作）。
            后端 StartUpdateTool(claude_code) 现成支持，复用 startUpdate 函数，不改 API 层。
          -->
          <AppButton
            v-if="activeCard.status?.hasUpdate"
            variant="primary"
            size="small"
            :disabled="activeCard.isOperating || checking || !!runningOperation || claudeBusy"
            @click="startUpdate(activeCard.key, activeCard.displayName, activeCard.status?.latestVersion || '')"
          >
            {{ activeCard.isUpdating ? '更新中...' : '更新' }}
          </AppButton>
          <AppButton
            variant="ghost"
            size="small"
            :disabled="activeCard.isOperating || checking || !!runningOperation || claudeBusy || !claudeInstallMethod"
            @click="startClaudeInstallWithMethod(activeCard.displayName, true)"
          >
            {{ activeCard.isInstalling || claudeInstalling ? '重装中...' : '重装' }}
          </AppButton>
        </template>
        <AppButton
          v-if="activeCard.status?.installed"
          variant="ghost"
          size="small"
          :disabled="activeCard.isOperating || checking || !!runningOperation || claudeBusy"
          @click="handleUninstallClaude(activeCard.status)"
        >
          {{ claudeUninstalling ? '卸载中...' : '卸载' }}
        </AppButton>
      </template>

      <!-- Non-Claude tools: keep the generic install / update flow. -->
      <template v-else>
        <AppButton
          v-if="!activeCard.status?.installed"
          variant="primary"
          size="small"
          :disabled="activeCard.isOperating || checking || !!runningOperation"
          @click="startInstall(activeCard.key, activeCard.displayName)"
        >
          {{ activeCard.isInstalling ? '安装中...' : '安装' }}
        </AppButton>
        <AppButton
          v-else-if="activeCard.status?.hasUpdate"
          variant="primary"
          size="small"
          :disabled="activeCard.isOperating || checking || !!runningOperation"
          @click="startUpdate(activeCard.key, activeCard.displayName, activeCard.status?.latestVersion || '')"
        >
          {{ activeCard.isUpdating ? '更新中...' : '更新' }}
        </AppButton>
        <!--
          Headroom: installed into a CodeBox-managed venv; uninstall via
          envcheck.Service.CleanHeadroom (no App-level wrapper), which removes
          the venv directory. Gated to headroom only; other non-Claude tools
          have no managed uninstall path.
        -->
        <AppButton
          v-if="activeCard.key === 'headroom' && activeCard.status?.installed"
          variant="ghost"
          size="small"
          :disabled="activeCard.isOperating || checking || !!runningOperation || headroomUninstalling"
          @click="handleUninstallHeadroom"
        >
          {{ headroomUninstalling ? '卸载中...' : '卸载' }}
        </AppButton>
      </template>
    </div>
  </div>
  </div>
  </template>
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
  startInstallClaudeWithMethodAsync,
  uninstallClaudeCode,
  cleanClaudeInstall,
  cleanHeadroom,
  fixClaudeConfig,
  runEnvFixAction,
} from '../../api/envcheck'
import { useToast } from '../../composables/useToast'
import AppButton from '../../components/ui/AppButton.vue'
import ProgressBar from '../../components/ui/ProgressBar.vue'

const { showSuccess, showError, showInfo } = useToast()

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

type ClaudeInstallMethod = 'native' | 'npm'

const TOOL_METAS: ToolMeta[] = [
  { key: 'claude_code', displayName: 'Claude Code', iconChar: 'C', bgColor: 'rgba(204,120,50,0.15)' },
  { key: 'opencode', displayName: 'OpenCode', iconChar: 'O', bgColor: 'rgba(79,195,247,0.15)' },
  { key: 'codex', displayName: 'Codex', iconChar: 'X', bgColor: 'rgba(102,187,106,0.15)' },
  { key: 'pi', displayName: 'Pi', iconChar: 'P', bgColor: 'rgba(52,199,89,0.15)' },
  { key: 'headroom', displayName: 'Headroom', iconChar: 'H', bgColor: 'rgba(149,117,205,0.15)' },
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

// R5 首屏竞态 loading：首次 snapshot 的 status.checking===true 时为 true。
// F-4：后端 OverallStatus.checking 是精确的"检测中"标志，CheckAll 进入即 true、
// 完成即 false。loading 收尾不再依赖 checkedAt 存在性 + 8s 超时的模糊判断。
// 期间显示 loading 占位，避免渲染「未安装/损坏」误导卡片。
// 超时兜底（INITIAL_LOADING_TIMEOUT_MS）保留为防御：极端慢盘或 checking 字段
// 异常时强制切到正常视图，让用户至少能看到「全部检测」按钮自救。
const INITIAL_LOADING_TIMEOUT_MS = 8000
const INITIAL_LOADING_TICK_MS = 100
const initialLoading = ref(true)
const initialLoadingPercent = ref(8)
const initialLoadingStartedAt = ref(0)
const initialLoadingTimer = ref<ReturnType<typeof setInterval> | null>(null)

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

// 侧边栏当前选中的工具 key。默认首个工具（claude_code）。
// 若选中的工具从列表中消失（理论上不会，TOOL_METAS 静态），兜底回首个。
const activeToolKey = ref<string>(TOOL_METAS[0]?.key || '')
const activeCard = computed<CardView | null>(() => {
  const list = cardList.value
  const found = list.find((c) => c.key === activeToolKey.value)
  return found || list[0] || null
})

function pathStateLabel(status: envcheck.CheckStatus): string {
  if (status.pathState && PATH_STATE_MAP[status.pathState]) {
    return PATH_STATE_MAP[status.pathState].label
  }
  return status.pathOk ? 'PATH 正常' : '未加入 PATH'
}

function installMethodLabel(method: string): string {
  const labels: Record<string, string> = {
    npm: 'npm 全局安装',
    native: '原生 / 应用内置',
    homebrew: 'Homebrew',
    pip: 'pip',
    'codebox-venv': 'CodeBox 虚拟环境',
    unknown: '外部 / 未知',
  }
  return labels[method] || '外部 / 未知'
}

function pathStateClass(status: envcheck.CheckStatus): string {
  if (status.pathState && PATH_STATE_MAP[status.pathState]) {
    return PATH_STATE_MAP[status.pathState].cls
  }
  return status.pathOk ? 'text-success' : 'text-error'
}

// ---------- Snapshot / Polling ----------

// F-2: 解析 cleanClaudeCodeNative / uninstallClaudeCode 返回 message 中的
// "versions/ 仍保留 N 个 Native 二进制"段落。后端语义是部分卸载成功：
// shim 已卸载 + native 二进制保留可复用（重装无需重新下载）。前端需要识别
// 这一段落把 banner 升级为 info/warning 态，避免误显"卸载失败"。
// 匹配两种后端文案：
//   - installer.go:2475 "...；versions/ 目录下仍保留 N 个 Native 二进制（...）"
//   - installer.go:2498 "...；versions/ 仍保留 N 个 Native 二进制（...）"
const NATIVE_KEPT_PATTERNS: ReadonlyArray<RegExp> = [
  /versions\/\s*(?:目录下)?\s*仍保留\s*(\d+)\s*个\s*Native\s*二进制[^。；]*[。；]?/i,
  /versions\/\s*(?:目录下)?\s*仍保留[^。；]*Native[^。；]*[。；]?/i,
]

function parseNativeKeptSegments(message: string): { preserved: boolean; preservedLine: string } {
  if (!message) return { preserved: false, preservedLine: '' }
  for (const pattern of NATIVE_KEPT_PATTERNS) {
    const match = message.match(pattern)
    if (match) {
      const line = match[0].replace(/[。；]$/, '').trim()
      return { preserved: true, preservedLine: line }
    }
  }
  return { preserved: false, preservedLine: '' }
}

// CheckAll 是否已经写完 cache。F-4：后端 OverallStatus 已新增显式 `checking`
// bool 字段（CheckAll 进入即 true、完成即 false），优先据此精确判断；
// checkedAt 存在性保留为兼容兜底（后端某些历史分支可能仍只写 checkedAt）。
function hasInitialCheckCompleted(s: envcheck.EnvCheckSnapshot | null): boolean {
  if (!s?.status) return false
  // F-4 主路径：checking === false 即检测已完成（非 in-progress）
  const checkingFlag = (s.status as any).checking
  if (typeof checkingFlag === 'boolean') {
    return !checkingFlag
  }
  // 兼容兜底：旧后端无 checking 字段时，靠 checkedAt 存在性判断。
  const at = (s.status as any).checkedAt
  if (!at) return false
  try {
    const d = new Date(at as any)
    return !isNaN(d.getTime())
  } catch {
    return false
  }
}

function stopInitialLoadingTimer(): void {
  if (initialLoadingTimer.value !== null) {
    clearInterval(initialLoadingTimer.value)
    initialLoadingTimer.value = null
  }
}

function startInitialLoadingTimer(): void {
  stopInitialLoadingTimer()
  initialLoadingStartedAt.value = Date.now()
  initialLoadingPercent.value = 8
  initialLoadingTimer.value = setInterval(() => {
    if (!mounted.value) {
      stopInitialLoadingTimer()
      return
    }
    const elapsed = Date.now() - initialLoadingStartedAt.value
    // 在 8s 内从 8% 平滑推进到 92%，避免在 100% 卡死（完成靠事件驱动）。
    // 超过 timeout 强制切到正常视图，让用户至少能看到「全部检测」按钮自救。
    const ratio = Math.min(elapsed / INITIAL_LOADING_TIMEOUT_MS, 1)
    initialLoadingPercent.value = Math.round(8 + ratio * 84)
    if (elapsed >= INITIAL_LOADING_TIMEOUT_MS) {
      stopInitialLoadingTimer()
      initialLoading.value = false
    }
  }, INITIAL_LOADING_TICK_MS)
}

async function fetchSnapshot(): Promise<void> {
  try {
    const s = await getEnvCheckSnapshot()
    if (mounted.value) {
      snapshot.value = s
      // R5：首次 CheckAll 完成（status.checking===false 或旧后端 checkedAt 出现）
      // 即收尾 loading。F-4 字段让极端慢盘 >8s 也能精确判断，无需等 8s 超时兜底。
      // 已收尾后再 fetch 不影响（initialLoading 已是 false）。
      if (initialLoading.value && hasInitialCheckCompleted(s)) {
        initialLoading.value = false
        stopInitialLoadingTimer()
      }
    }
  } catch (err: any) {
    console.warn('[EnvCheck] fetchSnapshot failed:', err?.message || err)
    // 拉取失败时不能让用户卡在 loading；切到正常视图让其看到错误态/重试按钮。
    if (initialLoading.value) {
      initialLoading.value = false
      stopInitialLoadingTimer()
    }
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
  if (runningOperation.value || checking.value || initialLoading.value) {
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

// ---------- Claude Code: explicit native / npm install ----------

const claudeInstallMethod = ref<ClaudeInstallMethod | ''>('')
const claudeInstalling = ref(false)
const claudeUninstalling = ref(false)
const configFixing = ref<Record<string, boolean>>({})
const claudeConfigExpanded = ref<Record<string, boolean>>({})
const fixLoadingKey = ref<string>('')

const claudeBusy = computed(() => claudeInstalling.value || claudeUninstalling.value)

const claudeInstallMethodOptions = computed<{ value: ClaudeInstallMethod; label: string }[]>(() => {
  const claudeStatus = snapshot.value?.status?.items?.claude_code || null
  const raw = (claudeStatus as any)?.canInstallByMethod as Record<string, boolean> | undefined
  const methods: ClaudeInstallMethod[] = ['native', 'npm']
  return methods
    .filter((method) => {
      // If the backend has not provided per-method capability data, surface
      // both options and let the backend reject at runtime if it must.
      if (!raw || Object.keys(raw).length === 0) return true
      return raw[method] === true
    })
    .map((method) => ({
      value: method,
      label: method === 'native' ? 'Native 安装 (npm + claude install)' : 'npm package 安装',
    }))
})

function onClaudeMethodChange(value: string): void {
  if (value === 'native' || value === 'npm') {
    claudeInstallMethod.value = value
  }
}

function syncClaudeInstallMethod(): void {
  const options = claudeInstallMethodOptions.value
  if (!claudeInstallMethod.value || !options.some((opt) => opt.value === claudeInstallMethod.value)) {
    claudeInstallMethod.value = options.length > 0 ? options[0].value : ''
  }
}

async function startClaudeInstallWithMethod(displayName: string, reinstall: boolean): Promise<void> {
  const method = claudeInstallMethod.value
  if (!method) {
    showError('请先选择安装方式（Native 或 npm）')
    return
  }
  if (reinstall) {
    // Clean previous install artifacts before reinstall so switching methods
    // does not leave stale binaries behind. Mirrors the legacy flow.
    try {
      await cleanClaudeInstall(method)
    } catch (err: any) {
      lastResult.value = {
        title: `清理旧版 ${displayName} 失败`,
        description: err?.message || String(err),
        type: 'error',
      }
      return
    }
  }
  claudeInstalling.value = true
  try {
    await startInstallClaudeWithMethodAsync(method)
    await fetchSnapshot()
    ensurePollingState()
  } catch (err: any) {
    lastResult.value = {
      title: `${reinstall ? '重装' : '安装'} ${displayName} 失败`,
      description: err?.message || String(err),
      type: 'error',
    }
  } finally {
    claudeInstalling.value = false
  }
}

async function handleUninstallClaude(status: envcheck.CheckStatus): Promise<void> {
  const method = (status.installMethod as string) || claudeInstallMethod.value || 'native'
  const confirmed = window.confirm(
    `确定要卸载当前 ${method === 'native' ? 'Native' : 'npm'} 安装的 Claude Code 吗？卸载后不会自动重新安装。`,
  )
  if (!confirmed) return
  claudeUninstalling.value = true
  try {
    let result: envcheck.InstallResult | envcheck.FixActionResult | null = null
    try {
      result = await uninstallClaudeCode(method)
    } catch (err: any) {
      // Only fall back to CleanClaudeInstall when the Wails binding itself is
      // missing; surface business errors instead of swallowing them.
      const msg = err?.message || String(err)
      if (msg.includes('not a function') || msg.includes('不可用')) {
        result = await cleanClaudeInstall(method)
      } else {
        throw err
      }
    }
    const ok = !!(result as any)?.success
    // F-2: success=false 时 message 可能含"versions/ 仍保留 N 个 Native 二进制"段落，
    // 实际语义是"shim 已卸载 + native 保留可复用"，并非真正失败。用 warning 态
    // 展示双段语义，避免用户误读为"卸载失败"。
    const rawMsg = (result as any)?.message || (result as any)?.error || ''
    const nativeKept = parseNativeKeptSegments(rawMsg)
    if (ok && !nativeKept.preserved) {
      showSuccess(rawMsg || 'Claude Code 已卸载')
      lastResult.value = {
        title: 'Claude Code 已卸载',
        description: rawMsg || undefined,
        type: 'success',
      }
      await runSingleCheck('claude_code')
    } else if (nativeKept.preserved) {
      // 双段状态：shim 已卸载（成功）+ native 二进制保留可复用（信息）
      const shimLine = ok ? 'shim 已卸载' : 'shim 已卸载（部分成功）'
      const composed = `${shimLine}；${nativeKept.preservedLine}`
      showInfo(composed)
      lastResult.value = {
        title: 'Claude Code 已部分卸载',
        description: composed,
        type: ok ? 'info' : 'warning',
      }
      if (ok) await runSingleCheck('claude_code')
    } else {
      const desc = rawMsg || '卸载失败'
      showError(desc)
      lastResult.value = { title: 'Claude Code 卸载失败', description: desc, type: 'error' }
    }
  } catch (err: any) {
    showError('卸载操作失败: ' + (err?.message || String(err)))
    lastResult.value = {
      title: 'Claude Code 卸载失败',
      description: err?.message || String(err),
      type: 'error',
    }
  } finally {
    claudeUninstalling.value = false
    if (mounted.value) await fetchSnapshot()
  }
}

// ---------- Headroom: remove CodeBox-managed venv via envcheck.Service.CleanHeadroom ----------

const headroomUninstalling = ref(false)

async function handleUninstallHeadroom(): Promise<void> {
  if (!confirm('确定要卸载 Headroom 吗？将删除 CodeBox 管理的 headroom venv 目录（包含 headroom-ai 及其依赖），卸载后不会自动重新安装。')) {
    return
  }
  headroomUninstalling.value = true
  try {
    const result = await cleanHeadroom()
    const ok = !!result?.success
    const rawMsg = result?.message || ''
    if (ok) {
      showSuccess(rawMsg || 'Headroom 已卸载')
      lastResult.value = {
        title: 'Headroom 已卸载',
        description: rawMsg || undefined,
        type: 'success',
      }
    } else {
      // venv 目录已删除但未确认成功：用 info 态提示重新检测
      showInfo(rawMsg || result?.error || '卸载未确认')
      lastResult.value = {
        title: 'Headroom 卸载未确认',
        description: rawMsg || result?.error || undefined,
        type: 'info',
      }
    }
    await runSingleCheck('headroom')
  } catch (err: any) {
    console.error('Headroom uninstall failed:', err)
    showError('Headroom 卸载失败: ' + (err?.message || String(err)))
    lastResult.value = {
      title: 'Headroom 卸载失败',
      description: err?.message || String(err),
      type: 'error',
    }
  } finally {
    headroomUninstalling.value = false
    if (mounted.value) await fetchSnapshot()
  }
}

// ---------- Claude Code: configuration items ----------

function claudeConfigItems(status: envcheck.CheckStatus | null): envcheck.ClaudeConfigItem[] {
  const cfg = (status as any)?.config as envcheck.ClaudeConfigStatus | null | undefined
  return cfg?.configItems || []
}

function claudeConfigConfiguredCount(status: envcheck.CheckStatus | null): number {
  return claudeConfigItems(status).filter((item) => !!item.configured).length
}

function claudeConfigAllConfigured(status: envcheck.CheckStatus | null): boolean {
  const items = claudeConfigItems(status)
  return items.length > 0 && items.every((item) => !!item.configured)
}

function toggleClaudeConfig(cardKey: string): void {
  claudeConfigExpanded.value = {
    ...claudeConfigExpanded.value,
    [cardKey]: !claudeConfigExpanded.value[cardKey],
  }
}

async function handleFixClaudeConfig(_cardKey: string, item: envcheck.ClaudeConfigItem): Promise<void> {
  configFixing.value = { ...configFixing.value, [item.key]: true }
  try {
    const result: any = await fixClaudeConfig(item.key, item.defaultValue || '', item.filePath || '')
    if (result?.success) {
      showSuccess(result.message || `配置项 ${item.key} 已写入`)
    } else {
      showInfo(result?.message || result?.error || '配置写入失败')
    }
    await runSingleCheck('claude_code')
  } catch (err: any) {
    showError('配置写入失败: ' + (err?.message || String(err)))
  } finally {
    configFixing.value = { ...configFixing.value, [item.key]: false }
  }
}

// ---------- Generic issue / solution runner ----------

function solutionKey(sol: envcheck.ResolutionAction, cardKey: string, idx: number): string {
  return `${cardKey}-${sol.type}-${idx}`
}

function solutionLabel(type: string): string {
  switch (type) {
    case 'fix_path': return '修复 PATH'
    case 'install_tool': return '安装工具'
    case 'install_node': return '安装 Node.js'
    case 'retry': return '重新检测'
    case 'clean_claude_install': return '清理 Claude 安装'
    case 'manual_command': return '查看手动命令'
    case 'restart_app': return '重启应用'
    case 'install_claude_method': return '重装 Claude Code'
    case 'fix_claude_config': return '修复 Claude 配置'
    default: return '解决方案'
  }
}

function issueSeverityLabel(sev: string): string {
  return ({ info: '信息', warning: '警告', error: '错误', critical: '严重' } as Record<string, string>)[sev] || sev
}

function issueSeverityClass(sev: string): string {
  return `sev-${sev || 'info'}`
}

async function executeSolution(sol: envcheck.ResolutionAction, cardKey: string, displayName: string): Promise<void> {
  // R6 manual_command：纯展示命令文案，不触发后端 action。后端在 solution
  // 的 description/command 字段承载建议命令，用户参考执行。HIG 克制：用
  // toast 展示而非弹窗，避免打断流程。
  if (sol.type === 'manual_command') {
    const cmd = sol.command || sol.description || ''
    const desc = sol.description && sol.command && sol.description !== sol.command
      ? `${sol.description}\n命令：${sol.command}`
      : cmd || '请参考 Claude Code 官方文档执行手动命令'
    showInfo(desc)
    lastResult.value = {
      title: `${displayName} 手动命令`,
      description: desc,
      type: 'info',
    }
    return
  }

  // Destructive operations require confirmation (HIG: never destroy user data
  // without explicit opt-in).
  if (sol.type === 'fix_path' || sol.type === 'install_node' || sol.type === 'clean_claude_install') {
    const msg = sol.type === 'fix_path'
      ? '此操作将备份并修改 PATH 配置，以加入必要的工具目录。是否继续？'
      : sol.type === 'install_node'
        ? '此操作将尝试安装 Node.js。是否继续？'
        : `此操作将清理当前 ${displayName} 安装产物（保留 versions/ 二进制可复用）。是否继续？`
    if (!window.confirm(msg)) return
  }

  const key = solutionKey(sol, cardKey, 0)
  fixLoadingKey.value = key
  try {
    if (sol.type === 'retry') {
      await runFullCheck()
      return
    }
    if (sol.type === 'install_tool') {
      await startInstall(cardKey, displayName)
      return
    }
    // R6 clean_claude_install：直接走 CleanClaudeInstall binding（已有 API），
    // 避免 RunEnvFixAction 后端分支不识别该 type。
    if (sol.type === 'clean_claude_install') {
      // F-1: 优先读后端在 issue.solution 上挂的显式 method（消除 installMethod
      // 缺失时 fallback 'native' 误清 npm 的理论风险）。缺失时按顺序回退到
      // snapshot.installMethod 和 'native'，保留防御。
      const method = (sol.method as string)
        || (snapshot.value?.status?.items?.claude_code?.installMethod as string)
        || 'native'
      const result: any = await cleanClaudeInstall(method)
      const ok = !!result?.success
      // F-2: 即使 success=true 也可能含"versions/ 仍保留"段落（部分卸载语义）；
      // success=false 时不一定是真失败，而是"shim 已卸载 + native 保留"。
      // 用 info/warning 态让用户读到双段语义，避免误显"卸载失败"。
      const nativeKept = parseNativeKeptSegments(result?.message || result?.error || '')
      const title = ok
        ? result.message || `${displayName} 安装已清理`
        : result.error || result.message || `${displayName} 清理失败`
      lastResult.value = {
        title,
        description: nativeKept.preservedLine || result?.message || undefined,
        type: nativeKept.preserved ? (ok ? 'info' : 'warning') : (ok ? 'success' : 'error'),
      }
      if (ok && !nativeKept.preserved) {
        showSuccess(title)
      } else if (nativeKept.preserved) {
        // 双段语义：shim 已卸载（成功）+ native 二进制保留可复用（信息）。
        showInfo(nativeKept.preservedLine || title)
      } else {
        showError(title)
      }
      if (ok) await runSingleCheck('claude_code')
      return
    }
    const result: any = await runEnvFixAction(sol.type, sol.tool || cardKey, '')
    const ok = !!result?.success
    const title = ok
      ? result.message || '修复操作已完成'
      : result.error || result.message || '修复操作失败'
    const parts: string[] = []
    if (result?.backupPath) parts.push(`备份文件：${result.backupPath}`)
    if (result?.profilePath) parts.push(`配置文件：${result.profilePath}`)
    if (Array.isArray(result?.addedPaths) && result.addedPaths.length) {
      parts.push('已加入路径：' + result.addedPaths.join(', '))
    }
    if (Array.isArray(result?.nextSteps) && result.nextSteps.length) {
      parts.push(result.nextSteps.join('. '))
    }
    lastResult.value = {
      title,
      description: parts.join('\n') || undefined,
      type: ok ? 'success' : 'error',
    }
    if (ok) showSuccess(title)
    else showError(title)
    if (ok && result?.changed) await fetchSnapshot()
  } catch (err: any) {
    showError('修复操作失败: ' + (err?.message || String(err)))
  } finally {
    fixLoadingKey.value = ''
  }
}

// Keep the method picker in sync once backend snapshot arrives.
watch(
  claudeInstallMethodOptions,
  () => {
    syncClaudeInstallMethod()
  },
  { immediate: true },
)

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
  // R5：在首次 snapshot 到达前进入 loading 占位，避免渲染过期 missing 态。
  // 即使 Startup goroutine 已完成（老进程复用 cache），首次 fetchSnapshot
  // 也会很快切到正常视图；首次启动场景则由轮询持续拉取直到 checkedAt 出现。
  initialLoading.value = true
  startInitialLoadingTimer()
  await fetchSnapshot()
  // CheckAll 未完成时启动短轮询（比常规 POLL_INTERVAL 更密），让用户
  // 在检测完成的瞬间就能看到真实状态，而不是等下次常规轮询。
  if (initialLoading.value && mounted.value) {
    startPolling()
  } else {
    ensurePollingState()
  }
})

onUnmounted(() => {
  mounted.value = false
  stopPolling()
  stopInitialLoadingTimer()
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
  background: var(--warning);
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

/* R5 首屏竞态 loading 占位（HIG 克制：单卡 + 文案 + 低饱和进度条） */
.envcheck-initial-loading {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.initial-loading-copy {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.initial-loading-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
}

.initial-loading-sub {
  font-size: 12px;
  color: var(--tertiary);
  line-height: 1.45;
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

/* sidebar + detail layout */
.envcheck-layout {
  display: flex;
  gap: 16px;
  align-items: flex-start;
}

.envcheck-sidebar {
  flex: 0 0 200px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px;
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 14px;
  box-shadow: var(--shadow);
  position: sticky;
  top: 0;
}

.envcheck-tool-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 10px;
  border: 1px solid transparent;
  background: transparent;
  cursor: pointer;
  text-align: left;
  width: 100%;
  transition: background 0.12s ease, border-color 0.12s ease;
}

.envcheck-tool-item:hover {
  background: var(--control);
}

.envcheck-tool-item.tool-active {
  background: var(--control);
  border-color: var(--accent, #007aff);
}

/* 侧边栏卡片沿用 detail 卡片的状态左边框提示（更克制：仅 active 项显示） */
.envcheck-tool-item.tool-active.card-missing {
  border-color: var(--error, #ff3b30);
}

.envcheck-tool-item.tool-active.card-update {
  border-color: var(--warning);
}

.tool-icon {
  width: 26px;
  height: 26px;
  border-radius: 7px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
  flex-shrink: 0;
}

.tool-name {
  flex: 1;
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.tool-tag {
  font-size: 10px;
  padding: 2px 6px;
  border-radius: 6px;
  flex-shrink: 0;
}

/* 右侧详情区填满剩余宽度 */
.envcheck-layout > .envcheck-card {
  flex: 1;
  min-width: 0;
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
  border-left: 3px solid var(--warning);
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
  color: var(--success-strong);
  background: rgba(52, 199, 89, 0.16);
}

.tag-warn {
  color: var(--warning-strong);
  background: rgba(255, 159, 10, 0.16);
}

.tag-danger {
  color: var(--danger-strong);
  background: rgba(255, 59, 48, 0.14);
}

.tag-primary {
  color: var(--accent-strong);
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
  color: var(--warning-strong);
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

/* ---------- issue + solution entries ---------- */
.issue-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 4px;
  padding-top: 8px;
  border-top: 1px dashed var(--separator);
}

.issue-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.issue-copy {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--secondary);
}

.issue-sev {
  font-size: 10px;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: 999px;
}

.issue-sev.sev-info {
  color: var(--secondary);
  background: var(--control);
}

.issue-sev.sev-warning {
  color: var(--warning-strong);
  background: rgba(255, 159, 10, 0.16);
}

.issue-sev.sev-error,
.issue-sev.sev-critical {
  color: var(--danger-strong);
  background: rgba(255, 59, 48, 0.14);
}

.issue-msg {
  flex: 1;
}

.issue-detail {
  font-size: 11px;
  color: var(--tertiary);
  line-height: 1.45;
  padding-left: 4px;
}

.issue-solutions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding-left: 4px;
}

/* ---------- Claude Code configuration panel ---------- */
.claude-config {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px dashed var(--separator);
}

.claude-config-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}

.claude-config-title {
  font-weight: 600;
  color: var(--label);
}

.claude-config-summary {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 999px;
}

.claude-config-toggle {
  margin-left: auto;
  background: none;
  border: none;
  color: var(--accent, #007aff);
  font-size: 12px;
  cursor: pointer;
  padding: 2px 4px;
}

.claude-config-toggle:hover {
  text-decoration: underline;
}

.claude-config-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 8px;
}

.claude-config-item {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
  padding: 6px 8px;
  background: var(--control);
  border-radius: 8px;
}

.claude-config-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.claude-config-key {
  font-size: 12px;
  font-weight: 600;
  color: var(--label);
  word-break: break-all;
}

.claude-config-desc {
  font-size: 11px;
  color: var(--secondary);
  line-height: 1.4;
}

.claude-config-current {
  font-size: 11px;
  color: var(--tertiary);
}

.claude-config-state {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 6px;
  flex-shrink: 0;
}

.mini-tag {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 999px;
}

/* ---------- native / npm method picker ---------- */
.method-select {
  height: 28px;
  padding: 0 8px;
  font-size: 12px;
  color: var(--label);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  outline: none;
  cursor: pointer;
  flex: 1;
  min-width: 160px;
}

.method-select:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.method-select:focus {
  border-color: var(--accent, #007aff);
}
</style>
