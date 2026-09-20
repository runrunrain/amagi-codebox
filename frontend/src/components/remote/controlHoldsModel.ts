/**
 * controlHoldsModel — 控制权管理卡（PG-05 卡⑤ 活动控制）的纯逻辑层。
 *
 * 桌面前端单测环境为 node（无 DOM，见 vitest.config.ts 注释），组件渲染
 * 分支由本模块的纯函数驱动（与 quotaModel / remoteShared 同款规范）：
 * 会话 ID 短显、持有状态徽标、空态文案、PG-06 确认后果文案，以及收回
 * 动作的 api 流（组件仅在成功后刷新 + 轻提示）。
 *
 * 语义冻结：收回 = 桌面根权威 TakeDesktop→ReleaseDesktop 组合链；设备端
 * 收到 takeover + released 事件后即可重新接管；终态无人持有。
 */

import { releaseSessionControl } from '../../api/remote';
import { classifyRemoteError } from './remoteShared';

/** 与后端 SessionControlHoldView 同构的持有投影（api 层返回形状）。 */
export interface ControlHold {
  sessionID: string;
  deviceID: string;
  deviceName: string;
  inGrace: boolean;
}

/** 空态文案（冻结契约）。 */
export const EMPTY_HOLDS_TEXT = '当前没有远程设备持有控制权';

/** 会话 ID 短显：≤8 位原样，超长取前 8 位 + …（完整 ID 在确认对话展示）。 */
export function shortSessionID(sid: string): string {
  if (!sid) return '—';
  return sid.length <= 8 ? sid : `${sid.slice(0, 8)}…`;
}

/** 持有设备显示名：deviceName → deviceID → 「未知设备」逐级回退。 */
export function holderDisplayName(h: ControlHold): string {
  return h.deviceName || h.deviceID || '未知设备';
}

export interface HoldBadge {
  text: string;
  /** ok=正常连接态；warn=宽限期（连接已断，保留期内） */
  tone: 'ok' | 'warn';
}

/** 持有状态徽标：连接中 / 宽限期。 */
export function holdBadge(h: ControlHold): HoldBadge {
  return h.inGrace ? { text: '宽限期', tone: 'warn' } : { text: '连接中', tone: 'ok' };
}

/** PG-06 确认对话后果文案（冻结契约：设备可重新接管，非断连/封禁）。 */
export function releaseConsequence(h: ControlHold): string {
  return `设备「${holderDisplayName(h)}」将立即失去会话 ${h.sessionID} 的控制权，可重新接管。`;
}

export interface ReleaseOutcome {
  ok: boolean;
  /** 失败时的用户可读错误（已分类）；成功时缺省。 */
  errorMessage?: string;
}

/** 执行收回：成功 → {ok:true}；失败 → 分类后的可读错误（不抛出，交组件展示）。 */
export async function performRelease(sessionID: string): Promise<ReleaseOutcome> {
  try {
    await releaseSessionControl(sessionID);
    return { ok: true };
  } catch (err) {
    return { ok: false, errorMessage: classifyRemoteError(err).message };
  }
}
