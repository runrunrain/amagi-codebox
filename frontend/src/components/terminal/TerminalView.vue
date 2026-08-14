<template>
  <div class="view-terminal">
    <!-- 会话工具栏 -->
    <div class="term-toolbar">
      <div class="term-tb-left">
        <span class="sess-dot" :style="{ background: statusColor }" />
        <span class="term-title" :title="sessionTitle">{{ sessionTitle }}</span>
        <span class="term-sep">/</span>
        <span class="term-dir" :title="session?.workDir || ''">{{ session?.workDir || '—' }}</span>
        <span class="model-pill" v-if="session?.model">{{ session.model }}</span>
      </div>
      <div class="term-tb-right">
        <button class="btn btn-ghost" @click="handleOpenDetail" title="会话详情">会话详情</button>
        <button
          class="btn btn-ghost danger"
          :disabled="isStopping || session?.status !== 'running'"
          @click="handleStop"
        >{{ isStopping ? '停止中…' : '停止' }}</button>
      </div>
    </div>

    <!-- 终端主体：xterm 挂载点 -->
    <!-- @wheel.stop 阻止 wheel 冒泡到 .main(overflow:auto)：opencode TUI 启用
         SGR/1006 鼠标上报后，xterm 把 wheel 编码为按钮事件发给 PTY，PTY 不消费
         时事件继续冒泡，触发 .main 整体视图滚动。stopPropagation 在冒泡阶段
         拦截，不影响 xterm 子元素自身的 wheel→scrollback 处理。
         绝不加 .prevent：会阻止 xterm 自身的 scrollback 滚动。 -->
    <div
      ref="bodyRef"
      class="term-body"
      @wheel.stop
      @contextmenu.prevent="handleContextMenu"
    >
      <!-- TerminalContextMenu 渲染在 term-body 上方 -->
      <TerminalContextMenu
        :visible="ctx.visible"
        :x="ctx.x"
        :y="ctx.y"
        :has-selection="hasSelection"
        @copy="onCtxCopy"
        @paste="onCtxPaste"
        @select-all="onCtxSelectAll"
        @close="closeCtx"
      />
    </div>

    <!-- 会话详情弹窗 -->
    <SessionDetailModal
      :visible="detailVisible"
      :session-id="sessionId"
      :session="session"
      @close="detailVisible = false"
    />
  </div>
</template>

<script setup lang="ts">
/**
 * TerminalView — single-session terminal surface.
 *
 * Owns the xterm mount lifecycle for one session via useTerminalEngine.
 * TerminalPage keeps every visited session mounted and only hides inactive
 * surfaces, so route/session reactivation reuses the same parsed buffer.
 * Refreshes re-fit and repaint that buffer while preserving its logical
 * viewport position.
 *
 * Selection:
 *   - macOS: hold Option and drag (native xterm escape hatch).
 *   - Windows/Linux: hold Shift and drag (engine attaches a capture-phase
 *     mousedown interceptor that synthesizes a selection via term.select()).
 *   - Once selected, Ctrl+Shift+C copies and Delete/Backspace bulk-erases.
 */
import {
  computed,
  onActivated,
  onBeforeUnmount,
  onDeactivated,
  onMounted,
  ref,
  watch,
} from 'vue'
import { session as sessionModels } from '../../../wailsjs/go/models'
import { useSessionStore } from '../../stores/session'
import { useSessionList } from '../../composables/useSessionList'
import { useToast } from '../../composables/useToast'
import { usePlatformCapabilities } from '../../composables/usePlatformCapabilities'
import { useTerminalEngine } from '../../composables/useTerminalEngine'
import { basename } from '../../utils/format'
import SessionDetailModal from '../session/SessionDetailModal.vue'
import TerminalContextMenu from './TerminalContextMenu.vue'

type SessionInfo = sessionModels.SessionInfo

const props = withDefaults(defineProps<{ sessionId: string; active?: boolean }>(), {
  active: true,
})

const sessionStore = useSessionStore()
const { stopAndRefresh, refresh } = useSessionList()
const { showError, showInfo } = useToast()
const platformCaps = usePlatformCapabilities()

// one engine instance per TerminalView; the whole tree below shares it.
const engine = useTerminalEngine()

const bodyRef = ref<HTMLElement | null>(null)
const stopping = ref(false)
const detailVisible = ref(false)
const hasSelection = ref(false)
const routeSurfaceActive = ref(true)
const surfaceActive = computed(() => props.active && routeSurfaceActive.value)

// right-click menu transient state
const ctx = ref({ visible: false, x: 0, y: 0 })

const session = computed<SessionInfo | null>(
  () => sessionStore.sessions.find((s) => s.id === props.sessionId) || null,
)

const sessionTitle = computed(() => {
  const s = session.value
  if (!s) return `#${props.sessionId}`
  const dir = s.workDir ? basename(s.workDir) : ''
  return `#${s.id} ${dir || '会话'}`
})

const isStopping = computed(() => stopping.value || session.value?.status === 'stopping')

const statusColor = computed(() => {
  const s = session.value
  if (!s) return 'var(--tertiary)'
  if (s.status === 'running') return 'var(--success)'
  if (s.status === 'stopping') return 'var(--warning, #FF9500)'
  return 'var(--tertiary)'
})

let rendererRefreshPending = false

function refreshVisibleSurface() {
  if (!surfaceActive.value) return
  const el = bodyRef.value
  if (!el) return
  requestAnimationFrame(() => {
    if (!surfaceActive.value || bodyRef.value !== el) return
    if (rendererRefreshPending) {
      rendererRefreshPending = false
      engine.refreshRenderer(props.sessionId)
    } else {
      engine.refreshTerminal(props.sessionId)
    }
  })
}

onActivated(() => {
  routeSurfaceActive.value = true
  refreshVisibleSurface()
})

onDeactivated(() => {
  routeSurfaceActive.value = false
})

watch(
  () => props.active,
  (active) => {
    if (active) refreshVisibleSurface()
  },
)

onMounted(async () => {
  // Platform caps must be loaded before terminal creation: otherwise
  // isDarwin/isWindows return false when the singleton cache is null (page
  // opened directly / refreshed), causing the WebGL guard to fail-open on
  // macOS and the windowsPty hint to be omitted on Windows.
  await platformCaps.ensure()

  const el = bodyRef.value
  if (!el) return

  engine.mountTerm(props.sessionId, el, {
    encodeShiftEnterAsCsiU:
      session.value?.appType === 'pi' || session.value?.appType === 'omp',
    onExit: (_info) => {
      // exit also surfaces via the 2s poll in useSessionList, but refresh
      // immediately so the dot turns grey without a perceptible delay.
      refresh()
      // Toast notification for session exit
      const s = session.value
      const title = s?.workDir ? s.workDir.split(/[/\\]/).pop() || '会话' : '会话'
      showInfo(`会话 #${props.sessionId} ${title} 已退出`)
    },
  })

  refreshVisibleSurface()

  // A delayed public-API redraw absorbs the first ResizeObserver callback and
  // late web-font metrics without replacing a renderer under queued writes.
  mountFallbackFitTimer = setTimeout(() => {
    if (bodyRef.value === el) refreshVisibleSurface()
  }, 200)
})

// when the container resizes (sidebar collapse, window resize), refit.
let resizeObserver: ResizeObserver | null = null
let resizeDebounce: ReturnType<typeof setTimeout> | null = null
// mount 后 200ms force fit 兜底句柄（见 onMounted），卸载时清理避免触发。
let mountFallbackFitTimer: ReturnType<typeof setTimeout> | null = null
onMounted(() => {
  const el = bodyRef.value
  if (!el || typeof ResizeObserver === 'undefined') return
  resizeObserver = new ResizeObserver(() => {
    if (resizeDebounce) clearTimeout(resizeDebounce)
    resizeDebounce = setTimeout(() => {
      if (surfaceActive.value) engine.fitTerminal(props.sessionId)
    }, 100)
  })
  resizeObserver.observe(el)
})

// A restored WKWebView may retain a correct xterm buffer but an invalidated
// paint layer. Force a complete redraw even when rows/cols did not change.
function onVisibility() {
  if (document.visibilityState !== 'visible') return
  refreshVisibleSurface()
}
onMounted(() => document.addEventListener('visibilitychange', onVisibility))

// Detect devicePixelRatio changes (moving the window between monitors with
// different DPI, or the OS zoom level changing). When DPR changes, the
// WebGL's backing store must be rebuilt on Windows/Linux. Hidden/cached
// terminals defer that rebuild until they become visible again.
let dprMql: MediaQueryList | null = null
function watchDpr() {
  const dpr = window.devicePixelRatio || 1
  dprMql = window.matchMedia(`(resolution: ${dpr}dppx)`)
  const handler = () => {
    rendererRefreshPending = true
    refreshVisibleSurface()
    // Re-arm: create a new MQL for the new DPR value.
    if (dprMql) dprMql.removeEventListener('change', handler)
    watchDpr()
  }
  if (dprMql.addEventListener) {
    dprMql.addEventListener('change', handler)
  } else {
    // Safari < 14 fallback
    dprMql.addListener(handler)
  }
  // Store cleanup ref on the component for onBeforeUnmount.
  dprCleanup = () => {
    if (dprMql) {
      if (dprMql.removeEventListener) dprMql.removeEventListener('change', handler)
      else dprMql.removeListener(handler)
    }
  }
}
let dprCleanup: (() => void) | null = null
onMounted(() => watchDpr())

onBeforeUnmount(() => {
  if (resizeDebounce) clearTimeout(resizeDebounce)
  if (mountFallbackFitTimer) {
    clearTimeout(mountFallbackFitTimer)
    mountFallbackFitTimer = null
  }
  resizeObserver?.disconnect()
  resizeObserver = null
  document.removeEventListener('visibilitychange', onVisibility)
  dprCleanup?.()
  dprCleanup = null
  dprMql = null
  engine.disposeTerm(props.sessionId)
})

// keep activeSessionId in store in sync with the displayed session so the
// sidebar highlight and any other consumer agree with what is on screen.
watch(
  () => props.sessionId,
  (id) => engine.switchSession(id),
  { immediate: true },
)

async function handleStop() {
  const s = session.value
  if (!s) return
  stopping.value = true
  try {
    await stopAndRefresh(s.id)
    // Stop success means the signal was accepted, not that Wait has produced a
    // terminal receipt. The exit callback/poll owns the eventual completion.
    showInfo('已请求停止，正在等待进程退出')
  } catch (err) {
    showError('停止失败: ' + err)
  } finally {
    stopping.value = false
  }
}

function handleOpenDetail() {
  detailVisible.value = true
}

// ---- right-click menu -----------------------------------------------------

function handleContextMenu(ev: MouseEvent) {
  const inst = engine.getTerm(props.sessionId)
  hasSelection.value = !!(inst && inst.term.getSelection())
  ctx.value = { visible: true, x: ev.clientX, y: ev.clientY }
}

function closeCtx() {
  ctx.value = { ...ctx.value, visible: false }
}

function onCtxCopy() {
  engine.copySelection(props.sessionId)
  closeCtx()
}

function onCtxPaste() {
  engine.pasteToTerminal(props.sessionId)
  closeCtx()
  // re-focus so keyboard input continues to reach the PTY
  const inst = engine.getTerm(props.sessionId)
  if (inst) inst.term.focus()
}

function onCtxSelectAll() {
  const inst = engine.getTerm(props.sessionId)
  if (inst) inst.term.selectAll()
  closeCtx()
}
</script>

<style scoped>
.view-terminal {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}

.term-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 26px;
  border-bottom: 1px solid var(--separator);
  flex-shrink: 0;
}

.term-tb-left {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.sess-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.term-title {
  font-size: 16px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--label);
}

.term-sep {
  color: var(--tertiary);
}

.term-dir {
  font-size: 13px;
  color: var(--secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 280px;
  font-family: var(--mono, monospace);
}

.model-pill {
  background: var(--control);
  border-radius: 6px;
  padding: 2px 8px;
  font-size: 11px;
  color: var(--secondary);
  flex-shrink: 0;
}

.term-tb-right {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  font-size: 12px;
  font-weight: 500;
  padding: 6px 12px;
  font-family: inherit;
  transition: background 0.15s, opacity 0.15s;
}

.btn-ghost {
  background: var(--control);
  color: var(--secondary);
}

.btn-ghost:hover:not(:disabled) {
  background: var(--controlHover);
}

.btn-ghost:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-ghost.danger {
  color: var(--danger);
}

/* xterm host: demo term-body uses var(--termBg); xterm paints its own bg
   but the host must be dark too to avoid flashes during teardown. */
.term-body {
  flex: 1;
  background: var(--termBg, #1b1b1f);
  min-height: 0;
  position: relative;
  overflow: hidden;
}

/* let xterm fill the host.
   padding:0 让终端文字贴合容器边缘——内部行列已由 fitTerminal 基于
   实际尺寸计算，去掉 padding 不会破坏 fit；此前 14px 18px 的留白会
   让 xterm 外圈露出一圈 --termBg #1B1B1F 黑色边框。 */
.term-body :deep(.xterm) {
  height: 100%;
  width: 100%;
  padding: 0;
  box-sizing: border-box;
  text-align: left;
}

/* Full-screen CLIs position borders and cursors by terminal cells. Disable
   discretionary ligatures/kerning so the DOM renderer cannot visually merge
   adjacent cells, especially around CJK input and box-drawing characters. */
.term-body :deep(.xterm-rows) {
  font-kerning: none;
  font-variant-ligatures: none;
  font-feature-settings: "liga" 0, "calt" 0;
}

/* Never force .xterm-screen to 100% width. xterm owns the exact
   cols*cellWidth geometry; stretching that surface introduces sub-pixel row
   and mouse-coordinate drift for every renderer. */

/* match demo scrollbar thumb so the terminal area reads as one surface */
.term-body :deep(.xterm-viewport::-webkit-scrollbar-thumb) {
  background: #3a3a42;
}

/* xterm.css sets .xterm-viewport background to #000, but our terminal theme
   uses #1B1B1F. Match the host so scroll/repaint never exposes a contrasting
   strip behind the renderer. */
.term-body :deep(.xterm-viewport) {
  background-color: var(--termBg, #1b1b1f);
}
</style>
