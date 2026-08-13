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
    sentInputFrames: { id: string; requestId: string; data: string }[] = [];
    backfillRequests: { fromSeq: number; toSeq: number }[] = [];
    disposed = false;
    forceReconnects = 0;

    constructor(opts: FakeClientOptions) {
      this.opts = opts;
      FakeWsClient.instances.push(this);
    }
    connect(): void {}
    dispose(): void {
      this.disposed = true;
    }
    forceReconnect(): void {
      this.forceReconnects += 1;
    }
    sendInput(text: string): boolean {
      this.sentInputs.push(text);
      return true;
    }
    sendInputFrame(frame: { id: string; requestId: string; data: string }): boolean {
      this.sentInputFrames.push({ id: frame.id, requestId: frame.requestId, data: frame.data });
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
    createOutputChunkDecoder: () => {
      let decoder = new TextDecoder('utf-8');
      return {
        decode: (b64: string) => {
          const bin = atob(b64);
          const bytes = new Uint8Array(bin.length);
          for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
          return decoder.decode(bytes, { stream: true });
        },
        flush: () => decoder.decode(),
        reset: () => { decoder = new TextDecoder('utf-8'); },
      };
    },
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

  it('live reorder 缺口 → GapMarker 原位可补齐；backfill frames 补齐原位替换', async () => {
    const { store, client } = await openStore();
    // 契约（addendum §1.2）：attached-time gap 只能是已逐出 origin 段（toSeq+1=earliestSeq）；
    // 可恢复缺口来自 live reorder（seq>F+1 缓冲时显示，design §3）。
    fireAttached(
      client,
      attachedEvent({
        earliestSeq: 1,
        latestSeq: 1,
        history: [{ type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('one\n') }],
      }),
    );
    // live seq=3 越洞 [2,2] → 缓冲高帧 + recoverable 原位标记。
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 3, chunk: b64('three\n') });
    const gap = store.timelineItems.find((i) => i.kind === 'gap');
    expect(gap).toBeDefined();
    if (!gap || gap.kind !== 'gap') return;
    expect([gap.fromSeq, gap.toSeq]).toEqual([2, 2]);
    expect(gap.exhausted).toBe(false);
    expect(gap.fillable).toBe(true);

    // 补齐：backfill.result frames 原位替换缺口
    store.requestGapFill(gap.id);
    expect(client.backfillRequests).toEqual([{ fromSeq: 2, toSeq: 2 }]);
    client.opts.onEvent({
      type: 'backfill.result',
      requestId: 'req-bf-1',
      sessionId: 'sess-1',
      fromSeq: 2,
      toSeq: 2,
      earliestSeq: 1,
      latestSeq: 3,
      frames: [{ type: 'output', sessionId: 'sess-1', seq: 2, chunk: b64('filled-2\n') }],
    });
    expect(store.timelineItems.some((i) => i.kind === 'gap')).toBe(false); // 缺口已被内容原位替换
    const texts = store.timelineItems.filter((i) => i.kind === 'mono').map((i) => (i as { text: string }).text);
    expect(texts.some((t) => t.includes('filled-2'))).toBe(true);
    // 原位语义：补齐段插入在 marker 原位置（sortKey=fromSeq）；已合并段内不做跨段重排。
    const filledIdx = store.timelineItems.findIndex((i) => i.kind === 'mono' && (i as { text: string }).text.includes('filled-2'));
    const attachedIdx = store.timelineItems.findIndex((i) => i.kind === 'mono' && (i as { text: string }).text.includes('one'));
    expect(filledIdx).toBeGreaterThan(attachedIdx);
    // M3-004：correlated backfill 推进 frontier（F: 1 → 2 → 吸收 pending seq3 → 3），
    // 缺口消失，高帧 seq3 不再缓冲。补齐帧 + 吸收帧均计入多重集。
    expect(store.settledFrontier).toBe(3);
    const snap = store.continuitySnapshot();
    expect(snap.appliedSeqCounts['2']).toBe(1);
    expect(snap.appliedSeqCounts['3']).toBe(1);
    expect(snap.gapRanges).toEqual([]);
  });

  it('attached earliestSeq>F+1 → 低于保留窗的缺口裁定 settled-unavailable（exhausted 原位保留）', async () => {
    const { store, client } = await openStore();
    // 首次 attach：F=0，earliest=10 → [1,9] 已逐出（design §3：显式不可恢复提示）。
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
          history: { state: 'gap', gap: { code: 'history.gap', fromSeq: 1, toSeq: 9 } },
        },
        history: [{ type: 'output', sessionId: 'sess-1', seq: 10, chunk: b64('retained\n') }],
      }),
    );
    const gap = store.timelineItems.find((i) => i.kind === 'gap');
    if (!gap || gap.kind !== 'gap') throw new Error('expected gap');
    expect([gap.fromSeq, gap.toSeq]).toEqual([1, 9]);
    expect(gap.exhausted).toBe(true);
    expect(gap.fillable).toBe(false);
    // exhausted 缺口不接受补齐请求（显式不可恢复）。
    store.requestGapFill(gap.id);
    expect(client.backfillRequests).toHaveLength(0);
    // frontier 允许跨过 settled-unavailable 范围（design §3）。
    expect(store.settledFrontier).toBe(10);
  });

  it('backfill gap 变体 → 缺口 exhausted + frontier 推进 + pending 吸收（M3-004 authoritative gap 结算）', async () => {
    const { store, client } = await openStore();
    fireAttached(
      client,
      attachedEvent({
        earliestSeq: 1,
        latestSeq: 1,
        history: [{ type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('one\n') }],
      }),
    );
    // F=1；live reorder 缺口 [2,2]（recoverable），seq3 缓冲高帧。
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 3, chunk: b64('three\n') });
    const gap = store.timelineItems.find((i) => i.kind === 'gap');
    if (!gap) throw new Error('expected gap');
    store.requestGapFill(gap.id);
    expect(client.backfillRequests).toEqual([{ fromSeq: 2, toSeq: 2 }]);
    client.opts.onEvent({
      type: 'backfill.result',
      requestId: 'req-bf-1',
      sessionId: 'sess-1',
      fromSeq: 2,
      toSeq: 2,
      earliestSeq: 3,
      latestSeq: 3,
      gap: { code: 'history.gap', fromSeq: 2, toSeq: 2 },
    });
    const after = store.timelineItems.find((i) => i.kind === 'gap');
    if (!after || after.kind !== 'gap') throw new Error('gap should remain');
    expect(after.exhausted).toBe(true);
    expect(after.fillable).toBe(false);
    // M3-004：authoritative gap 结算推进 frontier（1 → 2）并吸收 pending seq3（→ 3）。
    // gap marker 仍原位保留为 settled-unavailable（liveReorder=false 不被剪除）。
    expect(store.settledFrontier).toBe(3);
    const snap = store.continuitySnapshot();
    expect(snap.gapRanges).toEqual([{ fromSeq: 2, toSeq: 2, state: 'exhausted' }]);
    expect(snap.appliedSeqCounts['3']).toBe(1);
    // 高帧 seq3 投影到时间线（不再永久隐藏）。
    expect(store.timelineItems.some((i) => i.kind === 'mono' && (i as { text: string }).text.includes('three'))).toBe(true);
  });

  it('M3-004：backfill 范围错配 → 恢复 filling=false（不留下永久 filling marker）', async () => {
    const { store, client } = await openStore();
    fireAttached(
      client,
      attachedEvent({
        earliestSeq: 1,
        latestSeq: 1,
        history: [{ type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('one\n') }],
      }),
    );
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 3, chunk: b64('three\n') });
    const gap = store.timelineItems.find((i) => i.kind === 'gap');
    if (!gap || gap.kind !== 'gap') throw new Error('expected gap');
    store.requestGapFill(gap.id);
    expect(store.fillingGapIds.has(gap.id)).toBe(true);
    // 服务端返回的 fromSeq/toSeq 与请求 marker 不匹配 → 恢复 filling=false。
    client.opts.onEvent({
      type: 'backfill.result',
      requestId: 'req-bf-1',
      sessionId: 'sess-1',
      fromSeq: 5,
      toSeq: 6,
      earliestSeq: 3,
      latestSeq: 3,
      frames: [{ type: 'output', sessionId: 'sess-1', seq: 5, chunk: b64('mismatch\n') }],
    });
    const after = store.timelineItems.find((i) => i.kind === 'gap');
    if (!after || after.kind !== 'gap') throw new Error('gap should remain');
    expect(store.fillingGapIds.has(gap.id)).toBe(false);
    // 不应用错配结果（不伪造内容、不推进 frontier）。
    expect(after.exhausted).toBe(false);
    expect(store.settledFrontier).toBe(1);
    expect(store.timelineItems.some((i) => i.kind === 'mono' && (i as { text: string }).text.includes('mismatch'))).toBe(false);
    // gap 仍可重新请求（filling 已清除）。
    store.requestGapFill(gap.id);
    expect(store.fillingGapIds.has(gap.id)).toBe(true);
  });
});

// M3-B settledFrontier 连续 possession（design §3）—— late-attach cursor 修复 +
// live 帧越洞缓冲。detail.latestSeq 不得作游标；首次 attach omit；重连发送 frontier。
describe('settledFrontier 连续 possession（design §3）', () => {
  it('detail.latestSeq 是 REST advisory bound，不作 attach 游标（bug 修复）', async () => {
    // REST detail 声称 latestSeq=50，但客户端未持有任何 replay frame。
    mockFetch({ ...DETAIL, earliestSeq: 41, latestSeq: 50 });
    const { client } = await openStore();
    // 首次 attach：getLastSeq 必须返回 undefined（omit），绝不能用 50。
    expect(client.opts.getLastSeq()).toBeUndefined();
  });

  it('首次 attach 后 frontier=latestSeq；重连 getLastSeq 返回 frontier', async () => {
    const { store, client } = await openStore();
    fireAttached(
      client,
      attachedEvent({
        earliestSeq: 1,
        latestSeq: 5,
        history: [
          { type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('a\n') },
          { type: 'output', sessionId: 'sess-1', seq: 2, chunk: b64('b\n') },
        ],
      }),
    );
    expect(store.settledFrontier).toBe(5);
    expect(store.hasCompletedAttach).toBe(true);
    // 重连 cursor = frontier（显式发送，含 0）。
    expect(client.opts.getLastSeq()).toBe(5);
  });

  it('attached 空流 frontier=0；重连 getLastSeq 返回 0（显式发送）', async () => {
    const { store, client } = await openStore();
    fireAttached(client, attachedEvent({ earliestSeq: 0, latestSeq: 0, history: [] }));
    expect(store.settledFrontier).toBe(0);
    expect(store.hasCompletedAttach).toBe(true);
    // hasCompletedAttach=true 且 frontier=0 → getLastSeq 返回 0（重连显式），
    // 区别于首次 omit（undefined）。
    expect(client.opts.getLastSeq()).toBe(0);
  });

  it('live seq=F+1 投影并推进 frontier', async () => {
    const { store, client } = await openStore();
    fireAttached(client, attachedEvent({ earliestSeq: 0, latestSeq: 0, history: [] }));
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('x\n') });
    expect(store.settledFrontier).toBe(1);
    const mono = store.timelineItems.find((i) => i.kind === 'mono');
    expect(mono && mono.kind === 'mono' && mono.text).toContain('x');
  });

  it('live seq>F+1 仅缓冲、显示 recoverable gap、不投影高帧、不推进 F', async () => {
    const { store, client } = await openStore();
    fireAttached(client, attachedEvent({ earliestSeq: 0, latestSeq: 0, history: [] }));
    // F=0；投递 seq=3（越洞 [1,2]）→ 缓冲，不投影。
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 3, chunk: b64('c\n') });
    expect(store.settledFrontier).toBe(0);
    const gap = store.timelineItems.find((i) => i.kind === 'gap');
    expect(gap && gap.kind === 'gap' && [gap.fromSeq, gap.toSeq]).toEqual([1, 2]);
    // seq=3 的高帧不投影（无 mono）。
    expect(store.timelineItems.some((i) => i.kind === 'mono')).toBe(false);
    // 补齐 seq=1 → 投影 1，吸收 pending 不可（差 seq=2），F=1，gap 变 [2,2]。
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('a\n') });
    expect(store.settledFrontier).toBe(1);
    // 补齐 seq=2 → 投影 2，吸收 pending seq=3，F=3，gap 清除。
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 2, chunk: b64('b\n') });
    expect(store.settledFrontier).toBe(3);
    expect(store.timelineItems.some((i) => i.kind === 'gap')).toBe(false);
    // 三帧无边界 → 合并为一个活跃段。
    const mono = store.timelineItems.find((i) => i.kind === 'mono');
    expect(mono && mono.kind === 'mono' && mono.text).toContain('a');
    expect(mono && mono.kind === 'mono' && mono.text).toContain('c');
  });

  it('duplicate/late seq<=F 被丢弃，不重复投影', async () => {
    const { store, client } = await openStore();
    fireAttached(
      client,
      attachedEvent({
        earliestSeq: 1,
        latestSeq: 2,
        history: [
          { type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('a\n') },
          { type: 'output', sessionId: 'sess-1', seq: 2, chunk: b64('b\n') },
        ],
      }),
    );
    expect(store.settledFrontier).toBe(2);
    // 重发 seq=1（<= F）→ 丢弃。
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('DUP\n') });
    expect(store.settledFrontier).toBe(2);
    // 重发 seq=1 未新增内容（仍为一个段）。
    expect(store.timelineItems.filter((i) => i.kind === 'mono')).toHaveLength(1);
    const mono = store.timelineItems.find((i) => i.kind === 'mono');
    expect(mono && mono.kind === 'mono' && mono.text).toContain('a');
    expect(mono && mono.kind === 'mono' && mono.text).toContain('b');
  });

  it('reattach 清空 stale pending 并以 attached history 为权威', async () => {
    const { store, client } = await openStore();
    fireAttached(client, attachedEvent({ earliestSeq: 0, latestSeq: 0, history: [] }));
    // 缓冲 seq=3（越洞），F=0。
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 3, chunk: b64('c\n') });
    expect(store.settledFrontier).toBe(0);
    // 重连：attached history 权威返回 seq=1,2,3。
    fireAttached(
      client,
      attachedEvent({
        earliestSeq: 1,
        latestSeq: 3,
        history: [
          { type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('a\n') },
          { type: 'output', sessionId: 'sess-1', seq: 2, chunk: b64('b\n') },
          { type: 'output', sessionId: 'sess-1', seq: 3, chunk: b64('c\n') },
        ],
      }),
    );
    expect(store.settledFrontier).toBe(3);
    expect(store.timelineItems.some((i) => i.kind === 'gap')).toBe(false);
    const mono = store.timelineItems.find((i) => i.kind === 'mono');
    expect(mono && mono.kind === 'mono' && mono.text).toContain('a');
    expect(mono && mono.kind === 'mono' && mono.text).toContain('c');
  });
});

describe('Composer 防重复与控制权过滤', () => {
  it('控制者发送：canonical input 帧（text+\\r）；draft 清空（确认发送语义）→ 连续第二次发送被拒', async () => {
    const { store, client } = await openStore();
    fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
    store.draft = 'npm run build';
    expect(store.sendDraft()).toBe(true);
    // CG-03 M3-001：经 canonical outbox（msg-v1- MessageID），不再 legacy 直发。
    expect(client.sentInputs).toHaveLength(0);
    expect(client.sentInputFrames).toHaveLength(1);
    const f = client.sentInputFrames[0];
    expect(f.id).toMatch(/^msg-v1-[0-9a-f]{32}$/);
    expect(f.requestId).toMatch(/^req-v1-[0-9a-f]{32}$/);
    const decode = (v: string) => new TextDecoder().decode(Uint8Array.from(atob(v), (c) => c.charCodeAt(0)));
    expect(decode(f.data)).toBe('npm run build\r');
    expect(store.draft).toBe('');
    // 防连点：draft 已清空，第二次点击为空发送被拒
    expect(store.sendDraft()).toBe(false);
    expect(client.sentInputFrames).toHaveLength(1);
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
    expect(client.sentInputFrames).toHaveLength(0);
    expect(store.draft).toBe('should-not-send'); // 草稿保留
  });

  it('未 attached 时不可写', async () => {
    const { store } = await openStore();
    store.draft = 'x';
    expect(store.sendDraft()).toBe(false);
  });

  it('sendAnswer：结构化应答经 canonical outbox（M3-001）', async () => {
    const { store, client } = await openStore();
    fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
    expect(store.sendAnswer('2\r')).toBe(true); // 编号选项（含回车）
    // CG-03 M3-001：结构化应答走 canonical outbox（CSPRNG MessageID），不再 legacy。
    expect(client.sentInputs).toHaveLength(0);
    expect(client.sentInputFrames).toHaveLength(1);
    const f = client.sentInputFrames[0];
    expect(f.id).toMatch(/^msg-v1-[0-9a-f]{32}$/);
    const decode = (v: string) => new TextDecoder().decode(Uint8Array.from(atob(v), (c) => c.charCodeAt(0)));
    expect(decode(f.data)).toBe('2\r');
    // single-flight FIFO：第二笔回答在第一笔 ACK 前不发送（队首未结算）。
    expect(store.sendAnswer('y')).toBe(true);
    expect(client.sentInputFrames).toHaveLength(1);
    // ACK 队首后，第二笔成为新队首并发送。
    client.opts.onEvent({ type: 'input.ack', requestId: f.requestId, sessionId: 'sess-1', id: f.id });
    expect(client.sentInputFrames).toHaveLength(2);
    expect(decode(client.sentInputFrames[1].data)).toBe('y');
  });
});

// M3-INT R1：capability fail-closed（M3-001）、pending 冻结与 reattach 顺序（M3-002）、
// live-gap frontier 与有界 pending（M3-004）。
describe('M3-INT R1 边界与冻结', () => {
  it('M3-001 capability fail-closed：inputAckMode 缺失 → 只读，sendDraft/sendAnswer 被拒', async () => {
    const { store, client } = await openStore();
    fireAttached(client); // 默认无 inputAckMode
    expect(store.canWrite).toBe(false);
    expect(store.writeBlockReason).toContain('不支持输入确认');
    store.draft = 'should-not-send';
    expect(store.sendDraft()).toBe(false);
    expect(store.sendAnswer('1')).toBe(false);
    expect(client.sentInputs).toHaveLength(0);
    expect(client.sentInputFrames).toHaveLength(0);
    expect(store.draft).toBe('should-not-send'); // 草稿保留
  });

  it('M3-001 capability：inputAckMode 协商后 canWrite=true、writeBlockReason=null', async () => {
    const { store, client } = await openStore();
    fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
    expect(store.canWrite).toBe(true);
    expect(store.writeBlockReason).toBeNull();
  });

  it('M3-002：restart boundary 冻结 pending input（不落到新 run）', async () => {
    const { store, client } = await openStore();
    fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
    store.draft = 'cmd';
    expect(store.sendDraft()).toBe(true);
    expect(store.outboxView.pendingCount).toBe(1);
    // restart boundary（live 帧）→ 冻结
    client.opts.onEvent({ type: 'session.state', sessionId: 'sess-1', state: 'running', restartBoundary: true, seq: 1, occurredAt: '2026-08-03T01:00:00Z' });
    const user = store.timelineItems.find((i) => i.kind === 'user');
    if (!user || user.kind !== 'user') throw new Error('expected user item');
    expect(user.delivery).toBe('halted');
    expect(store.outboxView.haltedCount).toBe(1);
    expect(store.outboxView.pendingCount).toBe(0);
  });

  it('M3-002：terminal 会话态（exited）冻结 pending input', async () => {
    const { store, client } = await openStore();
    fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
    store.draft = 'cmd';
    expect(store.sendDraft()).toBe(true);
    client.opts.onEvent({ type: 'session.state', sessionId: 'sess-1', state: 'exited', occurredAt: '2026-08-03T01:00:00Z' });
    const user = store.timelineItems.find((i) => i.kind === 'user');
    if (!user || user.kind !== 'user') throw new Error('expected user item');
    expect(user.delivery).toBe('halted');
  });

  it('M3-002：reattach 权威丢失（control=other）→ 冻结，队首不重发', async () => {
    vi.useFakeTimers();
    try {
      const { store, client } = await openStore();
      fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
      store.draft = 'cmd';
      expect(store.sendDraft()).toBe(true);
      expect(client.sentInputFrames).toHaveLength(1);
      // 断线 → 重连；reattach snapshot 显示 control=other（权威丢失）。
      client.opts.onStateChange({ state: 'reconnecting', attempt: 1, nextDelayMs: 750, terminalReason: null });
      client.opts.onStateChange({ state: 'awaiting-attach', attempt: 1, nextDelayMs: null, terminalReason: null });
      fireAttached(
        client,
        attachedEvent({
          inputAckMode: 'session-window-v1',
          snapshot: {
            connection: { state: 'connected' },
            auth: { state: 'authorized' },
            session: { state: 'running' },
            control: { state: 'other', deviceName: 'iPad' },
            history: { state: 'continuous' },
          },
        }),
      );
      // 权威丢失 → 冻结，队首不重发（仍 1 帧）。
      expect(client.sentInputFrames).toHaveLength(1);
      expect(store.outboxView.haltedCount).toBe(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('M3-002：reattach 附带 restart boundary → 冻结，不跨 run 重发', async () => {
    vi.useFakeTimers();
    try {
      const { store, client } = await openStore();
      fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
      store.draft = 'cmd';
      expect(store.sendDraft()).toBe(true);
      expect(client.sentInputFrames).toHaveLength(1);
      client.opts.onStateChange({ state: 'reconnecting', attempt: 1, nextDelayMs: 750, terminalReason: null });
      client.opts.onStateChange({ state: 'awaiting-attach', attempt: 1, nextDelayMs: null, terminalReason: null });
      // reattach 权威 history 含 restart boundary → 新 run，不重发旧命令。
      fireAttached(
        client,
        attachedEvent({
          inputAckMode: 'session-window-v1',
          earliestSeq: 1,
          latestSeq: 1,
          history: [{ type: 'session.state', sessionId: 'sess-1', state: 'running', restartBoundary: true, seq: 1, occurredAt: '2026-08-03T01:00:00Z' }],
        }),
      );
      expect(client.sentInputFrames).toHaveLength(1);
      expect(store.outboxView.haltedCount).toBe(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('M3-004：同 Seq 冲突 → forceReconnect（流不一致 fail-closed）', async () => {
    const { client } = await openStore();
    fireAttached(client, attachedEvent({ earliestSeq: 0, latestSeq: 0, history: [] })); // F=0
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 3, chunk: b64('a') }); // 缓冲 seq3（越洞 [1,2]）
    expect(client.forceReconnects).toBe(0);
    // 同 seq=3 再次到达（不同 chunk）→ 冲突 → forceReconnect
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 3, chunk: b64('b') });
    expect(client.forceReconnects).toBe(1);
  });

  it('M3-004：live pending 字节越界 → forceReconnect', async () => {
    const { client } = await openStore();
    fireAttached(client, attachedEvent({ earliestSeq: 0, latestSeq: 0, history: [] })); // F=0
    // 大 chunk 帧快速逼近 1MiB 字节界（每帧 ~50KiB+ → 约十余帧越界）。
    const big = b64('x'.repeat(50000));
    for (let seq = 2; seq < 60; seq += 1) {
      client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq, chunk: big });
      if (client.forceReconnects > 0) break;
    }
    expect(client.forceReconnects).toBeGreaterThan(0);
  });
});

// M3-INT R2-001：takeover 后重新获权，新 input 仍可发送（旧 entry 不重发）。
// takeover/temporary authority loss = 非永久冻结（freezePending）；revoke/remove/
// terminal 才永久禁用（halt）。
describe('M3-INT R2-001：takeover → 重新获权 → 新 input 可发送', () => {
  it('takeover 冻结旧 entry；reacquire 后新 input 成功发送（旧 entry 不重发）', async () => {
    // acquire REST 返回 ControlSnapshot（{state:'you'}），区别于 detail。
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: unknown) => {
        const url = String(input);
        if (url.includes('acquire')) {
          return { ok: true, status: 200, json: async () => ({ state: 'you' }) } as Response;
        }
        return { ok: true, status: 200, json: async () => DETAIL } as Response;
      }),
    );
    const { store, client } = await openStore();
    fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
    // 首条命令发送（未 ACK，队首 pending）。
    store.draft = 'first-cmd';
    expect(store.sendDraft()).toBe(true);
    expect(client.sentInputFrames).toHaveLength(1);
    const firstId = client.sentInputFrames[0].id;
    // takeover（desktop 收回）→ 非永久冻结：旧 entry halted，不重发。
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'desktop', reason: 'takeover', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.outboxView.haltedCount).toBe(1);
    expect(store.outboxView.pendingCount).toBe(0);
    expect(store.canWrite).toBe(false);
    // 重新获权（REST acquire 成功 → control=you）。
    expect(await store.acquire()).toBe(true);
    // 同页不重建 outbox：新命令可以发送（accept 不再被永久 halt 拒绝）。
    expect(store.canWrite).toBe(true);
    store.draft = 'second-cmd';
    expect(store.sendDraft()).toBe(true);
    expect(client.sentInputFrames).toHaveLength(2);
    const secondId = client.sentInputFrames[1].id;
    // 旧 entry（first-cmd）未被重发：第二条是全新 MessageID。
    expect(secondId).not.toBe(firstId);
    expect(secondId).toMatch(/^msg-v1-[0-9a-f]{32}$/);
    // 旧 entry 仍 halted（不自动重发）。
    expect(store.outboxView.haltedCount).toBe(1);
  });

  it('R2-001：terminal 会话态（exited）永久禁用——reacquire 后新 input 仍被拒', async () => {
    const { store, client } = await openStore();
    fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
    store.draft = 'cmd';
    expect(store.sendDraft()).toBe(true);
    // terminal 会话态 → 永久 halt（区别于 takeover 的非永久冻结）。
    client.opts.onEvent({ type: 'session.state', sessionId: 'sess-1', state: 'exited', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.outboxView.haltedCount).toBe(1);
    // 即便 session.state 回到 running，outbox 仍永久 halt（this.halted=true）。
    client.opts.onEvent({ type: 'session.state', sessionId: 'sess-1', state: 'running', occurredAt: '2026-08-03T01:01:00Z' });
    store.draft = 'after-exit';
    expect(store.sendDraft()).toBe(false);
    expect(client.sentInputFrames).toHaveLength(1);
  });

  it('R2-001：other 设备 takeover 后 reacquire，新 input 可发送（E-06 被收回→重新申请成功）', async () => {
    const { store, client } = await openStore();
    fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
    store.draft = 'before-takeover';
    expect(store.sendDraft()).toBe(true);
    // other 设备 takeover（E-06 映射「由 iPad 取得」）。
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'other', deviceName: 'iPad', reason: 'takeover', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.controlNotice?.text).toBe('控制权已由 iPad 取得');
    expect(store.canWrite).toBe(false);
    // E-06 收回后用户重新申请成功（control.state=you）。
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'you', reason: 'acquired', occurredAt: '2026-08-03T01:01:00Z' });
    expect(store.controlNotice).toBeNull();
    expect(store.canWrite).toBe(true);
    store.draft = 'after-reacquire';
    expect(store.sendDraft()).toBe(true);
    expect(client.sentInputFrames).toHaveLength(2);
  });
});

// M3-INT R3-001：you→none(connection_expired) 必须冻结 pending——断连导致失权与
// takeover 同等语义（design §7 E-06）。旧 entry 停止自动重发，不跨失权窗口继续 retry；
// 重新获权后旧 entry 不自动重发，只有全新 input 可发送。本地 release 同等停发但无 E-06。
describe('M3-INT R3-001：you→none(connection_expired) 冻结 pending + 重获权全链', () => {
  it('connection_expired none 冻结旧 entry；timer 不增 wire；reacquire 后旧 entry 不发/新 ID 可发', async () => {
    vi.useFakeTimers();
    try {
      const { store, client } = await openStore();
      fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
      store.draft = 'before-expiry';
      expect(store.sendDraft()).toBe(true);
      expect(client.sentInputFrames).toHaveLength(1);
      expect(store.outboxView.pendingCount).toBe(1);
      // you→none(connection_expired)：E-06 失权 + 非永久冻结（与 takeover 同等语义）。
      client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'none', reason: 'connection_expired', occurredAt: '2026-08-03T01:00:00Z' });
      expect(store.controlNotice?.kind).toBe('lost');
      expect(store.controlNotice?.text).toBe('控制连接已过期，控制权已收回');
      expect(store.outboxView.haltedCount).toBe(1);
      expect(store.outboxView.pendingCount).toBe(0);
      // 旧 entry timer 已停：推进时钟不再增加 wire attempt（design §7：不跨失权窗口 retry）。
      vi.advanceTimersByTime(60_000);
      expect(client.sentInputFrames).toHaveLength(1);
      // 重新获权（none→you）：旧 entry 不自动重发（design §5），但可发送全新 input。
      client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'you', reason: 'acquired', occurredAt: '2026-08-03T01:05:00Z' });
      expect(store.controlNotice).toBeNull();
      expect(store.canWrite).toBe(true);
      // reacquire 后 timer 推进：旧 entry 仍不重发（仍 1 帧；halted 不转 pending）。
      vi.advanceTimersByTime(60_000);
      expect(client.sentInputFrames).toHaveLength(1);
      store.draft = 'after-reacquire';
      expect(store.sendDraft()).toBe(true);
      // 全新 input 在新 authority 下成功发送（旧 entry 保持 halted）。
      expect(client.sentInputFrames).toHaveLength(2);
      expect(store.outboxView.haltedCount).toBe(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('本地 release（REST you→none）同等冻结旧 pending，但不显示 E-06 收回提示', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: unknown) => {
        const url = String(input);
        if (url.includes('/control')) {
          return { ok: true, status: 200, json: async () => ({ state: 'none' }) } as Response;
        }
        return { ok: true, status: 200, json: async () => DETAIL } as Response;
      }),
    );
    vi.useFakeTimers();
    try {
      const { store, client } = await openStore();
      fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
      store.draft = 'before-release';
      expect(store.sendDraft()).toBe(true);
      expect(client.sentInputFrames).toHaveLength(1);
      expect(store.outboxView.pendingCount).toBe(1);
      // 本地 release：显式放弃控制权（you→none）→ 冻结旧 pending。
      expect(await store.release()).toBe(true);
      expect(store.outboxView.haltedCount).toBe(1);
      expect(store.outboxView.pendingCount).toBe(0);
      // 随后 correlated control.state(none, released) 不产 E-06（release intent 抑制 notice），
      // 但安全冻结已由 release() 完成（幂等，prevControl 已非 you，事件不再触发）。
      client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'none', reason: 'released', occurredAt: '2026-08-03T01:00:00Z' });
      expect(store.controlNotice).toBeNull();
      expect(store.outboxView.haltedCount).toBe(1);
      // timer 推进：旧 entry 不再重发（仍 1 帧）。
      vi.advanceTimersByTime(60_000);
      expect(client.sentInputFrames).toHaveLength(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('R4-001：WS 事件先于 REST response 到达（真实产品链顺序）也不误显 E-06', async () => {
    // server 在 release 事务内同步把 control.state(reason=released) 入 subscriber FIFO，
    // WS 事件可先于 HTTP response 到达；intent 必须在发起 REST 前已 armed。
    let resolveRelease: ((v: { state: string }) => void) | null = null;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: unknown) => {
        const url = String(input);
        if (url.includes('/control')) {
          return new Promise<Response>((resolve) => {
            resolveRelease = (v) =>
              resolve({ ok: true, status: 200, json: async () => v } as Response);
          });
        }
        return { ok: true, status: 200, json: async () => DETAIL } as Response;
      }),
    );
    const { store, client } = await openStore();
    fireAttached(client);
    expect(store.control.state).toBe('you');
    // 发起 release（REST 挂起中）——此时 intent 应已 armed。
    const releasePromise = store.release();
    // WS 事件先到达：correlated control.state(none, released) → 抑制 E-06。
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'none', reason: 'released', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.control.state).toBe('none');
    expect(store.controlNotice).toBeNull();
    // REST response 随后到达：成功、冻结 pending。
    resolveRelease!({ state: 'none' });
    expect(await releasePromise).toBe(true);
    expect(store.controlNotice).toBeNull();
    // intent 已消费：之后的失权事件（如重新获权后再被 takeover）照常显示 E-06。
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'you', reason: 'acquired', occurredAt: '2026-08-03T01:01:00Z' });
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'other', deviceName: 'iPad', reason: 'takeover', occurredAt: '2026-08-03T01:02:00Z' });
    expect(store.controlNotice?.kind).toBe('lost');
    expect(store.controlNotice?.text).toBe('控制权已由 iPad 取得');
  });

  it('R4-001：release REST 失败撤销 intent——随后被动收回照常显示 E-06', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: unknown) => {
        const url = String(input);
        if (url.includes('/control')) {
          return {
            ok: false,
            status: 409,
            json: async () => ({ requestId: 'r', code: 'control.busy', layer: 'control', message: 'control busy', actionHint: 'retry' }),
          } as Response;
        }
        return { ok: true, status: 200, json: async () => DETAIL } as Response;
      }),
    );
    const { store, client } = await openStore();
    fireAttached(client);
    expect(await store.release()).toBe(false);
    // 未失权：control 仍为 you，不冻结、不显示 E-06。
    expect(store.control.state).toBe('you');
    expect(store.controlNotice).toBeNull();
    // intent 已撤销：随后真实 takeover（非本地 release）必须显示 E-06。
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'desktop', reason: 'takeover', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.controlNotice?.kind).toBe('lost');
    expect(store.controlNotice?.controlState).toBe('desktop');
  });

  it('R4-001：release 在途时非 correlated 事件（reason=takeover）不被 intent 吞掉', async () => {
    let resolveRelease: ((v: Response) => void) | null = null;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: unknown) => {
        const url = String(input);
        if (url.includes('/control')) {
          return new Promise<Response>((resolve) => {
            resolveRelease = (v) => resolve(v);
          });
        }
        return { ok: true, status: 200, json: async () => DETAIL } as Response;
      }),
    );
    const { store, client } = await openStore();
    fireAttached(client);
    const releasePromise = store.release();
    // 在途 release 未提交时，他设备先 takeover：reason 不匹配 released，intent 不消费，E-06 照常。
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'other', deviceName: 'iPad', reason: 'takeover', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.controlNotice?.kind).toBe('lost');
    expect(store.controlNotice?.text).toBe('控制权已由 iPad 取得');
    // 随后 REST 失败（已非 holder）→ intent 撤销，不残留陈旧标记。
    resolveRelease!({
      ok: false,
      status: 403,
      json: async () => ({ requestId: 'r', code: 'control.forbidden', layer: 'control', message: 'not controller', actionHint: 'acquire' }),
    } as Response);
    expect(await releasePromise).toBe(false);
    // 之后若重新获权再被 takeover，仍正常显示 E-06（intent 未残留）。
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'you', reason: 'acquired', occurredAt: '2026-08-03T01:01:00Z' });
    expect(store.controlNotice).toBeNull();
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'other', deviceName: 'iPad', reason: 'takeover', occurredAt: '2026-08-03T01:02:00Z' });
    expect(store.controlNotice?.kind).toBe('lost');
  });
});

describe('control/auth/unknown 事件', () => {
  it('control.state → other（takeover）：E-06 映射文案 + 投影更新；→ you：提示清除', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'other', deviceName: 'iPad', reason: 'takeover', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.control).toEqual({ state: 'other', deviceName: 'iPad' });
    expect(store.controlNotice?.kind).toBe('lost');
    expect(store.controlNotice?.controlState).toBe('other');
    expect(store.controlNotice?.text).toBe('控制权已由 iPad 取得');
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'you', reason: 'acquired', occurredAt: '2026-08-03T01:01:00Z' });
    expect(store.control).toEqual({ state: 'you' });
    expect(store.controlNotice).toBeNull();
  });

  it('E-06：desktop takeover 映射「桌面端已收回控制权」；unknown reason 不直出', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'desktop', reason: 'takeover', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.controlNotice?.text).toBe('桌面端已收回控制权');
    expect(store.controlNotice?.kind).toBe('lost');
    // 恢复后再次失去：unknown reason → 通用文案，不透传原始 reason 字符串。
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'you', reason: 'acquired', occurredAt: '2026-08-03T01:01:00Z' });
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'desktop', reason: 'some-opaque-internal-reason', occurredAt: '2026-08-03T01:02:00Z' });
    expect(store.controlNotice?.text).toBe('控制权已收回');
    expect(store.controlNotice?.text).not.toContain('opaque');
  });

  it('E-06：初始 observer（attach 即 desktop）不伪称「被收回」；旁观态变迁无 notice', async () => {
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
    expect(store.controlNotice).toBeNull();
    // desktop → other 旁观变迁：previous≠you，不产 E-06。
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'other', deviceName: 'iPad', reason: 'takeover', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.controlNotice).toBeNull();
  });

  it('E-06：本地 release 只显示完成回执（无收回 notice）；connection_expired 映射文案', async () => {
    // release/acquire REST 返回 ControlSnapshot（非 SessionDetail）：按 URL 区分。
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: unknown) => {
        const url = String(input);
        if (url.includes('/control')) {
          return { ok: true, status: 200, json: async () => ({ state: 'none' }) } as Response;
        }
        return { ok: true, status: 200, json: async () => DETAIL } as Response;
      }),
    );
    const { store, client } = await openStore();
    fireAttached(client);
    // 本地释放（REST 成功）→ correlated control.state(none) 不产 E-06。
    expect(await store.release()).toBe(true);
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'none', reason: 'released', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.controlNotice).toBeNull();
    // 重新获取后 connection_expired → E-06 映射文案（不直出 reason）。
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'you', reason: 'acquired', occurredAt: '2026-08-03T01:01:00Z' });
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'none', reason: 'connection_expired', occurredAt: '2026-08-03T01:02:00Z' });
    expect(store.controlNotice?.kind).toBe('lost');
    expect(store.controlNotice?.text).toBe('控制连接已过期，控制权已收回');
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

  it('跨 output frame 的 UTF-8 字符保持完整', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 1, chunk: '5L0=' });
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 2, chunk: 'oA==' });
    expect(store.getRawTranscript()).toBe('你');
    expect(store.timelineItems.some((item) => item.kind === 'mono' && (item as { text: string }).text === '你')).toBe(true);
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

// ---------------------------------------------------------------------------
// M3-C：E-07 恢复回合 / outbox 可视镜像 / NoticeStack 优先级 / continuitySnapshot
// ---------------------------------------------------------------------------

describe('E-07 恢复回合（ContinuityBanner 投影源）', () => {
  it('断线 → reconnecting episode（尝试序号+退避倒计时）；attached → restored（≥3s 自动消退）', async () => {
    vi.useFakeTimers();
    try {
      const { store, client } = await openStore();
      fireAttached(client);
      expect(store.recoveryEpisode).toBeNull();

      // 断线 → reconnecting episode 开启（generation=1）。
      client.opts.onStateChange({ state: 'reconnecting', attempt: 1, nextDelayMs: 750, terminalReason: null });
      expect(store.recoveryEpisode?.state).toBe('reconnecting');
      expect(store.recoveryEpisode?.generation).toBe(1);
      expect(store.recoveryEpisode?.attempt).toBe(1);
      expect(store.recoveryEpisode?.nextDelayMs).toBe(750);
      // 退避推进：attempt=2 / 1500ms。
      client.opts.onStateChange({ state: 'reconnecting', attempt: 2, nextDelayMs: 1500, terminalReason: null });
      expect(store.recoveryEpisode?.attempt).toBe(2);
      expect(store.recoveryEpisode?.nextDelayMs).toBe(1500);

      // 重连握手（awaiting-attach 不打断 episode）→ attached → restored。
      client.opts.onStateChange({ state: 'awaiting-attach', attempt: 2, nextDelayMs: null, terminalReason: null });
      expect(store.recoveryEpisode?.state).toBe('reconnecting');
      fireAttached(client);
      expect(store.recoveryEpisode?.state).toBe('restored');
      expect(store.recoveryEpisode?.withGap).toBe(false);
      // ≥3s 后自动消退。
      vi.advanceTimersByTime(3100);
      expect(store.recoveryEpisode).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('restored 同拍有缺口 → withGap（「已恢复，部分历史不可用」，E-07 引用 E-08 不重编号）', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    client.opts.onStateChange({ state: 'reconnecting', attempt: 1, nextDelayMs: 750, terminalReason: null });
    fireAttached(
      client,
      attachedEvent({
        earliestSeq: 2,
        latestSeq: 2,
        snapshot: {
          connection: { state: 'connected' },
          auth: { state: 'authorized' },
          session: { state: 'running' },
          control: { state: 'you' },
          history: { state: 'gap', gap: { code: 'history.gap', fromSeq: 1, toSeq: 1 } },
        },
        history: [{ type: 'output', sessionId: 'sess-1', seq: 2, chunk: b64('two\n') }],
      }),
    );
    expect(store.recoveryEpisode?.state).toBe('restored');
    expect(store.recoveryEpisode?.withGap).toBe(true);
    // 手动关闭路径。
    store.dismissRecovery();
    expect(store.recoveryEpisode).toBeNull();
  });

  it('terminal close → episode 清除（P0 覆盖，不显示「已恢复」）', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    client.opts.onStateChange({ state: 'reconnecting', attempt: 1, nextDelayMs: 750, terminalReason: null });
    expect(store.recoveryEpisode?.state).toBe('reconnecting');
    client.opts.onStateChange({ state: 'closed', attempt: 0, nextDelayMs: null, terminalReason: '协议错误，连接已被关闭' });
    expect(store.recoveryEpisode).toBeNull();
    expect(store.primaryNotice).toBe('fatal');
  });

  it('R1 仅在有合格 R0 的回合结算（relay 断线无 online → 该 episode 无 R 样本）', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    // 无 online 事件（R0 未打）→ 恢复 attach 后不补造 R 样本，lane 保持 not_occurred。
    client.opts.onStateChange({ state: 'reconnecting', attempt: 1, nextDelayMs: 750, terminalReason: null });
    fireAttached(client);
    const snap = store.timingSnapshot();
    expect(snap.measurements.R0_R1.status).toBe('not_occurred');
  });
});

describe('M3-C outbox 可视镜像（canonical capability）', () => {
  function attachedWithAck(client: FakeWsClient) {
    fireAttached(client, attachedEvent({ inputAckMode: 'session-window-v1' }));
  }

  it('发送 → 镜像 sending/attemptNo + 用户卡 delivery；ACK → settled + 卡片已确认', async () => {
    const { store, client } = await openStore();
    attachedWithAck(client);
    store.draft = 'npm test';
    expect(store.sendDraft()).toBe(true);
    // 用户卡：delivery=sending，attemptNo=1（accept 同步触发首次 attempt）。
    const user = store.timelineItems.find((i) => i.kind === 'user');
    if (!user || user.kind !== 'user') throw new Error('expected user item');
    expect(user.delivery).toBe('sending');
    expect(user.attemptNo).toBe(1);
    // outboxView 投影：1 条待确认。
    expect(store.outboxView.pendingCount).toBe(1);
    expect(store.outboxView.maxAttemptNo).toBe(1);
    expect(store.outboxView.resending).toBe(false);
    // canonical wire 帧：msg-v1- MessageID + req-v1- requestId。
    expect(client.sentInputFrames).toHaveLength(1);
    const frame = client.sentInputFrames[0];
    expect(frame.id).toMatch(/^msg-v1-[0-9a-f]{32}$/);
    expect(frame.requestId).toMatch(/^req-v1-[0-9a-f]{32}$/);
    // 投影/快照不含 ID（隐私 fail-closed）。
    expect(JSON.stringify(store.continuitySnapshot())).not.toContain('msg-v1-');
    expect(JSON.stringify(store.continuitySnapshot())).not.toContain('req-v1-');

    // ACK 结算（按 MessageID）：用户卡已确认 + pending 清零 + settled 计数。
    client.opts.onEvent({ type: 'input.ack', requestId: frame.requestId, sessionId: 'sess-1', id: frame.id });
    const after = store.timelineItems.find((i) => i.kind === 'user');
    if (!after || after.kind !== 'user') throw new Error('expected user item');
    expect(after.delivery).toBe('settled');
    expect(store.outboxView.pendingCount).toBe(0);
    expect(store.continuitySnapshot().outbox).toEqual({ pending: 0, halted: 0, settled: 1 });
  });

  it('恢复后自动重发反馈：reattach 立即重试（同 MessageID、新 requestId），restored + pending → resending', async () => {
    vi.useFakeTimers();
    try {
      const { store, client } = await openStore();
      attachedWithAck(client);
      store.draft = 'cmd-resend';
      expect(store.sendDraft()).toBe(true);
      expect(client.sentInputFrames).toHaveLength(1);
      // 断线 → reconnecting；重连 attached → outbox.onReattach 立即重试队首。
      client.opts.onStateChange({ state: 'reconnecting', attempt: 1, nextDelayMs: 750, terminalReason: null });
      client.opts.onStateChange({ state: 'awaiting-attach', attempt: 1, nextDelayMs: null, terminalReason: null });
      attachedWithAck(client);
      // onStateChange(attached) 在 onEvent 前触发 onReattach：同一 MessageID 第二次 attempt。
      expect(client.sentInputFrames).toHaveLength(2);
      expect(client.sentInputFrames[1].id).toBe(client.sentInputFrames[0].id);
      expect(client.sentInputFrames[1].requestId).not.toBe(client.sentInputFrames[0].requestId);
      // 恢复条 restored 且仍有未确认项 → 自动重发反馈。
      expect(store.outboxView.resending).toBe(true);
      expect(store.outboxView.pendingCount).toBe(1);
      const user = store.timelineItems.find((i) => i.kind === 'user');
      if (!user || user.kind !== 'user') throw new Error('expected user item');
      expect(user.attemptNo).toBe(2);
      // 迟到 ACK 按任一 all-attempt requestId 结算。
      client.opts.onEvent({ type: 'input.ack', requestId: client.sentInputFrames[0].requestId, sessionId: 'sess-1', id: client.sentInputFrames[0].id });
      expect(store.continuitySnapshot().outbox.settled).toBe(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('控制权被接管 → outbox halt：镜像/用户卡 halted + outboxView.haltedCount', async () => {
    const { store, client } = await openStore();
    attachedWithAck(client);
    store.draft = 'cmd-one';
    expect(store.sendDraft()).toBe(true);
    expect(store.outboxView.pendingCount).toBe(1);
    // takeover → halt：卡片显示已停发，不再自动重试。
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'desktop', reason: 'takeover', occurredAt: '2026-08-03T01:00:00Z' });
    const user = store.timelineItems.find((i) => i.kind === 'user');
    if (!user || user.kind !== 'user') throw new Error('expected user item');
    expect(user.delivery).toBe('halted');
    expect(store.outboxView.haltedCount).toBe(1);
    expect(store.outboxView.pendingCount).toBe(0);
  });
});

describe('NoticeStack 优先级（design §7）', () => {
  it('P0 fatal 覆盖一切：terminal close 时 lastError/episode/degraded 均被压', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    client.opts.onEvent({ type: 'error', requestId: 'r', sessionId: 'sess-1', code: 'session.unavailable', layer: 'session', message: 'm', actionHint: 'retry', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.primaryNotice).toBe('error');
    client.opts.onStateChange({ state: 'closed', attempt: 0, nextDelayMs: null, terminalReason: '连接已被服务端正常关闭' });
    expect(store.primaryNotice).toBe('fatal');
  });

  it('P1 lastError 暂压 E-07；dismiss 后 episode 仍有效则恢复 recovery 级', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    client.opts.onStateChange({ state: 'reconnecting', attempt: 1, nextDelayMs: 750, terminalReason: null });
    expect(store.primaryNotice).toBe('recovery');
    client.opts.onEvent({ type: 'error', requestId: 'r', sessionId: 'sess-1', code: 'session.unavailable', layer: 'session', message: 'm', actionHint: 'retry', occurredAt: '2026-08-03T01:00:00Z' });
    expect(store.primaryNotice).toBe('error'); // E-07 被暂压
    store.dismissError();
    expect(store.primaryNotice).toBe('recovery'); // 状态仍有效 → 恢复
  });

  it('P3 degraded 最低：无更高优先级时才浮现', async () => {
    const { store, client } = await openStore();
    fireAttached(client);
    expect(store.primaryNotice).toBeNull();
    client.opts.onEvent({
      type: 'unknown',
      wireType: 'history',
      reason: 'unknown-shape',
      fallback: 'mark-history-gap',
      metadata: { inputKind: 'object', hasRequestId: false, hasSessionId: true, hasSeq: false },
    });
    expect(store.primaryNotice).toBe('degraded');
    store.dismissDegraded();
    expect(store.primaryNotice).toBeNull();
  });
});

describe('continuitySnapshot（design §8 机器 oracle 内存 seam）', () => {
  it('frontier/attachedCount/appliedSeq 多重集/gapRanges/outbox 计数；无 ID/payload', async () => {
    const { store, client } = await openStore();
    fireAttached(
      client,
      attachedEvent({
        earliestSeq: 1,
        latestSeq: 2,
        history: [
          { type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64('a\n') },
          { type: 'output', sessionId: 'sess-1', seq: 2, chunk: b64('b\n') },
        ],
      }),
    );
    // live 越洞：seq 4 先到（缓冲）→ seq 3 补齐后吸收。
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 4, chunk: b64('d\n') });
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 3, chunk: b64('c\n') });
    const snap = store.continuitySnapshot();
    expect(snap.frontier).toBe(4);
    expect(snap.attachedCount).toBe(1);
    expect(snap.appliedSeqCounts).toEqual({ '1': 1, '2': 1, '3': 1, '4': 1 });
    expect(snap.appliedSeqTruncated).toBe(false);
    expect(snap.gapRanges).toEqual([]);
    expect(snap.outbox).toEqual({ pending: 0, halted: 0, settled: 0 });
    expect(snap.recoveryGeneration).toBe(0);
    // 隐私：快照无 chunk 内容/ID。
    const raw = JSON.stringify(snap);
    expect(raw).not.toContain('a\\n');
    expect(raw).not.toContain('msg-');
  });
});
