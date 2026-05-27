<template>
  <div class="terminals-page">
    <!-- 终端标签栏 -->
    <div class="terminal-tabs-bar">
      <div class="tabs-left">
        <div
          v-for="sess in embeddedSessions"
          :key="sess.id"
          class="terminal-tab"
          :class="{ active: activeSessionId === sess.id }"
          @click="switchTab(sess.id)"
        >
          <span class="tab-dot" :class="`dot-${sess.status}`"></span>
          <span class="tab-app-type" :class="`app-${sess.appType}`">{{ appTypeLabel(sess.appType) }}</span>
          <span class="tab-label">{{ sess.model || basename(sess.workDir) }}</span>
          <span class="tab-id">#{{ sess.id }}</span>
          <span
            class="tab-close"
            @click.stop="handleClose(sess.id, sess.status)"
            title="关闭终端"
          >×</span>
          <button
            type="button"
            class="tab-detail"
            :class="{ active: detailSessionId === sess.id }"
            @click.stop="toggleDetail(sess.id)"
            title="查看会话详情"
          >详情</button>
        </div>
      </div>
      <div class="tabs-right">
        <span class="tab-count">{{ embeddedSessions.length }} 个终端</span>
      </div>
    </div>

    <!-- 终端内容区 -->
    <div ref="terminalContentRef" class="terminal-content" v-if="embeddedSessions.length > 0">
      <aside v-if="detailSession" class="session-detail-panel">
        <div class="detail-panel-header">
          <div>
            <h2>会话详情</h2>
            <p>{{ detailSession.id }}</p>
          </div>
          <button type="button" class="detail-close" @click="detailSessionId = ''">关闭</button>
        </div>
        <div class="detail-grid">
          <div class="detail-item">
            <span>状态</span>
            <strong :class="`status-text-${detailSession.status}`">{{ statusLabel(detailSession.status) }}</strong>
          </div>
          <div class="detail-item">
            <span>应用类型</span>
            <strong>{{ appTypeLongLabel(detailSession.appType) }}</strong>
          </div>
          <div class="detail-item">
            <span>模式</span>
            <strong>{{ detailSession.mode || '-' }}</strong>
          </div>
          <div class="detail-item">
            <span>PID</span>
            <strong>{{ detailSession.pid || '-' }}</strong>
          </div>
          <div class="detail-item">
            <span>提供商</span>
            <strong>{{ detailSession.provider || '-' }}</strong>
          </div>
          <div class="detail-item">
            <span>预设</span>
            <strong>{{ detailSession.preset || '-' }}</strong>
          </div>
          <div class="detail-item detail-item--wide">
            <span>模型</span>
            <strong>{{ detailSession.model || '-' }}</strong>
          </div>
          <div class="detail-item detail-item--wide">
            <span>工作目录</span>
            <strong :title="detailSession.workDir">{{ detailSession.workDir || '-' }}</strong>
          </div>
          <div class="detail-item">
            <span>启动时间</span>
            <strong>{{ formatDateTime(detailSession.startedAt) }}</strong>
          </div>
          <div class="detail-item">
            <span>运行时长</span>
            <strong>{{ detailSession.duration || '-' }}</strong>
          </div>
        </div>

        <section class="detail-tabs-section" aria-label="会话结构化详情">
          <div class="detail-tabs" role="tablist" aria-label="会话详情标签">
            <button
              v-for="tab in detailTabs"
              :key="tab.id"
              type="button"
              class="detail-tab-button"
              :class="{ active: activeDetailTab === tab.id }"
              role="tab"
              :aria-selected="activeDetailTab === tab.id"
              @click="activeDetailTab = tab.id"
            >{{ tab.label }}</button>
          </div>

          <div class="detail-tab-panel" role="tabpanel">
            <template v-if="activeDetailTab === 'transcript'">
              <div class="tab-panel-header">
                <div>
                  <h3>Transcript</h3>
                  <p>来自当前会话 Wails history snapshot 与 pty:data 事件的真实输出。</p>
                </div>
                <span class="detail-source-pill" :class="detailOutputStatusClass(detailOutput)">
                  {{ detailOutputStatusLabel(detailOutput) }}
                </span>
              </div>
              <div v-if="detailOutput.historyStatus === 'loading'" class="detail-empty-state detail-empty-state--loading">
                正在读取当前会话历史输出。
              </div>
              <div v-else-if="detailOutput.decodeError" class="detail-empty-state detail-empty-state--error">
                历史输出解码失败，详情面板仅展示后续实时 pty:data 输出。
              </div>
              <pre v-else-if="detailTranscriptText" class="detail-transcript-pre">{{ detailTranscriptText }}</pre>
              <div v-else class="detail-empty-state">
                当前会话暂无输出。详情面板不会生成示例 transcript，只等待真实 PTY 数据。
              </div>
            </template>

            <template v-else-if="activeDetailTab === 'diff'">
              <div class="tab-panel-header">
                <div>
                  <h3>Diff</h3>
                  <p>仅从真实终端输出中识别 unified diff 片段。</p>
                </div>
                <span class="detail-source-pill">{{ detailDiffBlocks.length }} blocks</span>
              </div>
              <div v-if="detailDiffBlocks.length > 0" class="diff-block-list">
                <article v-for="block in detailDiffBlocks" :key="block.id" class="diff-block">
                  <div class="diff-block-meta">Lines {{ block.startLine }}-{{ block.endLine }}</div>
                  <pre>{{ block.text }}</pre>
                </article>
              </div>
              <div v-else class="detail-empty-state">
                当前会话尚未产生可识别的 diff 输出。识别来源限于真实 PTY 文本中的 diff --git、---/+++、@@ 等组合特征。
              </div>
            </template>

            <template v-else-if="activeDetailTab === 'context'">
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
                  <strong>{{ detailOutput.totalBytes }}</strong>
                </div>
                <div class="context-summary-item">
                  <span>输出片段</span>
                  <strong>{{ detailOutput.totalChunks }}</strong>
                </div>
                <div class="context-summary-item">
                  <span>文本行数</span>
                  <strong>{{ detailContext.lineCount }}</strong>
                </div>
                <div class="context-summary-item">
                  <span>最新序号</span>
                  <strong>{{ detailOutput.lastSeq || '-' }}</strong>
                </div>
              </div>
              <div class="context-metadata-list">
                <div><span>应用</span><strong>{{ appTypeLongLabel(detailSession.appType) }}</strong></div>
                <div><span>Provider</span><strong>{{ detailSession.provider || '-' }}</strong></div>
                <div><span>Preset</span><strong>{{ detailSession.preset || '-' }}</strong></div>
                <div><span>Model</span><strong>{{ detailSession.model || '-' }}</strong></div>
                <div><span>WorkDir</span><strong :title="detailSession.workDir">{{ detailSession.workDir || '-' }}</strong></div>
              </div>
              <div v-if="detailContext.signalLines.length > 0" class="context-signal-list">
                <h4>识别到的上下文/工具输出行</h4>
                <pre v-for="line in detailContext.signalLines" :key="line">{{ line }}</pre>
              </div>
              <div v-else class="detail-empty-state detail-empty-state--compact">
                暂未从真实输出中识别到 context/tool 行。当前面板仍保留真实 metadata 与输出统计。
              </div>
            </template>

            <template v-else-if="activeDetailTab === 'files'">
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

            <template v-else-if="activeDetailTab === 'review'">
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
      </aside>
      <aside v-else-if="detailSessionId" class="session-detail-panel session-detail-panel--error">
        <div class="detail-panel-header">
          <div>
            <h2>会话详情不可用</h2>
            <p>找不到对应会话，可能已被移除。</p>
          </div>
          <button type="button" class="detail-close" @click="detailSessionId = ''">关闭</button>
        </div>
      </aside>
      <div
        v-for="sess in embeddedSessions"
        :key="'term-' + sess.id"
        :ref="(el) => setTermRef(sess.id, el as HTMLElement)"
        class="terminal-container"
        :class="{ visible: activeSessionId === sess.id }"
        @contextmenu.prevent="showContextMenu($event, sess.id)"
      ></div>
    </div>

    <!-- 空状态 -->
    <div class="terminal-empty" v-else>
      <div class="empty-icon">⬛</div>
      <p class="empty-text">暂无运行中的内嵌终端</p>
      <p class="empty-hint">在仪表盘中选择"内嵌终端"模式启动会话</p>
    </div>

    <!-- 右键菜单（使用 v-show 避免破坏 v-if/v-else 链） -->
    <div
      v-show="ctxMenu.visible"
      class="ctx-menu"
      :style="{ left: ctxMenu.x + 'px', top: ctxMenu.y + 'px' }"
    >
      <div class="ctx-item" @mousedown.prevent="ctxCopy">
        <span>复制</span>
        <span class="ctx-shortcut">Ctrl+Shift+C</span>
      </div>
      <div class="ctx-item" @mousedown.prevent="ctxPaste">
        <span>粘贴</span>
        <span class="ctx-shortcut">Ctrl+Shift+V</span>
      </div>
      <div class="ctx-sep"></div>
      <div class="ctx-item" @mousedown.prevent="ctxSelectAll">
        <span>全选</span>
        <span class="ctx-shortcut">Ctrl+Shift+A</span>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, computed, onMounted, onUnmounted, onActivated, watch, nextTick } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'
import { GetSessions, StopSession, RemoveSession, PtyWrite, PtyWriteLarge, PtyResize, GetOutputHistorySnapshot, OpenFileInEditor, SaveClipboardImage } from '../../wailsjs/go/main/App'
import { GetTerminalSettings } from '../../wailsjs/go/settings/Service'
import { EventsOn, BrowserOpenURL } from '../../wailsjs/runtime/runtime'
import { usePlatformCapabilities } from '../composables/usePlatformCapabilities'

defineOptions({ name: 'TerminalsPage' })

const platformCaps = usePlatformCapabilities()

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

interface TerminalInstance {
  term: Terminal
  fit: FitAddon
  webgl: WebglAddon | null
  disposeDataListener: (() => void) | null
  disposeExitListener: (() => void) | null
  lastCols: number
  lastRows: number
}

type DetailTabId = 'transcript' | 'diff' | 'context' | 'files' | 'review'

interface DetailOutputChunk {
  seq: number
  text: string
  byteLength: number
}

interface DetailOutputState {
  sessionId: string
  chunks: DetailOutputChunk[]
  historyStatus: 'idle' | 'loading' | 'loaded' | 'unavailable' | 'error'
  decodeError: boolean
  totalBytes: number
  totalChunks: number
  lastSeq: number
}

interface DiffBlock {
  id: string
  text: string
  startLine: number
  endLine: number
}

interface ContextSummary {
  lineCount: number
  signalLines: string[]
}

const DETAIL_OUTPUT_MAX_CHARS = 60000

const detailTabs: Array<{ id: DetailTabId; label: string }> = [
  { id: 'transcript', label: 'Transcript' },
  { id: 'diff', label: 'Diff' },
  { id: 'context', label: 'Context' },
  { id: 'files', label: 'Files' },
  { id: 'review', label: 'Review' },
]

// base64 → Uint8Array（正确处理二进制，避免 atob 的 Latin-1 问题）
function base64ToUint8(base64: string): Uint8Array {
  const bin = atob(base64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) {
    bytes[i] = bin.charCodeAt(i)
  }
  return bytes
}

// Uint8Array → base64
function uint8ToBase64(bytes: Uint8Array): string {
  let bin = ''
  for (let i = 0; i < bytes.length; i++) {
    bin += String.fromCharCode(bytes[i])
  }
  return btoa(bin)
}

// Decode GetOutputHistory / GetOutputHistorySnapshot returned data into Uint8Array.
// Handles three return shapes for maximum compatibility:
//   - string:         base64-encoded byte stream (Wails v2 runtime binding)
//   - Array<number>:  raw byte values (Wails-generated .d.ts declares Promise<Array<number>>)
//   - Uint8Array:     already decoded (defensive)
// Returns null if the data cannot be decoded, allowing the caller to fall through
// to live-only mode rather than silently producing garbled output.
function decodeHistoryData(data: unknown): Uint8Array | null {
  if (data == null) return null
  if (data instanceof Uint8Array) return data
  if (typeof data === 'string') {
    if (data.length === 0) return new Uint8Array()
    try {
      return base64ToUint8(data)
    } catch {
      console.warn('[amagi-codebox] history decode: base64 decode failed')
      return null
    }
  }
  if (Array.isArray(data)) {
    if (data.length === 0) return new Uint8Array()
    try {
      return new Uint8Array(data)
    } catch {
      console.warn('[amagi-codebox] history decode: Array<number> conversion failed')
      return null
    }
  }
  console.warn('[amagi-codebox] history decode: unexpected type', typeof data)
  return null
}

const sessions = ref<SessionInfo[]>([])
const activeSessionId = ref('')
const detailSessionId = ref('')
const activeDetailTab = ref<DetailTabId>('transcript')
const detailOutputStates = ref<Record<string, DetailOutputState>>({})
const terminals = new Map<string, TerminalInstance>()
const termRefs = new Map<string, HTMLElement>()
const terminalContentRef = ref<HTMLElement | null>(null)
const scrollbackLines = ref(100000)

// 右键菜单状态
const ctxMenu = ref({ visible: false, x: 0, y: 0, sessionId: '' })

let refreshInterval: number | null = null
let resizeObserver: ResizeObserver | null = null
let visibilityRefitTimer: number | null = null

const embeddedSessions = computed(() =>
  sessions.value.filter(s => s.mode === 'embedded')
)

const detailSession = computed(() => {
  if (!detailSessionId.value) return null
  return embeddedSessions.value.find((session) => session.id === detailSessionId.value) || null
})

const detailOutput = computed(() => getDetailOutputState(detailSessionId.value))

const detailTranscriptText = computed(() => buildTranscriptText(detailOutput.value))

const detailDiffBlocks = computed(() => extractDiffBlocks(detailTranscriptText.value))

const detailContext = computed(() => buildContextSummary(detailTranscriptText.value))

function basename(p: string): string {
  if (!p) return ''
  return p.replace(/\\/g, '/').split('/').pop() || p
}

function appTypeLabel(appType: string): string {
  const map: Record<string, string> = {
    claudecode: 'CC',
    opencode: 'OC',
    codex: 'CX',
  }
  return map[appType] || appType
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

function createDetailOutputState(sessionId: string): DetailOutputState {
  return {
    sessionId,
    chunks: [],
    historyStatus: 'idle',
    decodeError: false,
    totalBytes: 0,
    totalChunks: 0,
    lastSeq: 0,
  }
}

function getDetailOutputState(sessionId: string): DetailOutputState {
  if (!sessionId) return createDetailOutputState('')
  return detailOutputStates.value[sessionId] || createDetailOutputState(sessionId)
}

function updateDetailOutputState(sessionId: string, updater: (state: DetailOutputState) => DetailOutputState) {
  const current = detailOutputStates.value[sessionId] || createDetailOutputState(sessionId)
  detailOutputStates.value = {
    ...detailOutputStates.value,
    [sessionId]: updater(current),
  }
}

function decodeTerminalBytes(bytes: Uint8Array): string {
  try {
    return new TextDecoder().decode(bytes)
  } catch {
    let result = ''
    for (let i = 0; i < bytes.length; i++) result += String.fromCharCode(bytes[i])
    return result
  }
}

function stripAnsi(text: string): string {
  return text
    .replace(/\x1B\[[0-?]*[ -/]*[@-~]/g, '')
    .replace(/\x1B\][^\x07]*(?:\x07|\x1B\\)/g, '')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')
}

function appendDetailOutputChunk(sessionId: string, seq: number, bytes: Uint8Array) {
  const text = stripAnsi(decodeTerminalBytes(bytes))
  if (!text) return

  updateDetailOutputState(sessionId, (state) => {
    if (seq > 0 && state.chunks.some((chunk) => chunk.seq === seq)) return state

    const chunks = [...state.chunks, { seq, text, byteLength: bytes.length }]
    let totalChars = chunks.reduce((sum, chunk) => sum + chunk.text.length, 0)
    while (totalChars > DETAIL_OUTPUT_MAX_CHARS && chunks.length > 1) {
      const removed = chunks.shift()
      totalChars -= removed?.text.length || 0
    }

    return {
      ...state,
      chunks,
      totalBytes: state.totalBytes + bytes.length,
      totalChunks: state.totalChunks + 1,
      lastSeq: Math.max(state.lastSeq, seq || 0),
    }
  })
}

function loadDetailHistory(sessionId: string, jsonStr: string | null | undefined) {
  if (!jsonStr) {
    updateDetailOutputState(sessionId, (state) => ({
      ...state,
      historyStatus: 'unavailable',
    }))
    return
  }

  try {
    const snapshot = JSON.parse(jsonStr)
    const decoded = decodeHistoryData(snapshot.data)
    if (decoded === null) {
      updateDetailOutputState(sessionId, (state) => ({
        ...state,
        historyStatus: 'error',
        decodeError: true,
      }))
      return
    }

    const text = stripAnsi(decodeTerminalBytes(decoded))
    updateDetailOutputState(sessionId, (state) => {
      const liveChunks = state.chunks.filter((chunk) => !snapshot.seq || chunk.seq > snapshot.seq)
      const historyChunk = text
        ? [{ seq: snapshot.seq || 0, text, byteLength: decoded.length }]
        : []
      return {
        ...state,
        chunks: [...historyChunk, ...liveChunks],
        historyStatus: 'loaded',
        decodeError: false,
        totalBytes: Math.max(state.totalBytes, decoded.length + liveChunks.reduce((sum, chunk) => sum + chunk.byteLength, 0)),
        totalChunks: Math.max(state.totalChunks, historyChunk.length + liveChunks.length),
        lastSeq: Math.max(state.lastSeq, snapshot.seq || 0),
      }
    })
  } catch {
    updateDetailOutputState(sessionId, (state) => ({
      ...state,
      historyStatus: 'error',
      decodeError: true,
    }))
  }
}

function buildTranscriptText(state: DetailOutputState): string {
  if (state.chunks.length === 0) return ''
  const joined = state.chunks.map((chunk) => chunk.text).join('')
  return joined.length > DETAIL_OUTPUT_MAX_CHARS
    ? joined.slice(joined.length - DETAIL_OUTPUT_MAX_CHARS)
    : joined
}

function extractDiffBlocks(text: string): DiffBlock[] {
  if (!text.trim()) return []
  const lines = text.split('\n')
  const blocks: DiffBlock[] = []
  let start = -1
  let buffer: string[] = []
  let hasHeader = false
  let hasHunk = false
  let hasChange = false

  const flush = (endIndex: number) => {
    if (start >= 0 && buffer.length > 0 && (hasHunk || (hasHeader && hasChange))) {
      blocks.push({
        id: `${start + 1}-${endIndex + 1}-${blocks.length}`,
        text: buffer.join('\n'),
        startLine: start + 1,
        endLine: endIndex + 1,
      })
    }
    start = -1
    buffer = []
    hasHeader = false
    hasHunk = false
    hasChange = false
  }

  const isDiffLine = (line: string) =>
    line.startsWith('diff --git ') ||
    line.startsWith('index ') ||
    line.startsWith('--- ') ||
    line.startsWith('+++ ') ||
    line.startsWith('@@') ||
    line.startsWith('+') ||
    line.startsWith('-') ||
    line.startsWith(' ')

  lines.forEach((line, index) => {
    const startsBlock = line.startsWith('diff --git ') || (line.startsWith('--- ') && lines[index + 1]?.startsWith('+++ '))
    if (startsBlock) {
      if (start >= 0) flush(index - 1)
      start = index
    }

    if (start >= 0 && isDiffLine(line)) {
      buffer.push(line)
      if (line.startsWith('diff --git ') || line.startsWith('--- ') || line.startsWith('+++ ')) hasHeader = true
      if (line.startsWith('@@')) hasHunk = true
      if ((line.startsWith('+') && !line.startsWith('+++')) || (line.startsWith('-') && !line.startsWith('---'))) hasChange = true
      return
    }

    if (start >= 0) flush(index - 1)
  })

  if (start >= 0) flush(lines.length - 1)
  return blocks.slice(-8)
}

function buildContextSummary(text: string): ContextSummary {
  const lines = text.split('\n').map((line) => line.trim()).filter(Boolean)
  const signalPattern = /\b(context|tool|function|mcp|token|tokens|model|provider|workspace|workdir|reading|read file|edit|write|bash|grep|glob)\b/i
  return {
    lineCount: lines.length,
    signalLines: lines.filter((line) => signalPattern.test(line)).slice(-12),
  }
}

function detailOutputStatusLabel(state: DetailOutputState): string {
  if (state.historyStatus === 'loading') return 'loading history'
  if (state.decodeError || state.historyStatus === 'error') return 'history error'
  if (state.totalChunks > 0 || state.chunks.length > 0) return 'real output'
  if (state.historyStatus === 'unavailable') return 'live only'
  return 'waiting'
}

function detailOutputStatusClass(state: DetailOutputState): string {
  if (state.decodeError || state.historyStatus === 'error') return 'detail-source-pill--error'
  if (state.totalChunks > 0 || state.chunks.length > 0) return 'detail-source-pill--active'
  return ''
}

function toggleDetail(id: string) {
  detailSessionId.value = detailSessionId.value === id ? '' : id
}

function setTermRef(id: string, el: HTMLElement | null) {
  if (el) {
    termRefs.set(id, el)
  } else {
    termRefs.delete(id)
  }
}

function switchTab(id: string) {
  activeSessionId.value = id
  nextTick(() => fitTerminal(id, true))
}

function fitTerminal(id: string, force = false) {
  const inst = terminals.get(id)
  if (!inst) return
  const dims = inst.fit.proposeDimensions()
  if (!dims || dims.cols <= 0 || dims.rows <= 0) return

  const sameDims = dims.cols === inst.lastCols && dims.rows === inst.lastRows
  // 未发生实际尺寸变化时，避免重复 fit 引发 xterm/ConPTY 的重复重绘。
  // 仅在显式切换标签或页面重新激活这类可见性恢复场景下强制执行一次。
  if (sameDims && !force) return

  try {
    // 保存当前滚动状态：判断用户是否停留在底部
    const viewport = termRefs.get(id)?.querySelector('.xterm-viewport') as HTMLElement | null
    const scrollTop = viewport?.scrollTop ?? 0
    const isAtBottom = viewport
      ? viewport.scrollTop + viewport.clientHeight >= viewport.scrollHeight - 2
      : true

    inst.fit.fit()
    if (!sameDims) {
      inst.lastCols = dims.cols
      inst.lastRows = dims.rows
      PtyResize(id, dims.cols, dims.rows).catch(() => {})
    }

    // 若用户未停留在底部，则在下一帧恢复到原滚动位置，防止 fit.fit() 引发的瞬移
    if (!isAtBottom && viewport) {
      requestAnimationFrame(() => {
        viewport.scrollTop = scrollTop
      })
    }
  } catch {}
}

// ---- 复制 / 粘贴 ----

/** 将文本写入系统剪贴板 */
async function copyToClipboard(text: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // fallback: 使用旧式 execCommand
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.left = '-9999px'
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    document.body.removeChild(ta)
  }
}

/** 从系统剪贴板读取文本并写入到指定会话终端 */
async function pasteToTerminal(sessionId: string) {
  try {
    // 优先尝试读取文本
    const text = await navigator.clipboard.readText()
    if (text) {
      const bytes = new TextEncoder().encode(text)
      const encoded = uint8ToBase64(bytes)
      // 长文本使用分块写入避免 ConPTY 缓冲区溢出截断
      if (bytes.length > 1024) {
        await PtyWriteLarge(sessionId, encoded)
      } else {
        await PtyWrite(sessionId, encoded)
      }
      return
    }

    // 文本为空时，检查剪贴板是否包含图片（如 Windows 截图工具截图）
    try {
      const items = await navigator.clipboard.read()
      for (const item of items) {
        for (const type of item.types) {
          if (type.startsWith('image/')) {
            const blob = await item.getType(type)
            const arrayBuf = await blob.arrayBuffer()
            const uint8 = new Uint8Array(arrayBuf)
            const b64 = uint8ToBase64(uint8)
            // 调用后端保存图片为临时文件，返回文件路径
            const filePath = await SaveClipboardImage(b64)
            if (filePath) {
              const pathBytes = new TextEncoder().encode(filePath)
              await PtyWrite(sessionId, uint8ToBase64(pathBytes))
            }
            return
          }
        }
      }
    } catch {
      // clipboard.read() 可能不被支持或无权限，静默忽略
    }
  } catch (err) {
    console.error('paste error:', err)
  }
}

/** 复制当前终端选中内容 */
function copySelection(sessionId: string): boolean {
  const inst = terminals.get(sessionId)
  if (!inst) return false
  const sel = inst.term.getSelection()
  if (sel) {
    copyToClipboard(sel)
    inst.term.clearSelection()
    return true
  }
  return false
}

// ---- 右键菜单 ----

function showContextMenu(ev: MouseEvent, sessionId: string) {
  ctxMenu.value = { visible: true, x: ev.clientX, y: ev.clientY, sessionId }
}

function hideContextMenu() {
  ctxMenu.value = { ...ctxMenu.value, visible: false }
}

function ctxCopy() {
  copySelection(ctxMenu.value.sessionId)
  hideContextMenu()
}

function ctxPaste() {
  pasteToTerminal(ctxMenu.value.sessionId)
  hideContextMenu()
  // 重新聚焦终端
  const inst = terminals.get(ctxMenu.value.sessionId)
  if (inst) inst.term.focus()
}

function ctxSelectAll() {
  const inst = terminals.get(ctxMenu.value.sessionId)
  if (inst) inst.term.selectAll()
  hideContextMenu()
}

function loadWebglRenderer(sessionId: string, inst: TerminalInstance) {
  try {
    const webgl = new WebglAddon()
    webgl.onContextLoss(() => {
      if (inst.webgl === webgl) {
        inst.webgl.dispose()
        inst.webgl = null
      } else {
        webgl.dispose()
      }

      window.setTimeout(() => {
        if (terminals.get(sessionId) !== inst || !inst.term.element) return
        try {
          loadWebglRenderer(sessionId, inst)
          window.setTimeout(() => fitTerminal(sessionId), 50)
        } catch {
          inst.webgl = null
        }
      }, 500)
    })
    inst.term.loadAddon(webgl)
    inst.webgl = webgl
  } catch {
    inst.webgl = null
  }
}

// Probe whether the current environment can create a WebGL context.
// This is a capability check only -- the caller decides whether to actually
// enable the WebglAddon. On macOS WKWebView the context may be creatable but
// xterm.js WebGL texture atlas still produces scrollback corruption, so the
// caller skips WebGL on macOS regardless of probe result.
function isWebGLReliable(): boolean {
  try {
    const canvas = document.createElement('canvas')
    const gl = canvas.getContext('webgl2') || canvas.getContext('webgl')
    if (!gl) return false
    // Log renderer info for diagnostics; does not influence the decision.
    const ext = gl.getExtension('WEBGL_debug_renderer_info')
    if (ext) {
      const renderer = gl.getParameter(ext.UNMASKED_RENDERER_WEBGL)
      if (renderer) {
        console.info('[amagi-codebox] WebGL renderer:', renderer)
      }
    }
    return true
  } catch {
    return false
  }
}

function createTerminal(sessionId: string) {
  if (terminals.has(sessionId)) return

  const term = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    scrollback: scrollbackLines.value,
    fontFamily: "'Cascadia Code', 'Consolas', 'Courier New', monospace",
    // xterm.js disables its selection layer while a TUI enables mouse reporting.
    // Windows keeps a built-in Shift+drag escape hatch; macOS requires this
    // option so Option+drag can force local terminal selection without globally
    // intercepting ordinary drags that should still reach the TUI.
    macOptionClickForcesSelection: true,
    // ConPTY hint only on Windows
    ...(platformCaps.isWindows.value ? { windowsPty: { backend: 'conpty', buildNumber: 19041 } } : {}),
    theme: {
      background: '#1a1f2e',
      foreground: '#e0e0e0',
      cursor: '#4fc3f7',
      cursorAccent: '#1a1f2e',
      selectionBackground: '#3a4a6a',
      black: '#1a1f2e',
      red: '#ff5370',
      green: '#c3e88d',
      yellow: '#ffcb6b',
      blue: '#82aaff',
      magenta: '#c792ea',
      cyan: '#89ddff',
      white: '#e0e0e0',
      brightBlack: '#546e7a',
      brightRed: '#ff5370',
      brightGreen: '#c3e88d',
      brightYellow: '#ffcb6b',
      brightBlue: '#82aaff',
      brightMagenta: '#c792ea',
      brightCyan: '#89ddff',
      brightWhite: '#ffffff',
    },
    allowProposedApi: true,
  })

  const fit = new FitAddon()
  term.loadAddon(fit)

  // 键盘快捷键：复制 / 粘贴
  term.attachCustomKeyEventHandler((ev: KeyboardEvent) => {
    if (ev.type !== 'keydown') return true

    // Ctrl+Shift+C → 总是复制选中内容
    if (ev.ctrlKey && ev.shiftKey && ev.code === 'KeyC') {
      copySelection(sessionId)
      return false
    }
    // Ctrl+Shift+V → 总是粘贴
    if (ev.ctrlKey && ev.shiftKey && ev.code === 'KeyV') {
      pasteToTerminal(sessionId)
      return false
    }
    // Ctrl+Shift+A → 全选
    if (ev.ctrlKey && ev.shiftKey && ev.code === 'KeyA') {
      term.selectAll()
      return false
    }

    // Delete / Backspace → 有选中内容时，发送等量的退格字符删除选中文本
    if (!ev.ctrlKey && !ev.shiftKey && !ev.altKey &&
        (ev.code === 'Backspace' || ev.code === 'Delete')) {
      const sel = term.getSelection()
      if (sel && sel.length > 0) {
        const bsCount = sel.length
        const bsChars = '\b'.repeat(bsCount)
        const bytes = new TextEncoder().encode(bsChars)
        PtyWrite(sessionId, uint8ToBase64(bytes)).catch(() => {})
        term.clearSelection()
        return false
      }
    }

    // Ctrl+C → 有选中内容时复制，否则发送 SIGINT
    if (ev.ctrlKey && !ev.shiftKey && ev.code === 'KeyC') {
      const sel = term.getSelection()
      if (sel) {
        copyToClipboard(sel)
        term.clearSelection()
        return false // 阻止终端处理（不发送 SIGINT）
      }
      return true // 无选中，让终端发送 ^C
    }

    // Ctrl+V → 阻止 xterm 发送 ^V 字符；实际粘贴由下方的 textarea paste 监听器处理
    if (ev.ctrlKey && !ev.shiftKey && ev.code === 'KeyV') {
      return false
    }

    return true
  })

  // 用户输入 → 发送到后端 PTY
  term.onData((data: string) => {
    // 将 JS 字符串编码为 UTF-8 字节，再 base64 编码
    const bytes = new TextEncoder().encode(data)
    const encoded = uint8ToBase64(bytes)
    PtyWrite(sessionId, encoded).catch(err => {
      console.error('PtyWrite error:', err)
    })
  })

  // 后端 PTY 输出 → 写入 xterm
  // Buffer live data chunks until history replay completes, to prevent
  // live output from being interleaved with or appearing before history.
  // Each live event carries a monotonic emitSeq from the backend; after
  // history replay, chunks with seq <= snapshot seq are discarded because
  // they are already contained in the history snapshot (M1 dedup).
  interface LiveChunk { seq: number; bytes: Uint8Array }
  const liveBuffer: LiveChunk[] = []
  let historyReplayed = false
  let historySnapshotSeq = 0

  // Unified live chunk write: deduplicates against history snapshot.
  // Both flushLiveBuffer and the post-replay event handler direct-write
  // path must go through here to ensure seq-based dedup is never bypassed.
  // Safe to reference `inst` via closure: writeLiveChunk is only called after
  // historyReplayed becomes true, which happens after inst is initialized.
  function writeLiveChunk(seq: number, bytes: Uint8Array) {
    if (seq > 0 && seq <= historySnapshotSeq) return
    appendDetailOutputChunk(sessionId, seq, bytes)
    try {
      inst.term.write(bytes)
    } catch {}
  }

  const dataEvent = 'pty:data:' + sessionId
  const disposeDataListener = EventsOn(dataEvent, (eventData: any) => {
    try {
      // Backend sends { s: emitSeq, d: base64Data } for each chunk.
      let seq: number
      let base64Data: string
      if (eventData && typeof eventData === 'object' && 's' in eventData && 'd' in eventData) {
        seq = eventData.s as number
        base64Data = eventData.d as string
      } else if (typeof eventData === 'string') {
        // Fallback: legacy format without seq (should not happen with updated backend,
        // but handle gracefully by using seq=0 so all chunks flush after replay)
        seq = 0
        base64Data = eventData
      } else {
        return
      }
      const bytes = base64ToUint8(base64Data)
      if (!historyReplayed) {
        liveBuffer.push({ seq, bytes })
        return
      }
      writeLiveChunk(seq, bytes)
    } catch (err) {
      console.error('decode error:', err)
    }
  })

  // 进程退出通知
  const exitEvent = 'pty:exit:' + sessionId
  const disposeExitListener = EventsOn(exitEvent, (info: any) => {
      term.write('\r\n\x1b[33m[amagi-codebox] 进程已退出')
    if (info && info.exitCode !== undefined) {
      term.write(` (exit code: ${info.exitCode})`)
    }
    term.write('\x1b[0m\r\n')
    refreshSessions()
  })

  const inst: TerminalInstance = {
    term,
    fit,
    webgl: null,
    disposeDataListener,
    disposeExitListener,
    lastCols: 0,
    lastRows: 0,
  }
  terminals.set(sessionId, inst)

  // 延迟挂载到 DOM
  nextTick(() => {
    const el = termRefs.get(sessionId)
    if (el) {
      term.open(el)

      // 加载 WebLinksAddon：检测终端输出中的 HTTP/HTTPS URL，点击时使用系统默认浏览器打开
      try {
        const webLinks = new WebLinksAddon((_event: MouseEvent, uri: string) => {
          BrowserOpenURL(uri)
        })
        term.loadAddon(webLinks)
      } catch (e) {
        console.warn('WebLinksAddon failed to load', e)
      }

      // 注册自定义文件路径 LinkProvider：检测终端输出中的文件路径（含行号），
      // 点击时调用后端方法在编辑器中打开文件
      try {
        // 匹配常见文件路径模式（含可选行号和列号）
        // 要求必须含路径分隔符，避免匹配单纯文件名或版本号等误报
        // 示例：src/main.ts:42  ./lib/util.go:10:5  C:\path\to\file.go:100
        const FILE_PATH_REGEX = /(?:[A-Za-z]:[\/]|[.][\/])(?:[\w.\-]+[\/])*[\w.\-]+\.[a-zA-Z]{1,10}(?::(\d+)(?::\d+)?)?|(?:[\/]|(?:[\w.\-]+[\/]){1,})(?:[\w.\-]+[\/])*[\w.\-]+\.[a-zA-Z]{1,10}(?::(\d+)(?::\d+)?)?/g

        term.registerLinkProvider({
          provideLinks(bufferLineNumber: number, callback: (links: any[]) => void) {
            // 获取对应行的文本（bufferLineNumber 从 1 开始）
            const line = term.buffer.active.getLine(bufferLineNumber - 1)
            if (!line) {
              callback([])
              return
            }
            const lineText = line.translateToString(true)
            const links: any[] = []

            let match: RegExpExecArray | null
            FILE_PATH_REGEX.lastIndex = 0
            while ((match = FILE_PATH_REGEX.exec(lineText)) !== null) {
              const fullMatch = match[0]
              const lineNum = match[1] ? parseInt(match[1], 10) : 0
              // 提取纯文件路径（不含行号部分）
              const filePath = lineNum
                ? fullMatch.slice(0, fullMatch.lastIndexOf(':' + match[1]))
                : fullMatch

              // 过滤掉明显不是文件路径的内容（如纯数字、太短的字符串）
              if (filePath.length < 3 || !/[./\\]/.test(filePath)) continue
              // 排除 URL（已由 WebLinksAddon 处理）
              if (/^https?:\/\//i.test(filePath)) continue

              const startCol = match.index
              const endCol = match.index + fullMatch.length

              links.push({
                range: {
                  start: { x: startCol + 1, y: bufferLineNumber },
                  end: { x: endCol + 1, y: bufferLineNumber },
                },
                text: fullMatch,
                activate(_event: MouseEvent, _text: string) {
                  OpenFileInEditor(filePath, lineNum).catch((err: any) => {
                    console.warn('OpenFileInEditor failed:', err)
                  })
                },
                hover(_event: MouseEvent, _text: string) {
                  // tooltip 通过 xterm 默认 title 机制展示，无需额外操作
                },
              })
            }
            callback(links)
          },
        })
      } catch (e) {
        console.warn('registerLinkProvider failed', e)
      }

      // WebGL renderer: eliminates canvas ghosting/residual artifacts during scrolling.
      //
      // On macOS the embedded terminal runs inside WKWebView, where the xterm.js
      // WebGL addon triggers texture atlas corruption in scrollback (characters
      // garble, duplicate, or vanish after scrolling up). This is a correctness-
      // priority decision for macOS: skip WebglAddon and use xterm's default
      // canvas/dom renderer so that scrollback content is always visually correct.
      // Windows is unaffected and keeps the WebGL path.
      //
      // Fail-closed: requires platform capabilities to be loaded AND the OS to be
      // confirmed non-Darwin. If caps are still null (unexpected edge case), WebGL
      // is NOT loaded -- this is safer than risking scrollback corruption on macOS.
      if (platformCaps.caps.value && !platformCaps.isDarwin.value && isWebGLReliable()) {
        loadWebglRenderer(sessionId, inst)
      }

      // Replay output history for sessions that already have accumulated output
      // (e.g. page reload, component remount, or session restored from background).
      // This ensures the user sees the full terminal content rather than a blank screen.
      //
      // Atomic boundary (M1): GetOutputHistorySnapshot returns {data, seq} where seq
      // is the backend's monotonic emitSeq at snapshot time. Any live event with
      // seq <= snapshot seq is already in the history bytes and must be skipped.
      // Type compatibility (M2): decodeHistoryData handles string, Array<number>,
      // and Uint8Array return shapes.
      updateDetailOutputState(sessionId, (state) => ({
        ...state,
        historyStatus: 'loading',
      }))
      GetOutputHistorySnapshot(sessionId).then((jsonStr: string) => {
        loadDetailHistory(sessionId, jsonStr)
        if (!jsonStr) {
          historyReplayed = true
          flushLiveBuffer()
          return
        }
        try {
          const snapshot = JSON.parse(jsonStr)
          const decoded = decodeHistoryData(snapshot.data)
          if (decoded && decoded.length > 0) {
            inst.term.write(decoded)
            // Only set the dedup boundary after successful history write,
            // so buffered live chunks are not discarded when decode fails.
            historySnapshotSeq = snapshot.seq || 0
          } else if (decoded !== null && decoded.length === 0) {
            // decodeHistoryData returned empty: data was valid but empty.
            // The snapshot is authoritative (seq is valid), so set boundary.
            historySnapshotSeq = snapshot.seq || 0
          }
          // decoded === null means decode failed; leave historySnapshotSeq at 0
          // so all buffered live chunks flush through without being discarded.
        } catch (e) {
          console.warn('history replay failed:', e)
        }
        historyReplayed = true
        flushLiveBuffer()
      }).catch(() => {
        // Session may not support history (e.g. already exited); flush buffered live data
        updateDetailOutputState(sessionId, (state) => ({
          ...state,
          historyStatus: 'error',
          decodeError: true,
        }))
        historyReplayed = true
        flushLiveBuffer()
      })

      function flushLiveBuffer() {
        for (const chunk of liveBuffer) {
          writeLiveChunk(chunk.seq, chunk.bytes)
        }
        liveBuffer.length = 0
      }

      fit.fit()
      const dims = fit.proposeDimensions()
      if (dims && dims.cols > 0 && dims.rows > 0) {
        inst.lastCols = dims.cols
        inst.lastRows = dims.rows
        PtyResize(sessionId, dims.cols, dims.rows).catch(() => {})
      }

      // 拦截 xterm 内部 textarea 的 paste 事件（捕获阶段，先于 xterm 自身的处理器）。
      // 这样 Ctrl+V / 右键粘贴均只走一条路径，避免 xterm 内置 onData 与手动调用双重写入 PTY。
      const textarea = el.querySelector('textarea')
      if (textarea) {
        textarea.addEventListener('paste', (e: Event) => {
          e.preventDefault()
          e.stopImmediatePropagation()
          const clipEvent = e as ClipboardEvent
          const text = clipEvent.clipboardData?.getData('text') ?? ''
          if (text) {
            const bytes = new TextEncoder().encode(text)
            const encoded = uint8ToBase64(bytes)
            // 长文本使用分块写入避免截断
            if (bytes.length > 1024) {
              PtyWriteLarge(sessionId, encoded).catch(() => {})
            } else {
              PtyWrite(sessionId, encoded).catch(() => {})
            }
          } else {
            // 文本为空时检查是否有图片文件（如 Windows 截图工具）
            const files = clipEvent.clipboardData?.files
            if (files && files.length > 0) {
              const file = files[0]
              if (file.type.startsWith('image/')) {
                file.arrayBuffer().then(buf => {
                  const uint8 = new Uint8Array(buf)
                  const b64 = uint8ToBase64(uint8)
                  SaveClipboardImage(b64).then(filePath => {
                    if (filePath) {
                      const pathBytes = new TextEncoder().encode(filePath)
                      PtyWrite(sessionId, uint8ToBase64(pathBytes)).catch(() => {})
                    }
                  }).catch(() => {})
                }).catch(() => {})
              }
            }
          }
        }, true /* capture */)
      }
    }
  })
}

function destroyTerminal(sessionId: string) {
  const inst = terminals.get(sessionId)
  if (!inst) return

  inst.disposeDataListener?.()
  inst.disposeDataListener = null
  inst.disposeExitListener?.()
  inst.disposeExitListener = null
  inst.term.dispose()
  terminals.delete(sessionId)
  termRefs.delete(sessionId)
  const { [sessionId]: _removed, ...remainingDetailStates } = detailOutputStates.value
  detailOutputStates.value = remainingDetailStates
}

async function handleClose(sessionId: string, status: string) {
  if (status === 'running') {
    await StopSession(sessionId)
  }
  destroyTerminal(sessionId)
  await RemoveSession(sessionId)
  await refreshSessions()

  // 切到其他标签
  if (activeSessionId.value === sessionId) {
    const remaining = embeddedSessions.value
    activeSessionId.value = remaining.length > 0 ? remaining[0].id : ''
  }
  if (detailSessionId.value === sessionId) {
    detailSessionId.value = ''
  }
}

async function refreshSessions() {
  try {
    const newSessions = await GetSessions()
    // 只在 embedded 会话列表发生实质变化时更新，避免触发不必要的 watch 回调（
    // watch 回调会间接调用 fitTerminal，而 fit.fit() 可能导致滚动条瞬移到顶部）
    const oldEmbedded = sessions.value.filter(s => s.mode === 'embedded')
    const newEmbedded = newSessions.filter(s => s.mode === 'embedded')
    const hasChange =
      oldEmbedded.length !== newEmbedded.length ||
      oldEmbedded.some((s, i) => s.id !== newEmbedded[i]?.id || s.status !== newEmbedded[i]?.status)
    if (hasChange) {
      sessions.value = newSessions
      if (detailSessionId.value && !newEmbedded.some(s => s.id === detailSessionId.value)) {
        detailSessionId.value = ''
      }
    }
  } catch {}
}

// 监听 embedded 会话列表变化，自动创建/切换终端
watch(embeddedSessions, (newList, oldList) => {
  const oldIds = new Set((oldList || []).map(s => s.id))
  const newIds = new Set(newList.map(s => s.id))

  // 新增的会话：创建终端
  for (const sess of newList) {
    if (!oldIds.has(sess.id) && !terminals.has(sess.id)) {
      createTerminal(sess.id)
      activeSessionId.value = sess.id
      window.setTimeout(() => fitTerminal(sess.id), 200)
    }
  }

  // 移除的会话：清理终端
  for (const id of oldIds) {
    if (!newIds.has(id)) {
      destroyTerminal(id)
    }
  }
}, { deep: true })

// 容器尺寸变化时 fit（使用 ResizeObserver 替代 window resize，
// 仅在容器实际尺寸变化时触发，避免滚动时误触发 fitTerminal 导致重复内容）
let resizeDebounceTimer: number | null = null
function handleContainerResize() {
  if (resizeDebounceTimer) clearTimeout(resizeDebounceTimer)
  resizeDebounceTimer = window.setTimeout(() => {
    if (activeSessionId.value) {
      fitTerminal(activeSessionId.value)
    }
  }, 100)
}

function handleVisibilityChange() {
  if (document.visibilityState !== 'visible') return
  if (visibilityRefitTimer) clearTimeout(visibilityRefitTimer)
  visibilityRefitTimer = window.setTimeout(() => {
    if (activeSessionId.value) {
      fitTerminal(activeSessionId.value, true)
    }
  }, 100)
}

watch(terminalContentRef, (el) => {
  if (resizeObserver) {
    resizeObserver.disconnect()
    resizeObserver = null
  }

  if (el) {
    resizeObserver = new ResizeObserver(handleContainerResize)
    resizeObserver.observe(el)
    handleContainerResize()
  }
}, { immediate: true })

onMounted(async () => {
  // Ensure platform capabilities are loaded before any terminal creation.
  // Without this, isDarwin/isWindows return false when the singleton cache is
  // null (e.g. page opened directly or refreshed), causing the WebGL guard to
  // fail-open on macOS and the windowsPty hint to be omitted on Windows.
  await platformCaps.ensure()

  // 加载终端设置
  try {
    const ts = await GetTerminalSettings()
    if (ts.scrollback > 0) scrollbackLines.value = ts.scrollback
  } catch {}

  await refreshSessions()

  // 恢复已有 embedded 会话的终端
  for (const sess of embeddedSessions.value) {
    if (!terminals.has(sess.id)) {
      createTerminal(sess.id)
    }
  }
  if (embeddedSessions.value.length > 0 && !activeSessionId.value) {
    activeSessionId.value = embeddedSessions.value[0].id
  }

  refreshInterval = window.setInterval(refreshSessions, 2000)

  // 点击任意位置关闭右键菜单
  document.addEventListener('mousedown', hideContextMenu)
  document.addEventListener('visibilitychange', handleVisibilityChange)
  window.addEventListener('resize', handleContainerResize)
})

// keep-alive 激活时重新 fit 当前终端
onActivated(() => {
  nextTick(() => {
    // 延迟一帧确保容器已有正确尺寸
    requestAnimationFrame(() => {
      if (activeSessionId.value) {
        fitTerminal(activeSessionId.value, true)
      }
    })
  })
})

onUnmounted(() => {
  if (refreshInterval) {
    clearInterval(refreshInterval)
  }
  if (resizeDebounceTimer) {
    clearTimeout(resizeDebounceTimer)
    resizeDebounceTimer = null
  }
  if (visibilityRefitTimer) {
    clearTimeout(visibilityRefitTimer)
    visibilityRefitTimer = null
  }
  if (resizeObserver) {
    resizeObserver.disconnect()
    resizeObserver = null
  }
  document.removeEventListener('mousedown', hideContextMenu)
  document.removeEventListener('visibilitychange', handleVisibilityChange)
  window.removeEventListener('resize', handleContainerResize)
  // 不销毁终端实例——可能用户只是切换了页面
})
</script>

<style scoped>
.terminals-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
}

.terminal-tabs-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #1e2333;
  border-bottom: 1px solid #2a2f3e;
  padding: 0 8px;
  min-height: 38px;
  flex-shrink: 0;
}

.tabs-left {
  display: flex;
  overflow-x: auto;
  gap: 2px;
}

.tabs-left::-webkit-scrollbar {
  height: 2px;
}

.tabs-left::-webkit-scrollbar-thumb {
  background: #3a4a5e;
  border-radius: 1px;
}

.terminal-tab {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  cursor: pointer;
  color: #8899aa;
  font-size: 12px;
  border-radius: 4px 4px 0 0;
  white-space: nowrap;
  transition: all 0.15s;
  border-bottom: 2px solid transparent;
}

.terminal-tab:hover {
  background: #232838;
  color: #c0d0e0;
}

.terminal-tab.active {
  background: #1a1f2e;
  color: #e0e0e0;
  border-bottom-color: #4fc3f7;
}

.tab-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}

.tab-dot.dot-running { background: #4caf50; }
.tab-dot.dot-exited { background: #ff9800; }
.tab-dot.dot-stopped { background: #78909c; }
.tab-dot.dot-failed { background: #f44336; }

.tab-app-type {
  font-size: 10px;
  font-weight: 700;
  padding: 1px 4px;
  border-radius: 3px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  flex-shrink: 0;
}

.tab-app-type.app-claudecode {
  background: rgba(79, 195, 247, 0.15);
  color: #4fc3f7;
}

.tab-app-type.app-opencode {
  background: rgba(102, 187, 106, 0.15);
  color: #66bb6a;
}

.tab-app-type.app-codex {
  background: rgba(206, 147, 216, 0.15);
  color: #ce93d8;
}

.tab-label {
  max-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.tab-id {
  color: #556677;
  font-size: 11px;
}

.tab-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border-radius: 3px;
  font-size: 14px;
  line-height: 1;
  opacity: 0;
  transition: opacity 0.1s;
}

.terminal-tab:hover .tab-close {
  opacity: 0.6;
}

.tab-close:hover {
  opacity: 1 !important;
  background: rgba(244, 67, 54, 0.3);
  color: #ff5370;
}

.tab-detail {
  padding: 2px 6px;
  border: 1px solid #334155;
  border-radius: 999px;
  background: rgba(15, 23, 42, 0.72);
  color: #8aa0b8;
  font-size: 11px;
  cursor: pointer;
  transition: all 0.15s;
}

.tab-detail:hover,
.tab-detail.active {
  border-color: #4fc3f7;
  color: #4fc3f7;
  background: rgba(79, 195, 247, 0.1);
}

.tabs-right {
  flex-shrink: 0;
  padding: 0 8px;
}

.tab-count {
  color: #556677;
  font-size: 11px;
}

.terminal-content {
  flex: 1;
  position: relative;
  overflow: hidden;
  background: #1a1f2e;
}

.session-detail-panel {
  position: absolute;
  top: 16px;
  right: 16px;
  z-index: 5;
  width: min(420px, calc(100% - 32px));
  max-height: calc(100% - 32px);
  overflow: auto;
  padding: 18px;
  border: 1px solid rgba(79, 195, 247, 0.22);
  border-radius: 16px;
  background:
    radial-gradient(circle at top right, rgba(79, 195, 247, 0.12), transparent 38%),
    rgba(15, 18, 25, 0.96);
  box-shadow: 0 18px 50px rgba(0, 0, 0, 0.48);
  backdrop-filter: blur(12px);
}

.session-detail-panel--error {
  border-color: rgba(244, 67, 54, 0.28);
}

.detail-panel-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
  margin-bottom: 16px;
}

.detail-panel-header h2 {
  margin: 0 0 4px;
  color: #e0e0e0;
  font-size: 16px;
}

.detail-panel-header p {
  margin: 0;
  color: #6f8194;
  font-size: 12px;
  word-break: break-all;
}

.detail-close {
  padding: 5px 10px;
  border: 1px solid #334155;
  border-radius: 999px;
  background: rgba(15, 23, 42, 0.72);
  color: #8aa0b8;
  cursor: pointer;
}

.detail-close:hover {
  color: #e0e0e0;
  border-color: #4fc3f7;
}

.detail-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
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
  grid-column: 1 / -1;
}

.detail-item span {
  display: block;
  margin-bottom: 6px;
  color: #6f8194;
  font-size: 11px;
}

.detail-item strong {
  display: block;
  color: #d9e2ec;
  font-size: 13px;
  overflow: hidden;
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
  border: 1px solid rgba(58, 74, 94, 0.64);
  border-radius: 14px;
  background: rgba(10, 14, 22, 0.64);
  overflow: hidden;
}

.detail-tabs {
  display: flex;
  gap: 2px;
  padding: 6px;
  border-bottom: 1px solid rgba(58, 74, 94, 0.64);
  overflow-x: auto;
}

.detail-tab-button {
  flex: 1 0 auto;
  min-width: max-content;
  padding: 7px 9px;
  border: 1px solid transparent;
  border-radius: 9px;
  background: transparent;
  color: #7f93a8;
  font-size: 11px;
  font-weight: 700;
  cursor: pointer;
  transition: all 0.15s;
}

.detail-tab-button:hover {
  color: #c7d4e2;
  background: rgba(79, 195, 247, 0.07);
}

.detail-tab-button.active {
  color: #071018;
  border-color: rgba(137, 221, 255, 0.72);
  background: #89ddff;
  box-shadow: 0 0 18px rgba(79, 195, 247, 0.22);
}

.detail-tab-panel {
  padding: 12px;
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
  color: #9be69f;
  background: rgba(102, 187, 106, 0.08);
}

.detail-source-pill--empty {
  border-color: rgba(255, 203, 107, 0.36);
  color: #ffcb6b;
  background: rgba(255, 203, 107, 0.07);
}

.detail-source-pill--error {
  border-color: rgba(255, 83, 112, 0.42);
  color: #ff8aa0;
  background: rgba(255, 83, 112, 0.08);
}

.detail-empty-state {
  padding: 18px 14px;
  border: 1px dashed rgba(113, 134, 155, 0.42);
  border-radius: 12px;
  color: #9aabba;
  background: rgba(26, 31, 46, 0.52);
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
  max-height: 320px;
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
  max-height: 260px;
  overflow: auto;
}

.context-summary-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
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

.terminal-container {
  position: absolute;
  inset: 0;
  display: none;
  text-align: left;
}

.terminal-container.visible {
  display: flex;
  flex-direction: column;
}

/* xterm.js 容器撑满 */
.terminal-container :deep(.xterm) {
  height: 100%;
  width: 100%;
  text-align: left;
}

.terminal-container :deep(.xterm-screen) {
  width: 100% !important;
}

.terminal-container :deep(.xterm-viewport) {
  /* 不覆盖 xterm.js 默认的 overflow 设置，避免与内部虚拟滚动冲突导致重复内容 */
}

.terminal-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  color: #556677;
}

.empty-icon {
  font-size: 48px;
  margin-bottom: 16px;
  opacity: 0.3;
}

.empty-text {
  font-size: 16px;
  margin: 0 0 8px;
  color: #8899aa;
}

.empty-hint {
  font-size: 13px;
  margin: 0;
}

/* 右键菜单 */
.ctx-menu {
  position: fixed;
  z-index: 9999;
  background: #252a3a;
  border: 1px solid #3a4a5e;
  border-radius: 6px;
  padding: 4px 0;
  min-width: 180px;
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.45);
}

.ctx-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 14px;
  font-size: 13px;
  color: #d0d8e0;
  cursor: pointer;
  transition: background 0.1s;
}

.ctx-item:hover {
  background: #3a4a6a;
}

.ctx-shortcut {
  color: #667788;
  font-size: 11px;
  margin-left: 24px;
}

.ctx-sep {
  height: 1px;
  background: #3a4a5e;
  margin: 4px 8px;
}
</style>
