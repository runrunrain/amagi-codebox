/**
 * httpSource.ts — 真数据面源（design §7.2、§6 传输契约，t7）。
 *
 * 职责链：
 *   1. token 取 location.hash '#/t=<value>'（值须匹配 ^[A-Za-z0-9_-]{22,}$；
 *      token 视同会话权限——绝不进 query/path/请求行/日志，design §6 冻结红线）。
 *      缺失/非法由 App 层渲染页内错误页（白屏防线的另一半，§7.2-3）。
 *   2. API 全带 Authorization: Bearer <token>；全部 URL 经 resolvePlaneBase 推导
 *      （双形态硬约束——禁止根绝对路径与硬编码 127.0.0.1）。
 *   3. 启动序：GET /api/info（契约校验 v===1，失败 → onFatal('info')）→
 *      连 WS（子协议 ['webui', token]，URL 经 base.wsUrl()）→ open 后拉
 *      /api/history（先 WS 后 history 的 pi §10 时序——WS 先建立则缺口期事件
 *      由 ?since 重放兜底，重复内容按 uuid 在 doc 幂等合并）。
 *   4. 事件信封 {v,seq,ts,kind,event}：seq 单调；hello 携带续传点；重连带
 *      ?since=<lastSeq>；hello.stream 变化或 seq 缺口 → 全量 refetch history。
 *   5. 断线重连退避 1s/2s/5s 封顶（onReconnect 透出）；bye → onEnded，不再重连。
 */

import { resolvePlaneBase, type PlaneBase, type PlaneLocation } from './basePath'
import {
  backoffDelayMs,
  type DataSource,
  type HelloEvent,
  type SourceCallbacks,
  type SourceFatalKind,
  type TranscriptEvent,
  type Transport,
} from './source'

/** token 值域（webui.GenerateToken 的 128bit hex=32 字符；兼容 base64url 形态）。 */
const TOKEN_VALUE_RE = /^[A-Za-z0-9_-]{22,}$/

/**
 * extractPlaneToken 从 location.hash 提取 '#/t=' 承载的 capability token。
 * 返回 null = 缺失或非法（调用方显示页内错误态，不发起任何请求）。
 */
export function extractPlaneToken(hash: string): string | null {
  const m = /#\/t=([^&#]*)/.exec(hash)
  if (!m) return null
  return TOKEN_VALUE_RE.test(m[1]) ? m[1] : null
}

/** WS 子协议第一段（internal/claudewebui/ws.go 手工校验形状；remote 反代依赖同一形状）。 */
const WS_SUBPROTOCOL = 'webui'

// ---------------------------------------------------------------------------
// 浏览器 Transport 实现（IO 缝的默认实现；测试注入假实现）
// ---------------------------------------------------------------------------

export function browserTransport(): Transport {
  return {
    async fetchJson(url, init) {
      const res = await fetch(url, {
        method: init?.method ?? 'GET',
        headers: init?.headers,
        body: init?.body,
      })
      let body: unknown = null
      try {
        body = await res.json()
      } catch {
        body = null // 非 JSON 响应按空体处理（状态码仍可用）
      }
      return { status: res.status, body }
    },
    connectWs(url, protocols, handlers) {
      const ws = new WebSocket(url, [...protocols])
      ws.onmessage = (ev: MessageEvent) => handlers.onMessage(ev.data)
      ws.onopen = () => handlers.onOpen()
      ws.onclose = () => handlers.onClose()
      ws.onerror = () => handlers.onError()
      return {
        close: () => {
          try {
            ws.close()
          } catch {
            /* already closing */
          }
        },
      }
    },
    schedule(fn, delayMs) {
      const t = setTimeout(fn, delayMs)
      return () => clearTimeout(t)
    },
    now: () => Date.now(),
  }
}

// ---------------------------------------------------------------------------
// HttpDataSource
// ---------------------------------------------------------------------------

export interface HttpSourceOptions {
  transport: Transport
  loc: PlaneLocation
  token: string
  callbacks: SourceCallbacks
}

interface EnvelopeWire {
  v?: number
  seq?: number
  ts?: number
  kind?: string
  event?: unknown
}

type Phase = 'idle' | 'starting' | 'live' | 'ended' | 'fatal' | 'stopped'

export class HttpDataSource implements DataSource {
  private readonly transport: Transport
  private readonly base: PlaneBase
  private readonly token: string
  private readonly cb: SourceCallbacks

  private phase: Phase = 'idle'
  private ws: { close(): void } | null = null
  private cancelTimer: (() => void) | null = null
  private lastSeq = 0 // 已见最大 seq（hello 携带的续传点也算已见）
  private lastStream = '' // hello.stream（变化 = 流重建 → 全量 refetch）
  private reconnectAttempt = 0
  private historyLoaded = false
  private refetching = false

  constructor(opts: HttpSourceOptions) {
    this.transport = opts.transport
    this.base = resolvePlaneBase(opts.loc)
    this.token = opts.token
    this.cb = opts.callbacks
  }

  start(): void {
    if (this.phase === 'starting' || this.phase === 'live') return
    this.phase = 'starting'
    void this.boot()
  }

  stop(): void {
    this.phase = 'stopped'
    this.teardown()
  }

  private teardown(): void {
    if (this.cancelTimer) {
      this.cancelTimer()
      this.cancelTimer = null
    }
    if (this.ws) {
      this.ws.close()
      this.ws = null
    }
  }

  private authHeaders(): Record<string, string> {
    return { Authorization: `Bearer ${this.token}` }
  }

  /** 启动序：info → WS → (open) history。 */
  private async boot(): Promise<void> {
    try {
      const info = await this.transport.fetchJson(this.base.api('api/info'), {
        headers: this.authHeaders(),
      })
      const body = info.body as { v?: number; ready?: boolean } | null
      if (this.phase !== 'starting') return // 已被 stop
      if (info.status !== 200 || !body || body.v !== 1) {
        this.fail('info')
        return
      }
    } catch {
      if (this.phase !== 'starting') return
      this.fail('info')
      return
    }
    this.connectWs(false)
  }

  private fail(kind: SourceFatalKind): void {
    this.phase = 'fatal'
    this.teardown()
    this.cb.onFatal?.(kind)
  }

  // -------------------------------------------------------------------------
  // WS
  // -------------------------------------------------------------------------

  private connectWs(isReconnect: boolean): void {
    if (this.phase === 'stopped' || this.phase === 'ended' || this.phase === 'fatal') return
    let url = this.base.wsUrl()
    if (this.lastSeq > 0) url += `?since=${this.lastSeq}`
    const protocols = [WS_SUBPROTOCOL, this.token]
    this.ws = this.transport.connectWs(url, protocols, {
      onOpen: () => {
        this.reconnectAttempt = 0
        if (this.phase === 'starting') this.phase = 'live'
        // 先 WS 后 history（pi §10 时序）：连接建立即拉全量快照，
        // 缺口期事件已由 ?since 重放覆盖，重复内容在 doc 幂等合并。
        void this.fetchHistory(isReconnect)
      },
      onMessage: (raw) => this.handleRaw(raw),
      onClose: () => {
        this.ws = null
        if (this.phase === 'ended' || this.phase === 'stopped' || this.phase === 'fatal') return
        this.scheduleReconnect()
      },
      onError: () => {
        /* onError 后必随 onClose，统一在 onClose 处理 */
      },
    })
  }

  private scheduleReconnect(): void {
    this.reconnectAttempt += 1
    const delay = backoffDelayMs(this.reconnectAttempt)
    this.cb.onReconnect?.(this.reconnectAttempt, delay)
    this.cancelTimer = this.transport.schedule(() => {
      this.cancelTimer = null
      this.connectWs(true)
    }, delay)
  }

  private handleRaw(raw: unknown): void {
    if (typeof raw !== 'string') return
    let env: EnvelopeWire
    try {
      env = JSON.parse(raw) as EnvelopeWire
    } catch {
      return // 非 JSON 帧：忽略
    }
    if (typeof env.v !== 'number' || env.v !== 1) return
    switch (env.kind) {
      case 'hello':
        this.handleHello(env)
        return
      case 'bye':
        this.phase = 'ended'
        this.teardown()
        this.cb.onEnded?.()
        return
      case 'transcript':
        if (this.acceptSeq(env)) {
          this.cb.onTranscript?.({
            items: itemsOf(env.event),
            cursor: numberField(env.event, 'cursor'),
          })
        }
        return
      case 'busy': {
        const value = (env.event as { value?: unknown } | null | undefined)?.value
        if (value !== undefined) this.cb.onBusy?.(value === true)
        return
      }
      default:
        return // 未知 kind：前向兼容忽略
    }
  }

  /** hello：stream 变化 → 全量 refetch；seq 为续传点（不参与缺口判定）。 */
  private handleHello(env: EnvelopeWire): void {
    const ev = env.event as HelloEvent | null | undefined
    if (!ev || typeof ev.stream !== 'string') return
    const streamChanged = this.lastStream !== '' && this.lastStream !== ev.stream
    this.lastStream = ev.stream
    this.lastSeq = typeof env.seq === 'number' ? env.seq : 0
    this.cb.onHello?.(ev)
    this.cb.onBusy?.(ev.busy === true)
    if (streamChanged) void this.fetchHistory(true)
  }

  /**
   * 非 hello 信封的 seq 裁决：重复（≤lastSeq）丢弃；缺口（>lastSeq+1）触发
   * 全量 refetch 后继续接受本帧（design §6.2 缺口语义）。
   */
  private acceptSeq(env: EnvelopeWire): boolean {
    const seq = typeof env.seq === 'number' ? env.seq : 0
    if (seq <= this.lastSeq) return false
    const gap = seq > this.lastSeq + 1
    this.lastSeq = seq
    if (gap) void this.fetchHistory(true)
    return true
  }

  // -------------------------------------------------------------------------
  // history
  // -------------------------------------------------------------------------

  private async fetchHistory(isRefetch: boolean): Promise<void> {
    if (this.refetching) return
    this.refetching = true
    try {
      const res = await this.transport.fetchJson(this.base.api('api/history'), {
        headers: this.authHeaders(),
      })
      if (this.phase === 'stopped' || this.phase === 'fatal') return
      const body = res.body as
        | { v?: number; items?: unknown; cursor?: number; busy?: boolean; title?: string; truncatedHead?: boolean }
        | null
      if (res.status !== 200 || !body || body.v !== 1 || !Array.isArray(body.items)) {
        // 首次加载失败是致命错误（页内错误态 + 重试）；此后靠 WS 流继续。
        if (!this.historyLoaded && !isRefetch) this.fail('history')
        return
      }
      const ev: TranscriptEvent = {
        items: body.items as TranscriptEvent['items'],
        cursor: typeof body.cursor === 'number' ? body.cursor : 0,
        snapshot: true,
        title: typeof body.title === 'string' ? body.title : undefined,
        truncatedHead: body.truncatedHead === true,
      }
      this.historyLoaded = true
      if (typeof body.busy === 'boolean') this.cb.onBusy?.(body.busy)
      this.cb.onTranscript?.(ev)
    } catch {
      if (!this.historyLoaded && !isRefetch && this.phase === 'live') this.fail('history')
    } finally {
      this.refetching = false
    }
  }
}

function itemsOf(event: unknown): TranscriptEvent['items'] {
  const items = (event as { items?: unknown } | null | undefined)?.items
  return Array.isArray(items) ? (items as TranscriptEvent['items']) : []
}

function numberField(obj: unknown, key: string): number {
  const v = (obj as Record<string, unknown> | null | undefined)?.[key]
  return typeof v === 'number' ? v : 0
}
