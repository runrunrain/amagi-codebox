<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { TerminalWebSocket, type ConnectionState } from '../api/websocket'
import { useMarkdownHtmlCache } from '../composables/useMarkdownHtmlCache'
import { useOutputBuffer } from '../composables/useOutputBuffer'
import { useTodoOverlay } from '../composables/useTodoOverlay'
import { fetchSessionMetadata, type SessionMetadata } from '../composables/useSessionMetadata'
import { buildTranscriptBlocks } from '../composables/useTerminalTranscript'
import { useConnection } from '../stores/connection'
import type { TerminalBlock, TerminalTextBlock } from '../types/terminal-blocks'
import { classifyDiffLine } from '../utils/classifyDiffLine'
import { highlightCode } from '../utils/highlightCode'
import { isPathLikeLine } from '../utils/isPathLikeLine'
import '@xterm/xterm/css/xterm.css'
import 'highlight.js/styles/github-dark.css'

const FONT_SIZE_KEY = 'terminal-font-size'
const FONT_SIZE_MIN = 9
const FONT_SIZE_MAX = 18
const FONT_SIZE_DEFAULT = 12

function loadFontSize(): number {
  const saved = localStorage.getItem(FONT_SIZE_KEY)
  if (saved) {
    const n = parseInt(saved, 10)
    if (!isNaN(n) && n >= FONT_SIZE_MIN && n <= FONT_SIZE_MAX) return n
  }
  return FONT_SIZE_DEFAULT
}

const route = useRoute()
const router = useRouter()
const { serverUrl, token, isConnected } = useConnection()

const wrapperRef = ref<HTMLDivElement>()
const terminalRef = ref<HTMLDivElement>()
const textViewRef = ref<HTMLDivElement>()
const mobileInputRef = ref<HTMLTextAreaElement>()
const wsState = ref<ConnectionState>('disconnected')
const sessionId = route.params.sessionId as string
const fontSize = ref(loadFontSize())
const mobileTextMode = ref(false)
const terminalTextLines = ref<string[]>([])
const terminalBlocks = ref<TerminalBlock[]>([])
const mobileInput = ref('')
const sessionMetadata = ref<SessionMetadata | null>(null)
const {
  markdownHtmlById,
  refreshMarkdownBlocks,
  resetMarkdownCache,
} = useMarkdownHtmlCache()
const todoOverlay = useTodoOverlay(terminalBlocks)

const sessionLabelTitle = computed(() => {
  if (!sessionMetadata.value) return sessionId
  const parts = [sessionMetadata.value.appType, sessionMetadata.value.provider]
  if (sessionMetadata.value.model) {
    parts.push(sessionMetadata.value.model)
  }
  return parts.join(' · ')
})

const sessionMetaChips = computed(() => {
  if (!sessionMetadata.value) return []

  const inferredModel = summarySubtitle.value?.match(/^(Opus|Sonnet|Haiku|gpt-[^\s]+|o\d[^\s]*|Claude\s+[^\s]+|Gemini[^\s]*)/i)?.[0]

  const chips = [
    formatAppTypeLabel(sessionMetadata.value.appType),
    sessionMetadata.value.provider,
  ]

  if (sessionMetadata.value.model) {
    chips.push(sessionMetadata.value.model)
  } else if (inferredModel) {
    chips.push(inferredModel)
  }

  if (summaryEffort.value) {
    chips.push(summaryEffort.value)
  }

  return chips.filter(Boolean)
})

function extractVersionFromText(text: string): string | undefined {
  return text.match(/\bv\d+(?:\.\d+){0,2}\b/i)?.[0]
}

function extractEffortFromText(text: string): string | undefined {
  return text.match(/\b(low|medium|high) effort\b/i)?.[0]
}

function stripDecorativePrefix(text: string): string {
  return text.replace(/^[^\p{L}\p{N}/\\]+/u, '').trim()
}

function normalizeTranscriptLine(line: string): string {
  return stripDecorativePrefix(line).replace(/^PS\s+[A-Za-z]:\\[^>]+>\s+/i, '').trim()
}

function extractReadableSubtitle(text: string): string | undefined {
  const line = text.split('\n').find((value: string) => /context|effort|billing/i.test(value))
  if (!line) return undefined

  const normalized = normalizeTranscriptLine(line)
  const match = normalized.match(/(Opus|Sonnet|Haiku|gpt-[^\s]+|o\d[^\s]*|Claude [^\s]+|Gemini[^\s]*).*/i)
  return match?.[0]?.trim() || normalized || undefined
}

function extractWorkDirFromText(text: string): string | undefined {
  const lines = text.split('\n').map((line) => normalizeTranscriptLine(line)).filter(Boolean)
  const candidates: string[] = []

  for (const line of lines) {
    const match = line.match(/[A-Za-z]:\\[^\n\r>]+|\/[A-Za-z0-9._\-/]+/)
    if (!match) continue

    const pathValue = match[0].replace(/>\s*.+$/, '').trim()
    if (pathValue) {
      candidates.push(pathValue)
    }
  }

  return candidates.sort((a, b) => a.length - b.length)[0]
}

function isTextualTerminalBlock(block: TerminalBlock): block is TerminalTextBlock {
  return block.type === 'text' || block.type === 'prompt' || block.type === 'action' || block.type === 'markdown' || block.type === 'thinking' || block.type === 'status' || block.type === 'streaming'
}

const transcriptHead = computed(() => terminalTextLines.value.slice(0, 8).join('\n'))
const summaryVersion = computed(() => extractVersionFromText(transcriptHead.value))
const summarySubtitle = computed(() => extractReadableSubtitle(transcriptHead.value))
const summaryWorkDir = computed(() => extractWorkDirFromText(transcriptHead.value))
const summaryEffort = computed(() => extractEffortFromText(transcriptHead.value))

const displayedTerminalBlocks = computed(() => {
  if (!sessionMetadata.value) return terminalBlocks.value

  let summaryStripped = false
  return terminalBlocks.value.filter((block) => {
    if (block.type === 'summary') {
      return false
    }
    if (
      !summaryStripped
      && block.type === 'text'
      && isTextualTerminalBlock(block)
      && (Boolean(extractVersionFromText(block.content)) || Boolean(extractWorkDirFromText(block.content)))
    ) {
      summaryStripped = true
      return false
    }
    return true
  })
})

function formatAppTypeLabel(appType: SessionMetadata['appType']) {
  switch (appType) {
    case 'claudecode':
      return 'Claude Code'
    case 'opencode':
      return 'OpenCode'
    case 'codex':
      return 'Codex'
    default:
      return 'Terminal'
  }
}

let terminal: Terminal | null = null
let fitAddon: FitAddon | null = null
let ws: TerminalWebSocket | null = null
let resizeObserver: ResizeObserver | null = null
let textSyncRaf: number | null = null

// resize 防抖定时器
let resizeDebounceTimer: ReturnType<typeof setTimeout> | null = null

// --- 触摸滚动支持 ---
let touchStartY = 0
let touchAccumY = 0
let isTouchScrolling = false
const TOUCH_SCROLL_THRESHOLD = 8 // px，超过此值判定为滚动而非点击
const TOUCH_SCROLL_LINE_HEIGHT = 20 // px，每滑动多少 px 滚动一行

function onTouchStart(e: TouchEvent) {
  if (!terminal || e.touches.length !== 1) return
  touchStartY = e.touches[0].clientY
  touchAccumY = 0
  isTouchScrolling = false
}

function onTouchMove(e: TouchEvent) {
  if (!terminal || e.touches.length !== 1) return
  const deltaY = touchStartY - e.touches[0].clientY
  touchStartY = e.touches[0].clientY

  if (!isTouchScrolling && Math.abs(deltaY) < TOUCH_SCROLL_THRESHOLD) return
  isTouchScrolling = true

  // 阻止浏览器默认滚动/弹性回弹
  e.preventDefault()

  touchAccumY += deltaY
  const lines = Math.trunc(touchAccumY / TOUCH_SCROLL_LINE_HEIGHT)
  if (lines !== 0) {
    terminal.scrollLines(lines)
    touchAccumY -= lines * TOUCH_SCROLL_LINE_HEIGHT
  }
}

function onTouchEnd() {
  isTouchScrolling = false
  touchAccumY = 0
}

// --- 移动端键盘适配 ---
const terminalPageRef = ref<HTMLDivElement>()
let isKeyboardOpen = false
let scaleRaf1: number | null = null
let scaleRaf2: number | null = null
let scaleRetryTimer: ReturnType<typeof setTimeout> | null = null
let lastNaturalWidth = 0
let lastNaturalHeight = 0

function cancelScaleFrames() {
  if (scaleRaf1 !== null) {
    cancelAnimationFrame(scaleRaf1)
    scaleRaf1 = null
  }
  if (scaleRaf2 !== null) {
    cancelAnimationFrame(scaleRaf2)
    scaleRaf2 = null
  }
  if (scaleRetryTimer) {
    clearTimeout(scaleRetryTimer)
    scaleRetryTimer = null
  }
}

function updatePresentationMode() {
  mobileTextMode.value = window.innerWidth <= 768
  if (terminal) {
    terminal.options.disableStdin = mobileTextMode.value
    if (mobileTextMode.value) {
      terminal.blur()
    }
  }
}

function cancelTextSync() {
  if (textSyncRaf !== null) {
    cancelAnimationFrame(textSyncRaf)
    textSyncRaf = null
  }
}

function extractTerminalTextLines(): string[] {
  if (!terminal) return []

  const buffer = terminal.buffer.normal
  const lines: string[] = []

  for (let i = 0; i < buffer.length; i++) {
    const line = buffer.getLine(i)
    if (!line) continue

    const text = line.translateToString(true)
    const isWrapped = Boolean((line as unknown as { isWrapped?: boolean }).isWrapped)

    if (isWrapped && lines.length > 0) {
      lines[lines.length - 1] += text
    } else {
      lines.push(text || ' ')
    }
  }

  return lines
}

function scrollTextViewToBottom() {
  if (!mobileTextMode.value || !textViewRef.value) return
  requestAnimationFrame(() => {
    if (textViewRef.value) {
      textViewRef.value.scrollTop = textViewRef.value.scrollHeight
    }
  })
}

function isTextViewNearBottom(): boolean {
  if (!textViewRef.value) return true
  const el = textViewRef.value
  return (el.scrollHeight - el.scrollTop - el.clientHeight) < 80
}

function focusMobileInput() {
  if (!mobileTextMode.value) return
  requestAnimationFrame(() => {
    mobileInputRef.value?.focus()
  })
}

function syncTextView() {
  if (!mobileTextMode.value) return
  const textView = textViewRef.value
  const wasAtBottom = isTextViewNearBottom()
  const distanceFromBottom = textView
    ? textView.scrollHeight - textView.scrollTop
    : 0

  terminalTextLines.value = extractTerminalTextLines()
  terminalBlocks.value = buildTranscriptBlocks(
    terminalTextLines.value,
    sessionMetadata.value?.appType ?? 'generic',
  )
  void refreshMarkdownBlocks(terminalBlocks.value)
  void nextTick(() => {
    if (!textViewRef.value) return
    if (wasAtBottom) {
      scrollTextViewToBottom()
      return
    }

    textViewRef.value.scrollTop = Math.max(
      0,
      textViewRef.value.scrollHeight - distanceFromBottom,
    )
  })
}

function getTerminalBlockText(block: TerminalBlock): string {
  if (block.type === 'summary') {
    return [block.title, block.subtitle, block.workDir].filter(Boolean).join('\n')
  }
  if (block.type === 'code') return block.code
  if (block.type === 'tool') return block.summary || block.title
  if (block.type === 'diff') return block.diff
  if (block.type === 'raw-terminal') return block.lines.join('\n')
  return block.content
}

function getPromptActionText(block: TerminalTextBlock): string | undefined {
  if (block.primaryAction) return block.primaryAction
  const normalized = block.content.replace(/^>\s*/u, '').trim()
  const quoted = normalized.match(/"([^"]+)"/)
  if (quoted?.[1]) return quoted[1]
  const trimmedTry = normalized.replace(/^try\s+/i, '').trim()
  return trimmedTry || undefined
}

function getToolSummaryLines(block: TerminalBlock): string[] {
  if (block.type !== 'tool' || !block.summary) return []
  return block.summary.split('\n').map((line) => line.trim()).filter(Boolean)
}

function shouldRenderToolSummaryAsFileList(block: TerminalBlock): boolean {
  const lines = getToolSummaryLines(block)
  return lines.length > 0 && lines.every(isPathLikeLine)
}

function scheduleTextSync() {
  if (!mobileTextMode.value) return
  cancelTextSync()
  textSyncRaf = requestAnimationFrame(() => {
    textSyncRaf = null
    syncTextView()
  })
}

function scheduleApplyScale() {
  cancelScaleFrames()
  scaleRaf1 = requestAnimationFrame(() => {
    scaleRaf2 = requestAnimationFrame(() => {
      scaleRaf1 = null
      scaleRaf2 = null
      applyScale(0)
    })
  })
}

function onViewportResize() {
  updatePresentationMode()
  if (!terminalPageRef.value) return
  const vv = window.visualViewport
  if (!vv) return

  const fullHeight = window.innerHeight
  const viewportHeight = vv.height

  // 判断键盘是否弹起：可视视口比窗口高度小 100px 以上
  isKeyboardOpen = (fullHeight - viewportHeight) > 100

  // 设置页面高度为可视视口高度，排除虚拟键盘占用的空间
  terminalPageRef.value.style.height = `${viewportHeight}px`
  // 确保页面顶部对齐（某些浏览器会自动滚动页面）
  terminalPageRef.value.style.top = `${vv.offsetTop}px`

  // 键盘状态变化时：重新计算布局
  if (isPtySynced) {
    if (mobileTextMode.value) {
      scheduleTextSync()
    } else {
      scheduleApplyScale()
    }
  } else if (!mobileTextMode.value && fitAddon && terminal) {
    try {
      fitAddon.fit()
    } catch {}
  }
  // 键盘弹起时滚动到底部，确保用户看到光标/输入位置
  if (isKeyboardOpen && terminal) {
    if (mobileTextMode.value) {
      scrollTextViewToBottom()
    } else {
      terminal.scrollToBottom()
    }
  }
}

function setupViewportListener() {
  const vv = window.visualViewport
  if (vv) {
    vv.addEventListener('resize', onViewportResize)
    vv.addEventListener('scroll', onViewportResize)
  }
}

function cleanupViewportListener() {
  const vv = window.visualViewport
  if (vv) {
    vv.removeEventListener('resize', onViewportResize)
    vv.removeEventListener('scroll', onViewportResize)
  }
  isKeyboardOpen = false
  // 恢复默认高度
  if (terminalPageRef.value) {
    terminalPageRef.value.style.height = ''
    terminalPageRef.value.style.top = ''
  }
}

// 将 xterm 适配到容器大小（仅本地显示，不改 PTY 尺寸）
// 移动端作为 observer 永远不 resize PTY
function fitLocal() {
  if (!fitAddon || !terminal) return
  try {
    fitAddon.fit()
  } catch {}
}

// PTY 真实尺寸（由桌面端控制）
let ptyCols = 0
let ptyRows = 0
let isPtySynced = false

// 收到服务端 dimensions 帧时：匹配 PTY 真实尺寸 + CSS 缩放适配屏幕
function syncToPtyDimensions(cols: number, rows: number) {
  if (!terminal || !terminalRef.value) return

  ptyCols = cols
  ptyRows = rows
  isPtySynced = true

  try {
    terminal.resize(ptyCols, ptyRows)
  } catch {}

  if (mobileTextMode.value) {
    scheduleTextSync()
  } else {
    scheduleApplyScale()
  }
}

function clearScaleStyles() {
  if (!terminalRef.value) return
  terminalRef.value.style.transform = ''
  terminalRef.value.style.transformOrigin = ''
  terminalRef.value.style.width = ''
  terminalRef.value.style.height = ''
}

function fallbackToFitLocal() {
  clearScaleStyles()
  isPtySynced = false
  ptyCols = 0
  ptyRows = 0
  fitLocal()
}

function getNaturalTerminalSize(): { width: number; height: number } | null {
  if (!terminalRef.value) return null
  const xtermRoot = terminalRef.value.querySelector('.xterm') as HTMLElement | null
  const xtermScreen = terminalRef.value.querySelector('.xterm-screen') as HTMLElement | null
  const xtermViewport = terminalRef.value.querySelector('.xterm-viewport') as HTMLElement | null
  if (!xtermRoot || !xtermScreen) return null

  const width = Math.ceil(Math.max(
    xtermRoot.scrollWidth,
    xtermRoot.offsetWidth,
    xtermScreen.scrollWidth,
    xtermScreen.offsetWidth,
  ))

  const height = Math.ceil(Math.max(
    xtermRoot.scrollHeight,
    xtermRoot.offsetHeight,
    xtermScreen.scrollHeight,
    xtermScreen.offsetHeight,
    xtermViewport?.offsetHeight ?? 0,
  ))

  if (width <= 0 || height <= 0) {
    return null
  }

  return { width, height }
}

// 计算并应用 CSS transform scale，让整个终端等比缩放到稳定容器宽度内
// 注意：wrapper 是 ResizeObserver 目标；terminal-container 是 transform 目标，二者分离避免布局反馈环
function applyScale(retryCount: number) {
  if (mobileTextMode.value) return
  if (!terminalRef.value || !wrapperRef.value) return
  const container = wrapperRef.value
  const containerWidth = container.clientWidth
  const containerHeight = container.clientHeight
  if (containerWidth <= 0 || containerHeight <= 0) return

  const dims = getNaturalTerminalSize()
  if (!dims) {
    if (retryCount < 8) {
      scaleRetryTimer = setTimeout(() => {
        scaleRetryTimer = null
        applyScale(retryCount + 1)
      }, 30)
      return
    }

    if (lastNaturalWidth > 0 && lastNaturalHeight > 0) {
      terminalRef.value.style.width = `${lastNaturalWidth}px`
      terminalRef.value.style.height = `${lastNaturalHeight}px`
      const cachedScale = Math.min(containerWidth / lastNaturalWidth, containerHeight / lastNaturalHeight)
      if (Number.isFinite(cachedScale) && cachedScale > 0) {
        terminalRef.value.style.transform = `scale(${cachedScale})`
        terminalRef.value.style.transformOrigin = 'top left'
        return
      }
    }

    fallbackToFitLocal()
    return
  }

  const naturalWidth = dims.width
  const naturalHeight = dims.height
  lastNaturalWidth = naturalWidth
  lastNaturalHeight = naturalHeight

  terminalRef.value.style.width = `${naturalWidth}px`
  terminalRef.value.style.height = `${naturalHeight}px`

  const scale = Math.min(containerWidth / naturalWidth, containerHeight / naturalHeight)
  if (!Number.isFinite(scale) || scale <= 0) {
    fallbackToFitLocal()
    return
  }

  terminalRef.value.style.transformOrigin = 'top left'
  if (Math.abs(scale - 1) < 0.01) {
    terminalRef.value.style.transform = ''
  } else {
    terminalRef.value.style.transform = `scale(${scale})`
  }
}

// 防抖版 fitLocal
function debouncedFitLocal() {
  if (resizeDebounceTimer) clearTimeout(resizeDebounceTimer)
  resizeDebounceTimer = setTimeout(() => {
    fitLocal()
  }, 100)
}

const {
  bufferOutput,
  flushNow: flushOutput,
  dispose: disposeOutputBuffer,
} = useOutputBuffer({
  onFlush: (merged) => {
    terminal?.write(merged, () => {
      scheduleTextSync()
    })
  },
})

function changeFontSize(delta: number) {
  const next = fontSize.value + delta
  if (next < FONT_SIZE_MIN || next > FONT_SIZE_MAX) return
  fontSize.value = next
  localStorage.setItem(FONT_SIZE_KEY, String(next))
  if (terminal) {
    terminal.options.fontSize = next
    if (mobileTextMode.value) {
      scheduleTextSync()
    } else if (isPtySynced && ptyCols > 0 && ptyRows > 0) {
      try {
        terminal.resize(ptyCols, ptyRows)
      } catch {}
      scheduleApplyScale()
    } else {
      fitLocal()
    }
  }
}

function initTerminal() {
  if (!terminalRef.value) return

  updatePresentationMode()

  terminal = new Terminal({
    theme: {
      background: '#000000',
      foreground: '#c9d1d9',
      cursor: '#58a6ff',
      cursorAccent: '#000000',
      selectionBackground: 'rgba(88, 166, 255, 0.3)',
      black: '#0d1117',
      red: '#f85149',
      green: '#3fb950',
      yellow: '#d29922',
      blue: '#58a6ff',
      magenta: '#d2a8ff',
      cyan: '#39c5cf',
      white: '#c9d1d9',
      brightBlack: '#484f58',
      brightRed: '#ff7b72',
      brightGreen: '#56d364',
      brightYellow: '#e3b341',
      brightBlue: '#79c0ff',
      brightMagenta: '#d2a8ff',
      brightCyan: '#56d4dd',
      brightWhite: '#f0f6fc',
    },
    fontSize: fontSize.value,
    fontFamily: '"Cascadia Code", "Fira Code", "JetBrains Mono", monospace',
    cursorBlink: true,
    allowProposedApi: true,
    scrollback: 50000,
    disableStdin: mobileTextMode.value,
  })

  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.loadAddon(new WebLinksAddon())

  terminal.open(terminalRef.value)

  nextTick(() => {
    if (!mobileTextMode.value) {
      fitLocal()
    }
    scheduleTextSync()
  })

  terminal.onData((data: string) => {
    if (mobileTextMode.value) return
    ws?.sendInput(data)
  })

  resizeObserver = new ResizeObserver(() => {
    if (mobileTextMode.value) {
      scheduleTextSync()
    } else if (isPtySynced) {
      scheduleApplyScale()
    } else {
      debouncedFitLocal()
    }
  })
  if (wrapperRef.value) {
    resizeObserver.observe(wrapperRef.value)
  }

  // 注册触摸滚动事件（在 xterm 的 viewport 上，passive: false 以允许 preventDefault）
  const xtermViewport = terminalRef.value.querySelector('.xterm-screen')
  if (xtermViewport) {
    xtermViewport.addEventListener('touchstart', onTouchStart as EventListener, { passive: true })
    xtermViewport.addEventListener('touchmove', onTouchMove as EventListener, { passive: false })
    xtermViewport.addEventListener('touchend', onTouchEnd as EventListener, { passive: true })
  }

  // 注册移动端键盘适配
  setupViewportListener()

  connectWebSocket()
}

function sendMobileInput() {
  if (!mobileTextMode.value || !ws) return
  if (wsState.value !== 'connected') {
    focusMobileInput()
    return
  }

  const value = mobileInput.value
  if (!value.trim()) return
  // 先发送文本内容（将换行符统一为 \r，匹配终端 Enter 行为）
  const textToSend = value.replace(/\r?\n/g, '\r')
  ws.sendInput(textToSend)
  // 延迟发送 \r（Enter），确保 TUI 应用先处理完文本再触发执行
  // 桌面端 xterm 的 onData 逐字符触发，Enter 只发送 \r；移动端需匹配此行为
  setTimeout(() => {
    ws?.sendInput('\r')
  }, 50)
  mobileInput.value = ''
  scrollTextViewToBottom()
  focusMobileInput()
}

function handleMobileInputEnter(event: KeyboardEvent) {
  if (event.isComposing || event.keyCode === 229) return
  if (event.shiftKey) return
  event.preventDefault()
  sendMobileInput()
}

function base64ToUint8(base64: string): Uint8Array {
  const bin = atob(base64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) {
    bytes[i] = bin.charCodeAt(i)
  }
  return bytes
}

function connectWebSocket() {
  ws = new TerminalWebSocket()

  ws.onStateChange((state) => {
    wsState.value = state
    if (state === 'connected' && terminal) {
      terminal.writeln('\x1b[32mConnected to session.\x1b[0m')
    } else if (state === 'disconnected') {
      terminal?.writeln('\x1b[33mDisconnected. Reconnecting...\x1b[0m')
    } else if (state === 'error') {
      terminal?.writeln('\x1b[31mConnection error.\x1b[0m')
    }
  })

  ws.onMessage((frame) => {
    if (frame.type === 'output' && frame.data) {
      try {
        const bytes = base64ToUint8(frame.data)
        bufferOutput(bytes)
      } catch {
        terminal?.write(frame.data)
      }
    } else if (frame.type === 'dimensions' && frame.cols && frame.rows) {
      // PTY 尺寸变化 — 重新同步并缩放
      syncToPtyDimensions(frame.cols, frame.rows)
    } else if (frame.type === 'exit') {
      terminal?.writeln(`\r\n\x1b[33mSession exited with code ${frame.exitCode}\x1b[0m`)
    }
  })

  ws.connect(serverUrl.value, sessionId, token.value, 'observer')
}

function sendSpecialKey(key: string) {
  if (!ws) return
  switch (key) {
    case 'Tab':
      ws.sendInput('\t')
      break
    case 'Ctrl+C':
      ws.sendInput('\x03')
      break
    case 'Ctrl+D':
      ws.sendInput('\x04')
      break
    case 'Up':
      ws.sendInput('\x1b[A')
      break
    case 'Down':
      ws.sendInput('\x1b[B')
      break
    case 'Left':
      ws.sendInput('\x1b[D')
      break
    case 'Right':
      ws.sendInput('\x1b[C')
      break
    case 'Enter':
      ws.sendInput('\r')
      break
    case 'Esc':
      ws.sendInput('\x1b')
      break
    case 'Ctrl+L':
      ws.sendInput('\x0c')
      break
    case 'Ctrl+Z':
      ws.sendInput('\x1a')
      break
  }
  if (mobileTextMode.value) {
    focusMobileInput()
  } else {
    terminal?.focus()
  }
}

function goBack() {
  ws?.disconnect()
  router.back()
}

async function loadSessionMetadata() {
  try {
    sessionMetadata.value = await fetchSessionMetadata(sessionId)
    if (mobileTextMode.value && terminalTextLines.value.length > 0) {
      terminalBlocks.value = buildTranscriptBlocks(
        terminalTextLines.value,
        sessionMetadata.value?.appType ?? 'generic',
      )
      void refreshMarkdownBlocks(terminalBlocks.value)
    }
  } catch {
    sessionMetadata.value = null
  }
}

onMounted(() => {
  if (!isConnected.value) {
    router.replace('/')
    return
  }
  void loadSessionMetadata()
  initTerminal()
})

onUnmounted(() => {
  ws?.disconnect()
  resizeObserver?.disconnect()
  resetMarkdownCache()
  cleanupViewportListener()
  cancelScaleFrames()
  cancelTextSync()
  // 清理触摸事件
  if (terminalRef.value) {
    const xtermViewport = terminalRef.value.querySelector('.xterm-screen')
    if (xtermViewport) {
      xtermViewport.removeEventListener('touchstart', onTouchStart as EventListener)
      xtermViewport.removeEventListener('touchmove', onTouchMove as EventListener)
      xtermViewport.removeEventListener('touchend', onTouchEnd as EventListener)
    }
  }
  if (resizeDebounceTimer) {
    clearTimeout(resizeDebounceTimer)
    resizeDebounceTimer = null
  }
  flushOutput()
  disposeOutputBuffer()
  terminal?.dispose()
})
</script>

<template>
  <div ref="terminalPageRef" class="terminal-page">
    <div class="terminal-header">
      <button class="back-btn" @click="goBack">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="15 18 9 12 15 6" />
        </svg>
      </button>
      <span class="session-label" :title="sessionLabelTitle">{{ sessionId }}</span>
      <div class="font-controls">
        <button
          class="font-btn"
          :disabled="fontSize <= FONT_SIZE_MIN"
          @click="changeFontSize(-1)"
        >A-</button>
        <span class="font-size-label">{{ fontSize }}</span>
        <button
          class="font-btn"
          :disabled="fontSize >= FONT_SIZE_MAX"
          @click="changeFontSize(1)"
        >A+</button>
      </div>
      <div class="ws-status" :class="`ws-status--${wsState}`">
        {{ wsState }}
      </div>
    </div>

    <div ref="wrapperRef" class="terminal-scroll-wrapper">
      <div
        v-if="mobileTextMode"
        ref="textViewRef"
        class="terminal-text-view"
        :style="{ fontSize: `${Math.max(fontSize + 4, 16)}px` }"
      >
        <div class="terminal-text-mode-header">
          <div class="terminal-text-mode-badge">文本模式</div>
          <div class="terminal-text-mode-caption">移动端优化阅读视图</div>
        </div>
        <div v-if="sessionMetadata" class="terminal-summary-card terminal-summary-card--hero">
          <div class="terminal-summary-topline">
            <div class="terminal-summary-title-row">
              <h2 class="terminal-summary-title">{{ formatAppTypeLabel(sessionMetadata.appType) }}</h2>
              <span v-if="summaryVersion" class="terminal-summary-version">{{ summaryVersion }}</span>
            </div>
            <span v-if="summaryEffort" class="terminal-summary-effort">{{ summaryEffort }}</span>
          </div>
          <p v-if="summarySubtitle" class="terminal-summary-subtitle">{{ summarySubtitle }}</p>
          <div v-if="summaryWorkDir" class="terminal-summary-workdir">{{ summaryWorkDir }}</div>
        </div>
        <div v-if="sessionMetaChips.length > 0" class="terminal-meta-chip-row">
          <button
            v-for="chip in sessionMetaChips"
            :key="chip"
            type="button"
            class="terminal-meta-chip terminal-meta-button"
          >{{ chip }}</button>
        </div>
        <div
          v-for="block in displayedTerminalBlocks"
          :key="block.id"
          class="terminal-text-block"
          :class="`terminal-text-block--${block.type}`"
        >
          <template v-if="block.type === 'summary'">
            <div class="terminal-summary-card">
              <div class="terminal-summary-topline">
                <div class="terminal-summary-title-row">
                  <h2 class="terminal-summary-title">{{ block.title }}</h2>
                  <span v-if="block.version" class="terminal-summary-version">{{ block.version }}</span>
                </div>
                <span v-if="block.effort" class="terminal-summary-effort">{{ block.effort }}</span>
              </div>
              <p v-if="block.subtitle" class="terminal-summary-subtitle">{{ block.subtitle }}</p>
              <div v-if="block.workDir" class="terminal-summary-workdir">{{ block.workDir }}</div>
            </div>
          </template>
          <template v-else-if="block.type === 'tool'">
            <div class="terminal-tool-card">
              <div class="terminal-tool-head">
                <span class="terminal-tool-name">{{ block.toolName }}</span>
                <button v-if="block.shortcutHint" type="button" class="terminal-tool-shortcut">{{ block.shortcutHint }}</button>
              </div>
              <div class="terminal-tool-title">{{ block.title }}</div>
              <ul v-if="shouldRenderToolSummaryAsFileList(block)" class="terminal-tool-file-list">
                <li v-for="line in getToolSummaryLines(block)" :key="line" class="terminal-tool-file-item">{{ line }}</li>
              </ul>
              <div v-else-if="block.summary" class="terminal-tool-summary">{{ block.summary }}</div>
            </div>
          </template>
          <template v-else-if="block.type === 'action'">
            <div class="terminal-action-card">
              <div class="terminal-action-content">{{ block.content }}</div>
              <button v-if="block.shortcutHint" type="button" class="terminal-tool-shortcut">{{ block.shortcutHint }}</button>
            </div>
          </template>
          <template v-else-if="block.type === 'prompt'">
            <div class="terminal-prompt-card">
              <div class="terminal-text-line">{{ block.content }}</div>
              <button v-if="getPromptActionText(block)" type="button" class="terminal-prompt-button">{{ getPromptActionText(block) }}</button>
            </div>
          </template>
          <template v-else-if="block.type === 'code'">
            <div class="terminal-code-meta">
              <span v-if="block.language" class="terminal-code-chip">{{ block.language }}</span>
              <span v-if="block.filename" class="terminal-code-file">{{ block.filename }}</span>
            </div>
            <pre class="terminal-code-block hljs" v-html="highlightCode(block.code, block.language)"></pre>
          </template>
          <template v-else-if="block.type === 'diff'">
            <div class="terminal-diff-card">
              <div class="terminal-diff-head">
                <span class="terminal-diff-file">{{ block.filename }}</span>
                <div class="terminal-diff-stats">
                  <span class="terminal-diff-additions">+{{ block.additions }}</span>
                  <span class="terminal-diff-deletions">-{{ block.deletions }}</span>
                </div>
              </div>
              <pre class="terminal-diff-block"><div
                v-for="(line, index) in block.diff.split('\n')"
                :key="`${block.id}-${index}`"
                class="terminal-diff-line"
                :class="`terminal-diff-line--${classifyDiffLine(line)}`"
              >{{ line || ' ' }}</div></pre>
            </div>
          </template>
          <template v-else-if="block.type === 'todo'">
            <div class="terminal-todo-card">
              <div class="terminal-todo-header">
                <span class="terminal-todo-label">TODO</span>
                <span class="terminal-todo-count">
                  {{ block.items.filter(i => i.completed).length }}/{{ block.items.length }}
                </span>
              </div>
              <ul class="terminal-todo-list">
                <li
                  v-for="(item, idx) in block.items"
                  :key="idx"
                  class="terminal-todo-item"
                  :class="{ 'terminal-todo-item--done': item.completed }"
                >
                  <span class="terminal-todo-check">{{ item.completed ? '[x]' : '[ ]' }}</span>
                  <span class="terminal-todo-text">{{ item.text }}</span>
                </li>
              </ul>
            </div>
          </template>
          <template v-else-if="block.type === 'table'">
            <div class="terminal-table-card">
              <div class="terminal-table-meta">
                <span class="terminal-table-label">TABLE</span>
                <span class="terminal-table-dims">
                  {{ block.headers.length }} cols / {{ block.rows.length }} rows
                </span>
              </div>
              <div class="terminal-table-scroll">
                <div
                  v-if="markdownHtmlById[block.id]"
                  class="terminal-table-rendered"
                  v-html="markdownHtmlById[block.id]"
                >
                </div>
                <pre v-else class="terminal-text-line">{{ block.content }}</pre>
              </div>
            </div>
          </template>
          <template v-else-if="block.type === 'markdown'">
            <div class="terminal-markdown-card">
              <div v-if="markdownHtmlById[block.id]" class="terminal-markdown-block" v-html="markdownHtmlById[block.id]"></div>
              <div v-else class="terminal-text-line">{{ block.content }}</div>
            </div>
          </template>
          <template v-else>
            <div class="terminal-text-line">{{ getTerminalBlockText(block) }}</div>
          </template>
        </div>
        <div
          v-if="todoOverlay.hasItems.value"
          class="terminal-todo-overlay"
          :class="{ 'terminal-todo-overlay--expanded': todoOverlay.expanded.value }"
          @click="todoOverlay.toggle()"
        >
          <div class="terminal-todo-overlay-compact">
            <span class="terminal-todo-overlay-label">TODO</span>
            <span class="terminal-todo-overlay-progress">
              {{ todoOverlay.completedCount.value }}/{{ todoOverlay.totalCount.value }}
            </span>
            <svg class="terminal-todo-overlay-chevron" :class="{ 'terminal-todo-overlay-chevron--up': todoOverlay.expanded.value }" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="6 9 12 15 18 9" />
            </svg>
          </div>
          <ul v-if="todoOverlay.expanded.value" class="terminal-todo-overlay-list">
            <li
              v-for="(item, idx) in todoOverlay.items.value"
              :key="idx"
              class="terminal-todo-overlay-item"
              :class="{ 'terminal-todo-overlay-item--done': item.completed }"
            >
              <span class="terminal-todo-overlay-check">{{ item.completed ? '[x]' : '[ ]' }}</span>
              <span>{{ item.text }}</span>
            </li>
          </ul>
        </div>
      </div>
      <div
        ref="terminalRef"
        class="terminal-container"
        :class="{ 'terminal-container--hidden': mobileTextMode }"
      ></div>
    </div>

    <div v-if="mobileTextMode" class="mobile-input-bar">
      <textarea
        ref="mobileInputRef"
        v-model="mobileInput"
        class="mobile-input"
        rows="2"
        placeholder="输入命令或文本，回车发送"
        @keydown.enter="handleMobileInputEnter"
      ></textarea>
      <button
        type="button"
        class="mobile-send-btn"
        @click="sendMobileInput"
      >发送</button>
    </div>

    <div class="shortcut-bar">
      <button class="shortcut-btn" @click="sendSpecialKey('Tab')">Tab</button>
      <button class="shortcut-btn" @click="sendSpecialKey('Ctrl+C')">^C</button>
      <button class="shortcut-btn" @click="sendSpecialKey('Ctrl+D')">^D</button>
      <button class="shortcut-btn" @click="sendSpecialKey('Ctrl+L')">^L</button>
      <button class="shortcut-btn" @click="sendSpecialKey('Ctrl+Z')">^Z</button>
      <button class="shortcut-btn" @click="sendSpecialKey('Esc')">Esc</button>
      <button class="shortcut-btn arrow-btn" @click="sendSpecialKey('Up')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="18 15 12 9 6 15" />
        </svg>
      </button>
      <button class="shortcut-btn arrow-btn" @click="sendSpecialKey('Down')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="6 9 12 15 18 9" />
        </svg>
      </button>
      <button class="shortcut-btn arrow-btn" @click="sendSpecialKey('Left')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="15 18 9 12 15 6" />
        </svg>
      </button>
      <button class="shortcut-btn arrow-btn" @click="sendSpecialKey('Right')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="9 18 15 12 9 6" />
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped>
.terminal-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  height: 100dvh;
  width: 100%;
  position: fixed;
  top: 0;
  left: 0;
  background: #000;
  /* 键盘弹起时通过 JS 动态设置 height 和 top */
  overflow: hidden;
}

.terminal-header {
  display: flex;
  align-items: center;
  height: 44px;
  padding: 0 8px;
  background: #161b22;
  border-bottom: 1px solid #30363d;
  flex-shrink: 0;
  gap: 6px;
}

.back-btn {
  background: none;
  border: none;
  color: #c9d1d9;
  cursor: pointer;
  padding: 6px;
  border-radius: 4px;
  display: flex;
  align-items: center;
  min-width: 32px;
  min-height: 32px;
  justify-content: center;
}

.back-btn:active {
  background: #30363d;
}

.session-label {
  flex: 1;
  font-size: 12px;
  color: #8b949e;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.font-controls {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.font-btn {
  background: #21262d;
  border: 1px solid #30363d;
  border-radius: 4px;
  color: #c9d1d9;
  font-size: 12px;
  font-family: monospace;
  font-weight: 600;
  cursor: pointer;
  min-width: 32px;
  min-height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0 4px;
}

.font-btn:active:not(:disabled) {
  background: #30363d;
  border-color: #58a6ff;
}

.font-btn:disabled {
  opacity: 0.35;
  cursor: default;
}

.font-size-label {
  font-size: 11px;
  color: #8b949e;
  font-family: monospace;
  min-width: 20px;
  text-align: center;
}

.ws-status {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 10px;
  font-weight: 500;
  flex-shrink: 0;
}

.ws-status--connected {
  background: rgba(63, 185, 80, 0.15);
  color: #3fb950;
}

.ws-status--connecting {
  background: rgba(210, 153, 34, 0.15);
  color: #d29922;
}

.ws-status--disconnected {
  background: rgba(139, 148, 158, 0.15);
  color: #8b949e;
}

.ws-status--error {
  background: rgba(248, 81, 73, 0.15);
  color: #f85149;
}

.terminal-scroll-wrapper {
  flex: 1;
  position: relative;
  overflow: hidden;
  min-height: 0;
  padding: 8px;
  box-sizing: border-box;
  background:
    radial-gradient(circle at top left, rgba(88, 166, 255, 0.08), transparent 34%),
    linear-gradient(180deg, rgba(13, 17, 23, 0.96), rgba(0, 0, 0, 1));
}

.terminal-container {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  transform-origin: top left;
  will-change: transform;
}

.terminal-container--hidden {
  position: absolute;
  width: 1px !important;
  height: 1px !important;
  opacity: 0;
  pointer-events: none;
  overflow: hidden;
}

.terminal-text-view {
  position: relative;
  height: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 14px 14px 18px;
  border-radius: 16px;
  border: 1px solid rgba(88, 166, 255, 0.16);
  background:
    linear-gradient(180deg, rgba(22, 27, 34, 0.96), rgba(13, 17, 23, 0.98));
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.04),
    0 10px 30px rgba(0, 0, 0, 0.28);
  color: #e6edf3;
  line-height: 1.58;
  letter-spacing: 0.01em;
  -webkit-overflow-scrolling: touch;
}

.terminal-text-mode-header {
  position: sticky;
  top: -14px;
  z-index: 1;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 0 0 10px;
  margin: 0 0 12px;
  background: linear-gradient(180deg, rgba(22, 27, 34, 0.98), rgba(22, 27, 34, 0.82), transparent);
  backdrop-filter: blur(6px);
}

.terminal-text-mode-badge {
  display: inline-flex;
  align-items: center;
  padding: 4px 10px;
  border-radius: 999px;
  background: rgba(88, 166, 255, 0.12);
  border: 1px solid rgba(88, 166, 255, 0.24);
  color: #79c0ff;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.02em;
}

.terminal-text-mode-caption {
  color: #8b949e;
  font-size: 12px;
}

.terminal-text-line {
  white-space: pre-wrap;
  word-break: break-word;
  font-family: "SFMono-Regular", "Cascadia Code", "JetBrains Mono", monospace;
  margin: 0;
  color: #f0f6fc;
}

.terminal-text-line + .terminal-text-line {
  margin-top: 2px;
}

.terminal-text-block + .terminal-text-block {
  margin-top: 10px;
}

.terminal-text-block--status {
  padding: 10px 12px;
  border-radius: 12px;
  border: 1px solid rgba(88, 166, 255, 0.18);
  background: rgba(88, 166, 255, 0.08);
}

.terminal-text-block--summary {
  padding: 16px;
  border-radius: 18px;
  border: 1px solid rgba(88, 166, 255, 0.22);
  background:
    radial-gradient(circle at top right, rgba(88, 166, 255, 0.16), transparent 30%),
    linear-gradient(180deg, rgba(22, 27, 34, 0.98), rgba(13, 17, 23, 0.98));
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.03),
    0 12px 28px rgba(0, 0, 0, 0.28);
}

.terminal-text-block--prompt {
  padding: 12px 14px;
  border-radius: 14px;
  border: 1px solid rgba(210, 153, 34, 0.26);
  background: linear-gradient(180deg, rgba(210, 153, 34, 0.14), rgba(210, 153, 34, 0.06));
}

.terminal-prompt-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.terminal-prompt-button {
  align-self: flex-start;
  padding: 8px 12px;
  border-radius: 12px;
  border: 1px solid rgba(210, 153, 34, 0.3);
  background: rgba(210, 153, 34, 0.16);
  color: #ffd866;
  font-size: 13px;
  font-weight: 600;
  cursor: default;
}

.terminal-text-block--action {
  padding: 12px 14px;
  border-radius: 14px;
  border: 1px solid rgba(88, 166, 255, 0.24);
  background: linear-gradient(180deg, rgba(88, 166, 255, 0.16), rgba(88, 166, 255, 0.06));
}

.terminal-action-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.terminal-action-content {
  color: #f0f6fc;
  font-weight: 600;
  line-height: 1.45;
}

.terminal-text-block--code {
  padding: 12px;
  border-radius: 14px;
  border: 1px solid rgba(99, 110, 123, 0.24);
  background: rgba(1, 4, 9, 0.92);
}

.terminal-code-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
  font-size: 12px;
}

.terminal-text-block--tool {
  padding: 12px 14px;
  border-radius: 14px;
  border: 1px solid rgba(110, 118, 129, 0.22);
  background: rgba(22, 27, 34, 0.9);
}

.terminal-text-block--diff {
  padding: 12px 14px;
  border-radius: 14px;
  border: 1px solid rgba(88, 166, 255, 0.18);
  background: rgba(255, 255, 255, 0.02);
}

.terminal-diff-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.terminal-diff-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.terminal-diff-file {
  color: #f0f6fc;
  font-weight: 600;
  word-break: break-word;
}

.terminal-diff-stats {
  display: flex;
  gap: 8px;
  font-size: 12px;
  font-weight: 700;
}

.terminal-diff-additions {
  color: #56d364;
}

.terminal-diff-deletions {
  color: #ff7b72;
}

.terminal-diff-block {
  margin: 0;
  padding: 12px;
  border-radius: 12px;
  background: rgba(1, 4, 9, 0.88);
  overflow-x: auto;
  font-family: "SFMono-Regular", "Cascadia Code", "JetBrains Mono", monospace;
  font-size: 0.92em;
  line-height: 1.5;
  color: #e6edf3;
}

.terminal-diff-line {
  white-space: pre-wrap;
  word-break: break-word;
  padding: 1px 6px;
  border-radius: 6px;
}

.terminal-diff-line--add {
  background: rgba(46, 160, 67, 0.14);
  color: #7ee787;
}

.terminal-diff-line--delete {
  background: rgba(248, 81, 73, 0.14);
  color: #ffa198;
}

.terminal-diff-line--hunk {
  background: rgba(88, 166, 255, 0.14);
  color: #79c0ff;
}

.terminal-diff-line--file {
  color: #d2a8ff;
}

.terminal-text-block--markdown {
  padding: 12px 14px;
  border-radius: 14px;
  border: 1px solid rgba(88, 166, 255, 0.18);
  background: rgba(255, 255, 255, 0.02);
}

.terminal-markdown-card {
  color: #e6edf3;
}

.terminal-markdown-block {
  line-height: 1.6;
}

.terminal-markdown-block :deep(h1),
.terminal-markdown-block :deep(h2),
.terminal-markdown-block :deep(h3),
.terminal-markdown-block :deep(h4) {
  margin: 0 0 10px;
  color: #f0f6fc;
}

.terminal-markdown-block :deep(p),
.terminal-markdown-block :deep(ul),
.terminal-markdown-block :deep(ol),
.terminal-markdown-block :deep(blockquote) {
  margin: 0 0 10px;
}

.terminal-markdown-block :deep(ul),
.terminal-markdown-block :deep(ol) {
  padding-left: 18px;
}

.terminal-markdown-block :deep(code) {
  font-family: "SFMono-Regular", "Cascadia Code", "JetBrains Mono", monospace;
  background: rgba(110, 118, 129, 0.16);
  padding: 1px 5px;
  border-radius: 6px;
}

.terminal-markdown-block :deep(pre) {
  margin: 0 0 10px;
  padding: 12px;
  overflow-x: auto;
  border-radius: 12px;
  background: rgba(1, 4, 9, 0.88);
}

.terminal-markdown-block :deep(pre code) {
  background: transparent;
  padding: 0;
}

.terminal-markdown-block :deep(blockquote) {
  padding-left: 12px;
  border-left: 3px solid rgba(88, 166, 255, 0.4);
  color: #8b949e;
}

.terminal-tool-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.terminal-tool-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.terminal-tool-name {
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.03em;
  color: #79c0ff;
  text-transform: uppercase;
}

.terminal-tool-shortcut {
  font-size: 12px;
  color: #d2a8ff;
  padding: 3px 8px;
  border-radius: 999px;
  background: rgba(210, 168, 255, 0.12);
  border: 1px solid rgba(210, 168, 255, 0.2);
  cursor: default;
}

.terminal-tool-title {
  color: #f0f6fc;
  font-weight: 600;
  line-height: 1.45;
}

.terminal-tool-summary {
  color: #8b949e;
  line-height: 1.45;
  white-space: pre-wrap;
  word-break: break-word;
}

.terminal-tool-file-list {
  margin: 0;
  padding-left: 18px;
  color: #8b949e;
}

.terminal-tool-file-item {
  line-height: 1.5;
  word-break: break-word;
}

.terminal-summary-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.terminal-summary-card--hero {
  margin-bottom: 12px;
}

.terminal-summary-topline {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.terminal-summary-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.terminal-summary-title {
  margin: 0;
  font-size: 1.08em;
  line-height: 1.2;
  color: #f0f6fc;
}

.terminal-summary-version {
  font-size: 12px;
  color: #8b949e;
  padding: 2px 8px;
  border-radius: 999px;
  background: rgba(139, 148, 158, 0.12);
  border: 1px solid rgba(139, 148, 158, 0.2);
}

.terminal-summary-effort {
  font-size: 12px;
  font-weight: 600;
  color: #ffd866;
  padding: 4px 10px;
  border-radius: 999px;
  background: rgba(210, 153, 34, 0.14);
  border: 1px solid rgba(210, 153, 34, 0.28);
}

.terminal-summary-subtitle {
  margin: 0;
  color: #c9d1d9;
  line-height: 1.5;
}

.terminal-summary-workdir {
  display: inline-flex;
  align-self: flex-start;
  padding: 6px 10px;
  border-radius: 12px;
  background: rgba(110, 118, 129, 0.12);
  border: 1px solid rgba(110, 118, 129, 0.22);
  color: #8b949e;
  font-family: "SFMono-Regular", "Cascadia Code", "JetBrains Mono", monospace;
  font-size: 12px;
  word-break: break-all;
}

.terminal-meta-chip-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0 0 12px;
}

.terminal-meta-chip {
  display: inline-flex;
  align-items: center;
  padding: 4px 10px;
  border-radius: 999px;
  background: rgba(139, 148, 158, 0.12);
  border: 1px solid rgba(139, 148, 158, 0.2);
  color: #c9d1d9;
  font-size: 12px;
  font-weight: 500;
}

.terminal-meta-button {
  cursor: default;
}

.terminal-code-chip {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 999px;
  background: rgba(46, 160, 67, 0.14);
  border: 1px solid rgba(46, 160, 67, 0.28);
  color: #7ee787;
}

.terminal-code-file {
  color: #8b949e;
}

.terminal-code-block {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: "SFMono-Regular", "Cascadia Code", "JetBrains Mono", monospace;
  font-size: 0.95em;
  line-height: 1.55;
  color: #e6edf3;
}

/* --- TODO Block Card --- */
.terminal-text-block--todo {
  padding: 12px 14px;
  border-radius: 14px;
  border: 1px solid rgba(63, 185, 80, 0.24);
  background: rgba(63, 185, 80, 0.08);
}

.terminal-todo-card {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.terminal-todo-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.terminal-todo-label {
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.03em;
  color: #3fb950;
  text-transform: uppercase;
}

.terminal-todo-count {
  font-size: 12px;
  color: #8b949e;
  padding: 2px 8px;
  border-radius: 999px;
  background: rgba(63, 185, 80, 0.12);
}

.terminal-todo-list {
  list-style: none;
  padding: 0;
  margin: 0;
}

.terminal-todo-item {
  display: flex;
  gap: 8px;
  padding: 4px 0;
  font-size: 13px;
  color: #e6edf3;
  line-height: 1.5;
}

.terminal-todo-item--done {
  color: #8b949e;
  text-decoration: line-through;
}

.terminal-todo-check {
  font-family: "SFMono-Regular", "Cascadia Code", "JetBrains Mono", monospace;
  color: #3fb950;
  flex-shrink: 0;
}

.terminal-todo-item--done .terminal-todo-check {
  color: #8b949e;
}

/* --- TODO Floating Overlay --- */
.terminal-todo-overlay {
  position: sticky;
  bottom: 0;
  z-index: 10;
  margin: 8px -4px 0;
  padding: 10px 14px;
  border-radius: 14px 14px 0 0;
  border: 1px solid rgba(63, 185, 80, 0.3);
  border-bottom: none;
  background: rgba(13, 17, 23, 0.96);
  backdrop-filter: blur(12px);
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
}

.terminal-todo-overlay-compact {
  display: flex;
  align-items: center;
  gap: 8px;
}

.terminal-todo-overlay-label {
  font-size: 11px;
  font-weight: 700;
  color: #3fb950;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.terminal-todo-overlay-progress {
  font-size: 12px;
  color: #e6edf3;
  font-weight: 600;
}

.terminal-todo-overlay-chevron {
  margin-left: auto;
  color: #8b949e;
  transition: transform 0.2s;
}

.terminal-todo-overlay-chevron--up {
  transform: rotate(180deg);
}

.terminal-todo-overlay-list {
  list-style: none;
  padding: 0;
  margin: 8px 0 0;
  max-height: 40vh;
  overflow-y: auto;
}

.terminal-todo-overlay-item {
  display: flex;
  gap: 8px;
  padding: 4px 0;
  font-size: 13px;
  color: #e6edf3;
}

.terminal-todo-overlay-item--done {
  color: #8b949e;
  text-decoration: line-through;
}

.terminal-todo-overlay-check {
  font-family: "SFMono-Regular", "Cascadia Code", "JetBrains Mono", monospace;
  color: #3fb950;
  flex-shrink: 0;
}

.terminal-todo-overlay-item--done .terminal-todo-overlay-check {
  color: #8b949e;
}

/* --- Table Block Card --- */
.terminal-text-block--table {
  padding: 12px 14px;
  border-radius: 14px;
  border: 1px solid rgba(210, 168, 255, 0.22);
  background: rgba(22, 27, 34, 0.9);
  overflow: hidden;
}

.terminal-table-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.terminal-table-meta {
  display: flex;
  align-items: center;
  gap: 8px;
}

.terminal-table-label {
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.03em;
  color: #d2a8ff;
  text-transform: uppercase;
}

.terminal-table-dims {
  font-size: 12px;
  color: #8b949e;
}

.terminal-table-scroll {
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
  scrollbar-width: thin;
}

.terminal-table-rendered :deep(table) {
  border-collapse: collapse;
  width: 100%;
  font-size: 13px;
}

.terminal-table-rendered :deep(th),
.terminal-table-rendered :deep(td) {
  padding: 6px 10px;
  border: 1px solid rgba(110, 118, 129, 0.3);
  white-space: normal;
  word-break: break-word;
  text-align: left;
}

.terminal-table-rendered :deep(th) {
  font-weight: 700;
  color: #f0f6fc;
  background: rgba(110, 118, 129, 0.14);
}

.terminal-table-rendered :deep(th:nth-child(1)) { color: #79c0ff; }
.terminal-table-rendered :deep(th:nth-child(2)) { color: #d2a8ff; }
.terminal-table-rendered :deep(th:nth-child(3)) { color: #3fb950; }
.terminal-table-rendered :deep(th:nth-child(4)) { color: #ffd866; }
.terminal-table-rendered :deep(th:nth-child(5)) { color: #f97583; }
.terminal-table-rendered :deep(th:nth-child(n+6)) { color: #79c0ff; }

.terminal-table-rendered :deep(td) {
  color: #e6edf3;
}

.terminal-table-rendered :deep(tr:nth-child(even) td) {
  background: rgba(255, 255, 255, 0.02);
}

.mobile-input-bar {
  display: flex;
  align-items: flex-end;
  gap: 10px;
  padding: 10px 10px 8px;
  background: linear-gradient(180deg, rgba(13, 17, 23, 0.92), rgba(22, 27, 34, 0.98));
  border-top: 1px solid rgba(88, 166, 255, 0.12);
}

.mobile-input {
  flex: 1;
  min-height: 52px;
  max-height: 120px;
  resize: none;
  padding: 12px 14px;
  border-radius: 14px;
  border: 1px solid rgba(88, 166, 255, 0.18);
  background: rgba(13, 17, 23, 0.95);
  color: #f0f6fc;
  font-size: 15px;
  line-height: 1.45;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.03);
}

.mobile-input:focus {
  outline: none;
  border-color: rgba(88, 166, 255, 0.5);
  box-shadow: 0 0 0 3px rgba(88, 166, 255, 0.16);
}

.mobile-send-btn {
  flex-shrink: 0;
  min-width: 68px;
  min-height: 52px;
  padding: 0 16px;
  border: 1px solid rgba(88, 166, 255, 0.28);
  border-radius: 14px;
  background: linear-gradient(180deg, rgba(56, 139, 253, 0.95), rgba(31, 111, 235, 0.95));
  color: white;
  font-size: 14px;
  font-weight: 600;
  letter-spacing: 0.02em;
}

.mobile-send-btn:active {
  transform: translateY(1px);
}

.terminal-container :deep(.xterm-viewport) {
  overflow-y: auto !important;
  -webkit-overflow-scrolling: auto;
  overscroll-behavior: none;
}

.terminal-container :deep(.xterm-screen) {
  touch-action: none;
}

.shortcut-bar {
  display: flex;
  gap: 3px;
  padding: 6px 8px;
  background: #161b22;
  border-top: 1px solid #30363d;
  overflow-x: auto;
  flex-shrink: 0;
  padding-bottom: calc(4px + env(safe-area-inset-bottom, 0));
  -webkit-overflow-scrolling: touch;
}

.shortcut-btn {
  padding: 0;
  background: #21262d;
  border: 1px solid #30363d;
  border-radius: 4px;
  color: #c9d1d9;
  font-size: 12px;
  font-family: monospace;
  cursor: pointer;
  white-space: nowrap;
  min-width: 44px;
  min-height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.shortcut-btn:active {
  background: #30363d;
  border-color: #58a6ff;
}

.arrow-btn {
  min-width: 38px;
}

@media (max-width: 768px) {
  .terminal-header {
    height: 48px;
    padding: 0 10px;
  }

  .session-label {
    font-size: 13px;
  }

  .font-btn {
    min-width: 34px;
    min-height: 30px;
  }

  .shortcut-btn {
    min-width: 46px;
    min-height: 38px;
  }
}
</style>
