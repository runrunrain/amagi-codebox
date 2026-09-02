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
        <!-- TUI ⇄ Web 切换（蓝图 T-1.6；A-4：仅 pi 会话且 webui available 才显示） -->
        <div v-if="webUIToggleVisible" class="plane-toggle" role="group" aria-label="会话显示平面切换">
          <Segmented
            :model-value="activePlane"
            :options="planeOptions"
            variant="pill"
            @update:modelValue="onPlaneSelect"
          />
        </div>
        <!-- 快捷功能（锚定下拉菜单，所有内嵌终端会话通用）：Web 平面/会话未运行时禁用 -->
        <button
          ref="quickAnchorRef"
          class="btn btn-ghost"
          :disabled="quickMenuDisabled"
          :title="quickMenuTitle"
          :aria-expanded="quickMenuVisible"
          aria-haspopup="menu"
          @click="toggleQuickMenu"
        >快捷功能</button>
        <button ref="gitAnchorRef" class="btn btn-ghost" @click="gitPanelVisible = true" title="提交/推送">提交/推送</button>
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
      :class="{ 'web-active': activePlane === 'web' }"
      @wheel.stop
      @contextmenu.prevent="handleContextMenu"
    >
      <!-- Web 平面（pi + webui available）：与 xterm 平面互斥 v-show，
           切走后保留不销毁（交互文档 §3）；xterm 由 .web-active 类隐藏。 -->
      <WebPlaneHost
        v-if="webPlaneMounted"
        v-show="activePlane === 'web'"
        :url="webUrl"
        :session-id="sessionId"
        :ended="webEnded"
        @error="onWebPlaneError"
        @retry="handleWebRetry"
        @switch-to-tui="handleSwitchToTui"
      />
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

    <!-- Git 提交/推送浮层（锚定工具栏按钮的下拉浮层，非阻断） -->
    <GitPanel
      :visible="gitPanelVisible"
      :work-dir="session?.workDir || ''"
      :anchor="gitAnchorRef"
      @close="gitPanelVisible = false"
    />

    <!-- 快捷功能菜单（锚定「快捷功能」按钮的下拉浮层，非阻断；数据驱动，预留扩展） -->
    <Teleport to="body">
      <div
        v-if="quickMenuVisible"
        ref="quickMenuRef"
        class="quick-menu"
        :style="quickMenuStyle"
        role="menu"
        aria-label="快捷功能"
      >
        <button
          v-for="item in quickMenuItems"
          :key="item.key"
          type="button"
          role="menuitem"
          class="quick-menu-item"
          @click="onQuickMenuItem(item)"
        >{{ item.label }}</button>
      </div>
    </Teleport>

    <!-- 快捷功能：输入工作路径（目录多选 → 组装文本写入终端输入框） -->
    <PathPickerDialog
      :visible="pathPickerVisible"
      :work-dir="session?.workDir || ''"
      @close="pathPickerVisible = false"
      @confirm="onPathPickerConfirm"
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
import { buildAssociatedPathLines } from '../../utils/quickFunctions'
import GitPanel from './GitPanel.vue'
import TerminalContextMenu from './TerminalContextMenu.vue'
import WebPlaneHost from './WebPlaneHost.vue'
import PathPickerDialog from './PathPickerDialog.vue'
import Segmented from '../ui/Segmented.vue'
import { openWebPlane } from '../../api/webui'

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
const gitPanelVisible = ref(false)
const gitAnchorRef = ref<HTMLElement | null>(null)
const hasSelection = ref(false)
const routeSurfaceActive = ref(true)
const surfaceActive = computed(() => props.active && routeSurfaceActive.value)

// ---- 快捷功能（输入工作路径等）：锚定下拉菜单 + 路径选择器 ----
const quickMenuVisible = ref(false)
const quickAnchorRef = ref<HTMLElement | null>(null)
const quickMenuRef = ref<HTMLElement | null>(null)
const quickMenuStyle = ref<Record<string, string>>({})
const pathPickerVisible = ref(false)

// 数据驱动菜单项：首项「输入工作路径」，后续快捷能力在此追加。
const quickMenuItems: { key: string; label: string; action: () => void }[] = [
  {
    key: 'work-path',
    label: '输入工作路径',
    action: () => {
      pathPickerVisible.value = true
    },
  },
]

// Web 平面（pi Web）内嵌页面有自己的输入通道，终端写入会落到隐藏的 xterm；
// 会话非 running 时终端不再接受输入。两种情况都禁用入口。
const quickMenuDisabled = computed(
  () => activePlane.value === 'web' || session.value?.status !== 'running',
)

const quickMenuTitle = computed(() => {
  if (activePlane.value === 'web') return 'Web 平面请使用页面内快捷功能，或切回终端平面'
  if (session.value?.status !== 'running') return '会话未运行，无法使用快捷功能'
  return '快捷功能'
})

// ---- 锚定定位：正下方右对齐（对齐 GitPanel 浮层模式），clamp 在视口内 ----
const QUICK_MENU_PAD = 12
const QUICK_MENU_WIDTH = 220

function updateQuickMenuPosition() {
  const anchor = quickAnchorRef.value
  if (!anchor) return
  const rect = anchor.getBoundingClientRect()
  const width = Math.min(QUICK_MENU_WIDTH, window.innerWidth - QUICK_MENU_PAD * 2)
  const left = Math.max(
    QUICK_MENU_PAD,
    Math.min(rect.right - width, window.innerWidth - width - QUICK_MENU_PAD),
  )
  const top = Math.min(rect.bottom + 8, window.innerHeight - QUICK_MENU_PAD)
  quickMenuStyle.value = { left: `${left}px`, top: `${top}px`, width: `${width}px` }
}

// 非阻断关闭：菜单外按下即关闭，但不拦截事件（对齐 GitPanel）。
function onQuickMenuOutsideDown(event: Event) {
  const target = event.target as Node | null
  if (!target) return
  if (quickMenuRef.value?.contains(target)) return
  if (quickAnchorRef.value?.contains(target)) return
  quickMenuVisible.value = false
}

function onQuickMenuKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && quickMenuVisible.value) quickMenuVisible.value = false
}

function toggleQuickMenu() {
  if (quickMenuDisabled.value) return
  quickMenuVisible.value = !quickMenuVisible.value
}

function onQuickMenuItem(item: { key: string; label: string; action: () => void }) {
  quickMenuVisible.value = false
  item.action()
}

watch(quickMenuVisible, (visible) => {
  if (visible) {
    updateQuickMenuPosition()
    window.addEventListener('resize', updateQuickMenuPosition)
    document.addEventListener('pointerdown', onQuickMenuOutsideDown, true)
    document.addEventListener('mousedown', onQuickMenuOutsideDown, true)
    document.addEventListener('keydown', onQuickMenuKeydown, true)
  } else {
    window.removeEventListener('resize', updateQuickMenuPosition)
    document.removeEventListener('pointerdown', onQuickMenuOutsideDown, true)
    document.removeEventListener('mousedown', onQuickMenuOutsideDown, true)
    document.removeEventListener('keydown', onQuickMenuKeydown, true)
  }
})

onBeforeUnmount(() => {
  if (quickMenuVisible.value) {
    window.removeEventListener('resize', updateQuickMenuPosition)
    document.removeEventListener('pointerdown', onQuickMenuOutsideDown, true)
    document.removeEventListener('mousedown', onQuickMenuOutsideDown, true)
    document.removeEventListener('keydown', onQuickMenuKeydown, true)
  }
})

// 确认回调：组装「关联工作路径：…」文本 → 经引擎单一写入出口插入终端；
// bracketed paste 由引擎按当前 TUI 模式自动包裹。异常走既有 toast 报错。
async function onPathPickerConfirm(paths: string[]) {
  pathPickerVisible.value = false
  try {
    await engine.insertTextToTerminal(props.sessionId, buildAssociatedPathLines(paths))
  } catch (err) {
    showError('工作路径写入失败: ' + err)
  }
}

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

// ---- pi Web 平面切换（蓝图 T-1.6）----
// 切换状态按组件实例（= 按 sessionId，TerminalPage 每会话一实例 v-show 缓存）
// 隔离，内存持有不持久化（TD-9）。
const planeOptions = [
  { value: 'tui', label: '终端' },
  { value: 'web', label: '网页' },
]
const activePlane = ref<'tui' | 'web'>('tui')
// 用户显式选择 TUI 后本实例 sticky：preferWebPlane 不再自动切 Web，
// 仅当用户切回 web（onPlaneSelect）时清除。按组件实例隔离、不持久化（TD-9）。
const userPinnedTui = ref(false)
const webPlaneMounted = ref(false)
const webUrl = ref('')
const webEnded = ref(false)

const isPiSession = computed(() => session.value?.appType === 'pi')
const webuiState = computed(
  () => sessionStore.webuiStatus[props.sessionId]?.state ?? 'unknown',
)
// A-4：仅 pi 会话且探测 available 且会话运行中才显示切换控件；
// 会话结束后 toolbar 仅 TUI 可切（交互文档 §3.4）——ended 后隐藏控件，
// 若用户仍在 Web 平面则由 WebPlaneHost 结束态提供“切回终端”。
const webUIToggleVisible = computed(
  () =>
    isPiSession.value &&
    webuiState.value === 'available' &&
    session.value?.status === 'running',
)

// 用户手动切换平面：显式选 TUI 视为 pin，显式选回 web 时清除 pin。
// 经 watch(activePlane) 驱动既有的 activateWebPlane / refit 副作用。
function onPlaneSelect(plane: string) {
  userPinnedTui.value = plane === 'tui'
  activePlane.value = plane === 'web' ? 'web' : 'tui'
}

// 需求：pi 会话装有 web 插件（webui available）时，点击活动会话优先显示
// 网页平面；用户显式切回终端后不再自动切换（sticky，切回 web 时清除）。
// 守卫：surfaceActive 拦住后台/缓存会话，webUIToggleVisible 拦住非 pi、
// 未 available 或已结束的会话，userPinnedTui 尊重用户显式选择。
function preferWebPlane() {
  if (!surfaceActive.value) return
  if (!webUIToggleVisible.value) return
  if (userPinnedTui.value) return
  if (activePlane.value === 'web') return
  activePlane.value = 'web'
}

// WebPlaneHost 结束态『切回终端』按钮：视为用户显式选择 TUI（pin）。
function handleSwitchToTui() {
  userPinnedTui.value = true
  activePlane.value = 'tui'
}

async function activateWebPlane() {
  try {
    webUrl.value = await openWebPlane(props.sessionId)
    webEnded.value = false
    webPlaneMounted.value = true
  } catch (err) {
    showError('Web 平面不可用: ' + err)
    activePlane.value = 'tui'
    // 状态可能已变化（服务刚消亡）：重新探测刷新。
    sessionStore.ensureWebUIProbe(props.sessionId)
  }
}

function onWebPlaneError() {
  // iframe 加载失败：重新探测服务状态（可能已 ended/unavailable）。
  sessionStore.ensureWebUIProbe(props.sessionId)
}

async function handleWebRetry() {
  try {
    webUrl.value = await openWebPlane(props.sessionId)
  } catch (err) {
    showError('Web 平面重试失败: ' + err)
    sessionStore.ensureWebUIProbe(props.sessionId)
  }
}

watch(activePlane, (plane) => {
  if (plane === 'web') {
    void activateWebPlane()
  } else {
    // 切回 TUI：xterm 平面恢复 v-show 后 refit（交互文档 §7）。
    refreshVisibleSurface()
  }
})

// webui 探测演进为 available（启动后轮询出结果）或挂载时已 available
// （immediate）时，活动会话优先显示 Web 平面；surfaceActive 守卫拦住
// 后台缓存会话，userPinnedTui 守卫尊重用户显式选择。
watch(
  webUIToggleVisible,
  (visible) => {
    if (visible) preferWebPlane()
  },
  { immediate: true },
)

// 会话退出 → Web 平面结束态（保留最后画面 + badge）。
watch(
  () => session.value?.status,
  (status) => {
    if (status && status !== 'running' && status !== 'stopping') {
      webEnded.value = true
    }
  },
)

// 会话切换跟随（/resume、/new、fork、reload）：webuiStatus.url 演进（老扩展
// 端口漂移场景）时同步刷新 webUrl——此前只在打开/切换平面时经 openWebPlane
// 取一次。URL 直接取自 store 状态（available 且非空时采用），不重复调用
// openWebPlane（避免额外探测）；URL 未变时的强制 reload 由 WebPlaneHost
// 既有机制接管，不重复实现。
watch(
  () => sessionStore.webuiStatus[props.sessionId]?.url,
  (url) => {
    if (!url || webuiState.value !== 'available' || activePlane.value !== 'web') return
    webUrl.value = url
  },
)

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
  preferWebPlane()
})

onDeactivated(() => {
  routeSurfaceActive.value = false
})

watch(
  () => props.active,
  (active) => {
    if (active) {
      refreshVisibleSurface()
      preferWebPlane()
    }
  },
)

onMounted(async () => {
  // pi 会话启动 webui 探测轮询（0.5–1s 节奏，available/终态后自动停止）。
  // 必须在 mountTerm 之前启动与否无关，幂等可重入。
  if (isPiSession.value) {
    sessionStore.ensureWebUIProbe(props.sessionId)
  }

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
  sessionStore.stopWebUIProbe(props.sessionId)
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
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

/* TUI ⇄ Web 切换控件：与相邻按钮规格一致（交互文档 §4.1） */
.plane-toggle {
  min-width: 128px;
}

.plane-toggle :deep(.seg) {
  padding: 6px 10px;
  font-size: 12px;
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

/* 快捷功能菜单：锚定工具栏按钮的下拉浮层，视觉语言对齐 GitPanel */
.quick-menu {
  position: fixed;
  z-index: 3000;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 6px;
  border: 1px solid rgba(137, 221, 255, 0.24);
  border-radius: 12px;
  background:
    radial-gradient(circle at 12% 0%, rgba(137, 221, 255, 0.16), transparent 32%),
    #0b1018;
  box-shadow: 0 16px 48px rgba(0, 0, 0, 0.5);
  color: #d9e2ec;
}

.quick-menu-item {
  border: none;
  background: none;
  color: #d9e2ec;
  font-size: 13px;
  text-align: left;
  padding: 8px 12px;
  border-radius: 8px;
  cursor: pointer;
  font-family: inherit;
}

.quick-menu-item:hover,
.quick-menu-item:focus-visible {
  background: rgba(137, 221, 255, 0.12);
  color: #89ddff;
  outline: none;
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

/* Web 平面激活时隐藏 xterm 平面（互斥 v-show 语义；xterm 元素由 JS 创建，
   以容器类控制显隐，进程不停、缓冲保留）。 */
.term-body.web-active :deep(.xterm) {
  display: none;
}

/* 皮肤模式下 Web 平面透皮：xterm 平面保持 --termBg 不透明，仅在 Web 平面
   激活时让宿主底转透明，皮肤层经 WebPlaneHost（透明）+ iframe（webui 内嵌
   模式 body 透明）一路透出。 */
html[data-skin='on'] .term-body.web-active {
  background: transparent;
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
