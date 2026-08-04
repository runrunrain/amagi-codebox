/**
 * __tests__/stores/workspace.test.ts — PG-03 工作区 store 行为测试（M2-C）
 * 覆盖：open→attach 流、history/boundary/gap 时间线组装、Composer 防重复
 * 与控制权过滤、sendAnswer 形状、GapMarker 补齐原位、control/auth/unknown
 * 事件处理。WS 层经 vi.mock 注入假客户端，无真实网络。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

// --- vi.mock：lib/ws（必须在 store import 之前声明；vitest 提升 vi.mock） ---

interface FakeClientOptions {
  sessionId: string;
  getLastSeq: () => number | undefined;
  onEvent: (event: unknown) => void;
  onStateChange: (change: { state: string; attempt: number; nextDelayMs: number | null; terminalReason: string | null }) => void;
}

// vi.hoisted：mock 工厂与测试体共享 FakeWsClient（vi.mock 提升至文件顶部）。
const { FakeWsClient } = vi.hoisted(() => {
  class FakeWsClient {
    static instances: FakeWsClient[] = [];
    opts: FakeClientOptions;
    sentInputs: string[] = [];
    backfillRequests: { fromSeq: number; toSeq: number }[] = [];
    disposed = false;

    constructor(opts: FakeClientOptions) {
      this.opts = opts;
      FakeWsClient.instances.push(this);
    }
    connect(): void {}
    dispose(): void {
      this.disposed = true;
    }
    sendInput(text: string): boolean {
      this.sentInputs.push(text);
      return true;
    }
    sendResize(): boolean {
      return true;
    }
    requestBackfill(fromSeq: number, toSeq: number): string {
      this.backfillRequests.push({ fromSeq, toSeq });
      return `req-bf-${this.backfillRequests.length}`;
    }
  }
  return { FakeWsClient };
});

type FakeWsClient = InstanceType<typeof FakeWsClient>;

vi.mock('../../../src/lib/ws', () => {
  const encode = (s: string): string => {
    const bytes = new TextEncoder().encode(s);
    let bin = '';
    for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin);
  };
  const decode = (b64: string): string => {
    const bin = atob(b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return new TextDecoder('utf-8').decode(bytes);
  };
  return {
    SessionWsClient: FakeWsClient,
    decodeChunkToText: decode,
    encodeUtf8ToBase64: encode,
  };
});

import { useWorkspaceStore } from '../../../src/stores/workspace';

// --- fetch mock ---

const DETAIL = {
  id: 'sess-1',
  title: 'Claude Code · demo',
  cliType: 'claudecode',
  state: 'running',
  control: { state: 'you' },
  lastActivityAt: new Date().toISOString(),
  workdir: '/users/dev/demo',
  startedAt: new Date().toISOString(),
  earliestSeq: 0,
  latestSeq: 0,
};

function mockFetch(detail: Record<string, unknown> = DETAIL) {
  const fn = vi.fn(async (input: unknown) => {
    const url = String(input);
    if (url.includes('/stop')) {
      return { ok: true, status: 200, json: async () => ({ ...detail, state: 'stopped' }) } as Response;
    }
    return { ok: true, status: 200, json: async () => detail } as Response;
  });
  vi.stubGlobal('fetch', fn);
  return fn;
}

function attachedEvent(over: Record<string, unknown> = {}) {
  return {
    type: 'session.attached',
    requestId: 'req-a',
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
    ...over,
  };
}

function b64(text: string): string {
  const bytes = new TextEncoder().encode(text);
  let bin = '';
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin);
}

async function openStore() {
  const store = useWorkspaceStore();
  await store.open('sess-1');
  const client = FakeWsClient.instances[FakeWsClient.instances.length - 1];
  return { store, client };
}

function fireAttached(client: FakeWsClient, event: Record<string, unknown> = attachedEvent()) {
  client.opts.onEvent(event);
  client.opts.onStateChange({ state: 'attached', attempt: 0, nextDelayMs: null, terminalReason: null });
}

beforeEach(() => {
  setActivePinia(createPinia());
  FakeWsClient.instances = [];
  mockFetch();
});

describe('open → attach', () => {
  it('加载 detail 并创建 WS 客户端；无 replay 时 getLastSeq 为 undefined（omitted）', async () => {
    const { store, client } = await openStore();
    expect(store.detail?.title).toBe('Claude Code · demo');
    expect(client.opts.sessionId).toBe('sess-1');
    expect(client.opts.getLastSeq()).toBeUndefined();
    expect(store.loading).toBe(false);
  });

  it('detail 404 → 分类错误呈现，不创建 WS', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: false,
        status: 404,
        json: async () => ({ requestId: 'r', code: 'session.not_found', layer: 'session', message: 'session not found', actionHint: 'retry' }),
      })),
    );
    const store = useWorkspaceStore();
    await store.open('missing');
    expect(store.loadError).not.toBeNull();
    expect(FakeWsClient.instances).toHaveLength(0);
  });
});

describe('时间线组装', () => {
  it('attached history（output + 边界）→ 内容块 + 边界原位', async () => {
    const { store, client } = await openStore();
    fireAttached(
      client,
      attachedEvent({
        earliestSeq: 1,
        latestSeq: 3,
        history: [
          { type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('hello\n') },
          { type: 'output', sessionId: 'sess-1', seq: 2, chunk: b64('world\n') },
          { type: 'session.state', sessionId: 'sess-1', state: 'running', restartBoundary: true, seq: 3, occurredAt: '2026-08-03T01:00:00Z' },
        ],
      }),
    );
    const kinds = store.timelineItems.map((i) => i.kind);
    expect(kinds).toEqual(['mono', 'boundary']);
    const boundary = store.timelineItems[1];
    if (boundary.kind !== 'boundary') throw new Error('expected boundary');
    expect(boundary.seq).toBe(3);
    expect(store.latestSeq).toBe(3);
    expect(client.opts.getLastSeq()).toBe(3);
  });

  it('live output 追加到活跃段；边界后新输出另起新段', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('first\n') });
    client.opts.onEvent({ type: 'session.state', sessionId: 'sess-1', state: 'running', restartBoundary: true, seq: 2, occurredAt: '2026-08-03T01:00:00Z' });
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 3, chunk: b64('after-restart\n') });
    const kinds = store.timelineItems.map((i) => i.kind);
    expect(kinds).toEqual(['mono', 'boundary', 'mono']);
    const texts = store.timelineItems.filter((i) => i.kind === 'mono').map((i) => (i as { text: string }).text);
    expect(texts).toEqual(['first', 'after-restart']);
  });

  it('attached gap → GapMarker 原位；backfill frames 补齐原位替换', async () => {
    const { store, client } = await openStore();
    // 客户端持 lastSeq=5，服务端 earliest=10 → gap [6,9]
    fireAttached(
      client,
      attachedEvent({
        earliestSeq: 10,
        latestSeq: 11,
        snapshot: {
          connection: { state: 'connected' },
          auth: { state: 'authorized' },
          session: { state: 'running' },
          control: { state: 'you' },
          history: { state: 'gap', gap: { code: 'history.gap', fromSeq: 6, toSeq: 9 } },
        },
        history: [
          { type: 'output', sessionId: 'sess-1', seq: 10, chunk: b64('retained\n') },
          { type: 'output', sessionId: 'sess-1', seq: 11, chunk: b64('latest\n') },
        ],
      }),
    );
    const gap = store.timelineItems.find((i) => i.kind === 'gap');
    expect(gap).toBeDefined();
    if (!gap || gap.kind !== 'gap') return;
    expect([gap.fromSeq, gap.toSeq]).toEqual([6, 9]);

    // 补齐：backfill.result frames 原位替换缺口
    store.requestGapFill(gap.id);
    expect(client.backfillRequests).toEqual([{ fromSeq: 6, toSeq: 9 }]);
    client.opts.onEvent({
      type: 'backfill.result',
      requestId: 'req-bf-1',
      sessionId: 'sess-1',
      fromSeq: 6,
      toSeq: 7,
      earliestSeq: 6,
      latestSeq: 11,
      frames: [
        { type: 'output', sessionId: 'sess-1', seq: 6, chunk: b64('filled-6\n') },
        { type: 'output', sessionId: 'sess-1', seq: 7, chunk: b64('filled-7\n') },
      ],
    });
    const kinds = store.timelineItems.map((i) => i.kind);
    expect(kinds).toEqual(['mono', 'mono']); // 缺口已被内容原位替换
    const first = store.timelineItems[0];
    if (first.kind !== 'mono') throw new Error('expected mono');
    expect(first.text).toContain('filled-6');
  });

  it('backfill gap 变体 → 缺口 exhausted（诚实呈现不可补齐）', async () => {
    const { store, client } = await openStore();
    fireAttached(
      client,
      attachedEvent({
        earliestSeq: 10,
        latestSeq: 10,
        snapshot: {
          connection: { state: 'connected' },
          auth: { state: 'authorized' },
          session: { state: 'running' },
          control: { state: 'you' },
          history: { state: 'gap', gap: { code: 'history.gap', fromSeq: 6, toSeq: 9 } },
        },
        history: [{ type: 'output', sessionId: 'sess-1', seq: 10, chunk: b64('x\n') }],
      }),
    );
    const gap = store.timelineItems.find((i) => i.kind === 'gap');
    if (!gap) throw new Error('expected gap');
    store.requestGapFill(gap.id);
    client.opts.onEvent({
      type: 'backfill.result',
      requestId: 'req-bf-1',
      sessionId: 'sess-1',
      fromSeq: 6,
      toSeq: 9,
      earliestSeq: 10,
      latestSeq: 10,
      gap: { code: 'history.gap', fromSeq: 6, toSeq: 9 },
    });
    const after = store.timelineItems.find((i) => i.kind === 'gap');
    if (!after || after.kind !== 'gap') throw new Error('gap should remain');
    expect(after.exhausted).toBe(true);
  });
});

describe('Composer 防重复与控制权过滤', () => {
  it('控制者发送：input=text+\\r；draft 清空（确认发送语义）→ 连续第二次发送被拒', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    store.draft = 'npm run build';
    expect(store.sendDraft()).toBe(true);
    expect(client.sentInputs).toEqual(['npm run build\r']);
    expect(store.draft).toBe('');
    // 防连点：draft 已清空，第二次点击为空发送被拒
    expect(store.sendDraft()).toBe(false);
    expect(client.sentInputs).toHaveLength(1);
    // 用户指令块上屏 + 历史记录
    expect(store.timelineItems.some((i) => i.kind === 'user')).toBe(true);
    expect(store.commandHistory[0]).toBe('npm run build');
  });

  it('观察者（desktop 控制）禁用输入：sendDraft/sendAnswer 均不发送', async () => {
    const { store, client } = await openStore();
    fireAttached(
      client,
      attachedEvent({
        snapshot: {
          connection: { state: 'connected' },
          auth: { state: 'authorized' },
          session: { state: 'running' },
          control: { state: 'desktop' },
          history: { state: 'continuous' },
        },
      }),
    );
    expect(store.canWrite).toBe(false);
    expect(store.writeBlockReason).toContain('桌面端');
    store.draft = 'should-not-send';
    expect(store.sendDraft()).toBe(false);
    expect(store.sendAnswer('1')).toBe(false);
    expect(client.sentInputs).toHaveLength(0);
    expect(store.draft).toBe('should-not-send'); // 草稿保留
  });

  it('未 attached 时不可写', async () => {
    const { store } = await openStore();
    store.draft = 'x';
    expect(store.sendDraft()).toBe(false);
  });

  it('sendAnswer：input 为完整载荷，原样发送', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    expect(store.sendAnswer('2\r')).toBe(true); // 编号选项（含回车）
    expect(store.sendAnswer('y')).toBe(true); // y/n 单按键
    expect(client.sentInputs).toEqual(['2\r', 'y']);
  });
});

describe('control/auth/unknown 事件', () => {
  it('control.state → other：投影更新 + 被收回原因提示；→ you：提示清除', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'other', deviceName: 'iPad', reason: 'control taken by another device', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.control).toEqual({ state: 'other', deviceName: 'iPad' });
    expect(store.controlNotice).toContain('control taken');
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'you', reason: 'reacquired', occurredAt: '2026-08-03T01:01:00Z' });
    expect(store.control).toEqual({ state: 'you' });
    expect(store.controlNotice).toBeNull();
  });

  it('auth.revoked → authLost（页面踢回 PG-01）', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    client.opts.onEvent({ type: 'auth.revoked', reason: 'device_revoked', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.authLost).toBe('revoked');
  });

  it('unknown force-read-only → 保守降级为无控制权 + 降级提示（不读原始字段）', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    client.opts.onEvent({
      type: 'unknown',
      wireType: 'control.state',
      reason: 'unknown-enum',
      fallback: 'force-read-only',
      metadata: { inputKind: 'object', hasRequestId: false, hasSessionId: true, hasSeq: false },
    });
    expect(store.control).toEqual({ state: 'none' });
    expect(store.degradedNotice).not.toBeNull();
  });
});

describe('原始输出流（PG-04 诊断视图；M2-D）', () => {
  it('attach history 回放 + live output 均入流（原文含 ANSI，不做行处理）', async () => {
    const { store, client } = await openStore();
    const received: string[] = [];
    store.subscribeRawOutput((t) => received.push(t));
    fireAttached(
      client,
      attachedEvent({
        history: [{ type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('\x1b[32mok\x1b[0m\r\n') }],
        latestSeq: 1,
      }),
    );
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 2, chunk: b64('live\r\n') });
    expect(store.getRawTranscript()).toBe('\x1b[32mok\x1b[0m\r\nlive\r\n');
    expect(received).toEqual(['\x1b[32mok\x1b[0m\r\n', 'live\r\n']);
  });

  it('退订后不再接收；open() 新会话清空缓冲', async () => {
    const { store, client } = await openStore();
    const received: string[] = [];
    const unsub = store.subscribeRawOutput((t) => received.push(t));
    fireAttached(client);
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('a') });
    unsub();
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 2, chunk: b64('b') });
    expect(received).toEqual(['a']);
    expect(store.getRawTranscript()).toBe('ab');
    await store.open('sess-1');
    expect(store.getRawTranscript()).toBe('');
  });

  it('backfill 旧历史不注入直播流（乱序帧不在网格伪造内容）', async () => {
    const { store, client } = await openStore();
    fireAttached(
      client,
      attachedEvent({
        snapshot: {
          connection: { state: 'connected' },
          auth: { state: 'authorized' },
          session: { state: 'running' },
          control: { state: 'you' },
          history: { state: 'gap', gap: { fromSeq: 3, toSeq: 5 } },
        },
        latestSeq: 6,
      }),
    );
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 7, chunk: b64('new\r\n') });
    client.opts.onEvent({
      type: 'backfill.result',
      requestId: 'req-x',
      sessionId: 'sess-1',
      frames: [{ type: 'output', sessionId: 'sess-1', seq: 3, chunk: b64('old\r\n') }],
    });
    expect(store.getRawTranscript()).toBe('new\r\n');
  });
});

describe('停止运行', () => {
  it('控制者 stopRunning：REST 成功 → 会话态更新；防连点', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    expect(await store.stopRunning()).toBe(true);
    expect(store.sessionState).toBe('stopped');
    expect(store.stopping).toBe(false);
  });

  it('观察者 stopRunning 直接拒绝（不发请求）', async () => {
    const fetchFn = mockFetch();
    const { store, client } = await openStore();
    fireAttached(
      client,
      attachedEvent({
        snapshot: {
          connection: { state: 'connected' },
          auth: { state: 'authorized' },
          session: { state: 'running' },
          control: { state: 'other', deviceName: 'iPad' },
          history: { state: 'continuous' },
        },
      }),
    );
    const before = fetchFn.mock.calls.length;
    expect(await store.stopRunning()).toBe(false);
    expect(fetchFn.mock.calls.length).toBe(before);
  });
});
