/**
 * Claude 订阅账号区视图模型（Phase 1c，design §7.1）。
 *
 * 纯逻辑（无 Vue 依赖、无 IO），供 ClaudeAccountsSection.vue 消费、
 * Vitest 直接测试：三态渲染判定、状态反馈文案、遮罩邮箱显示不变量、
 * 恢复引导选项、录入向导步骤定义。
 */

import {
  CLAUDE_ACCOUNT_ERROR_MESSAGES,
  type ClaudeAccount,
  type ClaudeAccountNativeState,
  type ClaudeAccountOperation,
  type ClaudeAccountsPool,
} from '../../api/claudeAccounts';

// ---------------------------------------------------------------------------
// 区块状态机（design §7.1 三态 + 过程态）
// ---------------------------------------------------------------------------

/**
 * 账号区渲染状态：
 *  - hidden     平台不支持/未装配 → 整个区隐藏（任务书 A/B）
 *  - loading    首次加载中
 *  - absent     未录入（池不存在）→ 首次录入引导
 *  - managed    已管理 → 账号卡 + 操作
 *  - enrolling  录入向导进行中
 *  - needs-reconcile  存在未结事务 → 恢复引导
 *  - blocked    池不可用（Keychain 拒绝/损坏等）→ 错误与重试
 *  - error      读取失败但保留上次值
 */
export type ClaudeSectionState =
  | 'hidden'
  | 'loading'
  | 'absent'
  | 'managed'
  | 'enrolling'
  | 'needs-reconcile'
  | 'blocked'
  | 'error';

/**
 * 从池快照派生区块渲染状态。
 * loaded=false 表示尚未完成首次加载（不闪现「未录入」引导）。
 * loadError 存在且仍有上次数据 → error（保留旧值展示）；无数据 → blocked。
 */
export function deriveSectionState(
  pool: ClaudeAccountsPool | null,
  loaded: boolean,
  loadError: string,
): ClaudeSectionState {
  if (!loaded && !pool && !loadError) return 'loading';
  if (pool && pool.capability === 'unsupported') return 'hidden';
  if (loadError && !pool) return 'blocked';
  if (loadError && pool) return 'error';
  if (!pool) return loaded ? 'absent' : 'loading';
  switch (pool.state) {
    case 'unsupported':
      return 'hidden';
    case 'disabled':
      return 'absent';
    case 'enrolling':
      return 'enrolling';
    case 'needs-reconcile':
      return 'needs-reconcile';
    case 'blocked':
      return 'blocked';
    case 'enabled':
      return 'managed';
    default:
      return 'managed';
  }
}

// ---------------------------------------------------------------------------
// 双端登录健康（1a GrantHealth：ready/expired/needs-login/refresh-uncertain/quarantined）
// ---------------------------------------------------------------------------

export type HealthTone = 'ok' | 'warn' | 'error' | 'muted';

export interface GrantHealthView {
  label: string;
  tone: HealthTone;
}

const GRANT_HEALTH_VIEWS: Record<string, GrantHealthView> = {
  ready: { label: '正常', tone: 'ok' },
  expired: { label: '已过期', tone: 'warn' },
  'needs-login': { label: '需重新登录', tone: 'error' },
  'refresh-uncertain': { label: '刷新不确定', tone: 'warn' },
  quarantined: { label: '已隔离', tone: 'error' },
};

const GRANT_HEALTH_UNKNOWN: GrantHealthView = { label: '未知', tone: 'muted' };

export function grantHealthView(health?: string): GrantHealthView {
  return (health && GRANT_HEALTH_VIEWS[health]) || GRANT_HEALTH_UNKNOWN;
}

/** 身份配对状态：verified 正常；其余（空/未验证）给出保守提示。 */
export function identityStatusView(status?: string): GrantHealthView {
  if (status === 'verified') return { label: '身份已验证', tone: 'ok' };
  if (status) return { label: `身份：${status}`, tone: 'warn' };
  return { label: '身份未记录', tone: 'muted' };
}

/** 账号整体是否需要重新登录（任一 grant needs-login 或 reasonCode NEEDS_LOGIN）。 */
export function accountNeedsLogin(account: ClaudeAccount): boolean {
  return (
    account.health.claudeGrant === 'needs-login' ||
    account.health.piGrant === 'needs-login' ||
    account.health.reasonCode === 'NEEDS_LOGIN'
  );
}

// ---------------------------------------------------------------------------
// 遮罩邮箱显示（design §1.3-5：完整邮箱不出后端，前端只持有遮罩值）
// ---------------------------------------------------------------------------

/** 未显隐切换时的占位符（与真实遮罩形态一致，避免布局跳动）。 */
export const EMAIL_MASK_PLACEHOLDER = '•••••••@—';

/**
 * 遮罩邮箱显示不变量：revealed=true 时显示后端给定的遮罩值本身，
 * 否则显示占位符。任何输入下都不构造完整邮箱（完整邮箱从未跨越后端边界）。
 */
export function emailMaskDisplay(emailMasked: string | undefined, revealed: boolean): string {
  if (!emailMasked) return '—';
  return revealed ? emailMasked : EMAIL_MASK_PLACEHOLDER;
}

// ---------------------------------------------------------------------------
// operation 状态反馈文案（design §7.1 反馈状态全集）
// ---------------------------------------------------------------------------

/** operation 进行中（state=running）按 kind 的反馈文案。 */
const OPERATION_RUNNING_TEXT: Record<string, string> = {
  capture: '正在核对双端登录（可能等待 Keychain 授权，请勿关闭窗口）',
  switch: '正在回收当前账号并写入目标账号…',
  'reconcile-complete-target': '正在完成恢复（提交到目标账号）…',
  'reconcile-adopt-current': '正在采用当前原生登录…',
  'reconcile-restore-source': '正在恢复到切换前账号…',
};

const OPERATION_KIND_LABEL: Record<string, string> = {
  capture: '保存登录',
  switch: '切换账号',
  'reconcile-complete-target': '完成恢复',
  'reconcile-adopt-current': '采用当前登录',
  'reconcile-restore-source': '恢复源账号',
};

export function operationKindLabel(kind: string): string {
  return OPERATION_KIND_LABEL[kind] || '操作';
}

/**
 * operation → 用户可见状态文案。
 *  - running：检查登录/等待 Keychain 授权/正在回收/正在切换（按 kind）
 *  - blocked：等待会话退出
 *  - done：成功
 *  - error：错误码 → 中文文案
 */
export function operationStatusText(op: ClaudeAccountOperation): string {
  switch (op.state) {
    case 'running':
      return OPERATION_RUNNING_TEXT[op.kind] || '操作进行中…';
    case 'blocked':
      return '等待会话退出（相关会话退出后自动继续）';
    case 'done':
      return '成功';
    case 'error': {
      const code = op.code || '';
      const base = (code && CLAUDE_ACCOUNT_ERROR_MESSAGES[code]) || '操作失败';
      return op.warnings && op.warnings.length > 0 ? `${base}（${op.warnings[0]}）` : base;
    }
    default:
      return '操作进行中…';
  }
}

/**
 * 切换失败码是否构成「等待会话退出/占用」的 pending 场景：
 * SESSIONS_ACTIVE → 会话退出后可自动完成；EXTERNAL_SESSION_UNCERTAIN →
 * 需要主上关闭外部进程后手动重试（方向保守，不自动重试）。
 */
export function isPendingOccupationCode(code: string): boolean {
  return code === 'SESSIONS_ACTIVE' || code === 'EXTERNAL_SESSION_UNCERTAIN';
}

/** pending 等待文案：区分可自动完成与需人工介入。 */
export function pendingSwitchText(code: string): { title: string; detail: string; autoRetry: boolean } {
  if (code === 'SESSIONS_ACTIVE') {
    return {
      title: '等待会话退出',
      detail: '仍有会话在使用当前账号；相关会话退出后将自动完成切换。',
      autoRetry: true,
    };
  }
  return {
    title: '等待外部进程确认',
    detail: '检测到无法确认归属的 Claude/Pi 进程。请关闭外部相关进程后点击「重试切换」。',
    autoRetry: false,
  };
}

// ---------------------------------------------------------------------------
// 恢复引导（needs-reconcile：inspect 报告 → 动作选项）
// ---------------------------------------------------------------------------

export interface ReconcileActionView {
  action: 'complete-target' | 'adopt-current' | 'restore-source';
  title: string;
  description: string;
}

const RECONCILE_ACTIONS: Record<string, ReconcileActionView> = {
  'complete-target': {
    action: 'complete-target',
    title: '完成切换到目标账号',
    description: '原生文件部分/全部已是目标账号：按事务日志补完未写入的投影并提交（推荐默认）。',
  },
  'adopt-current': {
    action: 'adopt-current',
    title: '采用当前原生登录',
    description: '原生登录已被外部换成池内另一账号：验证双端身份后将其设为活动账号并回收快照。',
  },
  'restore-source': {
    action: 'restore-source',
    title: '恢复到切换前账号',
    description: '撤销本次切换：以标准切换协议写回源账号（不盲覆盖，双端重新验证）。',
  },
};

/**
 * 恢复动作选项：默认三个都可用（后端各自校验可行性，失败给稳定错误码）。
 * inspect 显示存在 swap journal 时优先 complete-target/restore-source；
 * externalChangeSuspected（无 journal 的外部换号）优先 adopt-current。
 */
export function reconcileActionOptions(inspect: ClaudeAccountNativeState | null): ReconcileActionView[] {
  const all = [
    RECONCILE_ACTIONS['complete-target'],
    RECONCILE_ACTIONS['adopt-current'],
    RECONCILE_ACTIONS['restore-source'],
  ];
  if (!inspect) return all;
  if (inspect.hasJournal) {
    return [RECONCILE_ACTIONS['complete-target'], RECONCILE_ACTIONS['restore-source']];
  }
  if (inspect.externalChangeSuspected) {
    return [RECONCILE_ACTIONS['adopt-current'], ...all.filter((a) => a.action !== 'adopt-current')];
  }
  return all;
}

/** inspect 报告 → 摘要文案（不暴露 token/hash，仅分类布尔与代际）。 */
export function inspectSummary(inspect: ClaudeAccountNativeState | null): string[] {
  if (!inspect) return ['尚未生成恢复检查报告（点击「重新检查」获取当前分类）'];
  const lines: string[] = [];
  lines.push(
    inspect.hasJournal
      ? `存在未结切换事务（阶段：${inspect.journalPhase || '未知'}）`
      : '没有未结事务日志',
  );
  if (inspect.matchesActiveSnapshot) {
    lines.push('原生登录与活动账号快照一致');
  } else {
    lines.push('原生登录与活动账号快照不一致');
  }
  if (inspect.externalChangeSuspected) {
    lines.push('疑似外部换号（原生登录被池外工具修改）');
  }
  const entries = Object.entries(inspect.classifications || {});
  if (entries.length > 0) {
    lines.push(`投影分类：${entries.map(([k, v]) => `${k}=${v}`).join('、')}`);
  }
  return lines;
}

// ---------------------------------------------------------------------------
// 录入向导（design §4.1 四步）
// ---------------------------------------------------------------------------

export interface EnrollmentStepView {
  key: 'begin' | 'login' | 'capture';
  title: string;
  detail: string;
}

/** §4.1：harvest 现有 → 指导原生登录 → 核对保存 →（失败说明在 capture 反馈）。 */
export const ENROLLMENT_STEPS: EnrollmentStepView[] = [
  {
    key: 'begin',
    title: '保存当前账号快照',
    detail: 'CodeBox 先回收并持久化当前双端登录（此步自动完成）。',
  },
  {
    key: 'login',
    title: '在原生客户端登录新账号',
    detail:
      '分别打开原生 Claude Code 与 Pi，退出当前账号并登录另一个订阅账号。' +
      'CodeBox 不接收密码/验证码，也不代理浏览器 OAuth；登录用的进程是录入期间唯一例外，不得同时用于推理。',
  },
  {
    key: 'capture',
    title: '核对并保存',
    detail:
      '关闭登录用的进程后点击「核对并保存」。CodeBox 将分别校验两份登录的账号身份；' +
      '不一致会提示「Claude Code 与 Pi 不是同一账号」且不入池。',
  },
];

export function enrollmentStepIndex(key: EnrollmentStepView['key']): number {
  return ENROLLMENT_STEPS.findIndex((s) => s.key === key);
}

// ---------------------------------------------------------------------------
// 杂项显示
// ---------------------------------------------------------------------------

/** 套餐档显示（后端 seatTier 原样透传；空 → 占位，不伪造）。 */
export function seatTierLabel(tier?: string): string {
  return tier || '—';
}

/** busy 时按钮禁用的原因文案（design §7.1：busy 时按钮说明原因）。 */
export function busyReasonText(
  busy: boolean,
  operation: ClaudeAccountOperation | null,
  actionLabel: string,
): string {
  if (!busy) return '';
  if (operation && operation.state === 'running') {
    return `${operationStatusText(operation)}，请稍候`;
  }
  return actionLabel ? `正在${actionLabel}…，请稍候` : '操作进行中，请稍候';
}

/**
 * 账号卡可用操作（design §7.1）：
 *  - 活动账号：更新当前登录（回收快照）/ 重命名；移除需先切换（§7.1 限制提示）
 *  - 待机账号：切换到此账号 / 重命名 / 移除
 *  - busy 时全部禁用（原因由 busyReasonText 说明）
 */
export interface AccountCardActions {
  canSwitch: boolean;
  canUpdateCurrent: boolean;
  canRemove: boolean;
  removeHint: string;
}

export function accountCardActions(
  account: ClaudeAccount,
  pool: ClaudeAccountsPool | null,
  busy: boolean,
  needsReconcile: boolean,
): AccountCardActions {
  const isActive = !!pool && pool.activeId === account.id;
  if (needsReconcile) {
    return { canSwitch: false, canUpdateCurrent: false, canRemove: false, removeHint: '需先完成恢复' };
  }
  // 活动账号不可直接移除（design §7.1/1a RemoveAccount 强制）：
  // 须先切换到另一完整账号，或显式停止管理（停止管理入口后续版本提供）。
  return {
    canSwitch: !isActive && !busy,
    canUpdateCurrent: isActive && !busy,
    canRemove: !isActive && !busy,
    removeHint: isActive ? '当前活动账号：请先切换到另一账号后再移除（停止管理将于后续版本提供）' : '',
  };
}
