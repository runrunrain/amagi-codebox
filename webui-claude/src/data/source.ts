/**
 * source.ts — 数据源抽象（design §7.2 结构、§6 传输契约，t7）。
 *
 * 分层：
 *   - Transport：IO 缝（fetchJson / connectWs / schedule / now 四点可注入）——
 *     单测与 mock 剧本回放不需要真实网络与定时器；
 *   - DataSource 回调面：onHello / onTranscript / onBusy / onEnded / onReconnect
 *     （+ onFatal 承载首屏页内错误态——白屏防线的另一半，design §7.2-3）；
 *   - wire 类型 1:1 对齐 internal/claudewebui 的 Go 端投影（tailer.go wireRecord、
 *     ws.go envelope/helloEvent、server.go historyResponse；json 字段名冻结）。
 */

// ---------------------------------------------------------------------------
// wire 类型（§6.2 / §6.3；字段名与 internal/claudewebui 冻结契约一致）
// ---------------------------------------------------------------------------

/** 工具调用块（assistant 行 content[].type=="tool_use"）。 */
export interface ToolUseWire {
  id: string
  name: string
  input?: unknown
}

/** 工具结果块（user 行 content[].type=="tool_result"）。 */
export interface ToolResultWire {
  id: string
  isError?: boolean
  content?: string
}

/** 用量四分项（§6.3 usage）。 */
export interface UsageWire {
  in: number
  out: number
  cacheRead: number
  cacheWrite: number
}

/** 单条转录记录（§6.3 TranscriptRecord）。 */
export interface TranscriptRecord {
  kind: string
  uuid: string
  isHuman?: boolean
  ts?: string
  text?: string
  thinking?: string
  toolUses?: ToolUseWire[]
  toolResults?: ToolResultWire[]
  usage?: UsageWire
  model?: string
}

/** WS hello 事件（连接即推；stream 标识流重建，floor 是环形缓冲最旧 seq）。 */
export interface HelloEvent {
  stream: string
  floor: number
  cursor: number
  busy: boolean
}

/**
 * transcript 事件 / history 快照的统一载荷。
 * WS 增量事件只带 {items, cursor}；httpSource 在 /api/history 响应上补
 * snapshot:true（权威全量——doc 按此重排）与 title/truncatedHead 透出。
 */
export interface TranscriptEvent {
  items: TranscriptRecord[]
  cursor: number
  snapshot?: boolean
  title?: string
  truncatedHead?: boolean
}

/**
 * 首屏致命错误类别（页内错误态 + 重试，design §7.2-3）。
 * 鉴权失败由 /api/info 的 Bearer 校验兜住（浏览器 WebSocket API 不暴露
 * 握手状态码，WS 侧被拒不可与网络故障区分，统一走断线重连）。
 */
export type SourceFatalKind =
  | 'info' // /api/info 不可达或契约不符（非 200 / v!==1，含 token 失效）
  | 'history' // 首次 /api/history 失败（此后靠 WS 流）

// ---------------------------------------------------------------------------
// Transport：IO 缝（全部可注入）
// ---------------------------------------------------------------------------

export interface JsonResponse {
  status: number
  body: unknown
}

export interface TransportRequest {
  method?: string
  headers?: Record<string, string>
  body?: string
}

export interface WsHandlers {
  /** 收到一条消息（原始载荷，通常是 JSON 字符串）。 */
  onMessage(raw: unknown): void
  onOpen(): void
  onClose(): void
  onError(err?: unknown): void
}

export interface WsConnection {
  close(): void
}

export interface Transport {
  fetchJson(url: string, init?: TransportRequest): Promise<JsonResponse>
  connectWs(url: string, protocols: string[], handlers: WsHandlers): WsConnection
  /** 延时调度；返回取消函数。 */
  schedule(fn: () => void, delayMs: number): () => void
  now(): number
}

// ---------------------------------------------------------------------------
// DataSource 回调面
// ---------------------------------------------------------------------------

export interface SourceCallbacks {
  onHello?(ev: HelloEvent): void
  onTranscript?(ev: TranscriptEvent): void
  onBusy?(value: boolean): void
  /** plane 停止（WS bye 信封，尽力而为）。 */
  onEnded?(): void
  /** 连接断开后的退避重连通知（attempt 从 1 起；重连成功后由 onHello 覆盖 UI 态）。 */
  onReconnect?(attempt: number, delayMs: number): void
  onFatal?(kind: SourceFatalKind): void
}

export interface DataSource {
  start(): void
  stop(): void
}

/** 重连退避表（design §7.2：1s/2s/5s 封顶）。 */
export const RECONNECT_BACKOFF_MS = [1000, 2000, 5000]

/** 退避取值：第 n 次尝试（1 起）→ 表内第 n 项，越界取末项封顶。 */
export function backoffDelayMs(attempt: number): number {
  if (attempt < 1) return RECONNECT_BACKOFF_MS[0]
  const i = Math.min(attempt, RECONNECT_BACKOFF_MS.length) - 1
  return RECONNECT_BACKOFF_MS[i]
}
