import { describe, expect, it } from 'vitest';
import type { ClaudeAccount, ClaudeAccountOperation, ClaudeAccountsPool } from '../../../api/claudeAccounts';
import {
  ENROLLMENT_STEPS,
  accountCardActions,
  accountNeedsLogin,
  busyReasonText,
  deriveSectionState,
  emailMaskDisplay,
  enrollmentStepIndex,
  grantHealthView,
  identityStatusView,
  inspectSummary,
  isPendingOccupationCode,
  operationStatusText,
  pendingSwitchText,
  reconcileActionOptions,
  seatTierLabel,
} from '../../../components/provider/claudeAccountsView';

function account(overrides: Partial<ClaudeAccount> = {}): ClaudeAccount {
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
  return {
    capability: 'supported',
    state: 'enabled',
    revision: 3,
    activeGeneration: 2,
    activeId: 'acc-a',
    accounts: [account()],
    rotationEnabled: false,
    ...overrides,
  } as ClaudeAccountsPool;
}

function operation(overrides: Partial<ClaudeAccountOperation> = {}): ClaudeAccountOperation {
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

describe('deriveSectionState（三态渲染 + 过程态）', () => {
  it('初始未加载 → loading（不闪现未录入引导）', () => {
    expect(deriveSectionState(null, false, '')).toBe('loading');
  });

  it('capability unsupported → hidden（整个区隐藏）', () => {
    expect(deriveSectionState(pool({ capability: 'unsupported', state: 'unsupported' }), true, '')).toBe('hidden');
  });

  it('池不存在（state=disabled / null）→ absent（未录入）', () => {
    expect(deriveSectionState(pool({ state: 'disabled' }), true, '')).toBe('absent');
    expect(deriveSectionState(null, true, '')).toBe('absent');
  });

  it('state=enabled → managed；state=enrolling → enrolling', () => {
    expect(deriveSectionState(pool(), true, '')).toBe('managed');
    expect(deriveSectionState(pool({ state: 'enrolling' }), true, '')).toBe('enrolling');
  });

  it('state=needs-reconcile → needs-reconcile；state=blocked → blocked', () => {
    expect(deriveSectionState(pool({ state: 'needs-reconcile' }), true, '')).toBe('needs-reconcile');
    expect(deriveSectionState(pool({ state: 'blocked' }), true, '')).toBe('blocked');
  });

  it('读取失败：有上次数据 → error（保留上次值）；无数据 → blocked（负向配对）', () => {
    expect(deriveSectionState(pool(), true, '刷新失败')).toBe('error');
    expect(deriveSectionState(null, true, '刷新失败')).toBe('blocked');
  });
});

describe('双端登录健康', () => {
  it('五种 grant 健康值全部映射（1a GrantHealth 值域）', () => {
    expect(grantHealthView('ready')).toEqual({ label: '正常', tone: 'ok' });
    expect(grantHealthView('expired').tone).toBe('warn');
    expect(grantHealthView('needs-login')).toEqual({ label: '需重新登录', tone: 'error' });
    expect(grantHealthView('refresh-uncertain').tone).toBe('warn');
    expect(grantHealthView('quarantined').tone).toBe('error');
  });

  it('未知/缺失健康值 → 保守「未知」（负向）', () => {
    expect(grantHealthView(undefined).tone).toBe('muted');
    expect(grantHealthView('whatever').label).toBe('未知');
  });

  it('身份状态：verified 正常；其他保守提示', () => {
    expect(identityStatusView('verified').tone).toBe('ok');
    expect(identityStatusView(undefined).tone).toBe('muted');
    expect(identityStatusView('mismatch').tone).toBe('warn');
  });

  it('accountNeedsLogin：grant needs-login 或 reasonCode NEEDS_LOGIN 均命中（正向+负向）', () => {
    expect(accountNeedsLogin(account({ health: { claudeGrant: 'needs-login', piGrant: 'ready' } }))).toBe(true);
    expect(accountNeedsLogin(account({ health: { claudeGrant: 'ready', piGrant: 'ready', reasonCode: 'NEEDS_LOGIN' } }))).toBe(true);
    expect(accountNeedsLogin(account())).toBe(false);
  });
});

describe('遮罩邮箱显隐（credential-free 不变量）', () => {
  it('未显隐 → 占位符；显隐 → 后端遮罩值本身（正向）', () => {
    expect(emailMaskDisplay('wo***@example.com', false)).toBe('•••••••@—');
    expect(emailMaskDisplay('wo***@example.com', true)).toBe('wo***@example.com');
  });

  it('空值 → 占位横线（负向）', () => {
    expect(emailMaskDisplay(undefined, false)).toBe('—');
    expect(emailMaskDisplay('', true)).toBe('—');
  });

  it('任何输入下都不构造完整邮箱：输出只能是遮罩值或占位符（负向不变量）', () => {
    const out = emailMaskDisplay('ab***@x.com', true);
    expect(out === 'ab***@x.com' || out === '•••••••@—').toBe(true);
    // 遮罩值包含 *** —— 永远不是完整明文形态
    expect(out).toContain('***');
  });
});

describe('operation 状态反馈文案（design §7.1 反馈状态全集）', () => {
  it('running 按 kind：capture 检查登录/等待 Keychain、switch 正在回收切换、reconcile 恢复中', () => {
    expect(operationStatusText(operation({ kind: 'capture', state: 'running' }))).toContain('Keychain');
    expect(operationStatusText(operation({ kind: 'switch', state: 'running' }))).toContain('回收');
    expect(operationStatusText(operation({ kind: 'reconcile-complete-target', state: 'running' }))).toContain('恢复');
  });

  it('done → 成功；blocked → 等待会话退出（正向）', () => {
    expect(operationStatusText(operation({ state: 'done' }))).toBe('成功');
    expect(operationStatusText(operation({ state: 'blocked' }))).toContain('等待会话退出');
  });

  it('error → 错误码中文文案；warnings 附注（正向）', () => {
    expect(operationStatusText(operation({ state: 'error', code: 'SESSIONS_ACTIVE' }))).toContain('退出相关会话');
    expect(
      operationStatusText(operation({ state: 'error', code: 'NEEDS_RECONCILE', warnings: ['journal 未清'] })),
    ).toContain('journal 未清');
  });

  it('未知 kind / 未知 state → 保守文案（负向）', () => {
    expect(operationStatusText(operation({ kind: 'mystery', state: 'running' }))).toContain('进行中');
    expect(operationStatusText(operation({ state: 'weird' } as ClaudeAccountOperation))).toContain('进行中');
  });
});

describe('等待会话退出（pending 占用码）', () => {
  it('SESSIONS_ACTIVE / EXTERNAL_SESSION_UNCERTAIN 是占用码；其他不是（正负配对）', () => {
    expect(isPendingOccupationCode('SESSIONS_ACTIVE')).toBe(true);
    expect(isPendingOccupationCode('EXTERNAL_SESSION_UNCERTAIN')).toBe(true);
    expect(isPendingOccupationCode('REVISION_CONFLICT')).toBe(false);
    expect(isPendingOccupationCode('')).toBe(false);
  });

  it('SESSIONS_ACTIVE：自动完成说明（autoRetry=true）；不确定：人工重试（autoRetry=false）', () => {
    const a = pendingSwitchText('SESSIONS_ACTIVE');
    expect(a.autoRetry).toBe(true);
    expect(a.detail).toContain('自动完成');
    const b = pendingSwitchText('EXTERNAL_SESSION_UNCERTAIN');
    expect(b.autoRetry).toBe(false);
    expect(b.detail).toContain('重试切换');
  });
});

describe('恢复引导（needs-reconcile 三选一）', () => {
  it('无报告 → 三个动作全部可用', () => {
    const opts = reconcileActionOptions(null);
    expect(opts.map((o) => o.action)).toEqual(['complete-target', 'adopt-current', 'restore-source']);
  });

  it('存在 swap journal → 优先 complete-target / restore-source（正向）', () => {
    const opts = reconcileActionOptions({ hasJournal: true, journalPhase: 'prepared', matchesActiveSnapshot: false, externalChangeSuspected: false, generation: 3 } as never);
    expect(opts.map((o) => o.action)).toEqual(['complete-target', 'restore-source']);
  });

  it('疑似外部换号（无 journal）→ adopt-current 优先（正向）', () => {
    const opts = reconcileActionOptions({ hasJournal: false, matchesActiveSnapshot: false, externalChangeSuspected: true, generation: 3 } as never);
    expect(opts[0].action).toBe('adopt-current');
    expect(opts).toHaveLength(3);
  });

  it('inspect 摘要：journal/一致性与分类行（正向）；空报告给引导文案（负向）', () => {
    const lines = inspectSummary({
      hasJournal: true,
      journalPhase: 'prepared',
      matchesActiveSnapshot: false,
      externalChangeSuspected: false,
      classifications: { 'pi-auth': 'before' },
      generation: 4,
    } as never);
    expect(lines.some((l) => l.includes('未结切换事务'))).toBe(true);
    expect(lines.some((l) => l.includes('不一致'))).toBe(true);
    expect(lines.some((l) => l.includes('pi-auth=before'))).toBe(true);
    expect(inspectSummary(null)[0]).toContain('重新检查');
  });
});

describe('录入向导（design §4.1 四步）', () => {
  it('三步定义齐全且步骤索引正确', () => {
    expect(ENROLLMENT_STEPS.map((s) => s.key)).toEqual(['begin', 'login', 'capture']);
    expect(enrollmentStepIndex('login')).toBe(1);
    expect(ENROLLMENT_STEPS[2].detail).toContain('不是同一账号');
  });
});

describe('账号卡操作（§7.1 移除限制）', () => {
  it('待机账号：可切换/可移除/不可更新当前（正向）', () => {
    const standby = account({ id: 'acc-b', active: false });
    const actions = accountCardActions(standby, pool({ activeId: 'acc-a' }), false, false);
    expect(actions.canSwitch).toBe(true);
    expect(actions.canRemove).toBe(true);
    expect(actions.canUpdateCurrent).toBe(false);
  });

  it('活动账号：可更新当前登录；不可直接移除且有限制提示（负向——1a 后端同样拒绝）', () => {
    const active = account({ id: 'acc-a' });
    const actions = accountCardActions(active, pool(), false, false);
    expect(actions.canUpdateCurrent).toBe(true);
    expect(actions.canSwitch).toBe(false);
    expect(actions.canRemove).toBe(false);
    expect(actions.removeHint).toContain('先切换');
  });

  it('busy / needs-reconcile 时全部禁用（负向）', () => {
    const standby = account({ id: 'acc-b', active: false });
    expect(accountCardActions(standby, pool(), true, false).canSwitch).toBe(false);
    expect(accountCardActions(standby, pool(), false, true).canRemove).toBe(false);
  });
});

describe('杂项', () => {
  it('busyReasonText：operation running 优先；否则动作标签（正负配对）', () => {
    expect(busyReasonText(true, operation({ kind: 'switch', state: 'running' }), 'x')).toContain('请稍候');
    expect(busyReasonText(true, null, '保存')).toContain('正在');
    expect(busyReasonText(false, null, 'x')).toBe('');
  });

  it('seatTierLabel：透传；空值占位不伪造（正负配对）', () => {
    expect(seatTierLabel('max-20x')).toBe('max-20x');
    expect(seatTierLabel(undefined)).toBe('—');
  });
});
