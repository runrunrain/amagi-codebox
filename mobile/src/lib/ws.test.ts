/**
 * lib/ws.test.ts — SessionWsClient 行为测试（M2-C）
 * 覆盖：attach 首帧形状（契约 isClientFrame）、lastSeq omitted/present 语义、
 * 帧 normalize fail-closed（unknown fallback）、重连状态机（≤5s 退避、
 * terminal 不重连）、input/resize/backfill 出站帧形状与校验。
 * 全部经注入的 FakeWebSocket，无真实网络。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isClientFrame } from './contract';
import {
  SessionWsClient,
  decodeChunkToText,
  encodeUtf8ToBase64,
  reconnectDelay,
  terminalCloseReason,
  type WebSocketLike,
  type WsStateChange,
} from './ws';

// ---------------------------------------------------------------------------
// FakeWebSocket：记录发送帧；手动触发 open/message/close。
// ---------------------------------------------------------------------------

class FakeWebSocket implements WebSocketLike {
  static OPEN = 1;
  readyState = 0;
  sent: string[] = [];
  closedWith: { code?: number; reason?: string } | null = null;
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number; reason?: string }) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;

  send(data: string): void {
    this.sent.push(data);
  }
  close(code?: number, reason?: string): void {
    this.closedWith = { code, reason };
    this.readyState = 3;
  }
  // 测试驱动：
  fireOpen(): void {
    this.readyState = 1;
    this.onopen?.({});
  }
  fireMessage(payload: unknown): void {
    this.onmessage?.({ data: typeof payload === 'string' ? payload : JSON.stringify(payload) });
  }
  fireClose(code: number, reason = ''): void {
    this.readyState = 3;
    this.onclose?.({ code, reason });
  }
}

interface Harness {
  client: SessionWsClient;
  sockets: FakeWebSocket[];
  events: unknown[];
  states: WsStateChange[];
  lastSeqRef: { value: number | undefined };
}

function makeHarness(opts: Partial<Parameters<typeof makeHarnessFull>[0]> = {}): Harness {
  return makeHarnessFull(opts);
}

function makeHarnessFull({
  lastSeq = undefined,
  pingIntervalMs = 0,
}: {
  lastSeq?: number | undefined;
  pingIntervalMs?: number;
} = {}): Harness {
  const sockets: FakeWebSocket[] = [];
  const events: unknown[] = [];
  const states: WsStateChange[] = [];
  const lastSeqRef = { value: lastSeq };
  const client = new SessionWsClient({
    sessionId: 'sess-1',
    getLastSeq: () => lastSeqRef.value,
    onEvent: (e) => events.push(e),
    onStateChange: (s) => states.push(s),
    createWebSocket: () => {
      const ws = new FakeWebSocket();
      sockets.push(ws);
      return ws;
    },
    pingIntervalMs,
  });
  return { client, sockets, events, states, lastSeqRef };
}

const ATTACHED_EVENT = {
  type: 'session.attached',
  requestId: 'req-attached-1',
  apiVersion: 'v1',
  sessionId: 'sess-1',
  history: [],
  earliestSeq: 0,
  latestSeq: 0,
  snapshot: {
    connection: { state: 'connected' },
    auth: { state: 'authorized' },
    session: { state: 'running' },
    control: { state: 'you' },
    history: { state: 'continuous' },
  },
};

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe('attach 首帧', () => {
  it('open 后首帧为 attach（契约形状；无 replay 时 lastSeq omitted）', () => {
    const h = makeHarness();
    h.client.connect();
    expect(h.sockets).toHaveLength(1);
    h.sockets[0].fireOpen();
    expect(h.sockets[0].sent).toHaveLength(1);
    const frame = JSON.parse(h.sockets[0].sent[0]);
    expect(frame.type).toBe('attach');
    expect(frame.apiVersion).toBe('v1');
    expect(frame.sessionId).toBe('sess-1');
    expect('lastSeq' in frame).toBe(false); // omitted ≠ 0（design §7.3）
    expect(isClientFrame(frame)).toBe(true);
    expect(h.client.getState()).toBe('awaiting-attach');
  });

  it('持有 replay 游标时 attach 携带 lastSeq', () => {
    const h = makeHarness({ lastSeq: 42 });
    h.client.connect();
    h.sockets[0].fireOpen();
    const frame = JSON.parse(h.sockets[0].sent[0]);
    expect(frame.lastSeq).toBe(42);
    expect(isClientFrame(frame)).toBe(true);
  });
});

describe('帧 normalize（只走契约，unknown fail-closed）', () => {
  it('session.attached → attached 状态并转发事件', () => {
    const h = makeHarness();
    h.client.connect();
    h.sockets[0].fireOpen();
    h.sockets[0].fireMessage(ATTACHED_EVENT);
    expect(h.client.getState()).toBe('attached');
    expect(h.events).toHaveLength(1);
    expect((h.events[0] as { type: string }).type).toBe('session.attached');
  });

  it('未知事件类型 → sanitized unknown（fallback ignore），不读原始字段', () => {
    const h = makeHarness();
    h.client.connect();
    h.sockets[0].fireOpen();
    h.sockets[0].fireMessage(ATTACHED_EVENT);
    h.sockets[0].fireMessage({ type: 'session.metrics', secret: 'must-not-leak' });
    const unknown = h.events[1] as { type: string; reason: string; fallback: string; wireType: string };
    expect(unknown.type).toBe('unknown');
    expect(unknown.reason).toBe('unknown-type');
    expect(unknown.fallback).toBe('ignore');
    expect(unknown.wireType).toBe('session.metrics');
    expect(JSON.stringify(unknown)).not.toContain('must-not-leak');
  });

  it('畸形 auth.revoked（未知 reason）→ force-unauthorized fail-closed', () => {
    const h = makeHarness();
    h.client.connect();
    h.sockets[0].fireOpen();
    h.sockets[0].fireMessage(ATTACHED_EVENT);
    h.sockets[0].fireMessage({ type: 'auth.revoked', reason: 'something-new', occurredAt: 'x' });
    const unknown = h.events[1] as { type: string; fallback: string };
    expect(unknown.type).toBe('unknown');
    expect(unknown.fallback).toBe('force-unauthorized');
  });

  it('非 JSON 文本 → 静默丢弃（不抛异常、不转发）', () => {
    const h = makeHarness();
    h.client.connect();
    h.sockets[0].fireOpen();
    h.sockets[0].fireMessage(ATTACHED_EVENT);
    h.sockets[0].fireMessage('not-json{{{');
    expect(h.events).toHaveLength(1);
  });
});

describe('重连状态机（AC-02 ≤5s）', () => {
  it('reconnectDelay 指数退避且封顶 5000ms', () => {
    expect(reconnectDelay(1, 5000)).toBe(750);
    expect(reconnectDelay(2, 5000)).toBe(1500);
    expect(reconnectDelay(3, 5000)).toBe(3000);
    expect(reconnectDelay(4, 5000)).toBe(5000);
    expect(reconnectDelay(99, 5000)).toBe(5000);
  });

  it('异常关闭（1006）→ reconnecting，≤5s 后自动重连并重发 attach（携带 lastSeq）', () => {
    const h = makeHarness();
    h.client.connect();
    h.sockets[0].fireOpen();
    h.sockets[0].fireMessage(ATTACHED_EVENT);
    // 收到 seq=7 的输出后更新游标（模拟 store 行为）
    h.lastSeqRef.value = 7;
    h.sockets[0].fireClose(1006);
    expect(h.client.getState()).toBe('reconnecting');
    const lastState = h.states[h.states.length - 1];
    expect(lastState.attempt).toBe(1);
    expect(lastState.nextDelayMs).toBeLessThanOrEqual(5000);

    vi.advanceTimersByTime(750);
    expect(h.sockets).toHaveLength(2);
    h.sockets[1].fireOpen();
    const reattach = JSON.parse(h.sockets[1].sent[0]);
    expect(reattach.type).toBe('attach');
    expect(reattach.lastSeq).toBe(7);
    h.sockets[1].fireMessage(ATTACHED_EVENT);
    expect(h.client.getState()).toBe('attached');
  });

  it('连续断线退避递增但不超过 5s', () => {
    const h = makeHarness();
    h.client.connect();
    h.sockets[0].fireOpen();
    h.sockets[0].fireClose(1006);
    vi.advanceTimersByTime(750);
    h.sockets[1].fireOpen();
    h.sockets[1].fireClose(1006);
    const s = h.states[h.states.length - 1];
    expect(s.attempt).toBe(2);
    expect(s.nextDelayMs).toBe(1500);
    vi.advanceTimersByTime(1500);
    h.sockets[2].fireOpen();
    h.sockets[2].fireClose(1011);
    const s2 = h.states[h.states.length - 1];
    expect(s2.attempt).toBe(3);
    expect(s2.nextDelayMs).toBe(3000);
  });

  it('terminal close（1008/1000/1002）→ closed，不再重连', () => {
    expect(terminalCloseReason(1008)).not.toBeNull();
    expect(terminalCloseReason(1000)).not.toBeNull();
    expect(terminalCloseReason(1002)).not.toBeNull();
    expect(terminalCloseReason(1006)).toBeNull();
    const h = makeHarness();
    h.client.connect();
    h.sockets[0].fireOpen();
    h.sockets[0].fireMessage(ATTACHED_EVENT);
    h.sockets[0].fireClose(1008, 'forbidden');
    expect(h.client.getState()).toBe('closed');
    vi.advanceTimersByTime(60_000);
    expect(h.sockets).toHaveLength(1); // 未重连
  });

  it('dispose 后不重连', () => {
    const h = makeHarness();
    h.client.connect();
    h.sockets[0].fireOpen();
    h.sockets[0].fireClose(1006);
    h.client.dispose();
    vi.advanceTimersByTime(60_000);
    expect(h.sockets).toHaveLength(1);
  });
});

describe('出站帧', () => {
  function attachedHarness(): Harness {
    const h = makeHarness();
    h.client.connect();
    h.sockets[0].fireOpen();
    h.sockets[0].fireMessage(ATTACHED_EVENT);
    return h;
  }

  it('input：UTF-8 → base64；帧经 isClientFrame 验证', () => {
    const h = attachedHarness();
    expect(h.client.sendInput('你好\r')).toBe(true);
    const frame = JSON.parse(h.sockets[0].sent[h.sockets[0].sent.length - 1]);
    expect(frame.type).toBe('input');
    expect(decodeChunkToText(frame.data)).toBe('你好\r');
    expect(isClientFrame(frame)).toBe(true);
  });

  it('未 attached 时 input 不发送', () => {
    const h = makeHarness();
    h.client.connect();
    h.sockets[0].fireOpen(); // awaiting-attach
    expect(h.client.sendInput('x')).toBe(false);
    expect(h.sockets[0].sent).toHaveLength(1); // 只有 attach
  });

  it('resize：非法尺寸拒绝；合法帧经契约验证', () => {
    const h = attachedHarness();
    expect(h.client.sendResize(0, 24)).toBe(false);
    expect(h.client.sendResize(80, 24)).toBe(true);
    const frame = JSON.parse(h.sockets[0].sent[h.sockets[0].sent.length - 1]);
    expect(frame.type).toBe('resize');
    expect(isClientFrame(frame)).toBe(true);
  });

  it('backfill：非法范围拒绝；合法返回 requestId 且帧经契约验证', () => {
    const h = attachedHarness();
    expect(h.client.requestBackfill(0, 5)).toBeNull();
    expect(h.client.requestBackfill(9, 5)).toBeNull();
    const rid = h.client.requestBackfill(3, 7);
    expect(rid).not.toBeNull();
    const frame = JSON.parse(h.sockets[0].sent[h.sockets[0].sent.length - 1]);
    expect(frame.type).toBe('backfill');
    expect(frame.requestId).toBe(rid);
    expect(isClientFrame(frame)).toBe(true);
  });

  it('encode/decode 互逆（RFC4648 base64 + UTF-8）', () => {
    const samples = ['plain ascii', '中文与 emoji 🚀', 'ansi [31mseq', '\r\n\r'];
    for (const s of samples) {
      expect(decodeChunkToText(encodeUtf8ToBase64(s))).toBe(s);
    }
  });
});
