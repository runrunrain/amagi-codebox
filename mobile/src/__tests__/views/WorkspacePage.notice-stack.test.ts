/**
 * __tests__/views/WorkspacePage.notice-stack.test.ts — M3-008 NoticeStack 渲染控制
 * ---------------------------------------------------------------------------
 * 谛听 M3-008：优先级只计算（store.primaryNotice 枚举）未完整控制页面渲染。
 * 本测试锁定 DOM 层互斥（design §7 冻结优先级 P0>P1>P2>P3）：
 *   · P3 degraded / P1 lastError 横幅必须按 noticeLevel 互斥渲染（高压级在位时隐藏，
 *     高压级 dismiss 后自然恢复——store 状态不被压制方改变）；
 *   · fatal（removed/terminal 等）必须直接禁 ControlBar——覆盖「removed 但 socket
 *     尚 attached 仍保留操作入口」场景；
 *   · E-06 control notice fatal 时隐藏（既有行为回归锁定）。
 * WS 层经 vi.mock 注入假客户端（同 __tests__/stores/workspace.test.ts 模式）；
 * store 状态经真实 WS 事件（attached）+ 返回 ref 直写驱动（pinia setup store
 * 返回 ref 即可写 state），页面经真实 vue-router 挂载。
 * ---------------------------------------------------------------------------
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia, type Pinia } from 'pinia';
import { createRouter, createMemoryHistory, type Router } from 'vue-router';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { defineComponent, nextTick } from 'vue';

// --- vi.mock：lib/ws（与 workspace.test.ts 同一 FakeWsClient 模式） ---

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
  const decode = (b64: string): string => {
    const bin = atob(b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return new TextDecoder('utf-8').decode(bytes);
  };
  return {
    SessionWsClient: FakeWsClient,
    decodeChunkToText: decode,
    encodeUtf8ToBase64: (s: string) => s,
  };
});

import { useWorkspaceStore } from '../../../src/stores/workspace';
import WorkspacePage from '../../../src/views/WorkspacePage.vue';

// --- 夹具 ---

const GUIDE_KEY = 'amagi.pg03.guide.dismissed';

const DETAIL = {
  id: 'sess-1',
  title: 'Claude Code · notice-stack',
  cliType: 'claudecode',
  state: 'running',
  control: { state: 'you' },
  lastActivityAt: new Date().toISOString(),
  workdir: '/users/dev/demo',
  startedAt: new Date().toISOString(),
  earliestSeq: 0,
  latestSeq: 0,
};

function mockFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ ok: true, status: 200, json: async () => DETAIL }) as Response),
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

const Stub = defineComponent({ template: '<div />' });
const AppShell = defineComponent({ template: '<router-view />' });

interface Mounted {
  wrapper: VueWrapper;
  store: ReturnType<typeof useWorkspaceStore>;
  client: FakeWsClient;
}

async function mountWorkspace(pinia: Pinia): Promise<Mounted> {
  const router: Router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/lobby', name: 'lobby', component: Stub },
      { path: '/connect', name: 'connect', component: Stub },
      { path: '/workspace/:sessionId', name: 'workspace', component: WorkspacePage },
    ],
  });
  const wrapper = mount(AppShell, { global: { plugins: [pinia, router] } });
  await router.push({ name: 'workspace', params: { sessionId: 'sess-1' } });
  await router.isReady();
  await flushPromises();
  const store = useWorkspaceStore();
  const client = FakeWsClient.instances[FakeWsClient.instances.length - 1];
  if (client) {
    // 真实 WS 事件驱动到 attached（socket 开、session running、control you）。
    client.opts.onEvent(attachedEvent());
    client.opts.onStateChange({ state: 'attached', attempt: 0, nextDelayMs: null, terminalReason: null });
    await flushPromises();
  }
  return { wrapper, store, client };
}

/** ControlBar 操作按钮（control=you → 释放控制权）。 */
function controlAction(wrapper: VueWrapper) {
  return wrapper.find('.control-bar .control-action');
}

describe('M3-008 NoticeStack 优先级渲染控制（design §7）', () => {
  let pinia: Pinia;

  beforeEach(() => {
    pinia = createPinia();
    setActivePinia(pinia);
    FakeWsClient.instances = [];
    mockFetch();
    localStorage.setItem(GUIDE_KEY, '1');
  });

  it('P3 degraded 单独存在：降级横幅渲染，无其他横幅并列', async () => {
    const { wrapper, store } = await mountWorkspace(pinia);
    store.degradedNotice = '部分历史可能不完整（收到无法识别的历史信息）。';
    await nextTick();

    expect(store.primaryNotice).toBe('degraded');
    expect(wrapper.find('.banner--warning').exists()).toBe(true);
    expect(wrapper.find('[data-testid="continuity-banner"]').exists()).toBe(false);
    // attached 且非 fatal：ControlBar 可用。
    expect(controlAction(wrapper).attributes('disabled')).toBeUndefined();
  });

  it('P2(E-07)+P3：ContinuityBanner 渲染，degraded 被压制不并列', async () => {
    const { wrapper, store } = await mountWorkspace(pinia);
    store.degradedNotice = '降级提示';
    store.recoveryEpisode = { generation: 1, state: 'restored', attempt: 1, nextDelayMs: null, withGap: false };
    await nextTick();

    expect(store.primaryNotice).toBe('recovery');
    expect(wrapper.find('[data-testid="continuity-banner"]').exists()).toBe(true);
    expect(wrapper.find('.banner--warning').exists()).toBe(false);
  });

  it('P1+P2+P3：仅 error 横幅渲染；E-07/degraded 隐藏；dismiss P1 后 P2 自然恢复', async () => {
    const { wrapper, store } = await mountWorkspace(pinia);
    store.degradedNotice = '降级提示';
    store.recoveryEpisode = { generation: 1, state: 'restored', attempt: 1, nextDelayMs: null, withGap: false };
    store.lastError = { code: 'session.write_rejected', message: '写入被服务端拒绝', actionHint: 'retry' };
    await nextTick();

    expect(store.primaryNotice).toBe('error');
    const errorBanner = wrapper.find('.banner--danger');
    expect(errorBanner.exists()).toBe(true);
    expect(errorBanner.text()).toContain('写入被服务端拒绝');
    expect(wrapper.find('[data-testid="continuity-banner"]').exists()).toBe(false);
    expect(wrapper.find('.banner--warning').exists()).toBe(false);
    // 非 fatal：ControlBar 仍可用。
    expect(controlAction(wrapper).attributes('disabled')).toBeUndefined();

    // dismiss P1：store 的 E-07 状态未被压制方改变 → 横幅自然恢复。
    store.dismissError();
    await nextTick();
    expect(store.primaryNotice).toBe('recovery');
    expect(wrapper.find('.banner--danger').exists()).toBe(false);
    expect(wrapper.find('[data-testid="continuity-banner"]').exists()).toBe(true);
  });

  it('P0(removed) 但 socket 尚 attached：仅 fatal 横幅；低优先级全隐藏；ControlBar 禁用', async () => {
    const { wrapper, store } = await mountWorkspace(pinia);
    store.degradedNotice = '降级提示';
    store.recoveryEpisode = { generation: 1, state: 'restored', attempt: 1, nextDelayMs: null, withGap: false };
    store.lastError = { code: 'x', message: '不应显示', actionHint: 'retry' };
    store.sessionState = 'removed';
    await nextTick();

    expect(store.primaryNotice).toBe('fatal');
    expect(store.wsState).toBe('attached'); // 关键场景：socket 未关
    const banners = wrapper.findAll('.banner--danger');
    expect(banners).toHaveLength(1);
    expect(banners[0].text()).toContain('会话已被移除');
    expect(wrapper.find('.banner--warning').exists()).toBe(false);
    expect(wrapper.find('[data-testid="continuity-banner"]').exists()).toBe(false);
    // fatal 直接禁 ControlBar（修复前 busy 只看 wsState → 操作入口残留）。
    expect(controlAction(wrapper).attributes('disabled')).toBeDefined();
  });

  it('P0(terminal)：terminal 横幅渲染；ControlBar 禁用；E-06 notice 隐藏', async () => {
    const { wrapper, store } = await mountWorkspace(pinia);
    store.controlNotice = { kind: 'lost', controlState: 'desktop', deviceName: null, text: '控制权已被桌面端接管' };
    await nextTick();
    // 非 fatal 时 E-06 可见（回归基线）。
    expect(wrapper.find('[data-testid="control-notice"]').exists()).toBe(true);

    store.wsState = 'closed';
    store.terminalReason = '会话已移除，控制权随之失效';
    await nextTick();

    expect(store.primaryNotice).toBe('fatal');
    expect(wrapper.find('[data-testid="terminal-banner"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="control-notice"]').exists()).toBe(false);
    expect(controlAction(wrapper).attributes('disabled')).toBeDefined();
  });

  it('P0(loadError)：附着前 detail 失败 → fatal；无低优先级横幅并列', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: false,
        status: 404,
        json: async () => ({ requestId: 'r', code: 'session.not_found', layer: 'session', message: 'not found', actionHint: 'retry' }),
      })),
    );
    const { wrapper, store } = await mountWorkspace(pinia);

    expect(store.primaryNotice).toBe('fatal');
    const errorBanner = wrapper.find('.banner--danger');
    expect(errorBanner.exists()).toBe(true);
    expect(wrapper.find('.banner--warning').exists()).toBe(false);
    expect(wrapper.find('[data-testid="continuity-banner"]').exists()).toBe(false);
  });
});
