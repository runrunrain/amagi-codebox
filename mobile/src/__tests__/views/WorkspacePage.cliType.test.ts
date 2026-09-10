/**
 * __tests__/views/WorkspacePage.cliType.test.ts — P1-A cliType 默认视图分支
 * ---------------------------------------------------------------------------
 * 验证：
 *   · pi / omp 会话默认渲染终端仿真面（RawTerminalView），无诊断视图降级标记，
 *     返回按钮为「大厅」；
 *   · claudecode / opencode / codex 会话维持时间线主面（TimelineView），
 *     ?view=terminal 维持诊断视图语义（带「诊断视图」徽标、返回主阅读面）；
 *   · 视图切换菜单与快捷返回文案精准适配。
 * ---------------------------------------------------------------------------
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia, type Pinia } from 'pinia';
import { createRouter, createMemoryHistory, type Router } from 'vue-router';
import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent } from 'vue';

// --- FakeWsClient ---

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
    sendInputFrame(): boolean {
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
    encodeUtf8ToBase64: (s: string) => s,
  };
});

// Mock xterm
const { FakeTerminal, FakeFitAddon } = vi.hoisted(() => {
  class FakeTerminal {
    written: string[] = [];
    cols = 80;
    rows = 24;
    dataListeners: ((d: string) => void)[] = [];
    options: Record<string, unknown>;
    constructor(options: Record<string, unknown>) {
      this.options = options;
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
    scrollLines(): void {}
    dispose(): void {}
  }
  class FakeFitAddon {
    fit(): void {}
  }
  return { FakeTerminal, FakeFitAddon };
});

vi.mock('@xterm/xterm', () => ({ Terminal: FakeTerminal }));
vi.mock('@xterm/addon-fit', () => ({ FitAddon: FakeFitAddon }));
vi.mock('@xterm/xterm/css/xterm.css', () => ({}));

import { useWorkspaceStore } from '../../../src/stores/workspace';
import WorkspacePage from '../../../src/views/WorkspacePage.vue';

const Stub = defineComponent({ template: '<div />' });
const AppShell = defineComponent({ template: '<router-view />' });

function mockFetch(detail: Record<string, unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ ok: true, status: 200, json: async () => detail }) as Response),
  );
}

function attachedEvent() {
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
  };
}

async function mountWithRoute(pinia: Pinia, initialQuery: Record<string, string> = {}) {
  const router: Router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', redirect: '/lobby' },
      { path: '/lobby', name: 'lobby', component: Stub },
      { path: '/connect', name: 'connect', component: Stub },
      { path: '/workspace/:sessionId', name: 'workspace', component: WorkspacePage },
    ],
  });
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
  return { wrapper, store, router };
}

describe('WorkspacePage cliType 默认视图分支', () => {
  let pinia: Pinia;

  beforeEach(() => {
    pinia = createPinia();
    setActivePinia(pinia);
    FakeWsClient.instances = [];
    localStorage.setItem('amagi.pg03.guide.dismissed', '1');
  });

  it('pi 会话在缺省 query 时默认渲染终端仿真面，显示终端仿真徽标与大厅返回键', async () => {
    mockFetch({
      id: 'sess-1',
      title: 'Pi TUI Session',
      cliType: 'pi',
      state: 'running',
      control: { state: 'you' },
      earliestSeq: 0,
      latestSeq: 0,
    });

    const { wrapper } = await mountWithRoute(pinia);

    // 默认展示终端仿真面
    expect(wrapper.find('[data-testid="terminal-host"]').exists()).toBe(true);
    // 展示终端仿真徽标，非诊断视图
    expect(wrapper.find('.tui-cli-badge').exists()).toBe(true);
    expect(wrapper.find('.diagnostic-badge').exists()).toBe(false);

    // 返回按钮文案为「大厅」
    expect(wrapper.find('.back-btn .btn-label').text()).toBe('大厅');

    // Composer 处于终端模式，按键托盘可用
    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(true);
  });

  it('pi 会话切换至 ?view=timeline 时显示时间线，返回按钮文案为「返回终端面」', async () => {
    mockFetch({
      id: 'sess-1',
      title: 'Pi TUI Session',
      cliType: 'pi',
      state: 'running',
      control: { state: 'you' },
      earliestSeq: 0,
      latestSeq: 0,
    });

    const { wrapper } = await mountWithRoute(pinia, { view: 'timeline' });

    // 时间线显示，终端面不显示
    expect(wrapper.find('[data-testid="terminal-host"]').exists()).toBe(false);
    expect(wrapper.find('.timeline').exists()).toBe(true);

    // 返回按钮为返回终端面
    expect(wrapper.find('.back-btn .btn-label').text()).toBe('返回终端面');
  });

  it('claudecode 会话在缺省 query 时默认渲染时间线主面', async () => {
    mockFetch({
      id: 'sess-1',
      title: 'Claude Code Session',
      cliType: 'claudecode',
      state: 'running',
      control: { state: 'you' },
      earliestSeq: 0,
      latestSeq: 0,
    });

    const { wrapper } = await mountWithRoute(pinia);

    expect(wrapper.find('.timeline').exists()).toBe(true);
    expect(wrapper.find('[data-testid="terminal-host"]').exists()).toBe(false);
    expect(wrapper.find('.back-btn .btn-label').text()).toBe('大厅');
    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(false);
  });

  it('claudecode 会话在 ?view=terminal 时维持诊断视图语义', async () => {
    mockFetch({
      id: 'sess-1',
      title: 'Claude Code Session',
      cliType: 'claudecode',
      state: 'running',
      control: { state: 'you' },
      earliestSeq: 0,
      latestSeq: 0,
    });

    const { wrapper } = await mountWithRoute(pinia, { view: 'terminal' });

    expect(wrapper.find('[data-testid="terminal-host"]').exists()).toBe(true);
    expect(wrapper.find('.diagnostic-badge').exists()).toBe(true);
    expect(wrapper.find('.back-btn .btn-label').text()).toBe('返回主阅读面');
  });
});
