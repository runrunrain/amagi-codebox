/**
 * Claude 订阅账号轮换 Store（Phase 1c，design §7.1/§7.2）。
 *
 * 职责：池快照加载/事件订阅（operationId/stage/state/generation 驱动刷新）、
 * 后台 operation 轮询兜底、operation 进行中防重复提交、switch requestId
 * 幂等（同目标在途请求复用同一 promise，不重发第二笔）、SESSIONS_ACTIVE
 * 等待会话退出的 pending 意图、录入向导状态。
 *
 * 数据流（仓库惯例）：api/claudeAccounts → store → 组件；跨域刷新
 * （loadLoginProviders）由组件对 generation 变化做 watch 触发。
 */

import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import * as api from '../api/claudeAccounts';
import type {
  ClaudeAccountNativeState,
  ClaudeAccountOperation,
  ClaudeAccountsPool,
  ClaudeReconcileAction,
} from '../api/claudeAccounts';
import {
  deriveSectionState,
  isPendingOccupationCode,
  operationKindLabel,
} from '../components/provider/claudeAccountsView';

/** operation 轮询节奏（事件驱动为主，轮询兜底丢失事件）。 */
const OPERATION_POLL_MS = 1000;
/** 轮询上限（后端 operation ctx 超时 2 分钟，留余量）。 */
const OPERATION_MAX_POLLS = 150;

/** 等待会话退出的切换意图（SESSIONS_ACTIVE / EXTERNAL_SESSION_UNCERTAIN）。 */
export interface PendingSwitch {
  targetId: string;
  alias: string;
  code: string;
}

type BusyAction = '' | 'capture' | 'enroll' | 'capture-enrolled' | 'switch' | 'inspect' | 'reconcile' | 'rotation' | 'remove' | 'rename';

/** 生成 switch requestId（幂等键）：优先 crypto.randomUUID。 */
export function newSwitchRequestId(): string {
  const c = globalThis.crypto;
  if (c && typeof c.randomUUID === 'function') return c.randomUUID();
  return `sw-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

/** 校验别名（与后端一致：1..64）。 */
export function validateAlias(alias: string): string | null {
  const v = alias.trim();
  if (v.length < 1) return '请输入账号别名';
  if (v.length > 64) return '别名长度不能超过 64 个字符';
  return null;
}

let eventBridgeReady = false;

export const useClaudeAccountsStore = defineStore('claudeAccounts', () => {
  // ---- State ----
  const pool = ref<ClaudeAccountsPool | null>(null);
  const loaded = ref(false);
  const loading = ref(false);
  const loadError = ref('');
  /** 最近一次跟踪的后台 operation（merge pool.operation 与轮询结果）。 */
  const operation = ref<ClaudeAccountOperation | null>(null);
  /** 等待会话退出的切换意图。 */
  const pendingSwitch = ref<PendingSwitch | null>(null);
  /** 录入向导：enrollmentId + 当前步骤（begin 完成后进入 login）。 */
  const enrollment = ref<{ enrollmentId: string; step: 'login' | 'capture' } | null>(null);
  /** inspect 恢复检查报告（needs-reconcile 引导用）。 */
  const lastInspect = ref<ClaudeAccountNativeState | null>(null);
  /** operation 查询失败文案（如 OPERATION_NOT_FOUND，保留上次池数据）。 */
  const operationQueryError = ref('');
  /** 同步阶段动作标记（防重复提交）。 */
  const busyAction = ref<BusyAction>('');
  /** 进行中的 switch 调用（targetId → promise），requestId 幂等入口。 */
  const switchInFlight = new Map<string, Promise<string>>();
  /** switch 操作对应的意图（用于错误时归因到目标账号）。 */
  const pendingTarget = ref<PendingSwitch | null>(null);

  // ---- Computed ----
  const sectionState = computed(() => deriveSectionState(pool.value, loaded.value, loadError.value));
  const busy = computed(
    () => busyAction.value !== '' || operation.value?.state === 'running' || switchInFlight.size > 0,
  );
  const accounts = computed(() => pool.value?.accounts ?? []);
  const activeAccount = computed(
    () => accounts.value.find((a) => a.id === pool.value?.activeId) ?? null,
  );
  const busyLabel = computed(() => {
    if (operation.value?.state === 'running') return operationKindLabel(operation.value.kind);
    if (busyAction.value) return busyActionLabel(busyAction.value);
    if (switchInFlight.size > 0) return operationKindLabel('switch');
    return '';
  });

  function busyActionLabel(action: BusyAction): string {
    const labels: Record<Exclude<BusyAction, ''>, string> = {
      capture: '保存当前登录',
      enroll: '开始录入',
      'capture-enrolled': '核对新账号',
      switch: '切换账号',
      inspect: '检查原生状态',
      reconcile: '执行恢复',
      rotation: '更新自动轮换设置',
      remove: '移除账号',
      rename: '重命名账号',
    };
    return labels[action as Exclude<BusyAction, ''>] || '操作';
  }

  /** operation 进行中防重复提交：busy 时抛出带原因的稳定错误。 */
  function assertNotBusy(): void {
    if (busy.value) {
      throw new Error(`POOL_LOCKED: ${busyLabel.value || '操作进行中'}，请稍候再试`);
    }
  }

  // ---- 池加载 ----

  /** 拉取池快照；失败保留上次值并写 loadError（design §7.1）。 */
  async function refresh(opts?: { silent?: boolean }): Promise<void> {
    if (!opts?.silent) loading.value = true;
    try {
      const res = await api.getClaudeAccounts();
      pool.value = res;
      loaded.value = true;
      loadError.value = '';
      if (res.operation && (!operation.value || operation.value.id !== res.operation.id)) {
        operation.value = res.operation;
      }
    } catch (err) {
      const { code } = api.parseClaudeAccountError(err);
      if (code === 'UNSUPPORTED_PLATFORM') {
        // 平台不支持（含服务未装配）：按 capability 隐藏整个区。
        pool.value = {
          capability: 'unsupported',
          state: 'unsupported',
          revision: 0,
          activeGeneration: 0,
          accounts: [],
          rotationEnabled: false,
        } as unknown as ClaudeAccountsPool;
        loaded.value = true;
        loadError.value = '';
        return;
      }
      loadError.value = api.claudeAccountErrorMessage(err);
      loaded.value = true;
    } finally {
      if (!opts?.silent) loading.value = false;
    }
  }

  // ---- 事件桥（幂等；单测环境下 EventsOn 不可用时降级为仅轮询） ----

  function ensureEventBridge(): void {
    if (eventBridgeReady) return;
    eventBridgeReady = true;
    try {
      EventsOn('claude-accounts:operation', (payload: unknown) => {
        const p = payload as { operationId?: string; state?: string } | null;
        if (!p || typeof p.operationId !== 'string') return;
        // 事件不带 code；终态时由轮询/refresh 拉全量 DTO，这里只做即时刷新。
        if (p.state === 'done' || p.state === 'error' || p.state === 'blocked') {
          void refresh({ silent: true });
        }
      });
      EventsOn('claude-accounts:event', () => {
        void refresh({ silent: true });
      });
    } catch (err) {
      // 非 Wails 环境（单测）：事件桥不可用，轮询兜底。
      console.error('[stores.claudeAccounts] event bridge unavailable:', err);
    }
  }

  // ---- operation 跟踪（轮询兜底；终态写入 pending/清理） ----

  let pollGeneration = 0;

  function applyOperationResult(op: ClaudeAccountOperation, thenRefresh = true): void {
    operation.value = op;
    if (op.state === 'error') {
      const code = op.code || '';
      if (
        op.kind === 'switch' &&
        isPendingOccupationCode(code) &&
        pendingTarget.value
      ) {
        pendingSwitch.value = { ...pendingTarget.value, code };
      }
      pendingTarget.value = null;
    } else if (op.state === 'done') {
      if (op.kind === 'capture') enrollment.value = null;
      if (op.kind === 'switch') pendingSwitch.value = null;
      pendingTarget.value = null;
    }
    if (thenRefresh) void refresh({ silent: true });
  }

  /** 轮询 operation 至终态（done/error）；blocked 视为仍在等待，继续轮询。 */
  async function trackOperation(operationId: string): Promise<void> {
    const token = ++pollGeneration;
    for (let i = 0; i < OPERATION_MAX_POLLS; i++) {
      try {
        const op = await api.getClaudeAccountOperation(operationId);
        if (token !== pollGeneration) return;
        applyOperationResult(op, false);
        if (op.state === 'done' || op.state === 'error') {
          operationQueryError.value = '';
          void refresh({ silent: true });
          return;
        }
      } catch (err) {
        if (token !== pollGeneration) return;
        operationQueryError.value = api.claudeAccountErrorMessage(err);
        return;
      }
      await new Promise((resolve) => setTimeout(resolve, OPERATION_POLL_MS));
      if (token !== pollGeneration) return;
    }
  }

  // ---- 动作 ----

  /** 首次录入：保存当前双端登录（capture，enrollmentID 空串）。 */
  async function saveCurrentLogin(alias: string): Promise<string> {
    assertNotBusy();
    busyAction.value = 'capture';
    try {
      const opId = await api.captureClaudeAccount('', alias, pool.value?.revision ?? 0);
      void trackOperation(opId);
      return opId;
    } finally {
      busyAction.value = '';
    }
  }

  /** 开始录入第二个账号（后端先 harvest 当前账号再进入 enrolling）。 */
  async function beginEnrollment(): Promise<string> {
    assertNotBusy();
    busyAction.value = 'enroll';
    try {
      const enrollmentId = await api.beginClaudeAccountEnrollment(pool.value?.revision ?? 0);
      enrollment.value = { enrollmentId, step: 'login' };
      await refresh({ silent: true });
      return enrollmentId;
    } finally {
      busyAction.value = '';
    }
  }

  /** 向导第 2 步推进（原生登录完成后）。 */
  function advanceEnrollmentStep(): void {
    if (enrollment.value && enrollment.value.step === 'login') {
      enrollment.value = { ...enrollment.value, step: 'capture' };
    }
  }

  /** 录入向导核对并保存新账号。 */
  async function captureEnrolled(alias: string): Promise<string> {
    if (!enrollment.value) throw new Error('POOL_MISSING: 尚未开始录入');
    assertNotBusy();
    busyAction.value = 'capture-enrolled';
    try {
      const opId = await api.captureClaudeAccount(
        enrollment.value.enrollmentId,
        alias,
        pool.value?.revision ?? 0,
      );
      void trackOperation(opId);
      return opId;
    } finally {
      busyAction.value = '';
    }
  }

  /** 取消录入。 */
  async function cancelEnrollment(): Promise<void> {
    assertNotBusy();
    busyAction.value = 'enroll';
    try {
      await api.cancelClaudeAccountEnrollment(pool.value?.revision ?? 0);
      enrollment.value = null;
      await refresh({ silent: true });
    } finally {
      busyAction.value = '';
    }
  }

  // ---- 切换（requestId 幂等 + pending 等待会话退出） ----

  async function doSwitch(targetId: string, alias: string): Promise<string> {
    const poolNow = pool.value;
    if (!poolNow) throw new Error('POOL_MISSING: 账号池未加载，请刷新后重试');
    assertNotBusy();
    busyAction.value = 'switch';
    const requestId = newSwitchRequestId();
    pendingTarget.value = { targetId, alias, code: '' };
    try {
      const opId = await api.switchClaudeAccount(targetId, poolNow.activeGeneration, requestId);
      void trackOperation(opId);
      return opId;
    } catch (err) {
      pendingTarget.value = null;
      throw err;
    } finally {
      busyAction.value = '';
    }
  }

  /**
   * 切换到目标账号。同目标在途请求复用同一 promise（同一 requestId，不重发
   * 第二笔 swap，design §7.1）；不同目标在 busy 期间被拒绝。
   */
  function switchTo(targetId: string, alias: string): Promise<string> {
    const existing = switchInFlight.get(targetId);
    if (existing) return existing;
    const p = doSwitch(targetId, alias).finally(() => {
      switchInFlight.delete(targetId);
    });
    switchInFlight.set(targetId, p);
    return p;
  }

  /**
   * 重试等待中的切换：重新读取池（拿最新 generation），再发起新一笔切换
   * （新 requestId——上一笔已知失败，不是响应丢失重放）。
   */
  async function retryPendingSwitch(): Promise<string> {
    const pending = pendingSwitch.value;
    if (!pending) throw new Error('POOL_MISSING: 没有等待中的切换');
    pendingSwitch.value = null;
    await refresh({ silent: true });
    return switchTo(pending.targetId, pending.alias);
  }

  /** 主上显式取消等待意图（不再自动重试）。 */
  function clearPendingSwitch(): void {
    pendingSwitch.value = null;
    pendingTarget.value = null;
  }

  // ---- 恢复 / 更新当前登录 ----

  /** 更新当前登录（active 卡操作）：采用并回收当前原生登录快照。 */
  async function updateCurrentLogin(): Promise<string> {
    assertNotBusy();
    busyAction.value = 'reconcile';
    try {
      const res = await api.reconcileClaudeAccounts('adopt-current', pool.value?.revision ?? 0);
      if (res.operationId) {
        void trackOperation(res.operationId);
        return res.operationId;
      }
      await refresh({ silent: true });
      return '';
    } finally {
      busyAction.value = '';
    }
  }

  /** 恢复检查（inspect 零写）。 */
  async function reconcileInspect(): Promise<ClaudeAccountNativeState | null> {
    assertNotBusy();
    busyAction.value = 'inspect';
    try {
      const res = await api.reconcileClaudeAccounts('inspect', pool.value?.revision ?? 0);
      lastInspect.value = res.inspect ?? null;
      return lastInspect.value;
    } finally {
      busyAction.value = '';
    }
  }

  /** 执行恢复动作（complete-target / adopt-current / restore-source）。 */
  async function reconcileApply(action: Exclude<ClaudeReconcileAction, 'inspect'>): Promise<string> {
    assertNotBusy();
    busyAction.value = 'reconcile';
    try {
      const res = await api.reconcileClaudeAccounts(action, pool.value?.revision ?? 0);
      if (res.operationId) {
        void trackOperation(res.operationId);
        return res.operationId;
      }
      await refresh({ silent: true });
      return '';
    } finally {
      busyAction.value = '';
    }
  }

  // ---- 轮换开关 / 账号管理 ----

  /** 启停自动轮换（启用需 riskAck 确认，由组件弹层保证；riskAckVersion 固定）。 */
  async function setRotation(enabled: boolean): Promise<void> {
    assertNotBusy();
    busyAction.value = 'rotation';
    try {
      await api.setClaudeAccountRotation(
        enabled,
        pool.value?.revision ?? 0,
        api.CLAUDE_ROTATION_RISK_ACK_VERSION,
      );
      await refresh({ silent: true });
    } finally {
      busyAction.value = '';
    }
  }

  /** 移除保存的账号（活动账号限制由后端强制，前端先做提示）。 */
  async function removeAccount(id: string): Promise<void> {
    assertNotBusy();
    busyAction.value = 'remove';
    try {
      await api.removeClaudeAccount(id, pool.value?.revision ?? 0);
      await refresh({ silent: true });
    } finally {
      busyAction.value = '';
    }
  }

  /** 重命名账号（别名 1..64，后端强制）。 */
  async function renameAccount(id: string, alias: string): Promise<void> {
    assertNotBusy();
    busyAction.value = 'rename';
    try {
      await api.renameClaudeAccount(id, alias, pool.value?.revision ?? 0);
      await refresh({ silent: true });
    } finally {
      busyAction.value = '';
    }
  }

  return {
    // State
    pool,
    loaded,
    loading,
    loadError,
    operation,
    pendingSwitch,
    enrollment,
    lastInspect,
    operationQueryError,
    // Computed
    sectionState,
    busy,
    busyLabel,
    accounts,
    activeAccount,
    // Actions
    refresh,
    ensureEventBridge,
    applyOperationResult,
    trackOperation,
    saveCurrentLogin,
    beginEnrollment,
    advanceEnrollmentStep,
    captureEnrolled,
    cancelEnrollment,
    switchTo,
    retryPendingSwitch,
    clearPendingSwitch,
    updateCurrentLogin,
    reconcileInspect,
    reconcileApply,
    setRotation,
    removeAccount,
    renameAccount,
  };
});
