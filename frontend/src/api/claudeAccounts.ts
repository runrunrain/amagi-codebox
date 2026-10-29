/**
 * Claude 订阅账号轮换 API 层（Phase 1c，design §7.1/§7.2）。
 *
 * 包装 1b App facade 的 10 个 Wails 绑定（frontend/wailsjs/go/main/App），
 * 并提供错误码 → 中文文案映射（1b reason.go 全集 + OPERATION_NOT_FOUND）。
 *
 * 约定：
 *  - 后端同步错误文本形如 "REASON_CODE: 人类可读信息"（1b facade 契约），
 *    异步 operation 结果的错误码在 ClaudeAccountOperationDTO.code。
 *  - 本层不做业务状态判断，只整理参数/类型与错误文案（callApi 惯例）。
 */

import {
  BeginClaudeAccountEnrollment,
  CancelClaudeAccountEnrollment,
  CaptureClaudeAccount,
  GetClaudeAccountOperation,
  GetClaudeAccounts,
  ReconcileClaudeAccounts,
  RemoveClaudeAccount,
  RenameClaudeAccount,
  SetClaudeAccountRotation,
  SwitchClaudeAccount,
} from '../../wailsjs/go/main/App';
import { main } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

export type ClaudeAccount = main.ClaudeAccountDTO;
export type ClaudeAccountHealth = main.ClaudeAccountHealthDTO;
export type ClaudeAccountOperation = main.ClaudeAccountOperationDTO;
export type ClaudeAccountsPool = main.ClaudeAccountsPoolDTO;
export type ClaudeAccountNativeState = main.ClaudeAccountNativeStateDTO;
export type ClaudeAccountReconcileResult = main.ClaudeAccountReconcileResultDTO;
export type ClaudeAccountRotationConfig = main.ClaudeAccountRotationConfigDTO;

/** 启用自动轮换前必须确认的风险声明版本（与后端 claudeAccountsRiskAckVersion 一致）。 */
export const CLAUDE_ROTATION_RISK_ACK_VERSION = 'claude-rotation-v1';

/** ReconcileClaudeAccounts 允许的动作白名单（后端拒绝任意其它值）。 */
export type ClaudeReconcileAction = 'inspect' | 'complete-target' | 'adopt-current' | 'restore-source';

/**
 * 错误码 → 中文文案映射（design §7.2 错误码全集）。
 * 文案面向主上说明「发生了什么 / 下一步做什么」，不含技术细节。
 */
export const CLAUDE_ACCOUNT_ERROR_MESSAGES: Record<string, string> = {
  UNSUPPORTED_PLATFORM: '当前平台不支持 Claude 订阅账号轮换（v1 仅支持 macOS）',
  POOL_LOCKED: '账号池正被其他操作占用，请稍后重试',
  POOL_MISSING: '账号池不存在，请先保存当前双端登录',
  POOL_CORRUPT: '账号池数据损坏，请查看日志后重试',
  REVISION_CONFLICT: '状态已被其他操作更新，请刷新后重试',
  IDENTITY_MISMATCH: 'Claude Code 与 Pi 当前登录的不是同一账号，请核对两端登录后重试',
  IDENTITY_UNVERIFIED: '无法验证账号身份（网络或协议受限），本次操作已被安全拒绝',
  SESSIONS_ACTIVE: '仍有会话在使用当前账号，请先退出相关会话',
  EXTERNAL_SESSION_UNCERTAIN: '检测到无法确认归属的 Claude/Pi 进程，请先关闭外部相关进程后重试',
  NATIVE_LOCK_BUSY: '原生配置文件被其他程序锁定（可能是正在运行的 CLI），请退出相关程序后重试',
  EXTERNAL_ACCOUNT_CHANGE: '原生登录已被外部修改，请先完成恢复或重新核对',
  NEEDS_RECONCILE: '存在未完成的切换事务，请先完成恢复',
  REFRESH_UNCERTAIN: '凭据刷新结果不确定，该账号已暂停参与自动操作，请稍后重试或重新登录',
  NEEDS_LOGIN: '账号凭据已失效，需要重新登录',
  UNSUPPORTED_CREDENTIAL_SCHEMA: '原生凭据格式不受当前版本支持，请重新登录或升级客户端',
  OPERATION_NOT_FOUND: '操作不存在或已随重启清除，请刷新当前账号状态',
};

/** 无法识别错误码时的兜底文案。 */
export const CLAUDE_ACCOUNT_ERROR_FALLBACK = '操作失败，请重试';

/**
 * 从错误对象中解析稳定错误码与详细信息。
 * 后端错误文本约定 "CODE: message"（1b facade）；无前缀时返回 code=''。
 */
export function parseClaudeAccountError(err: unknown): { code: string; detail: string } {
  const text = err instanceof Error ? err.message : String(err ?? '');
  const idx = text.indexOf(':');
  const head = (idx >= 0 ? text.slice(0, idx) : text).trim();
  const detail = (idx >= 0 ? text.slice(idx + 1) : '').trim();
  // 错误码形态：全大写 + 下划线（如 SESSIONS_ACTIVE）；避免把普通中文句首误判为码。
  if (head && head === head.toUpperCase() && /^[A-Z][A-Z0-9_]*$/.test(head)) {
    return { code: head, detail };
  }
  return { code: '', detail: text };
}

/** 错误 → 中文文案；能解析到稳定码时附后端补充信息。 */
export function claudeAccountErrorMessage(err: unknown): string {
  const { code, detail } = parseClaudeAccountError(err);
  const base = (code && CLAUDE_ACCOUNT_ERROR_MESSAGES[code]) || CLAUDE_ACCOUNT_ERROR_FALLBACK;
  return detail ? `${base}（${detail}）` : base;
}

// ---------------------------------------------------------------------------
// facade 包装（参数顺序与 wailsjs/go/main/App.d.ts 一致）
// ---------------------------------------------------------------------------

/** 拉取账号池快照（capability/state/revision/generation/accounts/operation）。 */
export function getClaudeAccounts(): Promise<main.ClaudeAccountsPoolDTO> {
  return callApi('[api.claudeAccounts.getClaudeAccounts]', () => GetClaudeAccounts());
}

/** 开始录入第二个账号：后端先 harvest 现有活动账号并持久化，再进入 enrolling。 */
export function beginClaudeAccountEnrollment(expectedRevision: number): Promise<string> {
  return callApi('[api.claudeAccounts.beginEnrollment]', () =>
    BeginClaudeAccountEnrollment(expectedRevision));
}

/** 采集当前双端登录（enrollmentID 空串 = 首次录入；后端读取秘密，前端不传 token）。 */
export function captureClaudeAccount(
  enrollmentID: string,
  alias: string,
  expectedRevision: number,
): Promise<string> {
  return callApi('[api.claudeAccounts.capture]', () =>
    CaptureClaudeAccount(enrollmentID, alias, expectedRevision));
}

/** 取消录入（只清 enrollment journal；原生槽位漂移保持 needs-reconcile 由 reconcile 处理）。 */
export function cancelClaudeAccountEnrollment(expectedRevision: number): Promise<void> {
  return callApi('[api.claudeAccounts.cancelEnrollment]', () =>
    CancelClaudeAccountEnrollment(expectedRevision));
}

/** 手动切换到目标账号（requestId 幂等键；reason 后端固定 manual）。 */
export function switchClaudeAccount(
  targetID: string,
  expectedGeneration: number,
  requestID: string,
): Promise<string> {
  return callApi('[api.claudeAccounts.switch]', () =>
    SwitchClaudeAccount(targetID, expectedGeneration, requestID));
}

/** 查询后台 operation（stage/state/code/warnings）；未知 ID 返回 OPERATION_NOT_FOUND。 */
export function getClaudeAccountOperation(operationID: string): Promise<main.ClaudeAccountOperationDTO> {
  return callApi('[api.claudeAccounts.getOperation]', () =>
    GetClaudeAccountOperation(operationID));
}

/** 启停自动轮换（启用需池 revision + riskAckVersion）。 */
export function setClaudeAccountRotation(
  enabled: boolean,
  expectedRevision: number,
  riskAckVersion: string,
): Promise<void> {
  return callApi('[api.claudeAccounts.setRotation]', () =>
    SetClaudeAccountRotation(enabled, expectedRevision, riskAckVersion));
}

/** 恢复入口：inspect 零写同步返回报告；其余动作返回后台 operationId。 */
export function reconcileClaudeAccounts(
  action: ClaudeReconcileAction,
  expectedRevision: number,
): Promise<main.ClaudeAccountReconcileResultDTO> {
  return callApi('[api.claudeAccounts.reconcile]', () =>
    ReconcileClaudeAccounts(action, expectedRevision));
}

/** 移除保存的账号（活动账号须先切换或停止管理；未清 journal 禁止移除——后端强制）。 */
export function removeClaudeAccount(id: string, expectedRevision: number): Promise<void> {
  return callApi('[api.claudeAccounts.remove]', () =>
    RemoveClaudeAccount(id, expectedRevision));
}

/** 修改账号别名（长度 1..64，后端强制）。 */
export function renameClaudeAccount(id: string, alias: string, expectedRevision: number): Promise<void> {
  return callApi('[api.claudeAccounts.rename]', () =>
    RenameClaudeAccount(id, alias, expectedRevision));
}
