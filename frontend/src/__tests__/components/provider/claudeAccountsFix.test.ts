/**
 * Phase 1 修复批次前端测试（diting F08/F09）。
 *
 * F08：真实组件模板分支验证——用 vue/server-renderer 的 renderToString（vue
 * 包自带子路径导出，不新增依赖；仓库规范禁新增 DOM 测试库）渲染真实
 * ClaudeAccountsSection.vue，断言 absent/enrolling/needs-reconcile 分支都能
 * 看见异步 operation 的失败反馈（此前横幅只存在于 managed/error 分支内）。
 * F09：刷新/重启后凭 pool.enrollmentId 续接录入向导。
 */

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { createSSRApp, h, nextTick, ref } from 'vue';
import { renderToString } from 'vue/server-renderer';

// 在 api 边界打桩（真实类型与错误码表保留）。
const { getAccounts, capture, cancelEnroll, reconcile, beginEnroll } = vi.hoisted(() => ({
  getAccounts: vi.fn(),
  capture: vi.fn(),
  cancelEnroll: vi.fn(),
  reconcile: vi.fn(),
  beginEnroll: vi.fn(),
}));

vi.mock('../../../api/claudeAccounts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../api/claudeAccounts')>();
  return {
    ...actual,
    getClaudeAccounts: getAccounts,
    captureClaudeAccount: capture,
    cancelClaudeAccountEnrollment: cancelEnroll,
    reconcileClaudeAccounts: reconcile,
    beginClaudeAccountEnrollment: beginEnroll,
  };
});

vi.mock('../../../api/session', () => ({
  getSessions: vi.fn(async () => []),
}));

vi.mock('../../../../wailsjs/runtime/runtime', () => ({
  EventsOn: vi.fn(() => () => {}),
}));

import type { ClaudeAccount, ClaudeAccountOperation, ClaudeAccountsPool } from '../../../api/claudeAccounts';
import ClaudeAccountsSection from '../../../components/provider/ClaudeAccountsSection.vue';
import { useClaudeAccountsStore } from '../../../stores/claudeAccounts';

function acc(overrides: Partial<ClaudeAccount> = {}): ClaudeAccount {
  return {
    id: 'acc-a',
    alias: '工作号',
    createdAt: '2026-10-28T10:00:00Z',
    updatedAt: '2026-10-28T10:00:00Z',
    emailMasked: 'wo***@example.com',
    seatTier: 'max-20x',
    active: true,
    health: { claudeGrant: 'ready', piGrant: 'ready', identityStatus: 'verified' },
    ...overrides,
  } as ClaudeAccount;
}

function poolOf(overrides: Partial<ClaudeAccountsPool> = {}): ClaudeAccountsPool {
  return {
    capability: 'supported',
    state: 'enabled',
    revision: 7,
    activeGeneration: 4,
    activeId: 'acc-a',
    accounts: [acc()],
    rotationEnabled: false,
    ...overrides,
  } as ClaudeAccountsPool;
}

function errOperation(kind: string): ClaudeAccountOperation {
  return {
    id: 'ca-1',
    kind,
    stage: 'failed',
    state: 'error',
    changed: false,
    activeGeneration: 4,
    code: 'IDENTITY_MISMATCH',
  } as ClaudeAccountOperation;
}

/** 用真实模板渲染组件（SSR；执行真实 computed/模板分支）。 */
async function renderSection(): Promise<string> {
  const app = createSSRApp({ render: () => h(ClaudeAccountsSection) });
  const html = await renderToString(app);
  return html;
}

describe('F08: 异步 operation 失败在 absent/enrolling/needs-reconcile 分支可见', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    getAccounts.mockReset();
    capture.mockReset();
    getAccounts.mockResolvedValue(poolOf());
  });

  it('absent 分支渲染 capture 异步失败横幅', async () => {
    getAccounts.mockResolvedValue(poolOf({ state: 'disabled', accounts: [], activeId: '' }));
    const store = useClaudeAccountsStore();
    await store.refresh();
    // 首次 capture 已提交、异步终态失败（IDENTITY_MISMATCH：两端不是同一账号）。
    store.applyOperationResult(errOperation('capture'), false);
    await nextTick();
    const html = await renderSection();
    expect(html).toContain('ca-operation-error');
    expect(html).toContain('Claude Code 与 Pi 当前登录的不是同一账号');
    expect(html).toContain('ca-absent'); // absent 分支主体仍在（替换此前恒真的空串断言）
    expect(html).not.toContain('ca-enroll'); // 未误入其他分支
    expect(html).toContain('保存当前双端登录'); // absent 引导未消失
  });

  it('enrolling 分支渲染 enrollment capture 异步失败横幅', async () => {
    getAccounts.mockResolvedValue(poolOf({ state: 'enrolling', enrollmentId: 'enroll-1' }));
    const store = useClaudeAccountsStore();
    await store.refresh();
    store.applyOperationResult(errOperation('capture'), false);
    await nextTick();
    const html = await renderSection();
    expect(html).toContain('ca-operation-error');
    expect(html).toContain('Claude Code 与 Pi 当前登录的不是同一账号');
    expect(html).toContain('已在两端登录新账号'); // 向导仍在（F09 续接后默认 login 步）
  });

  it('needs-reconcile 分支渲染恢复操作异步失败横幅', async () => {
    getAccounts.mockResolvedValue(
      poolOf({ state: 'needs-reconcile', pauseReason: '存在未结事务（swap）' }),
    );
    const store = useClaudeAccountsStore();
    await store.refresh();
    store.applyOperationResult(errOperation('reconcile-complete-target'), false);
    await nextTick();
    const html = await renderSection();
    expect(html).toContain('ca-operation-error');
    expect(html).toContain('存在未完成的事务');
  });

  it('running 反馈同样在共用区域可见', async () => {
    getAccounts.mockResolvedValue(poolOf({ state: 'disabled', accounts: [], activeId: '' }));
    const store = useClaudeAccountsStore();
    await store.refresh();
    store.applyOperationResult(
      { id: 'ca-2', kind: 'capture', stage: 'running', state: 'running', changed: false, activeGeneration: 0 } as ClaudeAccountOperation,
      false,
    );
    await nextTick();
    const html = await renderSection();
    expect(html).toContain('ca-operation-running');
  });
});

describe('F09: 重启/刷新后续接 enrollment', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    getAccounts.mockReset();
    capture.mockReset();
  });

  it('refresh 凭 pool.enrollmentId 重建向导（此前只有内存态，重建即丢）', async () => {
    getAccounts.mockResolvedValue(poolOf({ state: 'enrolling', enrollmentId: 'enroll-resume-1' }));
    const store = useClaudeAccountsStore();
    await store.refresh();
    expect(store.enrollment).toEqual({ enrollmentId: 'enroll-resume-1', step: 'login' });

    // 续接的 capture 用持久化 ID；后端仍校验 TxnID+revision。
    capture.mockResolvedValue('ca-op-1');
    getAccounts.mockResolvedValue(poolOf({ state: 'enrolling', enrollmentId: 'enroll-resume-1' }));
    const opId = await store.captureEnrolled('个人号');
    expect(opId).toBe('ca-op-1');
    expect(capture).toHaveBeenCalledWith('enroll-resume-1', '个人号', 7);
  });

  it('后端不再报告 enrollment（完成/ drifted）时清本地向导', async () => {
    getAccounts.mockResolvedValue(poolOf({ state: 'enrolling', enrollmentId: 'enroll-2' }));
    const store = useClaudeAccountsStore();
    await store.refresh();
    expect(store.enrollment).not.toBeNull();

    getAccounts.mockResolvedValue(poolOf({ state: 'needs-reconcile' }));
    await store.refresh();
    expect(store.enrollment).toBeNull();
    expect(store.sectionState).toBe('needs-reconcile');
  });

  it('本地已推进的步骤不被静默刷新重置', async () => {
    getAccounts.mockResolvedValue(poolOf({ state: 'enrolling', enrollmentId: 'enroll-3' }));
    const store = useClaudeAccountsStore();
    await store.refresh();
    store.advanceEnrollmentStep();
    getAccounts.mockResolvedValue(poolOf({ state: 'enrolling', enrollmentId: 'enroll-3' }));
    await store.refresh();
    expect(store.enrollment?.step).toBe('capture');
  });
});

describe('R03: 漂移 enrollment 在 UI 上有可执行的恢复出口', () => {
  const drifted = () =>
    poolOf({
      state: 'needs-reconcile',
      pauseReason: '存在未结事务（enrollment）',
      transactionKind: 'enrollment',
      enrollmentDrifted: true,
      enrollmentId: 'enroll-drift-1',
      recoveryActions: ['capture-enrollment', 'cancel-enrollment'],
    } as Partial<ClaudeAccountsPool>);

  beforeEach(() => {
    setActivePinia(createPinia());
    getAccounts.mockReset();
    capture.mockReset();
    cancelEnroll.mockReset();
    reconcile.mockReset();
    beginEnroll.mockReset();
  });

  it('录入→取消漂移→刷新后 SSR 仍有保存/取消出口，而非 swap 三选一', async () => {
    getAccounts.mockResolvedValue(poolOf());
    const store = useClaudeAccountsStore();
    await store.refresh();
    beginEnroll.mockResolvedValue('enroll-drift-1');
    getAccounts.mockResolvedValue(poolOf({
      state: 'enrolling', enrollmentId: 'enroll-drift-1', transactionKind: 'enrollment',
    } as Partial<ClaudeAccountsPool>));
    await store.beginEnrollment();
    expect(beginEnroll).toHaveBeenCalledWith(7);
    cancelEnroll.mockRejectedValueOnce(new Error('NEEDS_RECONCILE: 原生登录已变化'));
    await expect(store.cancelEnrollment()).rejects.toThrow('NEEDS_RECONCILE');
    getAccounts.mockResolvedValue(drifted());
    await store.refresh();
    expect(store.sectionState).toBe('needs-reconcile');
    // 漂移 enrollment 仍可续接：store 凭后端 ID 重建
    expect(store.enrollment?.enrollmentId).toBe('enroll-drift-1');
    const html = await renderSection();
    expect(html).toContain('ca-drifted-enrollment');
    expect(html).toContain('ca-drifted-capture');
    expect(html).toContain('ca-drifted-cancel');
    expect(html).not.toContain('完成切换到目标账号');
    expect(html).not.toContain('恢复到切换前账号');
  });

  it('保存出口以持久化 enrollmentId + 当前 revision 调后端 capture', async () => {
    getAccounts.mockResolvedValue(drifted());
    const store = useClaudeAccountsStore();
    await store.refresh();
    capture.mockResolvedValue('ca-op-drift');
    await expect(store.captureEnrolled('个人号')).resolves.toBe('ca-op-drift');
    expect(capture).toHaveBeenCalledWith('enroll-drift-1', '个人号', 7);
  });

  it('取消出口调用后端 CancelEnrollment（后端仍漂移时报错且事务保留）', async () => {
    getAccounts.mockResolvedValue(drifted());
    const store = useClaudeAccountsStore();
    await store.refresh();
    cancelEnroll.mockRejectedValueOnce(new Error('NEEDS_RECONCILE: 原生登录已变化，未自动取消'));
    await expect(store.cancelEnrollment()).rejects.toThrow('NEEDS_RECONCILE');
    expect(cancelEnroll).toHaveBeenCalledWith(7);
    expect(store.enrollment).not.toBeNull(); // 失败不清本地续接状态

    cancelEnroll.mockResolvedValueOnce(undefined);
    getAccounts.mockResolvedValue(poolOf());
    await store.cancelEnrollment();
    expect(store.enrollment).toBeNull();
    expect(store.sectionState).toBe('managed');
  });

  it('swap 事务按后端 recoveryActions 只展示 complete-target / restore-source', async () => {
    getAccounts.mockResolvedValue(
      poolOf({
        state: 'needs-reconcile',
        transactionKind: 'swap',
        recoveryActions: ['complete-target', 'restore-source'],
      } as Partial<ClaudeAccountsPool>),
    );
    const store = useClaudeAccountsStore();
    await store.refresh();
    const html = await renderSection();
    expect(html).toContain('完成切换到目标账号');
    expect(html).toContain('恢复到切换前账号');
    expect(html).not.toContain('采用当前原生登录');
    expect(html).not.toContain('ca-drifted-enrollment');
  });
});

// 保持 ref 导入被使用（模板渲染路径依赖 Vue 响应式；防止 lint 未用告警）。
void ref;
