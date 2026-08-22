<template>
  <div class="view-terminal remote-terminal">
    <!-- 远程状态条（交互稿 §2.3）：[主机名] 会话名 · 控制权 · 连接态 -->
    <div class="remote-statusbar">
      <span class="rsb-host" :title="`主机：${store.currentHostName}`">[{{ store.currentHostName }}]</span>
      <span class="rsb-title" :title="sessionTitle">{{ sessionTitle }}</span>
      <span class="rsb-sep">·</span>
      <span
        v-if="controlLabel"
        class="rsb-control"
        :class="`ctl-${controlStateTone(controlState)}`"
      >{{ controlLabel }}</span>
      <span class="rsb-conn" :class="`cs-${connTone}`">
        <span class="rsb-dot" aria-hidden="true" />{{ connText }}
      </span>
      <span class="rsb-spacer" />
      <AppButton
        variant="ghost"
        size="small"
        :disabled="stopping || sessionState !== 'running'"
        @click="handleStop"
      >{{ stopping ? '停止中…' : '停止' }}</AppButton>
      <AppButton variant="ghost" size="small" @click="handleClose">关闭终端</AppButton>
    </div>

    <!-- revoked：fail-closed（交互稿 §3 revoked 行） -->
    <RiskBanner v-if="revoked" title="授权已撤销" class="rsb-banner">
      本设备对主机「{{ store.currentHostName }}」的授权已被对方撤销，连接已断开。如需继续访问请重新配对。
    </RiskBanner>

    <!-- attach 失败（可重试） -->
    <StatusBanner
      v-else-if="attachError"
      type="error"
      :message="attachError"
      action-text="重新连接"
      @action="handleReattach"
    />

    <!-- 断线重连中：输出区冻结只读（无新事件流入），恢复后 Go 侧自动 backfill -->
    <StatusBanner
      v-else-if="reconnecting"
      type="warning"
      :message="reconnectText"
    />

    <!-- 只读（控制权≠你 / 输入能力缺失）：输入禁用 + 原因提示，输出流继续 -->
    <StatusBanner
      v-else-if="readonlyReason"
      type="warning"
      :message="readonlyReason"
    />

    <!-- 终端主体：xterm 挂载点（@wheel.stop 缘由同 TerminalView） -->
    <div
      ref="bodyRef"
      class="term-body"
      @wheel.stop
      @contextmenu.prevent="handleContextMenu"
    >
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
  </div>
</template>

<script setup lang="ts">
/**
 * RemoteTerminalView（RC2-5 桌面端互联 · 远程终端页，交互稿 §2.3）
 *
 * 复用 useTerminalEngine 渲染内核（T0-2 结论 B）：通过 MountOptions.transport
 * 注入远程传输适配器（rc:* 事件 + RemoteClientTerminal* 绑定），本地 Wails
 * 触点零涉及。会话元数据来自远程轮询（store.remoteSessions），连接/控制权
 * 状态来自 store 聚合的 rc:* 事件投影。
 *
 * 输入就绪条件：attached 且 control=you（Go 状态机 attached 已蕴含两者；
 * 见 conn.go refreshInputGate）。其余状态输入经 canInput 门静默丢弃 + 状态条
 * 原因提示。
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
import AppButton from '../ui/AppButton.vue'
import StatusBanner from '../ui/StatusBanner.vue'
import RiskBanner from '../remote/RiskBanner.vue'
import TerminalContextMenu from './TerminalContextMenu.vue'
import { useRemoteClientStore } from '../../stores/remoteClient'
import { useToast } from '../../composables/useToast'
import { usePlatformCapabilities } from '../../composables/usePlatformCapabilities'
import { useTerminalEngine } from '../../composables/useTerminalEngine'
import { createRemoteTerminalTransport } from '../../composables/remoteTerminalTransport'
import { controlStateLabel, controlStateTone, copyForRemoteError } from '../remote/remoteClientShared'

const props = withDefaults(defineProps<{ sessionId: string; active?: boolean }>(), {
  active: true,
})
const emit = defineEmits<{ close: [] }>()

const store = useRemoteClientStore()
const { showError, showInfo } = useToast()
const platformCaps = usePlatformCapabilities()

// 与 TerminalView 一致：每视图一个 engine 实例（terminals Map 为实例私有）。
const engine = useTerminalEngine()

const bodyRef = ref<HTMLElement | null>(null)
const stopping = ref(false)
const attachError = ref('')
const hasSelection = ref(false)
const routeSurfaceActive = ref(true)
const surfaceActive = computed(() => props.active && routeSurfaceActive.value)
const ctx = ref({ visible: false, x: 0, y: 0 })

// ---- 会话元数据（远程轮询列表；列表缺失时回退 id）----
const session = computed(
  () => store.remoteSessions.find((s) => s.id === props.sessionId) ?? null,
)
const sessionTitle = computed(() => session.value?.title || `#${props.sessionId}`)

// ---- 连接/控制权状态投影（rc:* 事件聚合；回退到列表快照）----
const termState = computed(() => store.remoteTerminalStates[props.sessionId] ?? null)
const connState = computed(() => termState.value?.connState ?? '')
const sessionState = computed(
  () => termState.value?.sessionState || session.value?.state || '',
)
const controlState = computed(
  () => termState.value?.controlState || session.value?.control?.state || '',
)
const controlLabel = computed(() => controlStateLabel(controlState.value))

const revoked = computed(
  () => store.connectRevoked || (termState.value?.detail ?? '').includes('撤销'),
)

const reconnecting = computed(() => {
  if (revoked.value || attachError.value) return false
  const st = connState.value
  // connecting 且非首轮 = 断线重连；disconnected 且未撤销 = 异常终态断开。
  return (st === 'connecting' && (termState.value?.attempt ?? 0) > 1) || st === 'disconnected'
})

const reconnectText = computed(() => {
  const t = termState.value
  if (connState.value === 'disconnected') {
    return `与主机的终端连接已断开${t?.detail ? `（${t.detail}）` : ''}。输出区已冻结。`
  }
  const retry = t?.nextRetryMs ? `，约 ${Math.max(1, Math.round(t.nextRetryMs / 1000))}s 后重试` : ''
  return `连接已断开，正在重连（第 ${t?.attempt ?? 0} 次尝试${retry}）。输出区已冻结，恢复后自动补齐历史。`
})

const readonlyReason = computed(() => {
  if (revoked.value || attachError.value || reconnecting.value) return ''
  if (connState.value === 'readonly') {
    if (controlState.value && controlState.value !== 'you') {
      return '当前没有该会话的控制权，终端为只读；输出流继续。'
    }
    return '远端未开放输入能力，终端为只读；输出流继续。'
  }
  if (connState.value === 'degraded') return '正在补齐终端历史，输入暂不可用…'
  if (connState.value === 'connecting') return '正在连接远程终端…'
  return ''
})

type ConnTone = 'ok' | 'busy' | 'down'
const connTone = computed<ConnTone>(() => {
  if (revoked.value || connState.value === 'disconnected') return 'down'
  if (connState.value === 'attached') return 'ok'
  return 'busy'
})

const connText = computed(() => {
  if (revoked.value) return '已撤销'
  switch (connState.value) {
    case 'attached':
      return '已连接'
    case 'readonly':
      return '只读'
    case 'degraded':
      return '同步历史…'
    case 'connecting':
      return (termState.value?.attempt ?? 0) > 1 ? '重连中' : '连接中…'
    case 'disconnected':
      return '已断开'
    default:
      return '待连接'
  }
})

// 输入门：attached 蕴含 control=you + 输入能力就绪（conn.go refreshInputGate）。
function canInput(): boolean {
  return store.remoteTerminalStates[props.sessionId]?.connState === 'attached'
}

// ---- 生命周期（镜像 TerminalView：保活/重绘/DPR/可见性）----

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
let rendererRefreshPending = false

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

let unsubscribeSessionState: (() => void) | null = null

onMounted(async () => {
  await platformCaps.ensure()
  const el = bodyRef.value
  if (!el) return

  // 先挂载（内部完成输出/会话态订阅）再 attach，避免漏掉 attach 回放首帧。
  engine.mountTerm(props.sessionId, el, {
    transport: createRemoteTerminalTransport(props.sessionId),
    canInput,
    encodeShiftEnterAsCsiU:
      session.value?.cliType === 'pi' || session.value?.cliType === 'omp',
    onExit: () => {
      showInfo(`远程会话 ${sessionTitle.value} 已退出`)
      void store.refreshRemoteSessions()
    },
  })

  // restart 边界：输出不跨运行续接的本地提示（restartBoundary 不产终端输出）。
  unsubscribeSessionState = store.subscribeRemoteSessionState(props.sessionId, (ev) => {
    if (ev.restartBoundary) {
      engine.writeLocalEcho(
        props.sessionId,
        '\r\n\x1b[90m[远程终端] 会话已进入新的运行\x1b[0m\r\n',
      )
    }
    if (ev.state === 'removed') {
      showInfo(`远程会话 ${sessionTitle.value} 已被移除`)
      emit('close')
    }
  })

  refreshVisibleSurface()

  try {
    await store.attachRemoteTerminal(props.sessionId)
    attachError.value = ''
  } catch (err) {
    attachError.value = copyForRemoteError(err)
  }
})

async function handleReattach() {
  attachError.value = ''
  try {
    await store.attachRemoteTerminal(props.sessionId)
  } catch (err) {
    attachError.value = copyForRemoteError(err)
  }
}

// 重连成功（→attached）后强制 fit：把当前网格上报宿主（断开期间的 resize
// 被传输层静默丢弃，见 remoteTerminalTransport.resize）。
watch(connState, (st, prev) => {
  if (st === 'attached' && prev !== 'attached') {
    engine.fitTerminal(props.sessionId, true)
  }
})

let resizeObserver: ResizeObserver | null = null
let resizeDebounce: ReturnType<typeof setTimeout> | null = null
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

function onVisibility() {
  if (document.visibilityState !== 'visible') return
  refreshVisibleSurface()
}
onMounted(() => document.addEventListener('visibilitychange', onVisibility))

let dprMql: MediaQueryList | null = null
let dprCleanup: (() => void) | null = null
function watchDpr() {
  const dpr = window.devicePixelRatio || 1
  dprMql = window.matchMedia(`(resolution: ${dpr}dppx)`)
  const handler = () => {
    rendererRefreshPending = true
    refreshVisibleSurface()
    if (dprMql) dprMql.removeEventListener('change', handler)
    watchDpr()
  }
  if (dprMql.addEventListener) {
    dprMql.addEventListener('change', handler)
  } else {
    dprMql.addListener(handler)
  }
  dprCleanup = () => {
    if (dprMql) {
      if (dprMql.removeEventListener) dprMql.removeEventListener('change', handler)
      else dprMql.removeListener(handler)
    }
  }
}
onMounted(() => watchDpr())

onBeforeUnmount(() => {
  if (resizeDebounce) clearTimeout(resizeDebounce)
  resizeObserver?.disconnect()
  resizeObserver = null
  document.removeEventListener('visibilitychange', onVisibility)
  dprCleanup?.()
  dprCleanup = null
  dprMql = null
  unsubscribeSessionState?.()
  unsubscribeSessionState = null
  engine.disposeTerm(props.sessionId)
  // 终止 Go 侧 /ws/v1 长连接（断开/换主机场景下绑定会失败，store 内已记录）。
  void store.detachRemoteTerminal(props.sessionId)
})

// ---- 操作 ----

async function handleStop() {
  if (sessionState.value !== 'running') return
  stopping.value = true
  try {
    await store.stopRemoteSession(props.sessionId)
    showInfo('已请求停止远端会话')
  } catch (err) {
    showError(copyForRemoteError(err))
  } finally {
    stopping.value = false
  }
}

function handleClose() {
  emit('close')
}

// ---- 右键菜单 ----

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

/* 远程状态条（交互稿 §2.3）：主机徽标 + 会话名 + 控制权 + 连接态 */
.remote-statusbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 18px;
  border-bottom: 1px solid var(--separator);
  flex-shrink: 0;
  min-width: 0;
}

.rsb-host {
  font-size: 12px;
  font-weight: 600;
  color: var(--accent-strong);
  white-space: nowrap;
  flex-shrink: 0;
}

.rsb-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}

.rsb-sep {
  color: var(--tertiary);
  flex-shrink: 0;
}

/* 控制权徽标：色调复用远程会话列表四态（视觉风格 §4） */
.rsb-control {
  font-size: 11px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 999px;
  white-space: nowrap;
  flex-shrink: 0;
}

.rsb-control.ctl-you {
  color: var(--success-strong);
  background: rgba(52, 199, 89, 0.14);
}

.rsb-control.ctl-other {
  color: var(--warning-strong);
  background: rgba(255, 149, 0, 0.14);
}

.rsb-control.ctl-desktop {
  color: var(--accent-strong);
  background: rgba(0, 122, 255, 0.1);
}

.rsb-conn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  font-weight: 500;
  white-space: nowrap;
  flex-shrink: 0;
}

.rsb-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.rsb-conn.cs-ok {
  color: var(--success-strong);
}
.rsb-conn.cs-ok .rsb-dot {
  background: var(--success);
}

.rsb-conn.cs-busy {
  color: var(--warning-strong);
}
.rsb-conn.cs-busy .rsb-dot {
  background: var(--warning, #ff9500);
}

.rsb-conn.cs-down {
  color: var(--danger-strong);
}
.rsb-conn.cs-down .rsb-dot {
  background: var(--danger);
}

.rsb-spacer {
  flex: 1;
  min-width: 8px;
}

.rsb-banner {
  margin: 10px 18px 0;
  flex-shrink: 0;
}

.remote-terminal > .status-banner {
  margin: 10px 18px 0;
  flex-shrink: 0;
}

/* xterm host（与 TerminalView 同款；见该文件注释） */
.term-body {
  flex: 1;
  background: var(--termBg, #1b1b1f);
  min-height: 0;
  position: relative;
  overflow: hidden;
}

.term-body :deep(.xterm) {
  height: 100%;
  width: 100%;
  padding: 0;
  box-sizing: border-box;
  text-align: left;
}

.term-body :deep(.xterm-rows) {
  font-kerning: none;
  font-variant-ligatures: none;
  font-feature-settings: "liga" 0, "calt" 0;
}

.term-body :deep(.xterm-viewport::-webkit-scrollbar-thumb) {
  background: #3a3a42;
}

.term-body :deep(.xterm-viewport) {
  background-color: var(--termBg, #1b1b1f);
}
</style>
