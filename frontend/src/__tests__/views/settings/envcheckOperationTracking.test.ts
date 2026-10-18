/**
 * 环境检测异步操作受理跟踪状态机回归测试。
 * 镜像 frontend/src/views/settings/envcheckOperationTracking.ts。
 *
 * 这是集成批次要求的「可运行状态逻辑测试」：不再只断言纯映射，而是按
 * 真实时序喂观察序列，锁定状态机行为：
 *  - snapshot-error：首拉失败/持续失败期间 pending 不丢、running 不回退；
 *  - 没见过 running 直接终态（快操作/乱序）也要出 terminal 事件；
 *  - uninstall 无 id 回执：tool+kind+时间界匹配，旧终态不误报；
 *  - 有界超时：unknown 事件携带 tool/kind，绝不无限等待；
 *  - 终态确认后不因乱序快照回退 running。
 */
import { describe, it, expect } from 'vitest';
import {
  beginOperation,
  observeOperation,
  isEffectivelyRunning,
  PENDING_OP_TIMEOUT_MS,
  type OperationTrackerState,
  type OperationObs,
} from '../../../views/settings/envcheckOperationTracking';

const T0 = 1_700_000_000_000;

function begin(
  ident: { id?: string; tool: string; kind: string; previousId?: string; requestedAt?: number },
  at = T0,
): OperationTrackerState {
  return beginOperation({ pending: null }, ident, at);
}

function feed(
  state: OperationTrackerState,
  op: OperationObs | null,
  now: number,
): { state: OperationTrackerState; running: boolean; event: string; progress: number } {
  const r = observeOperation(state, op, now);
  return {
    state: r.state,
    running: r.outcome.running,
    event: r.outcome.event.type,
    progress: r.outcome.progress,
  };
}

describe('Major 1：受理回执接管状态（snapshot-error 场景）', () => {
  it('首次及多次快照失败（观察=null）不清 pending，running 持续，轮询条件不丢', () => {
    let st = begin({ id: 'op-1', tool: 'pi', kind: 'install' });
    // 连续拉取失败（Leader smoke 第5项场景：fixture 已 succeeded 但界面未更新）
    for (let i = 1; i <= 5; i++) {
      const r = feed(st, null, T0 + i * 1500);
      expect(r.running).toBe(true);
      expect(r.event).toBe('none');
      st = r.state;
    }
    // 恢复后看到本操作终态 → 立即出 terminal，即使从未见过 running
    const r = feed(st, { id: 'op-1', tool: 'pi', kind: 'install', status: 'succeeded', result: { message: 'done' } }, T0 + 9000);
    expect(r.event).toBe('terminal');
    expect(r.running).toBe(false);
    expect(r.state.pending).toBeNull();
  });

  it('没见过 running 直接终态（快操作/乱序快照）也产出 terminal，不静默丢失', () => {
    const st = begin({ id: 'op-2', tool: 'codex', kind: 'update' });
    const r = feed(
      st,
      { id: 'op-2', tool: 'codex', kind: 'update', status: 'failed', error: 'npm exit 1' },
      T0 + 400,
    );
    expect(r.event).toBe('terminal');
    expect(r.running).toBe(false);
  });

  it('running 观察透传 progress/message；随后终态清空 pending，不再产出事件', () => {
    let st = begin({ id: 'op-3', tool: 'omp', kind: 'install' });
    let r = feed(st, { id: 'op-3', tool: 'omp', kind: 'install', status: 'running', progress: 40, message: 'downloading' }, T0 + 1000);
    expect(r.running).toBe(true);
    expect(r.progress).toBe(40);
    st = r.state;
    // 快照失败一轮：进度沿用旧值（不闪回 0）
    r = feed(st, null, T0 + 2500);
    expect(r.running).toBe(true);
    expect(r.progress).toBe(40);
    st = r.state;
    r = feed(st, { id: 'op-3', tool: 'omp', kind: 'install', status: 'succeeded', progress: 100, result: { version: '1.1.0' } }, T0 + 4000);
    expect(r.event).toBe('terminal');
    st = r.state;
    // 终态后同 id 的陈旧 running 快照不再产生任何事件（防回退）
    r = feed(st, { id: 'op-3', tool: 'omp', kind: 'install', status: 'running', progress: 55 }, T0 + 5000);
    expect(r.event).toBe('none');
    expect(r.running).toBe(false);
  });

  it('快照落在别的操作（旧槽/他窗）不干扰 pending，后续本操作终态正常确认', () => {
    let st = begin({ id: 'op-5', tool: 'pi', kind: 'uninstall' });
    // 服务器 operation 槽还留着上一个操作
    let r = feed(st, { id: 'op-4', tool: 'codex', kind: 'install', status: 'succeeded' }, T0 + 500);
    expect(r.running).toBe(true);
    expect(r.event).toBe('none');
    st = r.state;
    r = feed(st, { id: 'op-5', tool: 'pi', kind: 'uninstall', status: 'succeeded' }, T0 + 2000);
    expect(r.event).toBe('terminal');
  });
});

describe('Major 1：uninstall_tool 无 id 回执的 tool+kind+时间界匹配', () => {
  it('时间容忍窗口内的旧槽也不能认领；第一次匹配后锁定新 id', () => {
    let st = begin({ tool: 'pi', kind: 'uninstall', previousId: 'old' });
    let r = feed(st, { id: 'old', tool: 'pi', kind: 'uninstall', status: 'succeeded', startedAt: T0 - 100 }, T0 + 100);
    expect(r.event).toBe('none');
    r = feed(r.state, { id: 'new', tool: 'pi', kind: 'uninstall', status: 'running' }, T0 + 200);
    expect(r.state.pending?.id).toBe('new');
    st = r.state;
    r = feed(st, { id: 'other', tool: 'pi', kind: 'uninstall', status: 'succeeded', startedAt: T0 + 300 }, T0 + 400);
    expect(r.event).toBe('none');
  });

  it('受理回执慢于实际完成时，以请求开始时间匹配新终态', () => {
    const st = begin({ tool: 'pi', kind: 'uninstall', requestedAt: T0 }, T0 + 10_000);
    const r = feed(st, { id: 'new', tool: 'pi', kind: 'uninstall', status: 'succeeded', startedAt: T0 + 100 }, T0 + 10_100);
    expect(r.event).toBe('terminal');
  });

  it('受理后同 tool/kind 的 running/新终态被认领（startedAt ≥ 受理时刻）', () => {
    let st = begin({ id: '', tool: 'opencode', kind: 'uninstall' });
    let r = feed(st, { id: 'srv-9', tool: 'opencode', kind: 'uninstall', status: 'running', progress: 20, startedAt: new Date(T0 + 100).toISOString() }, T0 + 1000);
    expect(r.running).toBe(true);
    expect(r.progress).toBe(20);
    st = r.state;
    r = feed(st, { id: 'srv-9', tool: 'opencode', kind: 'uninstall', status: 'succeeded', startedAt: new Date(T0 + 100).toISOString(), finishedAt: new Date(T0 + 3000).toISOString() }, T0 + 3500);
    expect(r.event).toBe('terminal');
  });

  it('受理前就存在的旧卸载终态不误报（Leader 明确要求），走到有界超时兜底', () => {
    let st = begin({ id: '', tool: 'opencode', kind: 'uninstall' });
    // 旧终态：startedAt 远早于受理时刻
    const stale = {
      id: 'srv-old', tool: 'opencode', kind: 'uninstall', status: 'succeeded' as const,
      startedAt: new Date(T0 - 60_000).toISOString(), finishedAt: new Date(T0 - 55_000).toISOString(),
    };
    let r = feed(st, stale, T0 + 1500);
    expect(r.event).toBe('none');
    expect(r.running).toBe(true);
    st = r.state;
    r = feed(st, stale, T0 + PENDING_OP_TIMEOUT_MS + 1);
    expect(r.event).toBe('unknown');
    expect(r.state.pending).toBeNull();
  });

  it('不同 tool/kind 的操作不匹配 id-less pending', () => {
    const st = begin({ id: '', tool: 'pi', kind: 'uninstall' });
    const r = feed(
      st,
      { id: 'srv-x', tool: 'opencode', kind: 'uninstall', status: 'running', startedAt: new Date(T0).toISOString() },
      T0 + 500,
    );
    expect(r.event).toBe('none');
    expect(r.running).toBe(true);
  });
});

describe('Major 1：有界超时（不无限轮询）与 unknown 语义', () => {
  it('长安装持续有观察不超时，随后一次失败仍有完整重试窗口', () => {
    const st = begin({ id: 'long', tool: 'pi', kind: 'install' });
    const running = feed(st, { id: 'long', status: 'running' }, T0 + 600_000);
    expect(running.event).toBe('none');
    const failedRead = feed(running.state, null, T0 + 601_000);
    expect(failedRead.event).toBe('none');
    expect(failedRead.running).toBe(true);
    expect(feed(failedRead.state, null, T0 + 600_000 + PENDING_OP_TIMEOUT_MS + 1).event).toBe('unknown');
  });

  it('超时产出 unknown 且携带 tool/kind 供组件层复检；终态在超时前到达则不触发', () => {
    let st = begin({ id: 'op-t', tool: 'pi', kind: 'install' });
    let r = feed(st, null, T0 + PENDING_OP_TIMEOUT_MS - 1);
    expect(r.event).toBe('none');
    st = r.state;
    const out = observeOperation(st, null, T0 + PENDING_OP_TIMEOUT_MS + 1);
    expect(out.outcome.event).toEqual({ type: 'unknown', tool: 'pi', kind: 'install' });
    expect(out.state.pending).toBeNull();
  });
});

describe('Major 1：isEffectivelyRunning 乱序防回退', () => {
  it('已终结 id 的陈旧 running 快照不再视为运行中；其它 running 正常', () => {
    const resolved = new Set(['op-done']);
    expect(isEffectivelyRunning({ id: 'op-done', status: 'running' }, resolved)).toBe(false);
    expect(isEffectivelyRunning({ id: 'op-live', status: 'running' }, resolved)).toBe(true);
    expect(isEffectivelyRunning({ id: 'op-done', status: 'succeeded' }, resolved)).toBe(false);
    expect(isEffectivelyRunning(null, resolved)).toBe(false);
    expect(isEffectivelyRunning({ status: 'running' }, resolved)).toBe(true);
  });
});
