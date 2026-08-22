/**
 * Remote Client API（RC1-6 桌面端互联 · 客户端域）
 * 封装 app_remoteclient.go 的 15 个 RemoteClient* 绑定。
 * 绑定权威来源：frontend/wailsjs/go/main/App.d.ts + models.ts（自动生成，勿手改）。
 *
 * 错误契约：后端 ClientError 经 %w 包装后文本携带稳定错误码
 * （形如 "remoteclient: <code> (layer=<layer>, http=<n>)"），
 * 前端用 extractRemoteErrorCode 按码做恢复决策（契约 12 稳定错误码，
 * internal/remote/contract/errors.go）。
 */

import {
  RemoteClientListHosts,
  RemoteClientAddHost,
  RemoteClientUpdateHost,
  RemoteClientRenameHost,
  RemoteClientRemoveHost,
  RemoteClientProbeHost,
  RemoteClientCompletePairing,
  RemoteClientConnect,
  RemoteClientDisconnect,
  RemoteClientListRemoteSessions,
  RemoteClientGetRemoteSession,
  RemoteClientLaunchRemoteSession,
  RemoteClientStopRemoteSession,
  RemoteClientRestartRemoteSession,
  RemoteClientDeleteRemoteSession,
  RemoteClientTerminalAttach,
  RemoteClientTerminalDetach,
  RemoteClientTerminalSendInput,
  RemoteClientTerminalResize,
  RemoteClientAcquireControl,
  RemoteClientReleaseControl,
} from '../../wailsjs/go/main/App';
import type { remoteclient, contract, main } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

export type HostEntry = remoteclient.HostEntry;
export type ProbeResult = remoteclient.ProbeResult;
export type PairingResult = remoteclient.PairingResult;
export type ConnectResult = main.RemoteClientConnectResult;
export type RemoteSessionSummary = contract.SessionSummary;
export type RemoteSessionDetail = contract.SessionDetail;
export type RemoteTerminalAttachResult = main.RemoteClientTerminalAttachResult;
/** 控制权投影（RC3-3：acquire/release 响应；state/deviceName 顶层同契约快照）。 */
export type ControlView = remoteclient.ControlView;

/** 契约 12 稳定错误码（internal/remote/contract/errors.go KnownErrorCodes）。 */
export const REMOTE_ERROR_CODES = [
  'net.unreachable',
  'service.down',
  'auth.unpaired',
  'auth.window_expired',
  'auth.revoked',
  'session.not_found',
  'session.launch_failed',
  'control.busy',
  'control.forbidden',
  'history.gap',
  'rate.limited',
  'bad_request',
] as const;

export type RemoteErrorCode = (typeof REMOTE_ERROR_CODES)[number];

/**
 * 从绑定层错误文本提取稳定错误码；本地校验错误（地址非法、未连接等）
 * 不携带契约码，返回 null（调用方走通用文案）。
 */
export function extractRemoteErrorCode(err: unknown): RemoteErrorCode | null {
  const text = err instanceof Error ? err.message : String(err);
  const m = text.match(/remoteclient:\s*([a-z_.]+)\s*\(/);
  if (m && (REMOTE_ERROR_CODES as readonly string[]).includes(m[1])) {
    return m[1] as RemoteErrorCode;
  }
  return null;
}

/** 面向日志/展示的一行错误文本。 */
export function remoteErrorText(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === 'string') return err;
  try {
    return String(err);
  } catch {
    return '未知错误';
  }
}

/* ---------------------------------------------------------------------------
 * 登记簿 CRUD
 * ------------------------------------------------------------------------- */

/** 主机登记簿全部条目（恒为非 null 数组）。 */
export function listRemoteHosts(): Promise<HostEntry[]> {
  return callApi('[api.remoteClient.listRemoteHosts]', () => RemoteClientListHosts());
}

/** 新增未配对主机条目（Health=probing）。 */
export function addRemoteHost(displayName: string, hostPort: string): Promise<HostEntry> {
  return callApi('[api.remoteClient.addRemoteHost]', () => RemoteClientAddHost(displayName, hostPort));
}

/** 修改目标地址（域层重置配对态并清理旧凭据）。 */
export function updateRemoteHost(hostID: string, hostPort: string): Promise<void> {
  return callApi('[api.remoteClient.updateRemoteHost]', () => RemoteClientUpdateHost(hostID, hostPort));
}

/** 修改显示名。 */
export function renameRemoteHost(hostID: string, displayName: string): Promise<void> {
  return callApi('[api.remoteClient.renameRemoteHost]', () => RemoteClientRenameHost(hostID, displayName));
}

/** 移除条目并清理 Keychain 凭据。 */
export function removeRemoteHost(hostID: string): Promise<void> {
  return callApi('[api.remoteClient.removeRemoteHost]', () => RemoteClientRemoveHost(hostID));
}

/* ---------------------------------------------------------------------------
 * 探活与配对（蓝图 §6 流程 1）
 * ------------------------------------------------------------------------- */

/** 按 hostPort 探活：返回健康投影与（可达时的）宿主摘要。 */
export function probeRemoteHost(hostPort: string): Promise<ProbeResult> {
  return callApi('[api.remoteClient.probeRemoteHost]', () => RemoteClientProbeHost(hostPort));
}

/** 完整配对流：探活 → pairing/complete → 凭据验证 → Keychain + 登记簿回填（失败零残留）。 */
export function completeRemotePairing(hostPort: string, code: string): Promise<PairingResult> {
  return callApi('[api.remoteClient.completeRemotePairing]', () => RemoteClientCompletePairing(hostPort, code));
}

/* ---------------------------------------------------------------------------
 * 连接（单连接模型：Connect 成功即顶替既有连接）
 * ------------------------------------------------------------------------- */

/** 连接已配对宿主（含进程重启后的 Keychain 凭据恢复与凭据验证）。 */
export function connectRemoteHost(hostID: string): Promise<ConnectResult> {
  return callApi('[api.remoteClient.connectRemoteHost]', () => RemoteClientConnect(hostID));
}

/** 断开当前连接（仅丢弃本地连接视图与内存凭据）。 */
export function disconnectRemoteHost(hostID: string): Promise<void> {
  return callApi('[api.remoteClient.disconnectRemoteHost]', () => RemoteClientDisconnect(hostID));
}

/* ---------------------------------------------------------------------------
 * 会话域（作用于当前已连接宿主）
 * ------------------------------------------------------------------------- */

/**
 * 列出宿主全部会话。
 * 注：生成绑定声明的返回类型 contract.SessionList 是 Go 切片别名，
 * models.ts 中无对应具名导出，此处按元素类型 SessionSummary[] 收口。
 */
export async function listRemoteSessions(): Promise<RemoteSessionSummary[]> {
  return callApi('[api.remoteClient.listRemoteSessions]', async () => {
    const list = (await RemoteClientListRemoteSessions()) as unknown as RemoteSessionSummary[] | null;
    return list ?? [];
  });
}

/** 会话详情。 */
export function getRemoteSession(sessionID: string): Promise<RemoteSessionDetail> {
  return callApi('[api.remoteClient.getRemoteSession]', () => RemoteClientGetRemoteSession(sessionID));
}

/** 在宿主上启动新会话（可选参数空串表示交宿主默认解析）。 */
export function launchRemoteSession(params: {
  cliType: string;
  workdir?: string;
  providerRef?: string;
  presetRef?: string;
  modelRef?: string;
  shellRef?: string;
  useHeadroom?: boolean;
}): Promise<RemoteSessionDetail> {
  return callApi('[api.remoteClient.launchRemoteSession]', () =>
    RemoteClientLaunchRemoteSession(
      params.cliType,
      params.workdir ?? '',
      params.providerRef ?? '',
      params.presetRef ?? '',
      params.modelRef ?? '',
      params.shellRef ?? '',
      params.useHeadroom ?? false,
    ),
  );
}

/** 停止会话（幂等收敛）。 */
export function stopRemoteSession(sessionID: string): Promise<RemoteSessionDetail> {
  return callApi('[api.remoteClient.stopRemoteSession]', () => RemoteClientStopRemoteSession(sessionID));
}

/** 同 ID 重启会话（recipe 不变）。 */
export function restartRemoteSession(sessionID: string): Promise<RemoteSessionDetail> {
  return callApi('[api.remoteClient.restartRemoteSession]', () => RemoteClientRestartRemoteSession(sessionID));
}

/** 移除会话（不可逆；204 无 body）。 */
export function deleteRemoteSession(sessionID: string): Promise<void> {
  return callApi('[api.remoteClient.deleteRemoteSession]', () => RemoteClientDeleteRemoteSession(sessionID));
}

/* ---------------------------------------------------------------------------
 * 终端域（RC2-5：/ws/v1 长连接；输出只经 rc:* 事件总线，不经返回值）
 * ------------------------------------------------------------------------- */

/** attach（或复用）一个会话的终端连接；幂等。输出经 rc:terminal-output 事件回流。 */
export function attachRemoteTerminal(sessionID: string): Promise<RemoteTerminalAttachResult> {
  return callApi('[api.remoteClient.attachRemoteTerminal]', () => RemoteClientTerminalAttach(sessionID));
}

/** 终止会话终端连接（停止重连、销毁输入 outbox）。 */
export function detachRemoteTerminal(sessionID: string): Promise<void> {
  return callApi('[api.remoteClient.detachRemoteTerminal]', () => RemoteClientTerminalDetach(sessionID));
}

/** 发送终端输入（UTF-8 文本；Go 侧编码 base64 后经 outbox 幂等发送）。 */
export function sendRemoteTerminalInput(sessionID: string, data: string): Promise<void> {
  return callApi('[api.remoteClient.sendRemoteTerminalInput]', () => RemoteClientTerminalSendInput(sessionID, data));
}

/** 调整远端 PTY 尺寸（cols/rows 正整数）。 */
export function resizeRemoteTerminal(sessionID: string, cols: number, rows: number): Promise<void> {
  return callApi('[api.remoteClient.resizeRemoteTerminal]', () => RemoteClientTerminalResize(sessionID, cols, rows));
}

/* ---------------------------------------------------------------------------
 * 控制权域（RC3-3：v1 control/acquire|release 空 body POST；写权威在服务端，
 * 返回 ControlView 权威投影；409 control.busy / 403 control.forbidden 按契约
 * 错误码透传，调用方据码提示「他人持有」）
 * ------------------------------------------------------------------------- */

/** 获取会话控制权。 */
export function acquireRemoteControl(sessionID: string): Promise<ControlView> {
  return callApi('[api.remoteClient.acquireRemoteControl]', () => RemoteClientAcquireControl(sessionID));
}

/** 释放会话控制权。 */
export function releaseRemoteControl(sessionID: string): Promise<ControlView> {
  return callApi('[api.remoteClient.releaseRemoteControl]', () => RemoteClientReleaseControl(sessionID));
}
