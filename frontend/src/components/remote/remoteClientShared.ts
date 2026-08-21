/**
 * 远程客户端（RC1-6）共享工具：12 稳定错误码文案、健康/会话态/控制权文案。
 * 与 remoteShared.ts（服务端远程控制中心）区分：本文件面向"本机作为客户端
 * 连接远端 CodeBox"的方向。
 */

import { extractRemoteErrorCode, remoteErrorText } from '../../api/remoteClient';
import type { RemoteErrorCode } from '../../api/remoteClient';

/**
 * 12 稳定错误码 → 面向用户的分类文案 + 可执行动作
 * （契约：internal/remote/contract/errors.go；交互稿 §2.1 失败分支）。
 */
export const REMOTE_ERROR_CODE_COPY: Record<RemoteErrorCode, string> = {
  'net.unreachable': '无法连接到该地址：请确认对方 CodeBox 已启动、地址与端口正确且网络可达后重试。',
  'service.down': '对方远程服务异常或未开启：请在对方 CodeBox 确认远程访问服务已启动后重试。',
  'auth.unpaired': '本设备尚未与对方配对：请先完成配对流程。',
  'auth.window_expired': '配对码不正确、已被使用或配对窗口已过期：请在对方 CodeBox 重新打开配对窗口，用最新配对码重试。',
  'auth.revoked': '本设备授权已被对方撤销：需要重新配对才能继续访问。',
  'session.not_found': '目标会话不存在或已被移除：请刷新列表后重试。',
  'session.launch_failed': '远端会话启动失败：请确认对方环境已安装对应 CLI 后重试。',
  'control.busy': '会话控制权正被他人持有：请稍后再试或请求对方释放控制权。',
  'control.forbidden': '当前没有该会话的控制权：此操作需要持有控制权。',
  'history.gap': '终端历史存在缺口：缺失区间的输出无法恢复，可从中断点继续。',
  'rate.limited': '请求过于频繁：请稍候重试。',
  bad_request: '请求不合法：请检查输入格式（地址为 host:port）；若持续出现，可能是两端版本不兼容，请升级后重试。',
};

/** 按错误码取文案；无码（本地校验/未知错误）走通用兜底。 */
export function copyForRemoteError(err: unknown): string {
  const code = extractRemoteErrorCode(err);
  if (code) return REMOTE_ERROR_CODE_COPY[code];
  const text = remoteErrorText(err);
  if (text.includes('not paired') || text.includes('not connected')) {
    return '尚未连接到远程主机：请先完成配对并连接。';
  }
  if (text.includes('not found')) {
    return '目标主机不在登记簿中：请刷新主机列表后重试。';
  }
  if (text.includes('invalid') || text.includes('required')) {
    return '输入不合法：请检查地址（host:port）与名称后重试。';
  }
  return '操作未完成，请重试。';
}

/** 错误的原始一行文本（供排查）。 */
export function detailForRemoteError(err: unknown): string {
  return remoteErrorText(err);
}

/** 主机健康投影 → 状态灯颜色语义（交互稿：绿/灰/红）。 */
export type HostHealthTone = 'green' | 'gray' | 'red';

export function hostHealthTone(health: string): HostHealthTone {
  switch (health) {
    case 'reachable':
      return 'green';
    case 'revoked':
      return 'red';
    default:
      // probing / unreachable / 未知：灰（不可达或未探测）
      return 'gray';
  }
}

export function hostHealthLabel(health: string): string {
  switch (health) {
    case 'reachable':
      return '可达';
    case 'unreachable':
      return '不可达';
    case 'revoked':
      return '已撤销';
    case 'probing':
      return '未探测';
    default:
      return health || '未知';
  }
}

/** 远端会话五态（契约 contract.SessionState）→ 徽标文案与色调。 */
export function remoteSessionStateLabel(state: string): string {
  switch (state) {
    case 'running':
      return '运行中';
    case 'stopped':
      return '已停止';
    case 'exited':
      return '已退出';
    case 'unavailable':
      return '不可用';
    case 'removed':
      return '已移除';
    default:
      return state || '未知';
  }
}

export type SessionStateTone = 'success' | 'muted' | 'warning' | 'danger';

export function remoteSessionStateTone(state: string): SessionStateTone {
  switch (state) {
    case 'running':
      return 'success';
    case 'unavailable':
      return 'warning';
    case 'removed':
      return 'danger';
    default:
      return 'muted';
  }
}

/** 控制权四态（契约 contract.ControlState；视觉风格 §4）。 */
export function controlStateLabel(state: string): string {
  switch (state) {
    case 'you':
      return '控制权：你';
    case 'other':
      return '控制权：他人';
    case 'desktop':
      return '控制权：桌面';
    default:
      return '';
  }
}

export type ControlTone = 'none' | 'you' | 'other' | 'desktop';

export function controlStateTone(state: string): ControlTone {
  switch (state) {
    case 'you':
      return 'you';
    case 'other':
      return 'other';
    case 'desktop':
      return 'desktop';
    default:
      return 'none';
  }
}
