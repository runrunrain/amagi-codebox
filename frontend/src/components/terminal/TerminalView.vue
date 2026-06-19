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
          :disabled="stopping || session?.status !== 'running'"
          @click="handleStop"
        >{{ stopping ? '停止中…' : '停止' }}</button>
      </div>
    </div>

    <!-- 终端主体：xterm 挂载点 -->
    <div
      ref="bodyRef"
      class="term-body"
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
 * Switching sessions in the sidebar replaces this component (keyed by id) so
 * each mount/dispose pair is clean.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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

const props = defineProps<{ sessionId: string }>()

const sessionStore = useSessionStore()
const { stopAndRefresh, refresh } = useSessionList()
const { showSuccess, showError } = useToast()
const platformCaps = usePlatformCapabilities()

// one engine instance per TerminalView; the whole tree below shares it.
const engine = useTerminalEngine()

const bodyRef = ref<HTMLElement | null>(null)
const stopping = ref(false)
const detailVisible = ref(false)
const hasSelection = ref(false)

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

const statusColor = computed(() => {
  const s = session.value
  if (!s) return 'var(--tertiary)'
  return s.status === 'running' ? 'var(--success)' : 'var(--tertiary)'
})

onMounted(async () => {
  // Platform caps must be loaded before terminal creation: otherwise
  // isDarwin/isWindows return false when the singleton cache is null (page
  // opened directly / refreshed), causing the WebGL guard to fail-open on
  // macOS and the windowsPty hint to be omitted on Windows.
  await platformCaps.ensure()

  const el = bodyRef.value
  if (!el) return

  engine.mountTerm(props.sessionId, el, {
    onExit: () => {
      // exit also surfaces via the 2s poll in useSessionList, but refresh
      // immediately so the dot turns grey without a perceptible delay.
      refresh()
    },
  })

  // initial fit deferred one frame so the container has a measured size
  requestAnimationFrame(() => engine.fitTerminal(props.sessionId, true, el))
})

// when the container resizes (sidebar collapse, window resize), refit.
let resizeObserver: ResizeObserver | null = null
let resizeDebounce: ReturnType<typeof setTimeout> | null = null
onMounted(() => {
  const el = bodyRef.value
  if (!el || typeof ResizeObserver === 'undefined') return
  resizeObserver = new ResizeObserver(() => {
    if (resizeDebounce) clearTimeout(resizeDebounce)
    resizeDebounce = setTimeout(() => {
      engine.fitTerminal(props.sessionId, false, el)
    }, 100)
  })
  resizeObserver.observe(el)
})

// refit when the tab regains visibility (keep-alive reactivation, alt-tab).
function onVisibility() {
  if (document.visibilityState !== 'visible') return
  const el = bodyRef.value
  if (el) engine.fitTerminal(props.sessionId, true, el)
}
onMounted(() => document.addEventListener('visibilitychange', onVisibility))

onBeforeUnmount(() => {
  if (resizeDebounce) clearTimeout(resizeDebounce)
  resizeObserver?.disconnect()
  resizeObserver = null
  document.removeEventListener('visibilitychange', onVisibility)
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
    showSuccess('会话已停止')
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

/* let xterm fill the host */
.term-body :deep(.xterm) {
  height: 100%;
  width: 100%;
  padding: 14px 18px;
  box-sizing: border-box;
  text-align: left;
}

.term-body :deep(.xterm-screen) {
  width: 100% !important;
}

/* match demo scrollbar thumb so the terminal area reads as one surface */
.term-body :deep(.xterm-viewport::-webkit-scrollbar-thumb) {
  background: #3a3a42;
}
</style>
