/**
 * Terminal Engine Composable
 *
 * Migrated from src_legacy_backup/views/Terminals.vue (the 1178-line vetted
 * implementation). Preserves the core xterm.js lifecycle in full:
 *   - three-state history decoding (string base64 / Array<number> / Uint8Array)
 *   - seq-based dedup so live chunks already contained in the history snapshot
 *     are dropped (prevents interleaving on page reload / remount)
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
 */

import { ref } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import { CanvasAddon } from '@xterm/addon-canvas'
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

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface TerminalInstance {
  term: Terminal
  fit: FitAddon
  webgl: WebglAddon | null
  /** canvas renderer addon (macOS middle-tier between WebGL and DOM) */
  canvas: CanvasAddon | null
  /** dispose fn returned by EventsOn for the pty:data:<id> stream */
  disposeDataListener: (() => void) | null
  /** dispose fn returned by EventsOn for the pty:exit:<id> stream */
  disposeExitListener: (() => void) | null
  /** capture-phase paste listener removal on the xterm textarea */
  disposePasteListener: (() => void) | null
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
  /** highest emitSeq covered by the loaded history snapshot */
  historySnapshotSeq: number
  /** micro-batch scheduler token for writeLiveChunk (rAF coalesce). */
  liveBatchRaf: number | null
  /** accumulated chunks awaiting the next animation frame. */
  liveBatchQueue: LiveChunk[]
}

interface LiveChunk {
  seq: number
  bytes: Uint8Array
}

export interface MountOptions {
  /** Called when the backend reports the session process exited. */
  onExit?: (info: { exitCode?: number }) => void
  /** Optional scrollback override (defaults to 100000). */
  scrollback?: number
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

/** Uint8Array -> base64 */
function uint8ToBase64(bytes: Uint8Array): string {
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
        const bytes = new TextEncoder().encode(text)
        const encoded = uint8ToBase64(bytes)
        // Long text uses the chunked path to avoid ConPTY buffer overflow.
        if (bytes.length > 1024) {
          await PtyWriteLarge(sessionId, encoded)
        } else {
          await PtyWrite(sessionId, encoded)
        }
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
        // clipboard.read() may be unsupported / unpermitted: ignore silently.
      }
    } catch (err) {
      console.error('paste error:', err)
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

  /**
   * Load the canvas renderer as a middle-tier between WebGL and the default
   * DOM renderer. Used on macOS where the WebGL texture-atlas still
   * corrupts scrollback in WKWebView; canvas avoids the GPU texture path
   * while still rendering into a single <canvas> (far cheaper than reflowing
   * the DOM on every ANSI redraw from opencode TUI).
   *
   * Failures fall through silently — xterm keeps its default renderer.
   */
  function loadCanvasRenderer(sessionId: string, inst: TerminalInstance) {
    try {
      // CanvasAddon (0.8.0-beta.48) does not expose onContextLoss like
      // WebglAddon; if the underlying canvas context is lost, xterm's
      // renderer registry will fall back to its default DOM renderer on
      // the next render pass. We rely on the try/catch around loadAddon
      // to swallow constructor-time failures.
      const canvas = new CanvasAddon()
      inst.term.loadAddon(canvas)
      inst.canvas = canvas
    } catch (e) {
      console.warn('[amagi-codebox] canvas renderer load failed:', e)
      inst.canvas = null
    }
  }

  // ---- fit + resize ------------------------------------------------------

  function fitTerminal(sessionId: string, force = false, containerEl?: HTMLElement) {
    const inst = terminals.get(sessionId)
    if (!inst) return
    const dims = inst.fit.proposeDimensions()
    if (!dims || dims.cols <= 0 || dims.rows <= 0) return

    const sameDims = dims.cols === inst.lastCols && dims.rows === inst.lastRows
    if (sameDims && !force) return

    try {
      // Preserve user scroll position when not at the bottom: fit.fit() can
      // momentarily jump the viewport, so we restore it on the next frame.
      const viewport =
        containerEl?.querySelector('.xterm-viewport') as HTMLElement | null
      const scrollTop = viewport?.scrollTop ?? 0
      const isAtBottom = viewport
        ? viewport.scrollTop + viewport.clientHeight >= viewport.scrollHeight - 2
        : true

      inst.fit.fit()
      if (!sameDims) {
        inst.lastCols = dims.cols
        inst.lastRows = dims.rows
        PtyResize(sessionId, dims.cols, dims.rows).catch(() => {})
      }

      if (!isAtBottom && viewport) {
        requestAnimationFrame(() => {
          viewport.scrollTop = scrollTop
        })
      }
    } catch {
      // swallow: fit can throw transient errors during teardown
    }
  }

  function resizeTerm(sessionId: string, cols: number, rows: number) {
    const inst = terminals.get(sessionId)
    if (!inst) return
    inst.lastCols = cols
    inst.lastRows = rows
    PtyResize(sessionId, cols, rows).catch(() => {})
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

    // ----- keyboard: copy / paste / select-all / delete-selection -----
    term.attachCustomKeyEventHandler((ev: KeyboardEvent) => {
      if (ev.type !== 'keydown') return true

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
          const bytes = new TextEncoder().encode(bsChars)
          PtyWrite(sessionId, uint8ToBase64(bytes)).catch(() => {})
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

      return true
    })

    // user input -> backend PTY
    term.onData((data: string) => {
      const bytes = new TextEncoder().encode(data)
      const encoded = uint8ToBase64(bytes)
      PtyWrite(sessionId, encoded).catch((err) => {
        console.error('PtyWrite error:', err)
      })
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
      canvas: null,
      disposeDataListener: null,
      disposeExitListener: null,
      disposePasteListener: null,
      disposeForcedSelection: null,
      activeDragCleanup: null,
      lastCols: 0,
      lastRows: 0,
      historySnapshotSeq: 0,
      liveBatchRaf: null,
      liveBatchQueue: [],
    }
    terminals.set(sessionId, inst)

    // seq-based dedup: any live chunk with seq <= snapshot seq is already in
    // the history bytes -> skip it. Both the flush path and the direct path
    // go through here so dedup is never bypassed.
    //
    // Micro-batch: PTY data events arrive in bursts (a single opencode TUI
    // redraw splits into dozens of base64 chunks within ~16ms). Writing each
    // chunk synchronously forces the renderer to schedule a paint per chunk,
    // which is the dominant cause of tearing under the DOM/canvas renderer.
    // Coalesce all chunks arriving inside one animation frame into a single
    // term.write() call, preserving order and never dropping bytes.
    function writeLiveChunk(seq: number, bytes: Uint8Array) {
      if (seq > 0 && seq <= inst.historySnapshotSeq) return
      // If a flush is already scheduled, just append; otherwise seed + rAF.
      inst.liveBatchQueue.push({ seq, bytes })
      if (inst.liveBatchRaf !== null) return
      inst.liveBatchRaf = requestAnimationFrame(() => {
        inst.liveBatchRaf = null
        // Instance-identity guard: if the session was disposed (or switched
        // to a new instance) between scheduling and firing, accessing
        // inst.term would hit a disposed terminal. Mirrors the guard already
        // present in writeHistoryInChunks. try/catch below still backs us up,
        // but this avoids touching a dead term entirely.
        if (terminals.get(sessionId) !== inst) {
          inst.liveBatchQueue.length = 0
          return
        }
        const queue = inst.liveBatchQueue
        if (queue.length === 0) return
        inst.liveBatchQueue = []
        // Merge queued chunks into one Uint8Array so the renderer sees a
        // single write (one paint, one reflow). Order is preserved.
        let total = 0
        for (const c of queue) total += c.bytes.length
        const merged = new Uint8Array(total)
        let offset = 0
        for (const c of queue) {
          merged.set(c.bytes, offset)
          offset += c.bytes.length
        }
        try {
          // 写入即可，不手动 scrollToBottom：xterm 默认的 isUserScrolling
          // 保护已经实现"用户在底部时跟随新输出、用户上翻时不跟随"。
          // v1.2.67 在此显式 scrollToBottom，绕过了 isUserScrolling，
          // 在 TUI(opencode/Claude Code)启用鼠标 SGR 模式后 wheel 被转发给
          // PTY、.xterm-viewport.scrollTop 不变、userScrolledUp 永远 false，
          // 导致每个流式 chunk 都把视口拽回底部、用户无法上翻查看历史。
          // 与 legacy Terminals.vue:523-528 的 writeLiveChunk 行为对齐。
          inst.term.write(merged)
        } catch {
          /* term may be mid-teardown */
        }
      })
    }

    const dataEvent = 'pty:data:' + sessionId
    const disposeDataListener = EventsOn(dataEvent, (eventData: any) => {
      try {
        let seq: number
        let base64Data: string
        if (
          eventData &&
          typeof eventData === 'object' &&
          's' in eventData &&
          'd' in eventData
        ) {
          seq = eventData.s as number
          base64Data = eventData.d as string
        } else if (typeof eventData === 'string') {
          // legacy fallback without seq -> flush through after replay.
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

    const exitEvent = 'pty:exit:' + sessionId
    const disposeExitListener = EventsOn(exitEvent, (info: any) => {
      term.write('\r\n\x1b[33m[amagi-codebox] 进程已退出')
      if (info && info.exitCode !== undefined) {
        term.write(` (exit code: ${info.exitCode})`)
      }
      term.write('\x1b[0m\r\n')
      options.onExit?.(info && typeof info === 'object' ? { exitCode: info.exitCode } : {})
    })

    inst.disposeDataListener = disposeDataListener
    inst.disposeExitListener = disposeExitListener

    // open into DOM then attach addons + replay history + wire paste.
    try {
      term.open(containerEl)
    } catch (err) {
      console.error('[amagi-codebox] xterm open failed:', err)
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
        /(?:[A-Za-z]:[\/]|[.][\/])(?:[\w.\-]+[\/])*[\w.\-]+\.[a-zA-Z]{1,10}(?::(\d+)(?::\d+)?)?|(?:[\/]|(?:[\w.\-]+[\/]){1,})(?:[\w.\-]+[\/])*[\w.\-]+\.[a-zA-Z]{1,10}(?::(\d+)(?::\d+)?)?/g

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

    // Renderer selection.
    // - macOS WKWebView: WebGL addon historically corrupts the texture atlas
    //   on scrollback. We load the Canvas addon instead — it draws into a
    //   single <canvas> (no GPU texture path) and is dramatically faster
    //   than xterm's default DOM renderer under opencode TUI's high-frequency
    //   full-screen redraws.
    // - non-Darwin (Windows/Linux): keep the WebGL path when the probe
    //   succeeds; WebGL remains the fastest renderer on those platforms.
    // - any canvas/WebGL load failure fails open: xterm falls back to its
    //   built-in DOM renderer rather than bricking the terminal.
    if (platformCaps.caps.value && platformCaps.isDarwin.value) {
      loadCanvasRenderer(sessionId, inst)
    } else if (platformCaps.caps.value && !platformCaps.isDarwin.value && isWebGLReliable()) {
      loadWebglRenderer(sessionId, inst)
    }

    // Renderer activate-time size refresh (regression fix for v1.2.68 切会话撕裂黑屏).
    // Canvas/WebGL addon 的 activate 在 BaseRenderLayer 构造瞬间即
    // createElement+appendChild+_initCanvas+_refreshCharAtlas，device 像素与
    // texture atlas 在这一刻定型。而此处分枝紧跟 term.open(containerEl) 同步
    // 执行，早于下方 mountTerm 末尾的 fit.fit() 与外层 TerminalView.vue 的
    // rAF force fit——activate 时 term dimensions 仍是默认/中间态，atlas 用
    // 错误 cell 度量构建，后续 fit 即便触发 renderer.onResize 也未必纠正 atlas，
    // 表现为切会话回来撕裂近黑屏，需"窗口最大化→ResizeObserver→完整 onResize"
    // 才恢复。修复：renderer 加载后下一帧强制 force fit（让 fit.fit() 重算
    // cols/rows 并 term.resize → renderer.onResize），并对 canvas 显式调用
    // clearTextureAtlas 强制下一次渲染重建 atlas。WebGL 无 clearTextureAtlas，
    // 但 force fit 同样能触发其 dimensions change → atlas 重建。这一帧延迟
    // 用户感知不到，但能消除 activate 时序险境。force=true 绕过 sameDims bail。
    requestAnimationFrame(() => {
      if (terminals.get(sessionId) !== inst) return
      // force fit 让 proposeDimensions 即使与 lastCols/lastRows 相同也重跑 fit
      fitTerminal(sessionId, true, containerEl)
      // canvas renderer 的 texture atlas 可能已在错误尺寸下构建；CanvasAddon
      // typings 不暴露 clearTextureAtlas，用类型断言安全调用，缺失则 noop。
      const canvas = inst.canvas as unknown as {
        clearTextureAtlas?: () => void
      } | null
      try {
        canvas?.clearTextureAtlas?.()
      } catch {
        /* mid-teardown or addon mismatch: noop */
      }
    })

    // ----- history replay -----
    // M1 atomic boundary: snapshot returns {data, seq} where seq is the
    // backend's monotonic emitSeq at snapshot time; any live event with
    // seq <= snapshot seq is already in the history bytes.
    // M2 type compatibility: decodeHistoryData handles string / Array<number>
    // / Uint8Array return shapes.
    //
    // Chunked write: a 1MB snapshot written in a single term.write() call
    // blocks the main thread for hundreds of milliseconds on the DOM/canvas
    // renderer (parser + layout + paint all synchronous). Under opencode TUI
    // that means a perceptible "frozen" terminal right after switching
    // sessions. Slice the snapshot into ~64KB chunks and yield one frame
    // between them so the renderer can paint progressively. After the final
    // chunk, force a single scrollToBottom() so the viewport lands on the
    // latest output — this is the regression introduced by v1.2.59 which
    // removed the auto-follow call and left the viewport parked at buffer
    // row 0 after a snapshot replay, presenting the user with a blank / old
    // screen on every session switch.
    const HISTORY_CHUNK_SIZE = 64 * 1024
    function writeHistoryInChunks(decoded: Uint8Array, done: () => void) {
      let offset = 0
      const total = decoded.length
      function writeNextChunk() {
        // term may have been disposed mid-replay (rapid session switching).
        if (terminals.get(sessionId) !== inst) return
        const end = Math.min(offset + HISTORY_CHUNK_SIZE, total)
        try {
          inst.term.write(decoded.subarray(offset, end))
        } catch {
          /* term mid-teardown */
          return
        }
        offset = end
        if (offset < total) {
          // Yield one frame between chunks so paint/cursor blink don't pile
          // up. setTimeout(0) would also work but rAF aligns with the
          // display refresh and avoids extra layout passes.
          requestAnimationFrame(writeNextChunk)
        } else {
          done()
        }
      }
      writeNextChunk()
    }

    GetOutputHistorySnapshot(sessionId)
      .then((jsonStr: string) => {
        if (!jsonStr) {
          historyReplayed = true
          flushLiveBuffer()
          return
        }
        try {
          const snapshot = JSON.parse(jsonStr)
          const decoded = decodeHistoryData(snapshot.data)
          if (decoded && decoded.length > 0) {
            inst.historySnapshotSeq = snapshot.seq || 0
            writeHistoryInChunks(decoded, () => {
              // Final chunk done — pin the viewport to the latest output.
              // 切会话 replay 写入大量历史数据期间 dimensions 可能抖动，
              // texture atlas 或基于中间状态构建；done 时清理一次让最终
              // 渲染基于干净 atlas，与上方 renderer 加载后的清理同源。
              const canvas = inst.canvas as unknown as {
                clearTextureAtlas?: () => void
              } | null
              try {
                canvas?.clearTextureAtlas?.()
              } catch {
                /* mid-teardown or addon mismatch: noop */
              }
              try {
                inst.term.scrollToBottom()
              } catch {
                /* viewport not ready */
              }
              historyReplayed = true
              flushLiveBuffer()
            })
            return
          } else if (decoded !== null && decoded.length === 0) {
            // decodeHistoryData returned empty: data valid but empty.
            // Snapshot is authoritative (seq valid) -> set boundary.
            inst.historySnapshotSeq = snapshot.seq || 0
          }
          // decoded === null -> decode failed: leave seq at 0 so buffered
          // live chunks flush through without being discarded.
        } catch (e) {
          console.warn('history replay failed:', e)
        }
        historyReplayed = true
        flushLiveBuffer()
      })
      .catch(() => {
        // Session may not support history (e.g. already exited): flush live.
        historyReplayed = true
        flushLiveBuffer()
      })

    function flushLiveBuffer() {
      for (const chunk of liveBuffer) {
        writeLiveChunk(chunk.seq, chunk.bytes)
      }
      liveBuffer.length = 0
    }

    // initial fit + resize to backend
    try {
      fit.fit()
      const dims = fit.proposeDimensions()
      if (dims && dims.cols > 0 && dims.rows > 0) {
        inst.lastCols = dims.cols
        inst.lastRows = dims.rows
        PtyResize(sessionId, dims.cols, dims.rows).catch(() => {})
      }
    } catch {
      /* container may not have layout yet */
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
          const bytes = new TextEncoder().encode(text)
          const encoded = uint8ToBase64(bytes)
          if (bytes.length > 1024) {
            PtyWriteLarge(sessionId, encoded).catch(() => {})
          } else {
            PtyWrite(sessionId, encoded).catch(() => {})
          }
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
                  SaveClipboardImage(b64)
                    .then((filePath) => {
                      if (filePath) {
                        const pathBytes = new TextEncoder().encode(filePath)
                        PtyWrite(sessionId, uint8ToBase64(pathBytes)).catch(() => {})
                      }
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
    if (!inst) return
    const bytes = new TextEncoder().encode(data)
    const encoded = uint8ToBase64(bytes)
    PtyWrite(sessionId, encoded).catch((err) => {
      console.error('PtyWrite error:', err)
    })
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
    inst.disposeForcedSelection?.()
    inst.disposeForcedSelection = null

    // If a Shift+drag is in-flight (window mousemove/mouseup attached),
    // detach those listeners now — otherwise they would linger on window
    // until the next mouseup, holding a reference to this disposed inst.
    inst.activeDragCleanup?.()
    inst.activeDragCleanup = null

    // Cancel any pending micro-batch so it doesn't fire after dispose.
    if (inst.liveBatchRaf !== null) {
      cancelAnimationFrame(inst.liveBatchRaf)
      inst.liveBatchRaf = null
    }
    inst.liveBatchQueue.length = 0

    try {
      inst.canvas?.dispose()
    } catch {
      /* already disposed */
    }
    inst.canvas = null

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
    resizeTerm,
    fitTerminal,
    disposeTerm,
    disposeAll,
    getTerm,
    switchSession,
    attachForcedSelection,
    // exposed for the right-click menu component (TerminalContextMenu)
    copySelection,
    pasteToTerminal,
  }
}
