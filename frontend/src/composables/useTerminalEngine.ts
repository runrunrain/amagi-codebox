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
import { EventsOn, BrowserOpenURL } from '../../wailsjs/runtime/runtime'
import { usePlatformCapabilities } from './usePlatformCapabilities'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface TerminalInstance {
  term: Terminal
  fit: FitAddon
  webgl: WebglAddon | null
  /** dispose fn returned by EventsOn for the pty:data:<id> stream */
  disposeDataListener: (() => void) | null
  /** dispose fn returned by EventsOn for the pty:exit:<id> stream */
  disposeExitListener: (() => void) | null
  /** capture-phase paste listener removal on the xterm textarea */
  disposePasteListener: (() => void) | null
  lastCols: number
  lastRows: number
  /** highest emitSeq covered by the loaded history snapshot */
  historySnapshotSeq: number
  /** whether the user has manually scrolled up (disables auto-follow) */
  userScrolledUp: boolean
  /** container element for scroll detection */
  containerEl: HTMLElement | null
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
    try {
      await navigator.clipboard.writeText(text)
    } catch {
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

  async function pasteToTerminal(sessionId: string) {
    try {
      const text = await navigator.clipboard.readText()
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
      disposeDataListener: null,
      disposeExitListener: null,
      disposePasteListener: null,
      lastCols: 0,
      lastRows: 0,
      historySnapshotSeq: 0,
      userScrolledUp: false,
      containerEl: null,
    }
    terminals.set(sessionId, inst)

    // seq-based dedup: any live chunk with seq <= snapshot seq is already in
    // the history bytes -> skip it. Both the flush path and the direct path
    // go through here so dedup is never bypassed.
    function writeLiveChunk(seq: number, bytes: Uint8Array) {
      if (seq > 0 && seq <= inst.historySnapshotSeq) return
      try {
        inst.term.write(bytes)
        // Auto-scroll to bottom unless user manually scrolled up
        requestAnimationFrame(() => {
          if (!inst.userScrolledUp && inst.term.element) {
            inst.term.scrollToBottom()
          }
        })
      } catch {
        /* term may be mid-teardown */
      }
    }

    // Track user scroll position to disable auto-follow when scrolled up
    function setupScrollTracking() {
      if (!containerEl) return
      inst.containerEl = containerEl

      const viewport = containerEl.querySelector('.xterm-viewport') as HTMLElement
      if (!viewport) return

      viewport.addEventListener('scroll', () => {
        const isAtBottom = viewport.scrollTop + viewport.clientHeight >= viewport.scrollHeight - 10
        inst.userScrolledUp = !isAtBottom
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
      setupScrollTracking()
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

    // WebGL renderer. macOS WKWebView triggers texture-atlas scrollback
    // corruption with the WebglAddon, so fail-closed: require caps loaded AND
    // non-Darwin. If caps are still null (edge case), do NOT load WebGL --
    // safer than risking corruption on macOS.
    if (platformCaps.caps.value && !platformCaps.isDarwin.value && isWebGLReliable()) {
      loadWebglRenderer(sessionId, inst)
    }

    // ----- history replay -----
    // M1 atomic boundary: snapshot returns {data, seq} where seq is the
    // backend's monotonic emitSeq at snapshot time; any live event with
    // seq <= snapshot seq is already in the history bytes.
    // M2 type compatibility: decodeHistoryData handles string / Array<number>
    // / Uint8Array return shapes.
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
            inst.term.write(decoded)
            // Only set the dedup boundary after a successful history write,
            // so buffered live chunks are not discarded when decode fails.
            inst.historySnapshotSeq = snapshot.seq || 0
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
    // exposed for the right-click menu component (TerminalContextMenu)
    copySelection,
    pasteToTerminal,
  }
}
