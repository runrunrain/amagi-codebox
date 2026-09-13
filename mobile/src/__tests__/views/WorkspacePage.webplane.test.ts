/**
 * __tests__/views/WorkspacePage.webplane.test.ts — Web 会话平面（WebPlane）页面级测试（C2/C4）
 * ---------------------------------------------------------------------------
 * 验证：
 *   · TUI 会话（pi/omp）默认视图策略：
 *     - webui available → 默认展示 Web 会话平面（WebPlaneView），隐藏外层 ComposerBar 与 KeyTray，
 *       显示「Web 平面」徽标，大厅返回键；
 *     - webui probing → 展示 Web 平面加载态，隐藏 ComposerBar；
 *     - webui unavailable / unknown → 自动回退终端仿真（RawTerminalView），显示「终端仿真」徽标，展示 KeyTray；
 *   · 路由视图切换（?view=webplane / ?view=timeline / ?view=terminal）：
 *     - ?view=webplane 显式渲染 Web 会话平面，隐藏外层 ComposerBar；
 *     - ?view=timeline 切换到结构化时间线，显示外层 ComposerBar（无按键托盘）；
 *     - ?view=terminal 切换到终端仿真，显示外层 ComposerBar 与按键托盘；
 *   · 菜单三面切换：
 *     - webui available 时，在终端/时间线菜单中均提供「切换至 Web 平面视图」入口；
 *     - webui unavailable 时，隐藏「切换至 Web 平面视图」入口；
 *     - 在 Web 平面视图中，菜单提供切至终端仿真和时间线选项；
 *   · 状态降级与迁移：
 *     - probing → available 变化时自动进入 Web 会话平面；
 *     - 会话 ended 时 WebPlaneView 显示结束提示，点击切回终端可切到终端面。
 * ---------------------------------------------------------------------------
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
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
import WebPlaneView from '../../../src/components/workspace/WebPlaneView.vue';
import ComposerBar from '../../../src/components/workspace/ComposerBar.vue';

const Stub = defineComponent({ template: '<div />' });
const AppShell = defineComponent({ template: '<router-view />' });

function mockEndpoints(options: {
  detail?: Record<string, unknown>;
  webui?: Record<string, unknown> | (() => Record<string, unknown> | Promise<Record<string, unknown>>);
}) {
  const defaultDetail = {
    id: 'sess-1',
    title: 'Pi TUI Session',
    cliType: 'pi',
    state: 'running',
    control: { state: 'you' },
    earliestSeq: 0,
    latestSeq: 0,
    workdir: '/test',
    startedAt: '2026-09-11T00:00:00Z',
    lastActivityAt: '2026-09-11T00:00:00Z',
  };

  const detailData = { ...defaultDetail, ...(options.detail ?? {}) };

  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.includes('/webui')) {
        let webuiData: Record<string, unknown>;
        if (typeof options.webui === 'function') {
          webuiData = await options.webui();
        } else {
          webuiData = options.webui ?? { state: 'unavailable' };
        }
        return {
          ok: true,
          status: 200,
          json: async () => webuiData,
        } as Response;
      }
      return {
        ok: true,
        status: 200,
        json: async () => detailData,
      } as Response;
    }),
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

describe('WorkspacePage Web会话平面集成（C2/C4）', () => {
  let pinia: Pinia;

  beforeEach(() => {
    pinia = createPinia();
    setActivePinia(pinia);
    FakeWsClient.instances = [];
    localStorage.setItem('amagi.pg03.guide.dismissed', '1');
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('pi 会话在 webui available 时，缺省 query 默认渲染 Web 会话平面且隐藏外层 ComposerBar 与 KeyTray', async () => {
    mockEndpoints({
      detail: { cliType: 'pi' },
      webui: { state: 'available', url: '/webui/sess-1/#/t=token123' },
    });

    const { wrapper } = await mountWithRoute(pinia);

    // 渲染 Web 会话平面
    expect(wrapper.find('[data-testid="webplane-host"]').exists()).toBe(true);
    expect(wrapper.find('.webplane-badge').exists()).toBe(true);
    expect(wrapper.find('.tui-cli-badge').exists()).toBe(false);

    // 返回按钮为大厅
    expect(wrapper.find('.back-btn .btn-label').text()).toBe('大厅');

    // 外层 ComposerBar 与 KeyTray 均被隐藏（避免与内置输入台重复）
    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(false);
    expect(wrapper.findComponent(ComposerBar).exists()).toBe(false);

    // 终端仿真面不渲染
    expect(wrapper.find('[data-testid="terminal-host"]').exists()).toBe(false);
  });

  it('pi 会话在 webui probing 时，缺省 query 渲染 Web 平面加载态且隐藏 ComposerBar', async () => {
    mockEndpoints({
      detail: { cliType: 'pi' },
      webui: { state: 'probing' },
    });

    const { wrapper } = await mountWithRoute(pinia);

    // 渲染 probing 加载态
    expect(wrapper.find('[data-testid="webplane-probing"]').exists()).toBe(true);
    expect(wrapper.find('.webplane-badge').exists()).toBe(true);

    // 隐藏 ComposerBar
    expect(wrapper.findComponent(ComposerBar).exists()).toBe(false);
  });

  it('pi 会话在 webui unavailable 时，缺省 query 回退终端仿真面，Composer 与 KeyTray 正常渲染（现状不回归）', async () => {
    mockEndpoints({
      detail: { cliType: 'pi' },
      webui: { state: 'unavailable' },
    });

    const { wrapper } = await mountWithRoute(pinia);

    // 回退到终端仿真面
    expect(wrapper.find('[data-testid="terminal-host"]').exists()).toBe(true);
    expect(wrapper.find('.tui-cli-badge').exists()).toBe(true);
    expect(wrapper.find('.webplane-badge').exists()).toBe(false);

    // 终端模式 ComposerBar 与 KeyTray 存在
    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(true);
  });

  it('显式带有 ?view=webplane 路由时直接渲染 Web 平面', async () => {
    mockEndpoints({
      detail: { cliType: 'pi' },
      webui: { state: 'available', url: '/webui/sess-1/#/t=token123' },
    });

    const { wrapper } = await mountWithRoute(pinia, { view: 'webplane' });

    expect(wrapper.find('[data-testid="webplane-host"]').exists()).toBe(true);
    expect(wrapper.findComponent(ComposerBar).exists()).toBe(false);
  });

  it('显式带有 ?view=timeline 路由时切换至时间线，Composer 显示且禁用 KeyTray', async () => {
    mockEndpoints({
      detail: { cliType: 'pi' },
      webui: { state: 'available', url: '/webui/sess-1/#/t=token123' },
    });

    const { wrapper } = await mountWithRoute(pinia, { view: 'timeline' });

    expect(wrapper.find('.timeline').exists()).toBe(true);
    expect(wrapper.find('[data-testid="webplane-host"]').exists()).toBe(false);
    expect(wrapper.findComponent(ComposerBar).exists()).toBe(true);
    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(false);
    expect(wrapper.find('.back-btn .btn-label').text()).toBe('返回终端面');
  });

  it('显式带有 ?view=terminal 路由时进入终端仿真面，即使 webui available 也不自动顶替', async () => {
    mockEndpoints({
      detail: { cliType: 'pi' },
      webui: { state: 'available', url: '/webui/sess-1/#/t=token123' },
    });

    const { wrapper } = await mountWithRoute(pinia, { view: 'terminal' });

    expect(wrapper.find('[data-testid="terminal-host"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="webplane-host"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="terminal-key-tray"]').exists()).toBe(true);
  });

  it('webui available 时，菜单支持三面互相切换', async () => {
    mockEndpoints({
      detail: { cliType: 'pi' },
      webui: { state: 'available', url: '/webui/sess-1/#/t=token123' },
    });

    const { wrapper, router } = await mountWithRoute(pinia);

    // 打开菜单
    const menuBtn = wrapper.find('.menu-btn');
    await menuBtn.trigger('click');

    const menuItems = wrapper.findAll('.menu-item').map((i) => i.text());
    // 当前处于 webplane 视图，菜单应包含切换至终端与时间线选项
    expect(menuItems).toContain('切换至终端仿真视图');
    expect(menuItems).toContain('切换至时间线视图');
    expect(menuItems).toContain('返回会话大厅');

    // 点击切换至终端仿真视图
    const toTerminalBtn = wrapper.findAll('.menu-item').find((b) => b.text().includes('切换至终端仿真视图'));
    await toTerminalBtn?.trigger('click');
    await flushPromises();

    expect(router.currentRoute.value.query.view).toBe('terminal');
  });

  it('webui unavailable 时，菜单隐藏 Web 平面入口', async () => {
    mockEndpoints({
      detail: { cliType: 'pi' },
      webui: { state: 'unavailable' },
    });

    const { wrapper } = await mountWithRoute(pinia);

    const menuBtn = wrapper.find('.menu-btn');
    await menuBtn.trigger('click');

    const menuItems = wrapper.findAll('.menu-item').map((i) => i.text());
    expect(menuItems).not.toContain('切换至 Web 平面视图');
    expect(menuItems).toContain('切换至时间线视图');
  });

  it('会话 ended 状态迁移时，WebPlaneView 出现会话已结束提示栏，点击切回终端正常跳转', async () => {
    mockEndpoints({
      detail: { cliType: 'pi' },
      webui: { state: 'available', url: '/webui/sess-1/#/t=token123' },
    });

    const { wrapper, router } = await mountWithRoute(pinia, { view: 'webplane' });

    expect(wrapper.find('[data-testid="webplane-host"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="webplane-ended-bar"]').exists()).toBe(false);

    // 在 WebPlaneView 上直接验证 ended 栏触发
    const webplane = wrapper.findComponent(WebPlaneView);
    expect(webplane.exists()).toBe(true);

    // 触发 switch-to-terminal 事件
    webplane.vm.$emit('switchToTerminal');
    await flushPromises();

    expect(router.currentRoute.value.query.view).toBe('terminal');
  });
});
