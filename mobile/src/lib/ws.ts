/**
 * lib/ws.ts — PG-03 `/ws/v1` 会话附着客户端（M2-C）
 * ---------------------------------------------------------------------------
 * 权威依据：mobile/src/lib/contract/ws.ts（M0-03 冻结）+ M2-A design §6：
 *   · URL 唯一为 WEB_SOCKET_V1_PATH（无 token/session/mode；Cookie 同源凭据，
 *     浏览器 WebSocket 同源自动携带，前端不读、不存、不记任何凭据）；
 *   · 首帧必须是 attach（apiVersion/sessionId/lastSeq?；lastSeq omitted 与 0
 *     语义有别，design §7.3——本客户端仅在持有 replay frame 时携带 lastSeq）；
 *   · 服务端事件一律经 normalizeServerEvent（fail-closed unknown，本模块不
 *     自行解析任何字段）；出站帧一律构造后经 isClientFrame 验证再发送；
 *   · 断线自动重连：指数退避、上限 5s（AC-02「≤5s 恢复」语义——重连间隔
 *     不超过 5s）；重连即重新 attach，expectedRunPosition 因果语义由服务端
 *     保证，客户端如实呈现「恢复中/已恢复」状态，不伪造连续性；
 *   · terminal close 不重连：1000（正常/session removed）/1002（协议错误）
 *     /1008（forbidden·revoked·superseded）为终止；1001/1006/1011/1013 等
 *     重连。
 * 边界：真实服务器 E2E 属 M2-INT；本客户端的可测试 seam 为 createWebSocket
 * 注入（vitest 用 fake，Playwright 用 routeWebSocket mock，生产用原生）。
 * ---------------------------------------------------------------------------
 */

import {
  API_VERSION_V1,
  AUTH_REVOKED_CLOSE_CODE,
  CLIENT_FRAME_TYPE_ATTACH,
  CLIENT_FRAME_TYPE_BACKFILL,
  CLIENT_FRAME_TYPE_PING,
  CLIENT_FRAME_TYPE_RESIZE,
  WEB_SOCKET_V1_PATH,
  isClientFrame,
  normalizeServerEvent,
  type AttachFrame,
  type BackfillFrame,
  type ClientFrame,
  type DecodedServerEvent,
  type InputFrame,
  type PingFrame,
  type ResizeFrame,
  type Seq,
  type SessionID,
} from './contract';
import { decodeBase64ToUint8 } from '../utils/terminalFrameDecode';

// ---------------------------------------------------------------------------
// 类型
// ---------------------------------------------------------------------------

export type WsClientState =
  | 'idle'
  | 'connecting'
  /** 已 open，已发 attach，等待 session.attached / error。 */
  | 'awaiting-attach'
  | 'attached'
  /** 断线重连中（含退避等待与重连握手）。 */
  | 'reconnecting'
  /** 终止（dispose 或 terminal close），不再重连。 */
  | 'closed';

export interface WsStateChange {
  state: WsClientState;
  /** 重连尝试序号（reconnecting 时 ≥1）。 */
  attempt: number;
  /** 下次重连延迟 ms（reconnecting 等待期时有值）。 */
  nextDelayMs: number | null;
  /** terminal close 的说明（closed 时有值）。 */
  terminalReason: string | null;
}

export interface SessionWsClientOptions {
  sessionId: SessionID;
  /** 重连 attach 的游标：仅当持有 replay frame 时返回 Seq，否则 undefined（omitted）。 */
  getLastSeq: () => Seq | undefined;
  /** 每次成功 attach 后重置（服务端已保证水位，客户端呈现恢复态）。 */
  onEvent: (event: DecodedServerEvent) => void;
  onStateChange: (change: WsStateChange) => void;
  /** 完整 WebSocket URL（默认按当前 location 推导同源 ws(s)://host + WEB_SOCKET_V1_PATH）。 */
  url?: string;
  /** 可注入的 WebSocket 工厂（测试 seam）；默认原生 WebSocket。 */
  createWebSocket?: (url: string) => WebSocketLike;
  /** 重连退避上限 ms（AC-02：≤5s）。 */
  maxReconnectDelayMs?: number;
  /** 应用层 ping 间隔 ms（只刷新 liveness；0=关闭）。 */
  pingIntervalMs?: number;
}

/** 生产/测试共用的最小 WebSocket 形状。 */
export interface WebSocketLike {
  readonly readyState: number;
  send(data: string): void;
  close(code?: number, reason?: string): void;
  onopen: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
  onclose: ((ev: { code: number; reason?: string }) => void) | null;
  onerror: ((ev: unknown) => void) | null;
}

const WS_OPEN = 1;

/** 重连退避：750ms 起步翻倍，封顶 maxReconnectDelayMs（默认 5000，AC-02）。 */
export function reconnectDelay(attempt: number, maxDelayMs: number): number {
  const base = 750 * Math.pow(2, Math.max(0, attempt - 1));
  return Math.min(maxDelayMs, Math.round(base));
}

/** terminal close code → 说明；非 terminal 返回 null（可重连）。 */
export function terminalCloseReason(code: number): string | null {
  if (code === 1000) return '连接已被服务端正常关闭';
  if (code === 1002) return '协议错误，连接已被关闭';
  if (code === AUTH_REVOKED_CLOSE_CODE) return '访问权限已失效（授权撤销或被其他连接取代）';
  return null;
}

/** UTF-8 文本 → RFC4648 Base64（input 帧载荷）。 */
export function encodeUtf8ToBase64(text: string): string {
  const bytes = new TextEncoder().encode(text);
  let binary = '';
  for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]);
  return btoa(binary);
}

/** output/backfill chunk（Base64）→ UTF-8 文本（有损替换，不抛异常）。
 * 单帧工具仅适合独立文本；有序 PTY 输出必须复用 createOutputChunkDecoder，
 * 因为一次 UTF-8 字符可能跨两个 WebSocket frame。 */
export function decodeChunkToText(base64: string): string {
  try {
    const bytes = decodeBase64ToUint8(base64);
    return new TextDecoder('utf-8', { fatal: false }).decode(bytes);
  } catch {
    return '';
  }
}

export interface OutputChunkDecoder {
  decode(base64: string): string;
  flush(): string;
  reset(): void;
}

/** Stateful UTF-8 decoder for one ordered PTY byte stream. */
export function createOutputChunkDecoder(): OutputChunkDecoder {
  let decoder = new TextDecoder('utf-8', { fatal: false });
  return {
    decode(base64: string): string {
      try {
        return decoder.decode(decodeBase64ToUint8(base64), { stream: true });
      } catch {
        return '';
      }
    },
    flush(): string {
      return decoder.decode();
    },
    reset(): void {
      decoder = new TextDecoder('utf-8', { fatal: false });
    },
  };
}

let requestCounter = 0;
function nextRequestId(): string {
  requestCounter += 1;
  const rand =
    typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
      ? crypto.randomUUID()
      : `r${Date.now().toString(36)}-${requestCounter}`;
  return `req-${rand}`;
}

export class SessionWsClient {
  private readonly opts: SessionWsClientOptions;
  private readonly url: string;
  private readonly createWs: (url: string) => WebSocketLike;
  private readonly maxDelay: number;
  private readonly pingInterval: number;

  private ws: WebSocketLike | null = null;
  private state: WsClientState = 'idle';
  private attempt = 0;
  private disposed = false;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private pingTimer: ReturnType<typeof setInterval> | null = null;

  constructor(options: SessionWsClientOptions) {
    this.opts = options;
    this.url = options.url ?? SessionWsClient.deriveUrl();
    this.createWs = options.createWebSocket ?? ((url: string) => new WebSocket(url) as unknown as WebSocketLike);
    this.maxDelay = options.maxReconnectDelayMs ?? 5000;
    this.pingInterval = options.pingIntervalMs ?? 30_000;
  }

  /** 同源默认 URL：ws(s)://<host>/ws/v1（路径为契约唯一冻结值）。 */
  static deriveUrl(loc: { protocol: string; host: string } = window.location): string {
    const scheme = loc.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${scheme}//${loc.host}${WEB_SOCKET_V1_PATH}`;
  }

  getState(): WsClientState {
    return this.state;
  }

  /** 建立连接（或从重连等待中立即重连）。 */
  connect(): void {
    if (this.disposed) return;
    this.clearReconnectTimer();
    this.openSocket();
  }

  /** M3-004：强制重连（live pending 越界/同 Seq 冲突 fail-closed）。关闭当前 socket
   *  并触发保守 reattach（携带 frontier 游标重新对齐 retained window）。 */
  forceReconnect(): void {
    if (this.disposed) return;
    this.scheduleReconnect('forced-frontier-realign');
  }

  /** 终止：停止重连与 ping，关闭 socket，不再发出任何事件。 */
  dispose(): void {
    this.disposed = true;
    this.clearReconnectTimer();
    this.stopPing();
    const ws = this.ws;
    this.ws = null;
    if (ws) {
      ws.onopen = null;
      ws.onmessage = null;
      ws.onclose = null;
      ws.onerror = null;
      try {
        ws.close(1000);
      } catch {
        /* 忽略关闭异常 */
      }
    }
    this.setState('closed', { terminalReason: null });
  }

  /** 发送预构造 input 帧（CG-03 outbox 构造 canonical ID/data 后调用）。 */
  sendInputFrame(frame: InputFrame): boolean {
    if (this.state !== 'attached') return false;
    return this.sendFrame(frame);
  }

  /** 发送 resize（attached 才发）。 */
  sendResize(cols: number, rows: number): boolean {
    if (this.state !== 'attached') return false;
    if (!Number.isSafeInteger(cols) || !Number.isSafeInteger(rows) || cols < 1 || rows < 1) return false;
    const frame: ResizeFrame = {
      type: CLIENT_FRAME_TYPE_RESIZE,
      requestId: nextRequestId(),
      cols,
      rows,
    };
    return this.sendFrame(frame);
  }

  /** 请求 backfill（attached 才发；返回 requestId 供关联，未发送返回 null）。 */
  requestBackfill(fromSeq: Seq, toSeq: Seq): string | null {
    if (this.state !== 'attached') return null;
    if (!Number.isSafeInteger(fromSeq) || !Number.isSafeInteger(toSeq) || fromSeq < 1 || toSeq < fromSeq) return null;
    const frame: BackfillFrame = {
      type: CLIENT_FRAME_TYPE_BACKFILL,
      requestId: nextRequestId(),
      fromSeq,
      toSeq,
    };
    return this.sendFrame(frame) ? frame.requestId : null;
  }

  // -------------------------------------------------------------------------
  // 内部
  // -------------------------------------------------------------------------

  private openSocket(): void {
    const reconnecting = this.attempt > 0;
    this.setState(reconnecting ? 'reconnecting' : 'connecting', { nextDelayMs: null });
    let ws: WebSocketLike;
    try {
      ws = this.createWs(this.url);
    } catch {
      this.scheduleReconnect('socket-create-failed');
      return;
    }
    this.ws = ws;

    ws.onopen = () => {
      if (this.ws !== ws || this.disposed) return;
      // 首帧必须是 attach（design §6.2）；lastSeq 仅持有 replay frame 时携带。
      const lastSeq = this.opts.getLastSeq();
      const frame: AttachFrame = {
        type: CLIENT_FRAME_TYPE_ATTACH,
        requestId: nextRequestId(),
        apiVersion: API_VERSION_V1,
        sessionId: this.opts.sessionId,
        ...(lastSeq !== undefined ? { lastSeq } : {}),
      };
      if (!this.sendFrame(frame)) {
        this.scheduleReconnect('attach-send-failed');
        return;
      }
      this.setState('awaiting-attach', { nextDelayMs: null });
    };

    ws.onmessage = (ev) => {
      if (this.ws !== ws || this.disposed) return;
      this.handleMessage(ev.data);
    };

    ws.onclose = (ev) => {
      if (this.ws !== ws || this.disposed) return;
      this.ws = null;
      this.stopPing();
      const terminal = terminalCloseReason(ev.code);
      if (terminal !== null) {
        this.setState('closed', { terminalReason: terminal });
        return;
      }
      this.scheduleReconnect(`close-${ev.code}`);
    };

    ws.onerror = () => {
      // 错误后必随 onclose；此处不重复处理（避免双状态迁移）。
    };
  }

  private handleMessage(data: unknown): void {
    if (typeof data !== 'string') return; // binary 帧：协议外，忽略（服务端会 1002）
    let parsed: unknown;
    try {
      parsed = JSON.parse(data);
    } catch {
      return; // 非 JSON：不入 normalize（无法构造 wireType），直接丢弃
    }
    // 帧解析只走契约 normalize（fail-closed unknown）；本模块不读任何原始字段。
    const event: DecodedServerEvent = normalizeServerEvent(parsed);
    if (event.type === 'session.attached') {
      const wasReconnect = this.attempt > 0;
      this.attempt = 0;
      this.setState('attached', { nextDelayMs: null });
      this.startPing();
      this.opts.onEvent(event);
      if (wasReconnect) {
        // 恢复态呈现由 store 依据 attached 事件完成，此处不另造事件。
      }
      return;
    }
    this.opts.onEvent(event);
  }

  private sendFrame(frame: ClientFrame): boolean {
    const ws = this.ws;
    if (!ws || ws.readyState !== WS_OPEN) return false;
    // 出站帧一律经契约 isClientFrame 验证（冻结纪律），失败即不发送。
    if (!isClientFrame(frame)) return false;
    try {
      ws.send(JSON.stringify(frame));
      return true;
    } catch {
      return false;
    }
  }

  private scheduleReconnect(_cause: string): void {
    if (this.disposed) return;
    this.stopPing();
    const ws = this.ws;
    this.ws = null;
    if (ws) {
      ws.onopen = null;
      ws.onmessage = null;
      ws.onclose = null;
      ws.onerror = null;
      try {
        ws.close();
      } catch {
        /* 忽略 */
      }
    }
    this.attempt += 1;
    const delay = reconnectDelay(this.attempt, this.maxDelay);
    this.setState('reconnecting', { nextDelayMs: delay });
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.openSocket();
    }, delay);
  }

  private startPing(): void {
    this.stopPing();
    if (this.pingInterval <= 0) return;
    this.pingTimer = setInterval(() => {
      const frame: PingFrame = { type: CLIENT_FRAME_TYPE_PING, requestId: nextRequestId() };
      this.sendFrame(frame);
    }, this.pingInterval);
  }

  private stopPing(): void {
    if (this.pingTimer !== null) {
      clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private setState(state: WsClientState, extra: { nextDelayMs?: number | null; terminalReason?: string | null }): void {
    this.state = state;
    this.opts.onStateChange({
      state,
      attempt: this.attempt,
      nextDelayMs: extra.nextDelayMs ?? null,
      terminalReason: extra.terminalReason ?? null,
    });
  }
}
