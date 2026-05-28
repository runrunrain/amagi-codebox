<template>
  <Teleport to="body">
    <div
      v-if="visible"
      class="session-detail-modal-backdrop"
      role="presentation"
      @click.self="emitClose"
    >
      <section
        ref="dialogRef"
        class="session-detail-modal"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="headingId"
        tabindex="-1"
        @keydown.esc.stop.prevent="emitClose"
        @keydown.tab="handleTabKey"
      >
        <header class="modal-header">
          <div>
            <p class="modal-eyebrow">仪表盘终端会话详情</p>
            <h2 :id="headingId">会话 {{ session?.id || sessionId }}</h2>
            <p class="modal-subtitle">详情来自 Wails history snapshot 与 pty:data 实时事件，不生成示例数据。</p>
          </div>
          <button ref="closeButtonRef" type="button" class="modal-close" aria-label="关闭会话详情" @click="emitClose">关闭</button>
        </header>

        <div v-if="session" class="modal-body">
          <div class="detail-grid">
            <div class="detail-item">
              <span>状态</span>
              <strong :class="`status-text-${session.status}`">{{ statusLabel(session.status) }}</strong>
            </div>
            <div class="detail-item">
              <span>应用类型</span>
              <strong>{{ appTypeLongLabel(session.appType) }}</strong>
            </div>
            <div class="detail-item">
              <span>模式</span>
              <strong>{{ session.mode || '-' }}</strong>
            </div>
            <div class="detail-item">
              <span>PID</span>
              <strong>{{ session.pid || '-' }}</strong>
            </div>
            <div class="detail-item">
              <span>提供商</span>
              <strong>{{ session.provider || '-' }}</strong>
            </div>
            <div class="detail-item">
              <span>预设</span>
              <strong>{{ session.preset || '-' }}</strong>
            </div>
            <div class="detail-item detail-item--wide">
              <span>模型</span>
              <strong>{{ session.model || '-' }}</strong>
            </div>
            <div class="detail-item detail-item--wide">
              <span>工作目录</span>
              <strong :title="session.workDir">{{ session.workDir || '-' }}</strong>
            </div>
            <div class="detail-item">
              <span>启动时间</span>
              <strong>{{ formatDateTime(session.startedAt) }}</strong>
            </div>
            <div class="detail-item">
              <span>运行时长</span>
              <strong>{{ session.duration || '-' }}</strong>
            </div>
          </div>

          <section class="detail-tabs-section" aria-label="会话结构化详情">
            <div class="detail-tabs" role="tablist" aria-label="会话详情标签">
              <button
                v-for="tab in detailTabs"
                :key="tab.id"
                type="button"
                class="detail-tab-button"
                :class="{ active: activeTab === tab.id }"
                role="tab"
                :aria-selected="activeTab === tab.id"
                @click="activeTab = tab.id"
              >{{ tab.label }}</button>
            </div>

            <div class="detail-tab-panel" role="tabpanel">
              <template v-if="activeTab === 'transcript'">
                <div class="tab-panel-header">
                  <div>
                    <h3>Transcript</h3>
                    <p>来自当前会话 Wails history snapshot 与 pty:data 事件的真实输出。</p>
                  </div>
                  <span class="detail-source-pill" :class="outputStatusClass">
                    {{ outputStatusLabel }}
                  </span>
                </div>
                <div v-if="output.historyStatus === 'loading'" class="detail-empty-state detail-empty-state--loading">
                  正在读取当前会话历史输出。
                </div>
                <div v-else-if="output.decodeError" class="detail-empty-state detail-empty-state--error">
                  历史输出解码失败，详情弹窗仅展示后续实时 pty:data 输出。
                </div>
                <pre v-else-if="transcriptText" class="detail-transcript-pre">{{ transcriptText }}</pre>
                <div v-else class="detail-empty-state">
                  当前会话暂无输出。详情弹窗不会生成示例 transcript，只等待真实 PTY 数据。
                </div>
              </template>

              <template v-else-if="activeTab === 'diff'">
                <div class="tab-panel-header">
                  <div>
                    <h3>Diff</h3>
                    <p>仅从真实终端输出中识别 unified diff 片段。</p>
                  </div>
                  <span class="detail-source-pill">{{ diffBlocks.length }} blocks</span>
                </div>
                <div v-if="diffBlocks.length > 0" class="diff-block-list">
                  <article v-for="block in diffBlocks" :key="block.id" class="diff-block">
                    <div class="diff-block-meta">Lines {{ block.startLine }}-{{ block.endLine }}</div>
                    <pre>{{ block.text }}</pre>
                  </article>
                </div>
                <div v-else class="detail-empty-state">
                  当前会话尚未产生可识别的 diff 输出。识别来源限于真实 PTY 文本中的 diff --git、---/+++、@@ 等组合特征。
                </div>
              </template>

              <template v-else-if="activeTab === 'context'">
                <div class="tab-panel-header">
                  <div>
                    <h3>Context</h3>
                    <p>展示真实会话 metadata、输出统计与可识别上下文/工具行；不展示虚构 token 用量。</p>
                  </div>
                  <span class="detail-source-pill">metadata + PTY</span>
                </div>
                <div class="context-summary-grid">
                  <div class="context-summary-item">
                    <span>输出字节</span>
                    <strong>{{ output.totalBytes }}</strong>
                  </div>
                  <div class="context-summary-item">
                    <span>输出片段</span>
                    <strong>{{ output.totalChunks }}</strong>
                  </div>
                  <div class="context-summary-item">
                    <span>文本行数</span>
                    <strong>{{ contextSummary.lineCount }}</strong>
                  </div>
                  <div class="context-summary-item">
                    <span>最新序号</span>
                    <strong>{{ output.lastSeq || '-' }}</strong>
                  </div>
                </div>
                <div class="context-metadata-list">
                  <div><span>应用</span><strong>{{ appTypeLongLabel(session.appType) }}</strong></div>
                  <div><span>Provider</span><strong>{{ session.provider || '-' }}</strong></div>
                  <div><span>Preset</span><strong>{{ session.preset || '-' }}</strong></div>
                  <div><span>Model</span><strong>{{ session.model || '-' }}</strong></div>
                  <div><span>WorkDir</span><strong :title="session.workDir">{{ session.workDir || '-' }}</strong></div>
                </div>
                <div v-if="contextSummary.signalLines.length > 0" class="context-signal-list">
                  <h4>识别到的上下文/工具输出行</h4>
                  <pre v-for="line in contextSummary.signalLines" :key="line">{{ line }}</pre>
                </div>
                <div v-else class="detail-empty-state detail-empty-state--compact">
                  暂未从真实输出中识别到 context/tool 行。当前面板仍保留真实 metadata 与输出统计。
                </div>
              </template>

              <template v-else-if="activeTab === 'files'">
                <div class="tab-panel-header">
                  <div>
                    <h3>Files</h3>
                    <p>文件标签需要真实 file 数据源。</p>
                  </div>
                  <span class="detail-source-pill detail-source-pill--empty">未接入</span>
                </div>
                <div class="detail-empty-state">
                  当前桌面前端没有可用的真实文件标签 API 或 Wails 事件。本 tab 不展示假文件、示例路径或推断出的文件清单。
                </div>
              </template>

              <template v-else-if="activeTab === 'review'">
                <div class="tab-panel-header">
                  <div>
                    <h3>Review</h3>
                    <p>Review 结论需要真实审核数据源。</p>
                  </div>
                  <span class="detail-source-pill detail-source-pill--empty">未接入</span>
                </div>
                <div class="detail-empty-state">
                  当前桌面前端没有可用的真实 review 数据 API 或事件。本 tab 不生成假审核结论、风险列表或通过状态。
                </div>
              </template>
            </div>
          </section>
        </div>

        <div v-else class="modal-body">
          <div class="detail-empty-state detail-empty-state--error">
            找不到对应会话，可能已被移除。请关闭弹窗后刷新仪表盘会话列表。
          </div>
        </div>
      </section>
    </div>
  </Teleport>
</template>

<script lang="ts" setup>
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useSessionDetailOutput } from '../../composables/useSessionDetailOutput'

interface SessionInfo {
  id: string
  appType: string
  provider: string
  preset: string
  model: string
  mode: string
  workDir: string
  status: string
  pid: number
  startedAt: string
  duration: string
}

type DetailTabId = 'transcript' | 'diff' | 'context' | 'files' | 'review'

const props = defineProps<{
  visible: boolean
  sessionId: string
  session: SessionInfo | null
}>()

const emit = defineEmits<{
  (event: 'close'): void
}>()

const detailTabs: Array<{ id: DetailTabId; label: string }> = [
  { id: 'transcript', label: 'Transcript' },
  { id: 'diff', label: 'Diff' },
  { id: 'context', label: 'Context' },
  { id: 'files', label: 'Files' },
  { id: 'review', label: 'Review' },
]

const activeTab = ref<DetailTabId>('transcript')
const dialogRef = ref<HTMLElement | null>(null)
const closeButtonRef = ref<HTMLButtonElement | null>(null)
const headingId = computed(() => `session-detail-heading-${props.sessionId || 'unknown'}`)
let previousActiveElement: Element | null = null

const {
  output,
  transcriptText,
  diffBlocks,
  contextSummary,
  outputStatusLabel,
  outputStatusClass,
  open,
  close,
} = useSessionDetailOutput()

watch(
  () => [props.visible, props.sessionId] as const,
  ([visible, sessionId]) => {
    if (visible && sessionId) {
      previousActiveElement = document.activeElement
      activeTab.value = 'transcript'
      open(sessionId)
      nextTick(() => closeButtonRef.value?.focus())
      return
    }
    close()
    restoreFocus()
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  close()
  restoreFocus()
})

function emitClose() {
  emit('close')
}

function restoreFocus() {
  if (previousActiveElement instanceof HTMLElement) {
    previousActiveElement.focus()
  }
  previousActiveElement = null
}

function handleTabKey(event: KeyboardEvent) {
  const dialog = dialogRef.value
  if (!dialog) return
  const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(
    'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
  )).filter((element) => element.offsetParent !== null || element === closeButtonRef.value)
  if (focusable.length === 0) return

  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

function appTypeLongLabel(appType: string): string {
  const map: Record<string, string> = {
    claudecode: 'Claude Code',
    opencode: 'OpenCode',
    codex: 'Codex',
  }
  return map[appType] || appType || '-'
}

function statusLabel(status: string): string {
  const map: Record<string, string> = {
    running: '运行中',
    stopped: '已停止',
    exited: '已退出',
    failed: '启动失败',
    error: '错误',
  }
  return map[status] || status || '-'
}

function formatDateTime(value: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}
</script>

<style scoped>
.session-detail-modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 3000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgba(4, 8, 14, 0.68);
  backdrop-filter: blur(10px);
}

.session-detail-modal {
  width: min(980px, 100%);
  max-height: min(86vh, 860px);
  overflow: hidden;
  border: 1px solid rgba(137, 221, 255, 0.24);
  border-radius: 20px;
  background:
    radial-gradient(circle at 12% 0%, rgba(137, 221, 255, 0.16), transparent 32%),
    radial-gradient(circle at 92% 12%, rgba(102, 187, 106, 0.11), transparent 28%),
    #0b1018;
  box-shadow: 0 26px 80px rgba(0, 0, 0, 0.56);
  color: #d9e2ec;
  outline: none;
}

.modal-header {
  display: flex;
  justify-content: space-between;
  gap: 20px;
  padding: 22px 24px 18px;
  border-bottom: 1px solid rgba(58, 74, 94, 0.64);
}

.modal-eyebrow {
  margin: 0 0 6px;
  color: #89ddff;
  font-size: 11px;
  font-weight: 800;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.modal-header h2 {
  margin: 0 0 6px;
  color: #edf6ff;
  font-size: 20px;
}

.modal-subtitle {
  margin: 0;
  color: #71869b;
  font-size: 12px;
}

.modal-close {
  align-self: flex-start;
  padding: 7px 13px;
  border: 1px solid #334155;
  border-radius: 999px;
  background: rgba(15, 23, 42, 0.72);
  color: #c8d5e3;
  cursor: pointer;
}

.modal-close:hover,
.modal-close:focus-visible {
  border-color: #89ddff;
  color: #89ddff;
  outline: none;
}

.modal-body {
  max-height: calc(min(86vh, 860px) - 99px);
  overflow: auto;
  padding: 18px 24px 24px;
}

.detail-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
}

.detail-item {
  min-width: 0;
  padding: 10px 12px;
  border: 1px solid rgba(58, 74, 94, 0.7);
  border-radius: 12px;
  background: rgba(26, 31, 46, 0.72);
}

.detail-item--wide {
  grid-column: span 2;
}

.detail-item span {
  display: block;
  margin-bottom: 6px;
  color: #6f8194;
  font-size: 11px;
}

.detail-item strong {
  display: block;
  overflow: hidden;
  color: #d9e2ec;
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.status-text-running { color: #66bb6a !important; }
.status-text-exited { color: #ffa726 !important; }
.status-text-stopped { color: #90a4ae !important; }
.status-text-failed,
.status-text-error { color: #ff5370 !important; }

.detail-tabs-section {
  margin-top: 14px;
  overflow: hidden;
  border: 1px solid rgba(58, 74, 94, 0.64);
  border-radius: 14px;
  background: rgba(10, 14, 22, 0.64);
}

.detail-tabs {
  display: flex;
  gap: 2px;
  overflow-x: auto;
  padding: 6px;
  border-bottom: 1px solid rgba(58, 74, 94, 0.64);
}

.detail-tab-button {
  flex: 1 0 auto;
  min-width: max-content;
  padding: 8px 11px;
  border: 1px solid transparent;
  border-radius: 9px;
  background: transparent;
  color: #7f93a8;
  font-size: 11px;
  font-weight: 800;
  cursor: pointer;
}

.detail-tab-button:hover,
.detail-tab-button:focus-visible {
  color: #c7d4e2;
  background: rgba(137, 221, 255, 0.07);
  outline: none;
}

.detail-tab-button.active {
  border-color: rgba(137, 221, 255, 0.72);
  background: #89ddff;
  color: #071018;
  box-shadow: 0 0 18px rgba(79, 195, 247, 0.22);
}

.detail-tab-panel {
  padding: 14px;
}

.tab-panel-header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.tab-panel-header h3 {
  margin: 0 0 4px;
  color: #e4edf6;
  font-size: 14px;
}

.tab-panel-header p {
  margin: 0;
  color: #71869b;
  font-size: 11px;
  line-height: 1.45;
}

.detail-source-pill {
  align-self: flex-start;
  flex-shrink: 0;
  padding: 3px 7px;
  border: 1px solid rgba(58, 74, 94, 0.9);
  border-radius: 999px;
  color: #8aa0b8;
  font-size: 10px;
  line-height: 1.2;
  white-space: nowrap;
}

.detail-source-pill--active {
  border-color: rgba(102, 187, 106, 0.46);
  background: rgba(102, 187, 106, 0.08);
  color: #9be69f;
}

.detail-source-pill--empty {
  border-color: rgba(255, 203, 107, 0.36);
  background: rgba(255, 203, 107, 0.07);
  color: #ffcb6b;
}

.detail-source-pill--error {
  border-color: rgba(255, 83, 112, 0.42);
  background: rgba(255, 83, 112, 0.08);
  color: #ff8aa0;
}

.detail-empty-state {
  padding: 18px 14px;
  border: 1px dashed rgba(113, 134, 155, 0.42);
  border-radius: 12px;
  background: rgba(26, 31, 46, 0.52);
  color: #9aabba;
  font-size: 12px;
  line-height: 1.6;
}

.detail-empty-state--compact {
  margin-top: 10px;
  padding: 12px;
}

.detail-empty-state--loading {
  border-color: rgba(137, 221, 255, 0.34);
  color: #89ddff;
}

.detail-empty-state--error {
  border-color: rgba(255, 83, 112, 0.38);
  color: #ff9aab;
}

.detail-transcript-pre,
.diff-block pre,
.context-signal-list pre {
  margin: 0;
  padding: 12px;
  border: 1px solid rgba(58, 74, 94, 0.72);
  border-radius: 12px;
  background: #080d14;
  color: #d6e2ee;
  font-family: 'Cascadia Code', 'SFMono-Regular', Consolas, monospace;
  font-size: 11px;
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-word;
}

.detail-transcript-pre {
  max-height: 360px;
  overflow: auto;
}

.diff-block-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.diff-block-meta {
  margin-bottom: 5px;
  color: #89ddff;
  font-size: 10px;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.diff-block pre {
  max-height: 300px;
  overflow: auto;
}

.context-summary-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
  margin-bottom: 10px;
}

.context-summary-item,
.context-metadata-list div {
  min-width: 0;
  padding: 9px 10px;
  border: 1px solid rgba(58, 74, 94, 0.62);
  border-radius: 10px;
  background: rgba(26, 31, 46, 0.56);
}

.context-summary-item span,
.context-metadata-list span {
  display: block;
  margin-bottom: 5px;
  color: #6f8194;
  font-size: 10px;
}

.context-summary-item strong,
.context-metadata-list strong {
  display: block;
  overflow: hidden;
  color: #d9e2ec;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.context-metadata-list {
  display: grid;
  gap: 8px;
}

.context-signal-list {
  margin-top: 12px;
}

.context-signal-list h4 {
  margin: 0 0 8px;
  color: #a9bbcc;
  font-size: 12px;
}

.context-signal-list pre + pre {
  margin-top: 6px;
}

@media (max-width: 760px) {
  .session-detail-modal-backdrop {
    padding: 12px;
  }

  .modal-header,
  .modal-body {
    padding-left: 16px;
    padding-right: 16px;
  }

  .detail-grid,
  .context-summary-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .detail-item--wide {
    grid-column: 1 / -1;
  }
}
</style>
