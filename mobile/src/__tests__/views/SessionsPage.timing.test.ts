/**
 * __tests__/views/SessionsPage.timing.test.ts — M3-006 T0/T1 生产锚点（真实路由导航）
 * ---------------------------------------------------------------------------
 * 谛听 M3-006：T0/T1 只有测试 seam 没有生产锚点。本测试锁定生产接线：
 *   · T0 = lobby load() 起点（每次列表加载创建新 recorder，design §6
 *     「列表路由每次导航创建一个 recorder并完成 T lane」）；
 *   · T1 = SessionsPage 在 loading true→false 后 nextTick（列表成功/空态/
 *     可操作错误态渲染完成）；
 *   · auth 失效踢回 PG-01 不打 T1（fail-closed，该导航无 T 样本）；
 *   · 每次加载新 recorder：重复导航不产生 duplicate_mark invalid；
 *   · privacy：snapshot 固定 schema（exact key allowlist），无 console 默认输出。
 * API 经 vi.mock 注入（importOriginal 保留 ApiRequestError/toApiRequestError
 * 真实分类路径），router 为真实 vue-router（memory history），无真实网络。
 * ---------------------------------------------------------------------------
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia, type Pinia } from 'pinia';
import { createRouter, createMemoryHistory, type Router } from 'vue-router';
import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent } from 'vue';

// --- vi.mock：lib/api（提升；importOriginal 保留错误分类真实路径） ---

vi.mock('../../../src/lib/api', async (importOriginal) => {
  const orig = await importOriginal<typeof import('../../../src/lib/api')>();
  return {
    ...orig,
    getHostSummary: vi.fn(),
    listSessions: vi.fn(),
  };
});

import { ApiRequestError, getHostSummary, listSessions } from '../../../src/lib/api';
import {
  CLI_TYPE_CLAUDE_CODE,
  ERROR_CODE_AUTH_REVOKED,
  ERROR_CODE_SERVICE_DOWN,
  type HostSummary,
  type SessionSummary,
} from '../../../src/lib/contract';
import { useAuthStore } from '../../../src/stores/auth';
import { useLobbyStore } from '../../../src/stores/lobby';
import SessionsPage from '../../../src/views/SessionsPage.vue';

// --- 夹具 ---

const HOST: HostSummary = {
  apiVersion: 'v1',
  serverVersion: '1.0.5-test',
  cliAvailability: [{ cliType: CLI_TYPE_CLAUDE_CODE, available: true }],
};

const SESSION: SessionSummary = {
  id: 'sess-1',
  title: 'Claude Code · timing',
  cliType: CLI_TYPE_CLAUDE_CODE,
  state: 'running',
  control: { state: 'you' },
  lastActivityAt: new Date().toISOString(),
};

/** snapshot schema exact allowlist（privacy fail-closed 反解析断言）。 */
const TOP_KEYS = ['measurements', 'schemaVersion', 'unit'];
const MEASUREMENT_KEYS = ['budgetMs', 'budgetStatus', 'durationMs', 'invalidReason', 'status'];

const Stub = defineComponent({ template: '<div />' });
const AppShell = defineComponent({ template: '<router-view />' });

function buildRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', redirect: '/connect' },
      { path: '/connect', name: 'connect', component: Stub },
      { path: '/lobby', name: 'lobby', component: SessionsPage },
      { path: '/workspace/:sessionId', name: 'workspace', component: Stub },
    ],
  });
}

async function enterLobby(pinia: Pinia, router: Router) {
  const wrapper = mount(AppShell, { global: { plugins: [pinia, router] } });
  await router.push({ name: 'lobby' });
  await router.isReady();
  await flushPromises();
  return wrapper;
}

describe('M3-006 T0/T1 生产锚点（真实路由导航）', () => {
  let pinia: Pinia;
  let consoleInfo: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    pinia = createPinia();
    setActivePinia(pinia);
    vi.mocked(getHostSummary).mockReset();
    vi.mocked(listSessions).mockReset();
    vi.mocked(getHostSummary).mockResolvedValue(HOST);
    useAuthStore().status = 'paired';
    consoleInfo = vi.spyOn(console, 'info').mockImplementation(() => {});
  });

  it('正常分支（列表非空）：T lane observed，固定 schema，无 console 默认输出', async () => {
    vi.mocked(listSessions).mockResolvedValue([SESSION]);
    const router = buildRouter();
    const wrapper = await enterLobby(pinia, router);

    // 列表真实渲染。
    expect(wrapper.find('.session-list').exists()).toBe(true);

    const snap = useLobbyStore().listTimingSnapshot();
    expect(snap).not.toBeNull();
    expect(Object.keys(snap as object).sort()).toEqual(TOP_KEYS);
    expect(snap!.schemaVersion).toBe(1);
    expect(snap!.unit).toBe('ms');
    expect(Object.keys(snap!.measurements).sort()).toEqual(['R0_R1', 'T0_T1']);
    const t = snap!.measurements.T0_T1;
    expect(Object.keys(t).sort()).toEqual(MEASUREMENT_KEYS);
    expect(t.status).toBe('observed');
    expect(typeof t.durationMs).toBe('number');
    expect(Number.isFinite(t.durationMs)).toBe(true);
    expect(t.durationMs).toBeGreaterThanOrEqual(0);
    expect(t.budgetMs).toBe(3000);
    expect(['within_budget', 'over_budget']).toContain(t.budgetStatus);
    expect(t.invalidReason).toBeNull();
    // R lane 与本导航无关：not_occurred（不伪造样本）。
    expect(snap!.measurements.R0_R1.status).toBe('not_occurred');

    // 无 console 默认输出（design §6 消费面）。
    expect(consoleInfo.mock.calls.every((c: unknown[]) => !String(c[0]).startsWith('TIMING_REPORT_V1'))).toBe(true);
    consoleInfo.mockRestore();
  });

  it('空态分支：空列表渲染完成同样完成 T lane', async () => {
    vi.mocked(listSessions).mockResolvedValue([]);
    const router = buildRouter();
    const wrapper = await enterLobby(pinia, router);

    expect(wrapper.find('.empty-state').exists()).toBe(true);
    const snap = useLobbyStore().listTimingSnapshot();
    expect(snap?.measurements.T0_T1.status).toBe('observed');
    consoleInfo.mockRestore();
  });

  it('停止、退出与不可用会话不会残留在大厅卡片列表', async () => {
    vi.mocked(listSessions).mockResolvedValue([
      SESSION,
      { ...SESSION, id: 'sess-stopped', state: 'stopped' },
      { ...SESSION, id: 'sess-exited', state: 'exited' },
      { ...SESSION, id: 'sess-unavailable', state: 'unavailable' },
    ]);
    const router = buildRouter();
    const wrapper = await enterLobby(pinia, router);

    expect(wrapper.findAll('.session-card')).toHaveLength(1);
    expect(useLobbyStore().sessions.map((item) => item.id)).toEqual(['sess-1']);
    consoleInfo.mockRestore();
  });

  it('可操作错误态分支：分类错误 + 重试渲染完成同样完成 T lane', async () => {
    vi.mocked(listSessions).mockRejectedValue(
      new ApiRequestError({
        message: 'session service down',
        code: ERROR_CODE_SERVICE_DOWN,
        layer: 'session',
        actionHint: 'retry',
        status: 503,
        requestId: null,
      }),
    );
    const router = buildRouter();
    const wrapper = await enterLobby(pinia, router);

    const errorCard = wrapper.find('.lobby-error-card');
    expect(errorCard.exists()).toBe(true);
    expect(errorCard.text()).toContain('宿主会话服务不可用');
    // 可操作：重试按钮存在且未禁用。
    const retry = errorCard.find('button');
    expect(retry.exists()).toBe(true);
    expect(retry.attributes('disabled')).toBeUndefined();

    const snap = useLobbyStore().listTimingSnapshot();
    expect(snap?.measurements.T0_T1.status).toBe('observed');
    consoleInfo.mockRestore();
  });

  it('auth 失效：踢回 PG-01，不打 T1（fail-closed，无快照）', async () => {
    vi.mocked(listSessions).mockRejectedValue(
      new ApiRequestError({
        message: 'device revoked',
        code: ERROR_CODE_AUTH_REVOKED,
        layer: 'auth',
        actionHint: 're-pair',
        status: 401,
        requestId: null,
      }),
    );
    const router = buildRouter();
    await enterLobby(pinia, router);
    await flushPromises();

    expect(router.currentRoute.value.name).toBe('connect');
    expect(router.currentRoute.value.query.reason).toBe('revoked');
    // 授权失效不是列表可交互终态：T lane 不落完成快照。
    expect(useLobbyStore().listTimingSnapshot()).toBeNull();
    consoleInfo.mockRestore();
  });

  it('每次加载创建新 recorder：刷新/再导航后 T lane 仍 observed（无 duplicate fault）', async () => {
    vi.mocked(listSessions).mockResolvedValue([SESSION]);
    const router = buildRouter();
    const wrapper = await enterLobby(pinia, router);
    expect(useLobbyStore().listTimingSnapshot()?.measurements.T0_T1.status).toBe('observed');

    // 用户点刷新（又一次 load）：新 recorder，不 fault。
    await wrapper.find('.refresh-btn').trigger('click');
    await flushPromises();
    const afterRefresh = useLobbyStore().listTimingSnapshot();
    expect(afterRefresh?.measurements.T0_T1.status).toBe('observed');
    expect(afterRefresh?.measurements.T0_T1.invalidReason).toBeNull();

    // 离开大厅再回来（组件重挂载 → 又一次 load）：仍 observed。
    await router.push({ name: 'workspace', params: { sessionId: 'sess-1' } });
    await flushPromises();
    await router.push({ name: 'lobby' });
    await flushPromises();
    const afterReturn = useLobbyStore().listTimingSnapshot();
    expect(afterReturn?.measurements.T0_T1.status).toBe('observed');
    expect(afterReturn?.measurements.T0_T1.invalidReason).toBeNull();
    consoleInfo.mockRestore();
  });
});
