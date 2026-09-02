/**
 * Terminal Engine Composable
 *
 * Migrated from the legacy Terminal view (the 1178-line vetted
 * implementation, preserved in git history). Preserves the core xterm.js lifecycle in full:
 *   - three-state history decoding (string base64 / Array<number> / Uint8Array)
 *   - seq-based dedup so live chunks already contained in the history snapshot
 *     are dropped (prevents interleaving on page reload / remount)
 *   - run-token/version filtering so a restarted session's seq=1 is accepted
 *     while late output from the previous run is rejected
 *   - live-buffer + history-replay ordering
 *   - WebGL probe (skipped on macOS to avoid texture-atlas scrollback
 *     corruption in WKWebView), context-loss reconnect
 *   - LinkProvider (FILE_PATH_REGEX + OpenFileInEditor) and WebLinksAddon
 *     (BrowserOpenURL)
 *   - right-click-style copy / paste (split keyboard handler), clipboard image
 *     paste via SaveClipboardImage
 *   - xterm textarea paste interception (single write path)
 *
 * Theme colours come from the demo design tokens (var(--termBg) etc.) instead
 * of the legacy hardcoded #1a1f2e.
 *
 * RC2-5 传输接缝（T0-2 结论 B）：输出/退出/历史/输入/resize 五类 Wails 触点
 * （T1–T5）抽为可注入 TerminalTransport 适配器。默认适配器 = 现有 wailsjs
 * 调用（createLocalTerminalTransport），本地路径行为不变；远程路径由
 * RemoteTerminalView 注入 rc:* 事件 + RemoteClientTerminal* 绑定的适配器
 * （composables/remoteTerminalTransport.ts）。纯渲染内核不回退、不复制。
 */

import { ref } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
// WebGL addon 改为动态 import（见 loadWebglRenderer）：仅在非 macOS 且探测通过
// 时按需加载，退出 TerminalPageView 主 chunk。类型引用在编译期擦除，不产生静态依赖。
import type { WebglAddon } from '@xterm/addon-webgl'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'

import {
  PtyWrite,
  PtyWriteLarge,
  PtyResize,
  GetOutputHistorySnapshot,
  OpenFileInEditor,
  SaveClipboardImage,
} from '../../wailsjs/go/main/App'
import {
  EventsOn,
  BrowserOpenURL,
  ClipboardSetText,
  ClipboardGetText,
} from '../../wailsjs/runtime/runtime'
import { usePlatformCapabilities } from './usePlatformCapabilities'
import { wrapBracketedPaste } from '../utils/quickFunctions'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface TerminalInstance {
  term: Terminal
  fit: FitAddon
  webgl: WebglAddon | null
  /**
   * In-flight WebGL dynamic-import/load promise. mount / DPR refresh /
   * context-loss retry 三个入口并发触发时复用同一 Promise，不重复发起
   * 动态 import；settle 后清除（成功或失败都允许后续重试）。
   */
  webglLoadPromise: Promise<void> | null
  /** dispose fn returned by EventsOn for the pty:data:<id> stream */
  disposeDataListener: (() => void) | null
  /** dispose fn returned by EventsOn for the pty:exit:<id> stream */
  disposeExitListener: (() => void) | null
  /** capture-phase paste listener removal on the xterm textarea */
  disposePasteListener: (() => void) | null
  /** v1.3.23 scroll-jump guard：onScroll 订阅 + 容器 wheel/pointer/key 标记监听移除 */
  disposeScrollGuard: (() => void) | null
  /** detach handler for the Shift+drag forced-selection interceptor */
  disposeForcedSelection: (() => void) | null
  /**
   * Active Shift+drag cleanup. Set when a drag is in progress (window-level
   * mousemove/mouseup are attached); cleared on mouseup. If the component is
   * disposed mid-drag, disposeTerm invokes this to remove the dangling window
   * listeners rather than leaving them attached until the next mouseup.
   */
  activeDragCleanup: (() => void) | null
  lastCols: number
  lastRows: number
  /** Latest xterm geometry that the backend PTY must converge to. */
  desiredCols: number
  desiredRows: number
  /** Last geometry acknowledged by the backend. */
  acknowledgedCols: number
  acknowledgedRows: number
  /** Serialize Wails resize calls so an older response cannot win a race. */
  resizeInFlight: boolean
  /** Force one more resize even if the last acknowledged size is identical. */
  resizeForcePending: boolean
  resizeRetryTimer: number | null
  resizeFailureCount: number
  /** highest emitSeq covered by the loaded history snapshot */
  historySnapshotSeq: number
  /** current backend run identity; seq is only comparable within this pair. */
  runToken: string
  runVersion: string
  /** pending yield between history chunks; cancelled during teardown. */
  historyReplayTimer: number | null
  /** RC2-5 传输适配器（T1–T5 接缝；本地 = Wails，远程 = rc:* 事件 + 绑定）。 */
  transport: TerminalTransport
  /** 输入门（远程只读态丢弃用户输入；本地恒 true）。 */
  canInput: () => boolean
}

interface LiveChunk {
  seq: number
  bytes: Uint8Array
  runToken: string
  runVersion: string
}

export interface MountOptions {
  /** Called when the backend reports the session process exited. */
  onExit?: (info: { exitCode?: number }) => void
  /** Optional scrollback override (defaults to 100000). */
  scrollback?: number
  /**
   * Preserve the Shift modifier on Enter with a CSI-u sequence. xterm 6
   * otherwise emits the same CR for Enter and Shift+Enter.
   */
  encodeShiftEnterAsCsiU?: boolean
  /**
   * RC2-5 传输适配器（T1–T5 接缝）。缺省 = 本地 Wails 传输
   * （createLocalTerminalTransport(sessionId)），行为与改造前一致。
   */
  transport?: TerminalTransport
  /**
   * 输入门：返回 false 时用户输入（键盘/粘贴）被静默丢弃——远程只读态
   * （控制权≠你 / 未 attached）使用；缺省始终允许输入（本地路径不变）。
   */
  canInput?: () => boolean
}

// ---------------------------------------------------------------------------
// TerminalTransport（RC2-5：T1–T5 Wails 触点的可注入接缝）
// ---------------------------------------------------------------------------

/** 一帧终端输出（data 为 base64 编码字节流）。 */
export interface TerminalOutputChunk {
  seq: number
  data: string
  runToken?: string
  runVersion?: string
}

/** 会话进程退出通知。 */
export interface TerminalExitInfo {
  exitCode?: number
  runToken?: string
  runVersion?: string
}

/** 历史快照（data 兼容 string base64 / Array<number> / Uint8Array 三态）。 */
export interface TerminalHistorySnapshot {
  data: unknown
  seq?: number
  runToken?: string
  runVersion?: string
}

/**
 * 终端传输适配器：渲染内核与字节来源（本地 PTY / 远程 /ws/v1）之间的窄接口。
 * 本地实现 = 现有 wailsjs 调用上移；远程实现见 remoteTerminalTransport.ts。
 */
export interface TerminalTransport {
  /** T1 输出订阅；返回解除订阅函数。 */
  subscribeOutput(cb: (chunk: TerminalOutputChunk) => void): () => void
  /** T2 退出订阅；返回解除订阅函数。 */
  subscribeExit(cb: (info: TerminalExitInfo) => void): () => void
  /** T3 历史快照；null 表示无快照（直播-only，历史经输出帧补放）。 */
  fetchHistory(): Promise<TerminalHistorySnapshot | null>
  /** T4 发送一段终端输入文本（UTF-8）；编码/分包由传输内部负责。 */
  write(text: string): Promise<void>
  /** T4 剪贴板图片保存，返回可写入终端的路径；不支持时返回空串。 */
  saveClipboardImage(base64: string): Promise<string>
  /** T5 调整 PTY 尺寸。 */
  resize(cols: number, rows: number): Promise<void>
}

/**
 * 本地（默认）传输适配器：现有 Wails 触点原样上移，行为不变。
 */
export function createLocalTerminalTransport(sessionId: string): TerminalTransport {
  return {
    subscribeOutput(cb) {
      return EventsOn('pty:data:' + sessionId, (eventData: any) => {
        if (
          eventData &&
          typeof eventData === 'object' &&
          's' in eventData &&
          'd' in eventData
        ) {
          cb({
            seq: eventData.s as number,
            data: eventData.d as string,
            runToken: typeof eventData.r === 'string' ? eventData.r : '',
            runVersion: typeof eventData.v === 'string' ? eventData.v : '',
          })
        } else if (typeof eventData === 'string') {
          // legacy fallback without seq -> flush through after replay.
          cb({ seq: 0, data: eventData })
        }
      })
    },
    subscribeExit(cb) {
      return EventsOn('pty:exit:' + sessionId, (info: any) => {
        cb({
          exitCode: info && typeof info === 'object' ? info.exitCode : undefined,
          runToken: info && typeof info.r === 'string' ? info.r : '',
          runVersion: info && typeof info.v === 'string' ? info.v : '',
        })
      })
    },
    async fetchHistory() {
      const jsonStr = await GetOutputHistorySnapshot(sessionId)
      if (!jsonStr) return null
      try {
        return JSON.parse(jsonStr) as TerminalHistorySnapshot
      } catch (e) {
        console.warn('history replay failed:', e)
        return null
      }
    },
    async write(text) {
      const bytes = new TextEncoder().encode(text)
      const encoded = uint8ToBase64(bytes)
      // Long text uses the chunked path to avoid ConPTY buffer overflow.
      if (bytes.length > 1024) {
        await PtyWriteLarge(sessionId, encoded)
      } else {
        await PtyWrite(sessionId, encoded)
      }
    },
    saveClipboardImage(base64) {
      return SaveClipboardImage(base64)
    },
    resize(cols, rows) {
      return PtyResize(sessionId, cols, rows)
    },
  }
}

// ---------------------------------------------------------------------------
// Pure helpers (migrated verbatim from legacy Terminals.vue)
// ---------------------------------------------------------------------------

/** base64 -> Uint8Array (binary-safe; avoids atob Latin-1 issue) */
function base64ToUint8(base64: string): Uint8Array {
  const bin = atob(base64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) {
    bytes[i] = bin.charCodeAt(i)
  }
  return bytes
}

/** Uint8Array -> base64（exported：远程传输的提示条合成复用） */
export function uint8ToBase64(bytes: Uint8Array): string {
  let bin = ''
  for (let i = 0; i < bytes.length; i++) {
    bin += String.fromCharCode(bytes[i])
  }
  return btoa(bin)
}

/**
 * Decode GetOutputHistory / GetOutputHistorySnapshot returned data into a
 * Uint8Array. Handles three return shapes for maximum compatibility:
 *   - string:        base64-encoded byte stream (Wails v2 runtime binding)
 *   - Array<number>: raw byte values (declared Promise<Array<number>>)
 *   - Uint8Array:    already decoded (defensive)
 * Returns null if the data cannot be decoded so the caller can fall through to
 * live-only mode instead of silently producing garbled output.
 */
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

/** Compare non-negative decimal uint64 strings without JS number coercion. */
function compareRunVersions(left: string, right: string): number {
  const a = left.replace(/^0+(?=\d)/, '')
  const b = right.replace(/^0+(?=\d)/, '')
  if (a.length !== b.length) return a.length < b.length ? -1 : 1
  return a < b ? -1 : a > b ? 1 : 0
}

function concatBytes(left: Uint8Array, right: Uint8Array): Uint8Array {
  const result = new Uint8Array(left.length + right.length)
  result.set(left, 0)
  result.set(right, left.length)
  return result
}

// ---------------------------------------------------------------------------
// xterm theme (demo tokens, not legacy hardcoded #1a1f2e)
// ---------------------------------------------------------------------------

function buildXtermTheme() {
  return {
    background: '#1B1B1F',
    foreground: '#E6E6E6',
    cursor: '#5EA6FF',
    cursorAccent: '#1B1B1F',
    selectionBackground: '#3a4a6a',
    black: '#1B1B1F',
    red: '#ff5370',
    green: '#3BC260',
    yellow: '#ffcb6b',
    blue: '#5EA6FF',
    magenta: '#c792ea',
    cyan: '#89ddff',
    white: '#E6E6E6',
    brightBlack: '#8E8E93',
    brightRed: '#ff5370',
    brightGreen: '#3BC260',
    brightYellow: '#ffcb6b',
    brightBlue: '#5EA6FF',
    brightMagenta: '#c792ea',
    brightCyan: '#89ddff',
    brightWhite: '#ffffff',
  }
}

// ---------------------------------------------------------------------------
// Probe helpers
// ---------------------------------------------------------------------------

/**
 * Probe whether the current environment can create a WebGL context.
 * Capability check only -- the caller decides whether to actually enable the
 * WebglAddon. On macOS WKWebView the context may be creatable but xterm.js
 * WebGL texture atlas still produces scrollback corruption, so the caller
 * skips WebGL on macOS regardless of probe result.
 */
function isWebGLReliable(): boolean {
  try {
    const canvas = document.createElement('canvas')
    const gl = canvas.getContext('webgl2') || canvas.getContext('webgl')
    if (!gl) return false
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

// ---------------------------------------------------------------------------
// Composable
// ---------------------------------------------------------------------------

export function useTerminalEngine() {
  const platformCaps = usePlatformCapabilities()

  /** session id -> instance. Non-reactive: xterm holds external state. */
  const terminals = new Map<string, TerminalInstance>()

  const activeSessionId = ref<string | null>(null)

  function getTerm(sessionId: string): TerminalInstance | undefined {
    return terminals.get(sessionId)
  }

  function switchSession(sessionId: string | null) {
    activeSessionId.value = sessionId
  }

  // ---- clipboard helpers -------------------------------------------------

  async function copyToClipboard(text: string) {
    if (!text) return
    // 降级链：execCommand 同步优先 → Wails 原生 → WebView 异步 API。
    //
    // execCommand('copy') 必须放在所有 await 之前：async 函数在首个 await
    // 之前的语句与调用者（keydown 回调）同步执行，处于按键手势的同步
    // 上下文内，WebView2 / WKWebView 都允许同步复制，这是最可靠的路径。
    // 注意：transient user activation 本身约有 5 秒有效期，并非「await
    // 一发生就失效」；但在异步降级链末端再调 execCommand 时，往往已因
    // IPC 往返耗时与激活窗口推移，不再被 WebView2 视为有效用户手势，
    // 复制会被静默拒绝。
    //
    // 这正是 Windows「选中后 Ctrl+C 复制不走」的根因：旧顺序把 Wails
    // ClipboardSetText 放在第一级，而它在 WebView2 下走 OpenClipboard(0)，
    // 常被输入法 / 剪贴板工具 / WebView2 自身占用而失败（IPC 返回 false）；
    // 随即降级到 navigator.clipboard.writeText（焦点在 xterm canvas 上抛
    // NotAllowedError），再到 execCommand——此时已在异步链末端，脱离了
    // 按键的同步手势上下文，整条降级链断裂。Mac 不受影响是因为 Cmd+C
    // 走浏览器原生 copy 事件，根本不进入此 Ctrl+C handler。

    // 一级（同步，user activation 有效）：临时 textarea + execCommand('copy')。
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.left = '-9999px'
    ta.style.top = '0'
    document.body.appendChild(ta)
    let copied = false
    try {
      // 必须先 focus 再 select，否则焦点仍在 xterm canvas 上会导致
      // execCommand('copy') 复制不到内容。
      ta.focus()
      ta.select()
      copied = document.execCommand('copy')
    } catch {
      /* execCommand 已废弃或不可用：降级 */
    }
    document.body.removeChild(ta)
    if (copied) return

    // 二级：Wails 原生 ClipboardSetText（走系统剪贴板 API，不依赖 WebView
    // 焦点 / user activation）。execCommand 失败时的兜底。
    try {
      const ok = await ClipboardSetText(text)
      if (ok) return
    } catch {
      /* Wails runtime 不可用（非桌面环境）时降级 */
    }

    // 三级：WebView 异步剪贴板 API（前两级均失败时的最后兜底）。
    try {
      await navigator.clipboard.writeText(text)
    } catch {
      /* NotAllowedError 或权限拒绝：全部降级失败，忽略 */
    }
  }

  async function pasteToTerminal(sessionId: string) {
    // 此函数仅服务于右键粘贴这条主动读取路径；Ctrl+V 走 xterm textarea 的
    // paste 事件钩子（见 mountTerm 内 onPaste capture 监听），不经此函数，
    // 改动不应影响它。读剪贴板权限比写更严格，在 WebView2 里更易失败，故
    // 优先用 Wails 原生 ClipboardGetText（走 Windows API）。
    // RC2-5：写入经实例传输适配器（本地 = PtyWrite*；远程 = SendInput 绑定）。
    const inst = terminals.get(sessionId)
    if (!inst || !inst.canInput()) return
    let text = ''
    try {
      text = await ClipboardGetText()
    } catch {
      /* Wails runtime 不可用（非桌面环境）时降级 */
    }
    if (!text) {
      try {
        text = await navigator.clipboard.readText()
      } catch {
        /* 读权限拒绝：text 保持空，后续尝试图片路径 */
      }
    }
    try {
      if (text) {
        await inst.transport.write(text)
        return
      }

      // Empty text -> maybe an image on the clipboard (e.g. Windows Snipping).
      try {
        const items = await navigator.clipboard.read()
        for (const item of items) {
          for (const type of item.types) {
            if (type.startsWith('image/')) {
              const blob = await item.getType(type)
              const arrayBuf = await blob.arrayBuffer()
              const uint8 = new Uint8Array(arrayBuf)
              const b64 = uint8ToBase64(uint8)
              const filePath = await inst.transport.saveClipboardImage(b64)
              if (filePath) {
                await inst.transport.write(filePath)
              }
              return
            }
          }
        }
      } catch {
        // clipboard.read() may be unsupported / unpermitted: ignore silently.
      }
    } catch (err) {
      console.error('paste error:', err)
    }
  }

  /**
   * 向终端写入一段已组装好的文本（快捷功能「输入工作路径」等）。
   *
   * 与粘贴路径（pasteToTerminal）的差异：文本由调用方直接提供，不读剪贴板。
   * 与 sendInput 的差异：异步、可感知 bracketed paste——当 TUI 已协商
   * bracketedPasteMode（\x1b[?2004h）时用 wrapBracketedPaste 包裹写入，
   * 避免多行文本被逐行当作 Enter 提交；未协商时原文写入。
   * 写入统一经 inst.transport（远程会话走同一适配器，天然支持）。
   */
  async function insertTextToTerminal(sessionId: string, text: string) {
    const inst = terminals.get(sessionId)
    if (!inst || !inst.canInput()) return
    if (!text) return
    const payload = inst.term.modes.bracketedPasteMode
      ? wrapBracketedPaste(text)
      : text
    try {
      await inst.transport.write(payload)
      inst.term.focus()
    } catch (err) {
      console.error('[amagi-codebox] terminal insert write failed:', err)
      throw err
    }
  }

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

  // ---- WebGL renderer with context-loss reconnect ------------------------

  function loadWebglRenderer(sessionId: string, inst: TerminalInstance) {
    // 动态 import：addon-webgl 独立 chunk，仅在非 macOS 且探测通过时拉取
    // （对齐 mobile RawTerminalView 的动态 xterm 栈做法）。永不抛出——
    // 失败时回退 xterm 内置 DOM renderer。
    // 并发入口去重：同一实例同时只允许一代加载，复用 in-flight Promise。
    if (inst.webglLoadPromise) return
    inst.webglLoadPromise = (async () => {
      try {
        const { WebglAddon } = await import('@xterm/addon-webgl')
        // import 期间实例可能已销毁/替换或宿主已 detach，加载前再校验一次。
        if (terminals.get(sessionId) !== inst || !inst.term.element) return
        // 已有激活的 renderer（如 refreshRenderer 之外的路径先完成）：
        // 本代不再覆盖，避免叠加 context-loss handler 与所有权混乱。
        if (inst.webgl) return
        const webgl = new WebglAddon()
        webgl.onContextLoss(() => {
          if (inst.webgl === webgl) {
            inst.webgl.dispose()
            inst.webgl = null
          } else {
            webgl.dispose()
          }

          window.setTimeout(() => {
            // 重试前确认实例仍存活、尚无新 renderer 且没有加载在进行，
            // 避免与 DPR 刷新等入口重复加载。
            if (terminals.get(sessionId) !== inst || !inst.term.element) return
            if (inst.webgl || inst.webglLoadPromise) return
            loadWebglRenderer(sessionId, inst)
            window.setTimeout(() => fitTerminal(sessionId), 50)
          }, 500)
        })
        inst.term.loadAddon(webgl)
        inst.webgl = webgl
      } catch {
        // 本代是 in-flight 窗口内唯一可能创建 addon 的一代；失败回退 DOM
        // renderer，webgl 保持/恢复为 null，不覆盖较新状态。
        inst.webgl = null
      } finally {
        // settle 后清除 in-flight 标记，允许 context-loss / DPR 路径重试。
        inst.webglLoadPromise = null
      }
    })()
  }

  // ---- fit + resize ------------------------------------------------------

  /**
   * Send PTY resizes one at a time and always converge to the newest desired
   * geometry.  ResizeObserver, route activation and the mount fallback can
   * fire close together; issuing their IPC calls concurrently allows an older
   * request to complete last and leave a full-screen TUI several rows shorter
   * than xterm.  Transient gate/startup failures are retried instead of being
   * silently discarded.
   */
  function pumpPtyResize(sessionId: string, inst: TerminalInstance) {
    if (terminals.get(sessionId) !== inst || inst.resizeInFlight || inst.resizeRetryTimer !== null) {
      return
    }
    if (inst.desiredCols <= 0 || inst.desiredRows <= 0) return

    const alreadyAcknowledged =
      inst.acknowledgedCols === inst.desiredCols &&
      inst.acknowledgedRows === inst.desiredRows
    if (alreadyAcknowledged && !inst.resizeForcePending) return

    const cols = inst.desiredCols
    const rows = inst.desiredRows
    inst.resizeForcePending = false
    inst.resizeInFlight = true
    let failed = false

    inst.transport
      .resize(cols, rows)
      .then(() => {
        if (terminals.get(sessionId) !== inst) return
        inst.acknowledgedCols = cols
        inst.acknowledgedRows = rows
        inst.resizeFailureCount = 0
      })
      .catch((error) => {
        failed = true
        if (terminals.get(sessionId) !== inst) return
        inst.resizeForcePending = true
        inst.resizeFailureCount++
        if (inst.resizeFailureCount === 1 || inst.resizeFailureCount % 10 === 0) {
          console.warn('[amagi-codebox] PTY resize failed; retrying', {
            sessionId,
            cols,
            rows,
            error,
          })
        }
        const retryDelay = Math.min(1000, 100 * 2 ** Math.min(inst.resizeFailureCount - 1, 4))
        inst.resizeRetryTimer = window.setTimeout(() => {
          inst.resizeRetryTimer = null
          pumpPtyResize(sessionId, inst)
        }, retryDelay)
      })
      .finally(() => {
        if (terminals.get(sessionId) !== inst) return
        inst.resizeInFlight = false
        // A newer ResizeObserver result may have arrived while IPC was in
        // flight. Send it now, but leave failures to their backoff timer.
        if (!failed) pumpPtyResize(sessionId, inst)
      })
  }

  function requestPtyResize(
    sessionId: string,
    inst: TerminalInstance,
    cols: number,
    rows: number,
    force = false,
  ) {
    if (cols <= 0 || rows <= 0 || terminals.get(sessionId) !== inst) return
    const changed = cols !== inst.desiredCols || rows !== inst.desiredRows
    inst.desiredCols = cols
    inst.desiredRows = rows
    if (force) inst.resizeForcePending = true

    // A genuinely newer geometry should not wait behind backoff for a stale
    // failed size. It still cannot overtake an in-flight request.
    if (changed && inst.resizeRetryTimer !== null) {
      clearTimeout(inst.resizeRetryTimer)
      inst.resizeRetryTimer = null
    }
    pumpPtyResize(sessionId, inst)
  }

  function fitTerminal(sessionId: string, force = false) {
    const inst = terminals.get(sessionId)
    if (!inst) return
    const dims = inst.fit.proposeDimensions()
    if (!dims || dims.cols <= 0 || dims.rows <= 0) return

    const proposedSameDims = dims.cols === inst.lastCols && dims.rows === inst.lastRows
    if (proposedSameDims && !force) return

    try {
      // xterm 6 owns scrolling through its virtual .xterm-scrollable-element;
      // the legacy .xterm-viewport DOM node no longer carries scrollTop.
      // Capture the public logical buffer position before fit/renderer sync.
      const buffer = inst.term.buffer.active
      const viewportY = buffer.viewportY
      const wasAtBottom = viewportY >= buffer.baseY

      inst.fit.fit()
      // Use xterm's applied geometry rather than the pre-fit proposal. Font
      // metric changes can make those differ by one row/column.
      const cols = inst.term.cols
      const rows = inst.term.rows
      const sameDims = cols === inst.lastCols && rows === inst.lastRows
      if (!sameDims || force) {
        inst.lastCols = cols
        inst.lastRows = rows
        requestPtyResize(sessionId, inst, cols, rows, force)
      }

      // fit() and its queued renderer sync can each reset the virtual
      // scrollable surface. Restore synchronously and after both paint turns.
      const restoreViewport = () => {
        if (terminals.get(sessionId) !== inst) return
        try {
          if (wasAtBottom) {
            inst.term.scrollToBottom()
          } else {
            inst.term.scrollToLine(Math.min(viewportY, inst.term.buffer.active.baseY))
          }
        } catch {
          /* terminal may be mid-teardown */
        }
      }
      restoreViewport()
      requestAnimationFrame(() => {
        restoreViewport()
        requestAnimationFrame(restoreViewport)
      })
    } catch {
      // swallow: fit can throw transient errors during teardown
    }
  }

  function resizeTerm(sessionId: string, cols: number, rows: number) {
    const inst = terminals.get(sessionId)
    if (!inst) return
    inst.lastCols = cols
    inst.lastRows = rows
    requestPtyResize(sessionId, inst, cols, rows, true)
  }

  /** Rebuild WebGL after a DPR change, then force a complete redraw. */
  function refreshRenderer(sessionId: string) {
    const inst = terminals.get(sessionId)
    if (!inst || !inst.term.element) return

    // macOS intentionally stays on xterm 6's built-in DOM renderer. The
    // published CanvasAddon only declares compatibility with xterm 5 and its
    // stale canvas rows are the source of the WKWebView tearing regression.
    if (!platformCaps.isDarwin.value) {
      try {
        inst.webgl?.dispose()
      } catch {
        /* already disposed */
      }
      inst.webgl = null
      if (platformCaps.caps.value && isWebGLReliable()) {
        loadWebglRenderer(sessionId, inst)
      }
    }

    requestAnimationFrame(() => {
      if (terminals.get(sessionId) !== inst) return
      fitTerminal(sessionId, true)
      try {
        inst.term.clearTextureAtlas()
        inst.term.refresh(0, inst.term.rows - 1)
      } catch {
        /* noop */
      }
    })
  }

  /**
   * Re-measure and repaint the visible surface without replacing its buffer or
   * renderer. Used after route/session reactivation and OS visibility changes.
   */
  function refreshTerminal(sessionId: string) {
    const inst = terminals.get(sessionId)
    if (!inst || !inst.term.element) return
    fitTerminal(sessionId, true)
    try {
      inst.term.clearTextureAtlas()
      inst.term.refresh(0, inst.term.rows - 1)
    } catch {
      /* terminal may be mid-teardown */
    }
  }

  // ---- core mount --------------------------------------------------------

  function mountTerm(
    sessionId: string,
    containerEl: HTMLElement,
    options: MountOptions = {},
  ): TerminalInstance | null {
    if (terminals.has(sessionId)) {
      const existing = terminals.get(sessionId)!
      // Re-open on a new container if the previous element was detached.
      if (!existing.term.element) {
        try {
          existing.term.open(containerEl)
        } catch {
          /* already attached */
        }
      }
      return existing
    }

    const scrollback = options.scrollback ?? 100000

    // RC2-5 传输接缝：本地缺省适配器 = 现有 Wails 调用；远程注入 rc:* 适配器。
    const transport = options.transport ?? createLocalTerminalTransport(sessionId)
    const canInput = options.canInput ?? (() => true)
    // 单一输入出口：所有用户输入（键盘/粘贴/右键菜单）经此过输入门后发往传输。
    const sendInput = (text: string) => {
      if (!text || !canInput()) return
      transport.write(text).catch((err) => {
        console.error('[amagi-codebox] terminal input write failed:', err)
      })
    }

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      scrollback,
      fontFamily:
        "'SF Mono','JetBrains Mono','Cascadia Code','Consolas','Courier New',monospace",
      // xterm disables its selection layer while a TUI enables mouse reporting.
      // Windows keeps a built-in Shift+drag escape hatch; macOS needs this so
      // Option+drag can force selection without globally intercepting drags.
      macOptionClickForcesSelection: true,
      ...(platformCaps.isWindows.value
        ? { windowsPty: { backend: 'conpty' as const, buildNumber: 19041 } }
        : {}),
      theme: buildXtermTheme(),
      allowProposedApi: true,
    })

    const fit = new FitAddon()
    term.loadAddon(fit)

    const csiUShiftEnter = '\x1b[13;2u'
    let rewriteNextEnterAsCsiU = false

    // ----- keyboard: copy / paste / select-all / delete-selection -----
    term.attachCustomKeyEventHandler((ev: KeyboardEvent) => {
      if (ev.type !== 'keydown') return true

      // Pi/OMP negotiate extended keyboard input, but xterm 6 ignores both
      // Kitty keyboard protocol and modifyOtherKeys. Mark the next CR emitted
      // by xterm so the normal onData path can preserve the Shift modifier
      // without sending both the replacement and xterm's original CR.
      if (
        options.encodeShiftEnterAsCsiU &&
        ev.shiftKey &&
        !ev.ctrlKey &&
        !ev.altKey &&
        !ev.metaKey &&
        !ev.isComposing &&
        ev.key === 'Enter'
      ) {
        rewriteNextEnterAsCsiU = true
        window.setTimeout(() => {
          rewriteNextEnterAsCsiU = false
        }, 0)
        return true
      }

      if (ev.ctrlKey && ev.shiftKey && ev.code === 'KeyC') {
        copySelection(sessionId)
        return false
      }
      if (ev.ctrlKey && ev.shiftKey && ev.code === 'KeyV') {
        pasteToTerminal(sessionId)
        return false
      }
      if (ev.ctrlKey && ev.shiftKey && ev.code === 'KeyA') {
        term.selectAll()
        return false
      }

      // Delete / Backspace with selection -> emit equal-length backspaces.
      if (
        !ev.ctrlKey &&
        !ev.shiftKey &&
        !ev.altKey &&
        (ev.code === 'Backspace' || ev.code === 'Delete')
      ) {
        const sel = term.getSelection()
        if (sel && sel.length > 0) {
          const bsChars = '\b'.repeat(sel.length)
          sendInput(bsChars)
          term.clearSelection()
          return false
        }
      }

      // Ctrl+C: copy if selection present, otherwise forward SIGINT to PTY.
      if (ev.ctrlKey && !ev.shiftKey && ev.code === 'KeyC') {
        const sel = term.getSelection()
        if (sel) {
          copyToClipboard(sel)
          term.clearSelection()
          return false
        }
        return true
      }

      // Ctrl+V: block ^V; actual paste handled by the textarea paste hook.
      if (ev.ctrlKey && !ev.shiftKey && ev.code === 'KeyV') {
        return false
      }

      // macOS Option+key → ESC-prefixed sequence, so terminal apps receive real
      // Alt bindings (pi/amagi ⌥W board, ⌥T tasks, ⌥R review). Without this,
      // xterm (macOptionIsMeta off by default) types special characters instead.
      // Scope: single-char keys + Backspace (word-delete); arrows/compose/dead
      // keys and Cmd/Ctrl combos untouched. Same pattern as CSI-u rewrite above.
      if (
        !ev.ctrlKey &&
        !ev.metaKey &&
        ev.altKey &&
        !ev.isComposing &&
        ev.key !== 'Dead' &&
        ev.key !== 'Alt' &&
        !ev.key.startsWith('Arrow')
      ) {
        let seq: string | null = null
        if (ev.key === 'Backspace') seq = '\x1b\x7f'
        else if (ev.key.length === 1) seq = '\x1b' + ev.key
        if (seq) {
          sendInput(seq)
          return false
        }
      }

      return true
    })

    // ----- scroll-jump guard -----
    // xterm 6 的虚拟滚动面在 renderer 维度抖动（DPR/fit/WKWebView 滚动条重排）时，
    // 内部 ScrollState 钳制可能把 scrollTop 瞬时归 0，且 _sync 在 ydisp 未变时
    // 不恢复位置——表现为"偶尔跳到最顶部"。无法从 xterm 内部根治，行为级治理：
    // 区分用户主动滚动（滚轮/滚动条/翻页键）与非用户意图跳顶；后者发生时，
    // 若此前视口贴底（跟随最新输出），自动滚回底部。
    let prevAtBottom = true
    let userScrollUntil = 0
    const markUserScroll = () => {
      userScrollUntil = performance.now() + 800
    }
    containerEl.addEventListener('wheel', markUserScroll, { capture: true, passive: true })
    containerEl.addEventListener('pointerdown', markUserScroll, { capture: true, passive: true })
    const markUserScrollKeydown = (ev: KeyboardEvent) => {
      // 33/34 PageUp/PageDown, 35/36 End/Home, 38/40 ↑/↓
      if ([33, 34, 35, 36, 38, 40].includes(ev.keyCode)) markUserScroll()
    }
    containerEl.addEventListener('keydown', markUserScrollKeydown, {
      capture: true,
      passive: true,
    })
    let guardBusy = false
    const disposeScrollGuard = term.onScroll(() => {
      if (guardBusy) return
      const buffer = term.buffer.active
      const atBottom = buffer.viewportY >= buffer.baseY
      const spurious =
        performance.now() > userScrollUntil &&
        prevAtBottom &&
        !atBottom &&
        buffer.baseY >= term.rows * 2 &&
        buffer.viewportY === 0
      if (spurious) {
        // 延迟到当前滚动事务外恢复，避免在 onScroll 回调内重入
        guardBusy = true
        window.setTimeout(() => {
          guardBusy = false
          try {
            term.scrollToBottom()
          } catch {
            /* mid-teardown */
          }
        }, 0)
        return
      }
      prevAtBottom = atBottom
    })

    // user input -> transport（本地 = 后端 PTY；远程 = /ws/v1 outbox）
    term.onData((data: string) => {
      let forwardedData = data
      if (rewriteNextEnterAsCsiU) {
        rewriteNextEnterAsCsiU = false
        if (data === '\r') forwardedData = csiUShiftEnter
      }
      sendInput(forwardedData)
    })

    // ----- live output buffering until history replay completes -----
    const liveBuffer: LiveChunk[] = []
    let historyReplayed = false

    // forward-declared so both flushLiveBuffer and the post-replay path route
    // through the same seq-dedup gate.
    const inst: TerminalInstance = {
      term,
      fit,
      webgl: null,
      webglLoadPromise: null,
      disposeDataListener: null,
      disposeExitListener: null,
      disposePasteListener: null,
      disposeScrollGuard: () => {
        disposeScrollGuard?.dispose?.()
        const el = term.element?.parentElement ?? containerEl
        el.removeEventListener('wheel', markUserScroll, true)
        el.removeEventListener('pointerdown', markUserScroll, true)
        el.removeEventListener('keydown', markUserScrollKeydown, true)
      },
      disposeForcedSelection: null,
      activeDragCleanup: null,
      lastCols: 0,
      lastRows: 0,
      desiredCols: 0,
      desiredRows: 0,
      acknowledgedCols: 0,
      acknowledgedRows: 0,
      resizeInFlight: false,
      resizeForcePending: false,
      resizeRetryTimer: null,
      resizeFailureCount: 0,
      historySnapshotSeq: 0,
      runToken: '',
      runVersion: '',
      historyReplayTimer: null,
      transport,
      canInput,
    }
    terminals.set(sessionId, inst)

    // seq-based dedup: any live chunk with seq <= snapshot seq is already in
    // the history bytes -> skip it. Both the flush path and the direct path
    // go through here so dedup is never bypassed.
    // xterm's WriteBuffer already batches and time-slices parser work. Adding
    // an outer requestAnimationFrame queue makes delivery depend on the WebView
    // paint loop and can leave PTY bytes stranded when that loop is throttled.
    function prepareLiveChunk(chunk: LiveChunk): Uint8Array | null {
      const tagged = chunk.runToken !== '' || chunk.runVersion !== ''
      const sameRun =
        chunk.runToken === inst.runToken && chunk.runVersion === inst.runVersion

      if (tagged && !sameRun) {
        if (inst.runToken === '' && inst.runVersion === '') {
          // Tagged live-only fallback (snapshot unavailable): establish the
          // first observed run as the local boundary.
          inst.runToken = chunk.runToken
          inst.runVersion = chunk.runVersion
          inst.historySnapshotSeq = 0
        } else {
          const versionOrder = compareRunVersions(chunk.runVersion, inst.runVersion)
          // Older run, or a conflicting token at the same version: fail closed.
          if (versionOrder <= 0) return null

          // Same session, newer run. emitSeq restarts at 1, so reset the dedup
          // frontier before accepting bytes from the replacement process.
          inst.runToken = chunk.runToken
          inst.runVersion = chunk.runVersion
          inst.historySnapshotSeq = 0
          const boundary = new TextEncoder().encode(
            '\r\n\x1b[90m[amagi-codebox] 会话已进入新的运行\x1b[0m\r\n',
          )
          return concatBytes(boundary, chunk.bytes)
        }
      }

      if (chunk.seq > 0 && chunk.seq <= inst.historySnapshotSeq) return null
      return chunk.bytes
    }

    function writeLiveChunk(chunk: LiveChunk): boolean {
      const bytes = prepareLiveChunk(chunk)
      if (!bytes || terminals.get(sessionId) !== inst) return false
      try {
        inst.term.write(bytes)
        return true
      } catch {
        /* term may be mid-teardown */
        return false
      }
    }

    const disposeDataListener = transport.subscribeOutput((out) => {
      try {
        const bytes = base64ToUint8(out.data)
        const chunk: LiveChunk = {
          seq: out.seq,
          bytes,
          runToken: out.runToken ?? '',
          runVersion: out.runVersion ?? '',
        }
        if (!historyReplayed) {
          liveBuffer.push(chunk)
          return
        }
        writeLiveChunk(chunk)
      } catch (err) {
        console.error('decode error:', err)
      }
    })

    const disposeExitListener = transport.subscribeExit((info) => {
      let message = '\r\n\x1b[33m[amagi-codebox] 进程已退出'
      if (info && info.exitCode !== undefined) {
        message += ` (exit code: ${info.exitCode})`
      }
      const marker = new TextEncoder().encode(message + '\x1b[0m\r\n')
      const chunk: LiveChunk = {
        seq: 0,
        bytes: marker,
        runToken: info && typeof info.runToken === 'string' ? info.runToken : '',
        runVersion: info && typeof info.runVersion === 'string' ? info.runVersion : '',
      }
      // Exit can race the history snapshot. Keep its marker behind the same
      // replay barrier so it cannot appear in the middle of historical ANSI.
      if (historyReplayed) writeLiveChunk(chunk)
      else liveBuffer.push(chunk)
      options.onExit?.({ exitCode: info?.exitCode })
    })

    inst.disposeDataListener = disposeDataListener
    inst.disposeExitListener = disposeExitListener

    // open into DOM then attach addons + replay history + wire paste.
    try {
      term.open(containerEl)
    } catch (err) {
      console.error('[amagi-codebox] xterm open failed:', err)
    }

    // Establish the final rows/cols before an accelerated renderer is loaded.
    // Loading WebGL against xterm's default 80x24 dimensions and fitting only
    // afterwards creates a transient backing store with the wrong geometry.
    try {
      fit.fit()
      const cols = term.cols
      const rows = term.rows
      if (cols > 0 && rows > 0) {
        inst.lastCols = cols
        inst.lastRows = rows
        requestPtyResize(sessionId, inst, cols, rows, true)
      }
    } catch {
      /* outer mount frame will retry once layout is measurable */
    }

    // WebLinksAddon: detect HTTP/HTTPS URLs in output, open with system browser.
    try {
      const webLinks = new WebLinksAddon((_event: MouseEvent, uri: string) => {
        BrowserOpenURL(uri)
      })
      term.loadAddon(webLinks)
    } catch (e) {
      console.warn('WebLinksAddon failed to load', e)
    }

    // Custom file-path LinkProvider: detect file paths (with optional line
    // number) in output and open them in the editor via the backend.
    try {
      // Require a path separator to avoid matching bare filenames / versions.
      // Matches: src/main.ts:42  ./lib/util.go:10:5  C:\path\to\file.go:100
      const FILE_PATH_REGEX =
        /(?:[A-Za-z]:[/]|[.][/])(?:[\w.-]+[/])*[\w.-]+\.[a-zA-Z]{1,10}(?::(\d+)(?::\d+)?)?|(?:[/]|(?:[\w.-]+[/]){1,})(?:[\w.-]+[/])*[\w.-]+\.[a-zA-Z]{1,10}(?::(\d+)(?::\d+)?)?/g

      term.registerLinkProvider({
        provideLinks(bufferLineNumber: number, callback: (links: any[]) => void) {
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
            const filePath = lineNum
              ? fullMatch.slice(0, fullMatch.lastIndexOf(':' + match[1]))
              : fullMatch

            if (filePath.length < 3 || !/[./\\]/.test(filePath)) continue
            // URLs already handled by WebLinksAddon.
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
                // tooltip via xterm default title mechanism
              },
            })
          }
          callback(links)
        },
      })
    } catch (e) {
      console.warn('registerLinkProvider failed', e)
    }

    // macOS WKWebView uses xterm 6's built-in DOM renderer. The available
    // CanvasAddon is a beta package whose peer range is xterm 5 only; loading
    // it into xterm 6 produced stale rows and torn canvases. Windows/Linux keep
    // the compatible WebGL accelerator and fail open to DOM if it cannot load.
    if (platformCaps.caps.value && !platformCaps.isDarwin.value && isWebGLReliable()) {
      loadWebglRenderer(sessionId, inst)
    }

    // One post-layout repaint covers delayed CSS/font layout without replacing
    // the renderer while parser writes may still be queued.
    requestAnimationFrame(() => {
      if (terminals.get(sessionId) !== inst) return
      refreshTerminal(sessionId)
    })

    // ----- history replay -----
    // M1 atomic boundary: snapshot returns {data, seq} where seq is the
    // backend's monotonic emitSeq at snapshot time; any live event with
    // seq <= snapshot seq is already in the history bytes.
    // M2 type compatibility: decodeHistoryData handles string / Array<number>
    // / Uint8Array return shapes.
    //
    // xterm's write() only enqueues parser work. A replay chunk is complete
    // exclusively when its callback fires; treating the method return as
    // completion interleaves history, live output and renderer replacement.
    // Serialize chunks through callbacks and yield with a timer so parsing is
    // independent of whether the WebView currently grants animation frames.
    const HISTORY_CHUNK_SIZE = 64 * 1024
    function writeHistoryInChunks(decoded: Uint8Array, done: () => void) {
      let offset = 0
      const total = decoded.length
      function writeNextChunk() {
        if (terminals.get(sessionId) !== inst) return
        const end = Math.min(offset + HISTORY_CHUNK_SIZE, total)
        try {
          inst.term.write(decoded.subarray(offset, end), () => {
            if (terminals.get(sessionId) !== inst) return
            offset = end
            if (offset < total) {
              inst.historyReplayTimer = window.setTimeout(() => {
                inst.historyReplayTimer = null
                writeNextChunk()
              }, 0)
            } else {
              done()
            }
          })
        } catch {
          /* term mid-teardown */
        }
      }
      writeNextChunk()
    }

    function finishHistoryReplay() {
      if (terminals.get(sessionId) !== inst) return
      historyReplayed = true
      flushLiveBuffer(() => {
        if (terminals.get(sessionId) !== inst) return
        try {
          inst.term.scrollToBottom()
          inst.term.clearTextureAtlas()
          inst.term.refresh(0, inst.term.rows - 1)
        } catch {
          /* viewport may be tearing down */
        }
      })
    }

    // T3：历史快照经传输获取（本地 = GetOutputHistorySnapshot；远程 = null，
    // 历史由 Go 侧 attach 回放经输出事件补放）。
    transport
      .fetchHistory()
      .then((snapshot) => {
        if (!snapshot) {
          finishHistoryReplay()
          return
        }
        inst.runToken = typeof snapshot.runToken === 'string' ? snapshot.runToken : ''
        inst.runVersion = typeof snapshot.runVersion === 'string' ? snapshot.runVersion : ''
        const decoded = decodeHistoryData(snapshot.data)
        if (decoded && decoded.length > 0) {
          inst.historySnapshotSeq = snapshot.seq || 0
          writeHistoryInChunks(decoded, finishHistoryReplay)
          return
        } else if (decoded !== null && decoded.length === 0) {
          // decodeHistoryData returned empty: data valid but empty.
          // Snapshot is authoritative (seq valid) -> set boundary.
          inst.historySnapshotSeq = snapshot.seq || 0
        }
        // decoded === null -> decode failed: leave seq at 0 so buffered
        // live chunks flush through without being discarded.
        finishHistoryReplay()
      })
      .catch(() => {
        // Session may not support history (e.g. already exited): flush live.
        finishHistoryReplay()
      })

    function flushLiveBuffer(done: () => void) {
      const accepted: Uint8Array[] = []
      for (const chunk of liveBuffer) {
        const bytes = prepareLiveChunk(chunk)
        if (bytes) accepted.push(bytes)
      }
      liveBuffer.length = 0
      if (accepted.length === 0) {
        done()
        return
      }
      let total = 0
      for (const bytes of accepted) total += bytes.length
      const merged = new Uint8Array(total)
      let offset = 0
      for (const bytes of accepted) {
        merged.set(bytes, offset)
        offset += bytes.length
      }
      try {
        // The callback is the ordering barrier: only repaint after all bytes
        // buffered during the snapshot request have actually been parsed.
        inst.term.write(merged, done)
      } catch {
        done()
      }
    }

    // Refit once all web fonts have loaded. The initial cell measurement
    // (done synchronously during term.open) may use a fallback font if the
    // primary font ('SF Mono' etc.) hasn't finished loading yet. When the
    // real font renders at a different width, cell geometry and mouse mapping
    // drift. Refit and request a complete public-API redraw once fonts settle.
    if (document.fonts && document.fonts.ready) {
      document.fonts.ready
        .then(() => {
          if (terminals.get(sessionId) !== inst) return
          refreshTerminal(sessionId)
        })
        .catch(() => {
          /* fonts.ready rejected (rare): ignore */
        })
    }

    // ----- xterm textarea paste interception (capture phase) -----
    // Ensures Ctrl+V / right-click paste take a single path, avoiding the
    // double-write that xterm's built-in onData would otherwise cause.
    const textarea = containerEl.querySelector('textarea')
    if (textarea) {
      const onPaste = (e: Event) => {
        e.preventDefault()
        e.stopImmediatePropagation()
        const clipEvent = e as ClipboardEvent
        const text = clipEvent.clipboardData?.getData('text') ?? ''
        if (text) {
          sendInput(text)
        } else {
          // Empty text -> check for image files (e.g. Windows Snipping Tool).
          const files = clipEvent.clipboardData?.files
          if (files && files.length > 0) {
            const file = files[0]
            if (file.type.startsWith('image/')) {
              file
                .arrayBuffer()
                .then((buf) => {
                  const uint8 = new Uint8Array(buf)
                  const b64 = uint8ToBase64(uint8)
                  transport
                    .saveClipboardImage(b64)
                    .then((filePath) => {
                      if (filePath) sendInput(filePath)
                    })
                    .catch(() => {})
                })
                .catch(() => {})
            }
          }
        }
      }
      textarea.addEventListener('paste', onPaste, true /* capture */)
      inst.disposePasteListener = () => {
        textarea.removeEventListener('paste', onPaste, true)
      }
    }

    // Windows/Linux: Shift+drag forced selection (macOS uses native
    // Option+drag via macOptionClickForcesSelection). Attached inside the
    // composable so it's wired up the same way for every mount point.
    inst.disposeForcedSelection = attachForcedSelection(sessionId, containerEl)

    return inst
  }

  function writeInput(sessionId: string, data: string) {
    const inst = terminals.get(sessionId)
    if (!inst || !inst.canInput()) return
    inst.transport.write(data).catch((err) => {
      console.error('[amagi-codebox] terminal input write failed:', err)
    })
  }

  /**
   * 写一条本地提示行（不经传输、不入 PTY）：远程会话的 restart 边界等
   * 状态提示用（RC2-5）。
   */
  function writeLocalEcho(sessionId: string, text: string) {
    const inst = terminals.get(sessionId)
    if (!inst) return
    try {
      inst.term.write(new TextEncoder().encode(text))
    } catch {
      /* term may be mid-teardown */
    }
  }

  function disposeTerm(sessionId: string) {
    const inst = terminals.get(sessionId)
    if (!inst) return

    inst.disposeDataListener?.()
    inst.disposeDataListener = null
    inst.disposeExitListener?.()
    inst.disposeExitListener = null
    inst.disposePasteListener?.()
    inst.disposePasteListener = null
    inst.disposeScrollGuard?.()
    inst.disposeScrollGuard = null
    inst.disposeForcedSelection?.()
    inst.disposeForcedSelection = null

    // If a Shift+drag is in-flight (window mousemove/mouseup attached),
    // detach those listeners now — otherwise they would linger on window
    // until the next mouseup, holding a reference to this disposed inst.
    inst.activeDragCleanup?.()
    inst.activeDragCleanup = null

    if (inst.historyReplayTimer !== null) {
      clearTimeout(inst.historyReplayTimer)
      inst.historyReplayTimer = null
    }
    if (inst.resizeRetryTimer !== null) {
      clearTimeout(inst.resizeRetryTimer)
      inst.resizeRetryTimer = null
    }

    try {
      inst.term.dispose()
    } catch {
      /* already disposed */
    }
    terminals.delete(sessionId)

    if (activeSessionId.value === sessionId) {
      activeSessionId.value = null
    }
  }

  function disposeAll() {
    // copy keys because disposeTerm mutates the map during iteration
    const ids = Array.from(terminals.keys())
    for (const id of ids) {
      disposeTerm(id)
    }
  }

  // ---- forced selection (Shift+drag on Windows/Linux) -------------------
  //
  // opencode TUI enables SGR/1006 mouse reporting, which makes xterm forward
  // mouse events to the PTY and disables its own selection layer. macOS has
  // a built-in escape hatch via `macOptionClickForcesSelection: true`
  // (Option+drag); Windows/Linux need an equivalent. xterm.js 6 doesn't
  // expose a public API to force selection under TUI mouse mode, so we
  // synthesize one with term.select() and pure-DOM geometry:
  //
  //   1. On Shift+mousedown inside the xterm viewport, prevent the event
  //      from reaching xterm (capture phase + stopImmediatePropagation) so
  //      the TUI never sees the drag.
  //   2. Convert the mouse coordinates to {col, row} using the rendered
  //      .xterm-screen pixel size and term.cols / term.rows (cell width and
  //      height derived from the live layout, not from private APIs).
  //   3. On mousemove, recompute the end {col, row} and call term.select()
  //      with the length between start and end in the buffer's flat row
  //      coordinate space.
  //   4. On mouseup, release the capture and let the existing Delete/
  //      Ctrl+C copy path observe term.getSelection().
  //
  // Bound on the container (not the textarea) so it works regardless of
  // focus. Only triggers when the Shift modifier is held on non-Darwin
  // platforms; macOS keeps using the native Option+drag path.
  function attachForcedSelection(
    sessionId: string,
    containerEl: HTMLElement,
  ): () => void {
    if (platformCaps.isDarwin.value) {
      // macOS already has Option+drag via macOptionClickForcesSelection.
      return () => {}
    }

    const onMouseDown = (ev: MouseEvent) => {
      if (!ev.shiftKey) return
      const inst = terminals.get(sessionId)
      if (!inst) return
      const viewport = containerEl.querySelector('.xterm-viewport') as HTMLElement | null
      const screen = containerEl.querySelector('.xterm-screen') as HTMLElement | null
      if (!viewport || !screen) return

      // Stop the event before xterm's mouse service sees it. Capture-phase
      // listener + stopImmediatePropagation is the only reliable way to
      // preempt xterm's own listeners (which sit on the same element).
      ev.preventDefault()
      ev.stopImmediatePropagation()

      const rect = screen.getBoundingClientRect()
      const cols = inst.term.cols || 80
      const rows = inst.term.rows || 24
      const cellWidth = rect.width / cols
      const cellHeight = rect.height / rows
      if (cellWidth <= 0 || cellHeight <= 0) return

      // Account for the viewport's current scroll offset so dragging on a
      // scrolled-up view still maps to the correct buffer row.
      const scrollTop = viewport.scrollTop
      const startCol = Math.max(0, Math.min(cols - 1, Math.floor((ev.clientX - rect.left) / cellWidth)))
      const startRow = Math.max(0, Math.floor((ev.clientY - rect.top + scrollTop) / cellHeight))

      let endCol = startCol
      let endRow = startRow

      const applySelection = () => {
        const buffer = inst.term.buffer.active
        const startRowBase = buffer.baseY - (inst.term.rows - 1) + startRow
        const endRowBase = buffer.baseY - (inst.term.rows - 1) + endRow
        // Normalise so the smaller coordinate is always the anchor.
        const lo = { row: Math.min(startRowBase, endRowBase), col: Math.min(startCol, endCol) }
        const hi = { row: Math.max(startRowBase, endRowBase), col: endRow === startRow && endCol < startCol ? startCol : Math.max(startCol, endCol) }
        // term.select(column, row, length) — length spans the flat
        // col/row rectangle, including trailing cells on shorter lines.
        const length = (hi.row - lo.row) * cols + (hi.col - lo.col) + 1
        try {
          inst.term.select(lo.col, lo.row, Math.max(1, length))
        } catch {
          /* selection out of bounds during rapid drag — ignore */
        }
      }
      applySelection()

      const onMouseMove = (moveEv: MouseEvent) => {
        endCol = Math.max(0, Math.min(cols - 1, Math.floor((moveEv.clientX - rect.left) / cellWidth)))
        endRow = Math.max(0, Math.floor((moveEv.clientY - rect.top + scrollTop) / cellHeight))
        applySelection()
      }
      const onMouseUp = () => {
        window.removeEventListener('mousemove', onMouseMove)
        window.removeEventListener('mouseup', onMouseUp)
        // Drag finished normally — drop the cleanup reference so disposeTerm
        // doesn't try to remove listeners that are already gone.
        inst.activeDragCleanup = null
      }
      window.addEventListener('mousemove', onMouseMove)
      window.addEventListener('mouseup', onMouseUp)
      // Track the active drag's window listeners on the instance so that
      // disposeTerm can tear them down if the component unmounts mid-drag
      // (e.g. user switches session while dragging). Without this, the
      // window mousemove/mouseup would linger until the next mouseup
      // somewhere, keeping a disposed inst alive in closure.
      inst.activeDragCleanup = () => {
        window.removeEventListener('mousemove', onMouseMove)
        window.removeEventListener('mouseup', onMouseUp)
        inst.activeDragCleanup = null
      }
    }

    // Capture phase is essential: xterm registers its own mousedown listener
    // on the same element in bubble phase, so we must intercept first.
    containerEl.addEventListener('mousedown', onMouseDown, true)
    return () => {
      containerEl.removeEventListener('mousedown', onMouseDown, true)
    }
  }

  return {
    activeSessionId,
    terminals,
    mountTerm,
    writeInput,
    writeLocalEcho,
    resizeTerm,
    fitTerminal,
    refreshTerminal,
    refreshRenderer,
    disposeTerm,
    disposeAll,
    getTerm,
    switchSession,
    attachForcedSelection,
    // exposed for the right-click menu component (TerminalContextMenu)
    copySelection,
    pasteToTerminal,
    // 快捷功能（输入工作路径等）的文本插入出口
    insertTextToTerminal,
  }
}
