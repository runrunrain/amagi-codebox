import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

// 在 api/claudeAccounts 边界打桩：保留类型与错误码表（真实），替换绑定包装。
const { getAccounts, begin, capture, cancelEnroll, sw, getOp, setRotation, reconcile, removeAcc, renameAcc } =
  vi.hoisted(() => ({
    getAccounts: vi.fn(),
    begin: vi.fn(),
    capture: vi.fn(),
    cancelEnroll: vi.fn(),
    sw: vi.fn(),
    getOp: vi.fn(),
    setRotation: vi.fn(),
    reconcile: vi.fn(),
    removeAcc: vi.fn(),
    renameAcc: vi.fn(),
  }));

vi.mock('../../api/claudeAccounts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/claudeAccounts')>();
  return {
    ...actual,
    getClaudeAccounts: getAccounts,
    beginClaudeAccountEnrollment: begin,
    captureClaudeAccount: capture,
    cancelClaudeAccountEnrollment: cancelEnroll,
    switchClaudeAccount: sw,
    getClaudeAccountOperation: getOp,
    setClaudeAccountRotation: setRotation,
    reconcileClaudeAccounts: reconcile,
    removeClaudeAccount: removeAcc,
    renameClaudeAccount: renameAcc,
  };
});

vi.mock('../../../wailsjs/runtime/runtime', () => ({
  EventsOn: vi.fn(() => () => {}),
}));

import type { ClaudeAccount, ClaudeAccountOperation, ClaudeAccountsPool } from '../../api/claudeAccounts';
import { useClaudeAccountsStore, validateAlias } from '../../stores/claudeAccounts';

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

function pool(overrides: Partial<ClaudeAccountsPool> = {}): ClaudeAccountsPool {
  const a = acc();
  const b = acc({ id: 'acc-b', alias: '个人号', active: false, emailMasked: 'pe***@example.com' });
  return {
    capability: 'supported',
    state: 'enabled',
    revision: 3,
    activeGeneration: 2,
    activeId: 'acc-a',
    accounts: [a, b],
    rotationEnabled: false,
    ...overrides,
  } as ClaudeAccountsPool;
}

function op(overrides: Partial<ClaudeAccountOperation> = {}): ClaudeAccountOperation {
  return {
    id: 'op-1',
    kind: 'switch',
    stage: 'running',
    state: 'running',
    changed: false,
    activeGeneration: 2,
    ...overrides,
  } as ClaudeAccountOperation;
}

describe('stores/claudeAccounts（状态机 + 幂等 + pending + 防重）', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    [
      getAccounts, begin, capture, cancelEnroll, sw, getOp, setRotation, reconcile, removeAcc, renameAcc,
    ].forEach((fn) => fn.mockReset());
    // operation 轮询在测试中拒绝（OPERATION_NOT_FOUND 形态），由用例直接驱动 applyOperationResult
    getOp.mockRejectedValue(new Error('OPERATION_NOT_FOUND: 测试环境不轮询'));
    // 持久默认池（空池）：动作内部的 silent refresh 不受 Once 队列影响；用例按阶段覆盖默认值
    getAccounts.mockResolvedValue(pool({ state: 'disabled', revision: 0, activeGeneration: 0, activeId: '', accounts: [] }));
  });

  describe('三态渲染（absent / managed / blocked + 过程态）', () => {
    it('初始未加载 → loading；不闪现「未录入」', () => {
      const store = useClaudeAccountsStore();
      expect(store.sectionState).toBe('loading');
    });

    it('空池（disabled）→ absent；有账号（enabled）→ managed（正向）', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValueOnce(pool({ state: 'disabled', revision: 0, activeGeneration: 0, activeId: '', accounts: [] }));
      await store.refresh();
      expect(store.sectionState).toBe('absent');

      getAccounts.mockResolvedValueOnce(pool());
      await store.refresh();
      expect(store.sectionState).toBe('managed');
      expect(store.accounts).toHaveLength(2);
      expect(store.activeAccount?.id).toBe('acc-a');
    });

    it('needs-reconcile / blocked / enrolling 过程态（正向）', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValueOnce(pool({ state: 'needs-reconcile', pauseReason: '存在未结事务（swap）' }));
      await store.refresh();
      expect(store.sectionState).toBe('needs-reconcile');

      getAccounts.mockResolvedValueOnce(pool({ state: 'blocked', pauseReason: 'KEYCHAIN_DENIED' }));
      await store.refresh();
      expect(store.sectionState).toBe('blocked');

      getAccounts.mockResolvedValueOnce(pool({ state: 'enrolling' }));
      await store.refresh();
      expect(store.sectionState).toBe('enrolling');
    });

    it('capability=unsupported（或 UNSUPPORTED_PLATFORM 错误）→ hidden（负向）', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValueOnce(pool({ capability: 'unsupported', state: 'unsupported' }));
      await store.refresh();
      expect(store.sectionState).toBe('hidden');

      getAccounts.mockRejectedValueOnce(new Error('UNSUPPORTED_PLATFORM: 仅支持 macOS'));
      await store.refresh();
      expect(store.sectionState).toBe('hidden');
    });

    it('刷新失败：有上次数据 → error 且保留上次账号（负向——保留上次值）', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValue(pool());
      await store.refresh();
      getAccounts.mockRejectedValueOnce(new Error('POOL_LOCKED: 已被占用'));
      await store.refresh();
      expect(store.sectionState).toBe('error');
      expect(store.loadError).toContain('占用');
      // 保留上次值：账号数据未被清空
      expect(store.accounts).toHaveLength(2);
    });
  });

  describe('主路径：空池 → 录入 A → enrollment B → 切换 pending → 成功 → 需恢复', () => {
    it('全链路（真实动作序列，含 identity 不一致不入池的中段校验）', async () => {
      const store = useClaudeAccountsStore();

      // ① 空池（beforeEach 已设持久默认）
      await store.refresh();
      expect(store.sectionState).toBe('absent');

      // ② 首次录入 A（capture，enrollmentID 空串）
      capture.mockResolvedValueOnce('op-cap-a');
      await store.saveCurrentLogin('工作号');
      expect(capture).toHaveBeenCalledWith('', '工作号', 0);
      store.applyOperationResult(op({ id: 'op-cap-a', kind: 'capture', state: 'done' }), false);
      expect(store.enrollment).toBeNull();

      // ③ 切到「仅 A 已管理」阶段（begin 内部 silent refresh 也读该池）
      getAccounts.mockResolvedValue(pool({ revision: 1, activeGeneration: 1, accounts: [acc()] }));
      await store.refresh();
      expect(store.sectionState).toBe('managed');

      // ④ 添加另一个账号：begin（harvest A）→ 向导
      begin.mockResolvedValueOnce('enr-1');
      await store.beginEnrollment();
      expect(store.enrollment).toEqual({ enrollmentId: 'enr-1', step: 'login' });

      // ⑤ 核对保存 B：身份不一致（负向）→ 不清 enrollment，可重试
      capture.mockResolvedValue('op-cap-b');
      await store.captureEnrolled('个人号');
      expect(capture).toHaveBeenLastCalledWith('enr-1', '个人号', 1);
      store.applyOperationResult(
        op({ id: 'op-cap-b', kind: 'capture', state: 'error', code: 'IDENTITY_MISMATCH' }),
        false,
      );
      expect(store.enrollment).not.toBeNull();

      // ⑥ 再次核对成功（正向配对）
      await store.captureEnrolled('个人号');
      store.applyOperationResult(op({ id: 'op-cap-b2', kind: 'capture', state: 'done' }), false);
      expect(store.enrollment).toBeNull();

      // ⑦ 切换到 B：SESSIONS_ACTIVE → pending 等待会话退出
      getAccounts.mockResolvedValue(pool({ revision: 3, activeGeneration: 3, activeId: 'acc-b' }));
      await store.refresh();
      sw.mockResolvedValueOnce('op-sw-1');
      await store.switchTo('acc-b', '个人号');
      expect(sw).toHaveBeenCalledWith('acc-b', 3, expect.any(String));
      const firstRequestId = sw.mock.calls[0][2];
      store.applyOperationResult(
        op({ id: 'op-sw-1', kind: 'switch', state: 'error', code: 'SESSIONS_ACTIVE' }),
        false,
      );
      expect(store.pendingSwitch).toEqual({ targetId: 'acc-b', alias: '个人号', code: 'SESSIONS_ACTIVE' });

      // ⑧ 会话退出自动重试：刷新代际 + 新 requestId（上一笔已知失败，非响应丢失重放）
      getAccounts.mockResolvedValueOnce(pool({ revision: 3, activeGeneration: 4, activeId: 'acc-b' }));
      sw.mockResolvedValueOnce('op-sw-2');
      await store.retryPendingSwitch();
      const retryCall = sw.mock.calls.at(-1);
      expect(retryCall![0]).toBe('acc-b');
      expect(retryCall![1]).toBe(4); // 刷新后的代际（乐观锁不拿陈旧值）
      expect(retryCall![2]).not.toBe(firstRequestId);

      // ⑨ 切换成功 → pending 清理
      store.applyOperationResult(op({ id: 'op-sw-2', kind: 'switch', state: 'done', changed: true, activeGeneration: 5 }), false);
      expect(store.pendingSwitch).toBeNull();

      // ⑩ 需恢复
      getAccounts.mockResolvedValue(pool({ state: 'needs-reconcile' }));
      await store.refresh();
      expect(store.sectionState).toBe('needs-reconcile');
    });
  });

  describe('operation 进行中防重复提交 + requestId 幂等', () => {
    it('同目标并发切换：只发一笔（同一 requestId 复用），两个调用拿到同一 operationId（正向）', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValue(pool());
      await store.refresh();

      let resolveSwitch!: (v: string) => void;
      sw.mockReturnValueOnce(new Promise((r) => { resolveSwitch = r; }));
      const p1 = store.switchTo('acc-b', '个人号');
      const p2 = store.switchTo('acc-b', '个人号');
      resolveSwitch('op-9');
      const [a, b] = await Promise.all([p1, p2]);
      expect(a).toBe('op-9');
      expect(b).toBe('op-9');
      expect(sw).toHaveBeenCalledTimes(1);
    });

    it('operation running 时：不同目标切换 / 首次录入 / 启停轮换全部被拒（负向）', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValue(pool());
      await store.refresh();
      store.applyOperationResult(op({ kind: 'switch', state: 'running' }), false);
      expect(store.busy).toBe(true);

      await expect(store.switchTo('acc-zzz', 'x')).rejects.toThrow('POOL_LOCKED');
      await expect(store.saveCurrentLogin('新号')).rejects.toThrow('POOL_LOCKED');
      await expect(store.setRotation(true)).rejects.toThrow('POOL_LOCKED');
      expect(sw).not.toHaveBeenCalled();
      expect(capture).not.toHaveBeenCalled();
      expect(setRotation).not.toHaveBeenCalled();
    });

    it('RPC 在途时其他 mutation 被拒（同步阶段 busy 防重，负向）', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValue(pool());
      await store.refresh();
      let resolveSwitch!: (v: string) => void;
      sw.mockReturnValueOnce(new Promise((r) => { resolveSwitch = r; }));
      const p = store.switchTo('acc-b', '个人号');
      await expect(store.switchTo('acc-a', '工作号')).rejects.toThrow('POOL_LOCKED');
      resolveSwitch('op-10');
      await p;
    });

    it('blocked（等待会话退出）不算终态：pending 保留、继续可重试（负向）', () => {
      const store = useClaudeAccountsStore();
      store.applyOperationResult(
        op({ kind: 'switch', state: 'error', code: 'SESSIONS_ACTIVE' }),
        false,
      );
      // 无 pendingTarget（未经过 switchTo）→ 不产生 pending 意图
      expect(store.pendingSwitch).toBeNull();
    });
  });

  describe('pending 等待会话退出（等待重试语义）', () => {
    it('EXTERNAL_SESSION_UNCERTAIN 也进入 pending，但不自动重试的判断交给视图层', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValue(pool());
      await store.refresh();
      sw.mockResolvedValueOnce('op-e1');
      await store.switchTo('acc-b', '个人号');
      store.applyOperationResult(
        op({ id: 'op-e1', kind: 'switch', state: 'error', code: 'EXTERNAL_SESSION_UNCERTAIN' }),
        false,
      );
      expect(store.pendingSwitch?.code).toBe('EXTERNAL_SESSION_UNCERTAIN');

      // 显式取消等待（负向出口）
      store.clearPendingSwitch();
      expect(store.pendingSwitch).toBeNull();
      await expect(store.retryPendingSwitch()).rejects.toThrow('POOL_MISSING');
    });

    it('非占用错误不产生 pending（REVISION_CONFLICT 直接失败，负向）', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValue(pool());
      await store.refresh();
      sw.mockResolvedValueOnce('op-r1');
      await store.switchTo('acc-b', '个人号');
      store.applyOperationResult(
        op({ id: 'op-r1', kind: 'switch', state: 'error', code: 'REVISION_CONFLICT' }),
        false,
      );
      expect(store.pendingSwitch).toBeNull();
    });
  });

  describe('其余 facade 动作参数与状态联动', () => {
    it('reconcile inspect 零写并保存报告；adopt-current 更新当前登录（正向）', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValue(pool({ revision: 5 }));
      await store.refresh();

      reconcile.mockResolvedValueOnce({
        inspect: { hasJournal: true, matchesActiveSnapshot: false, externalChangeSuspected: false, generation: 2 },
      });
      const report = await store.reconcileInspect();
      expect(reconcile).toHaveBeenCalledWith('inspect', 5);
      expect(report?.hasJournal).toBe(true);
      expect(store.lastInspect?.hasJournal).toBe(true);

      reconcile.mockResolvedValue({ operationId: 'op-rc' });
      const opId = await store.updateCurrentLogin();
      expect(reconcile).toHaveBeenLastCalledWith('adopt-current', 5);
      expect(opId).toBe('op-rc');
    });

    it('reconcileApply 白名单动作透传（正向）', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValue(pool({ revision: 6 }));
      await store.refresh();
      reconcile.mockResolvedValueOnce({ operationId: 'op-rs' });
      await store.reconcileApply('restore-source');
      expect(reconcile).toHaveBeenCalledWith('restore-source', 6);
    });

    it('setRotation 携带 riskAck 版本与池 revision；remove/rename 同步 revision（正向）', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValue(pool({ revision: 7 }));
      await store.refresh();
      setRotation.mockResolvedValueOnce(undefined);
      getAccounts.mockResolvedValue(pool({ revision: 7, rotationEnabled: true }));
      await store.setRotation(true);
      expect(setRotation).toHaveBeenCalledWith(true, 7, 'claude-rotation-v1');
      expect(store.pool?.rotationEnabled).toBe(true);

      removeAcc.mockResolvedValueOnce(undefined);
      getAccounts.mockResolvedValue(pool({ revision: 8, accounts: [acc()] }));
      await store.removeAccount('acc-b');
      expect(removeAcc).toHaveBeenCalledWith('acc-b', 7);

      renameAcc.mockResolvedValueOnce(undefined);
      getAccounts.mockResolvedValue(pool({ revision: 9 }));
      await store.renameAccount('acc-a', '主号');
      expect(renameAcc).toHaveBeenCalledWith('acc-a', '主号', 8);
    });

    it('cancelEnrollment 清向导状态并透传 revision（正向）', async () => {
      const store = useClaudeAccountsStore();
      getAccounts.mockResolvedValue(pool({ revision: 4 }));
      await store.refresh();
      begin.mockResolvedValueOnce('enr-2');
      await store.beginEnrollment();
      cancelEnroll.mockResolvedValueOnce(undefined);
      await store.cancelEnrollment();
      expect(cancelEnroll).toHaveBeenCalledWith(4);
      expect(store.enrollment).toBeNull();
    });

    it('captureEnrolled 在未开始录入时拒绝（负向）', async () => {
      const store = useClaudeAccountsStore();
      await expect(store.captureEnrolled('x')).rejects.toThrow('POOL_MISSING');
    });
  });

  describe('别名校验（与后端 1..64 一致）', () => {
    it('空/超长拒绝；首尾空白参与 trim 判定（正负配对）', () => {
      expect(validateAlias('')).toBe('请输入账号别名');
      expect(validateAlias('   ')).toBe('请输入账号别名');
      expect(validateAlias('a'.repeat(65))).toContain('64');
      expect(validateAlias('a'.repeat(64))).toBeNull();
      expect(validateAlias(' 工作号 ')).toBeNull();
    });
  });
});
