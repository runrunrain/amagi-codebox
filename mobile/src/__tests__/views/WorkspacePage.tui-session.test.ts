/**
 * __tests__/views/WorkspacePage.tui-session.test.ts — P1-C TUI 会话页面级集成
 * ---------------------------------------------------------------------------
 * 页面级（WorkspacePage → store → RawTerminalView/ComposerBar/KeyTray 全链）
 * 验证 P1-A 契约的集成行为：
 *   · omp 会话默认渲染终端仿真面（cliType 分支补齐：pi 之外的第二 TUI CLI）；
 *   · pi 显式 ?view=terminal 同样进入终端面（R3 语义升级）；
 *   · KeyTray 按键点击 → store.sendRaw → canonical input 帧（base64 载荷精确）；
 *   · 观察态（control=other）降级：只读横幅出现、按键禁用、无 input 帧；
 *     control.state 恢复 you → 横幅消失、按键恢复、可发（横幅出现/消失全链）；
 *   · 合成全屏 TUI 帧 + restart boundary：store 原样透传帧字节到 xterm 写入，
 *     重启提示行按序注入，重绘语义不破坏（oracle 验证）。
 * WS/xterm 均为结构替身，无真实网络。
 * ---------------------------------------------------------------------------
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia, type Pinia } from 'pinia';
import { createRouter, createMemoryHistory, type Router } from 'vue-router';
import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent } from 'vue';

// --- FakeWsClient（记录 input 帧，供 sendRaw 载荷断言） ---

interface FakeClientOptions {
  sessionId: string;
  getLastSeq: () => number | undefined;
  onEvent: (event: unknown) => void;
  onStateChange: (change: { state: string; attempt: number; nextDelayMs: number | null; terminalReason: string | null }) => void;
}

const { FakeWsClient } = vi.hoisted(() => {
  class FakeWsClient {
    static instances: FakeWsClient[] = [];
    opts: FakeClientOptions;
    sentInputFrames: { id: string; requestId: string; data: string }[] = [];
    disposed = false;
    constructor(opts: FakeClientOptions) {
      this.opts = opts;
      FakeWsClient.instances.push(this);
    }
    connect(): void {}
    dispose(): void {
      this.disposed = true;
    }
    forceReconnect(): void {}
    sendInput(): boolean {
      return true;
    }
    sendInputFrame(frame: { id: string; requestId: string; data: string }): boolean {
      this.sentInputFrames.push({ id: frame.id, requestId: frame.requestId, data: frame.data });
      return true;
    }
    sendResize(): boolean {
      return true;
    }
    requestBackfill(): string {
      return 'req-bf';
    }
  }
  return { FakeWsClient };
});

type FakeWsClient = InstanceType<typeof FakeWsClient>;

vi.mock('../../../src/lib/ws', () => {
  // 真实 UTF-8 → base64（载荷断言依赖正确编码，不用恒等替身）。
  const encode = (s: string): string => {
    const bytes = new TextEncoder().encode(s);
    let bin = '';
    for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin);
  };
  return {
    SessionWsClient: FakeWsClient,
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
        reset: () => {
          decoder = new TextDecoder('utf-8');
        },
      };
    },
    encodeUtf8ToBase64: encode,
  };
});

// Mock xterm（instances 静态成员供测试体取最新终端替身）
const { FakeTerminal, FakeFitAddon } = vi.hoisted(() => {
  class FakeTerminalT {
    static instances: FakeTerminalT[] = [];
    written: string[] = [];
    cols = 80;
    rows = 24;
    dataListeners: ((d: string) => void)[] = [];
    options: Record<string, unknown>;
    constructor(options: Record<string, unknown>) {
      this.options = options;
      FakeTerminalT.instances.push(this);
    }
    open(): void {}
    loadAddon(): void {}
    write(d: string): void {
      this.written.push(d);
    }
    onData(cb: (d: string) => void) {
      this.dataListeners.push(cb);
      return { dispose: () => {} };
    }
    emitData(d: string): void {
      for (const cb of this.dataListeners) cb(d);
    }
    scrollLines(): void {}
    dispose(): void {}
  }
  class FakeFitAddon {
    fit(): void {}
  }
  return { FakeTerminal: FakeTerminalT, FakeFitAddon };
});

type FakeTerminalT = InstanceType<(typeof FakeTerminal)>;

vi.mock('@xterm/xterm', () => ({ Terminal: FakeTerminal }));
vi.mock('@xterm/addon-fit', () => ({ FitAddon: FakeFitAddon }));
vi.mock('@xterm/xterm/css/xterm.css', () => ({}));

import { useWorkspaceStore } from '../../../src/stores/workspace';
import WorkspacePage from '../../../src/views/WorkspacePage.vue';
import { PI_FULLSCREEN_REDRAW, allLines, countOccurrences, renderTuiStream, screenText } from '../fixtures/tui-frames';

const Stub = defineComponent({ template: '<div />' });
const AppShell = defineComponent({ template: '<router-view />' });

/** 与 store 内部常量一致的契约值（P1-A §4.2：重启边界提示行）。 */
const RESTART_NOTICE_TEXT = '[远程终端] 会话已进入新的运行';

function b64(text: string): string {
  const bytes = new TextEncoder().encode(text);
  let bin = '';
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin);
}

function mockFetch(detail: Record<string, unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ ok: true, status: 200, json: async () => detail }) as Response),
  );
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
    inputAckMode: 'session-window-v1',
    ...over,
  };
}

async function mountWithRoute(pinia: Pinia, detail: Record<string, unknown>, initialQuery: Record<string, string> = {}) {
  const router: Router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', redirect: '/lobby' },
      { path: '/lobby', name: 'lobby', component: Stub },
      { path: '/connect', name: 'connect', component: Stub },
      { path: '/workspace/:sessionId', name: 'workspace', component: WorkspacePage },
    ],
  });
  mockFetch(detail);
  const wrapper = mount(AppShell, { global: { plugins: [pinia, router] } });
  await router.push({ name: 'workspace', params: { sessionId: 'sess-1' }, query: initialQuery });
  await router.isReady();
  await flushPromises();
  const store = useWorkspaceStore();
  const client = FakeWsClient.instances[FakeWsClient.instances.length - 1];
  if (client) {
    client.opts.onEvent(attachedEvent());
    client.opts.onStateChange({ state: 'attached', attempt: 0, nextDelayMs: null, terminalReason: null });
    await flushPromises();
  }
  return { wrapper, store, router, client };
}

function piDetail(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'sess-1',
    title: 'Pi TUI Session',
    cliType: 'pi',
    state: 'running',
    control: { state: 'you' },
    earliestSeq: 0,
    latestSeq: 0,
    ...over,
  };
}

function lastTerminal(): FakeTerminalT {
  const cls = FakeTerminal as unknown as { instances: FakeTerminalT[] };
  return cls.instances[cls.instances.length - 1];
}

/** canonical outbox 序列化在途帧：ACK 已上线帧后下一帧才上线（session-window-v1）。 */
function ackWireFrames(client: FakeWsClient, acked: Set<string>): void {
  for (const f of client.sentInputFrames) {
    if (acked.has(f.id)) continue;
    acked.add(f.id);
    client.opts.onEvent({ type: 'input.ack', sessionId: 'sess-1', id: f.id, requestId: f.requestId });
  }
}

/** 等待分批器把期望文本全部写入。 */
async function waitWritten(term: FakeTerminalT, expected: string): Promise<void> {
  await vi.waitFor(
    () => {
      expect(term.written.join('')).toBe(expected);
    },
    { timeout: 3000, interval: 10 },
  );
}

describe('WorkspacePage TUI 会话页面级集成（P1-C）', () => {
  let pinia: Pinia;

  beforeEach(() => {
    pinia = createPinia();
    setActivePinia(pinia);
    FakeWsClient.instances = [];
    FakeTerminal.instances.length = 0;
    localStorage.setItem('amagi.pg03.guide.dismissed', '1');
  });

  it('omp 会话缺省 query 时默认渲染终端仿真面（cliType 分支第二 TUI CLI）', async () => {
    const { wrapper } = await mountWithRoute(pinia, piDetail({ cliType: 'omp', title: 'Omp Session' }));

    expect(wrapper.find('[data-testid="terminal-host"]').exists()).toBe(true);
    expect(wrapper.find('.tui-cli-badge').text()).toBe('终端仿真');
    expect(wrapper.find('.diagnostic-badge').exists()).toBe(false);
    expect(wrapper.find('.back-btn .btn-label').text()).toBe('大厅');
    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(true);
  });

  it('pi 会话显式 ?view=terminal 进入终端面（R3 语义升级）', async () => {
    const { wrapper } = await mountWithRoute(pinia, piDetail(), { view: 'terminal' });

    expect(wrapper.find('[data-testid="terminal-host"]').exists()).toBe(true);
    expect(wrapper.find('.tui-cli-badge').exists()).toBe(true);
    expect(wrapper.find('.diagnostic-badge').exists()).toBe(false);
  });

  it('KeyTray 按键点击 → store.sendRaw → canonical input 帧载荷精确', async () => {
    const { wrapper, client } = await mountWithRoute(pinia, piDetail());
    expect(client.sentInputFrames).toHaveLength(0);
    const acked = new Set<string>();

    // ^C 中断生成
    await wrapper.find('[data-testid="key-ctrl-c"]').trigger('click');
    expect(atob(client.sentInputFrames[0].data)).toBe('\x03');
    ackWireFrames(client, acked);

    // 方向键上
    await wrapper.find('[data-testid="key-up"]').trigger('click');
    expect(atob(client.sentInputFrames[1].data)).toBe('\x1b[A');
    ackWireFrames(client, acked);

    // ⌥W 看板（pi 专属 Alt 组合）
    await wrapper.find('[data-testid="key-alt-w"]').trigger('click');
    expect(atob(client.sentInputFrames[2].data)).toBe('\x1bw');
    ackWireFrames(client, acked);

    // Alt 修饰键 + Enter 组合：\x1b\r
    await wrapper.find('[data-testid="key-alt-modifier"]').trigger('click');
    await wrapper.find('[data-testid="key-enter"]').trigger('click');
    expect(atob(client.sentInputFrames[3].data)).toBe('\x1b\r');
  });

  it('终端网格键入（onData）→ store.sendRaw → input 帧（网格输入通路）', async () => {
    const { client } = await mountWithRoute(pinia, piDetail());
    const term = lastTerminal();
    const acked = new Set<string>();

    term.emitData('hello');
    expect(client.sentInputFrames.map((f) => atob(f.data))).toEqual(['hello']);
    ackWireFrames(client, acked);

    term.emitData('\x1b[B');
    expect(client.sentInputFrames.map((f) => atob(f.data))).toEqual(['hello', '\x1b[B']);
  });

  it('观察态降级：横幅出现、按键禁用、网格输入被拦截；恢复控制后横幅消失可发', async () => {
    const { wrapper, client, store } = await mountWithRoute(
      pinia,
      piDetail({ control: { state: 'other', deviceName: 'iPad Pro' } }),
      {},
    );
    // attach 快照已是观察态
    client.opts.onEvent(
      attachedEvent({
        snapshot: {
          connection: { state: 'connected' },
          auth: { state: 'authorized' },
          session: { state: 'running' },
          control: { state: 'other', deviceName: 'iPad Pro' },
          history: { state: 'continuous' },
        },
      }),
    );
    await flushPromises();
    expect(store.canWrite).toBe(false);

    // RawTerminalView/KeyTray 挂载链含 xterm 动态 import：全量并行负载下渲染
    // 可晚于首个 flushPromises（本用例曾在 full-suite 间歇性抖动）——改 waitFor
    // 轮询断言，消除时序敏感。
    await vi.waitFor(() => {
      const banner = wrapper.find('[data-testid="terminal-readonly-indicator"]');
      expect(banner.exists()).toBe(true);
      expect(banner.text()).toContain('iPad Pro');
      expect(wrapper.find('[data-testid="key-esc"]').attributes('disabled')).toBeDefined();
    });

    // 网格输入被只读门拦截 → 无 input 帧
    lastTerminal().emitData('\x03');
    expect(client.sentInputFrames).toHaveLength(0);

    // 控制权恢复 → 横幅消失、按键恢复、输入放行
    client.opts.onEvent({ type: 'control.state', sessionId: 'sess-1', state: 'you', reason: 'acquired', occurredAt: '2026-09-10T00:00:00Z' });
    await flushPromises();
    expect(store.canWrite).toBe(true);
    await vi.waitFor(() => {
      expect(wrapper.find('[data-testid="terminal-readonly-indicator"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="key-esc"]').attributes('disabled')).toBeUndefined();
    });

    await wrapper.find('[data-testid="key-esc"]').trigger('click');
    expect(client.sentInputFrames).toHaveLength(1);
    expect(atob(client.sentInputFrames[0].data)).toBe('\x1b');
  });

  it('合成全屏帧 + restart boundary：帧字节原样透传、提示行按序注入、重绘语义不破坏', async () => {
    const { client } = await mountWithRoute(pinia, piDetail());
    const term = lastTerminal();

    // run-1：前两帧（seq 1-2）
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 1, chunk: b64(PI_FULLSCREEN_REDRAW.frames[0]) });
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 2, chunk: b64(PI_FULLSCREEN_REDRAW.frames[1]) });
    // 重启边界（seq 3）→ 本地提示行注入原始流
    client.opts.onEvent({
      type: 'session.state',
      sessionId: 'sess-1',
      state: 'running',
      restartBoundary: true,
      seq: 3,
      occurredAt: '2026-09-10T00:00:00Z',
    });
    // run-2：第 3 帧（seq 4）
    client.opts.onEvent({ type: 'output', sessionId: 'sess-1', seq: 4, chunk: b64(PI_FULLSCREEN_REDRAW.frames[2]) });

    const expected =
      PI_FULLSCREEN_REDRAW.frames[0] +
      PI_FULLSCREEN_REDRAW.frames[1] +
      '\r\n\x1b[90m' + RESTART_NOTICE_TEXT + '\x1b[0m\r\n' +
      PI_FULLSCREEN_REDRAW.frames[2];
    await waitWritten(term, expected);

    // oracle：终屏只有 run-2 的 frame 3；旧帧零残留。
    const oracle = renderTuiStream(term.written);
    const screen = screenText(oracle);
    expect(countOccurrences(screen, '[pi-tui] frame 3 ready')).toBe(1);
    expect(countOccurrences(allLines(oracle).join('\n'), 'frame 1 ready')).toBe(0);
    expect(countOccurrences(allLines(oracle).join('\n'), 'frame 2 ready')).toBe(0);

    // 提示行在 run-2 重绘前可见（帧序内插）；下一次整屏重绘（ED2）将其擦除是
    // 正确终端语义 —— 提示行是瞬态的，不伪造内容。
    const joined = term.written.join('');
    const frame3Start = joined.indexOf(PI_FULLSCREEN_REDRAW.frames[2]);
    const midScreen = screenText(renderTuiStream([joined.slice(0, frame3Start)]));
    expect(midScreen).toContain(RESTART_NOTICE_TEXT);
  });
});
