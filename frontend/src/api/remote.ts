/**
 * Remote API
 * Encapsulates remote control operations
 */

import {
  GetRemoteToken,
  GetRemoteStatus,
  GetRemoteWebUIStatus,
  OpenRemoteWebUI,
  RegenerateRemoteToken,
  ToggleRemoteServer,
  SetRemotePort,
  SetRemoteHost,
  SetRemoteEndpoint,
  CreateRemotePairingWindow,
  GetRemotePairingWindow,
  CancelRemotePairingWindow,
  ListRemoteDevices,
  RevokeRemoteDevice,
  ListRemoteSecurityEvents,
  GetRemoteSecurityHealth,
  AcknowledgeRemoteSecurityHealth,
  GetExternalCleanupRecoveryStatus,
  ConfirmExternalCleanupRecovery,
  GetStartupWarnings,
} from '../../wailsjs/go/main/App';
import type { main, remote } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

/**
 * Get remote token
 */
export function getRemoteToken(): Promise<string> {
  return callApi('[api.remote.getRemoteToken]', () => GetRemoteToken());
}

/**
 * Get remote status
 */
export function getRemoteStatus(): Promise<Record<string, any>> {
  return callApi('[api.remote.getRemoteStatus]', () => GetRemoteStatus());
}

/**
 * Get remote Web UI status
 */
export function getRemoteWebUIStatus(): Promise<main.RemoteWebUIStatusResult> {
  return callApi('[api.remote.getRemoteWebUIStatus]', () => GetRemoteWebUIStatus());
}

/**
 * Open remote Web UI
 */
export function openRemoteWebUI(): Promise<main.OpenRemoteWebUIResult> {
  return callApi('[api.remote.openRemoteWebUI]', () => OpenRemoteWebUI());
}

/**
 * Regenerate remote token
 */
export function regenerateRemoteToken(): Promise<string> {
  return callApi('[api.remote.regenerateRemoteToken]', () => RegenerateRemoteToken());
}

/**
 * Toggle remote server
 */
export function toggleRemoteServer(enabled: boolean): Promise<void> {
  return callApi('[api.remote.toggleRemoteServer]', () => ToggleRemoteServer(enabled));
}

/**
 * Set remote port
 */
export function setRemotePort(port: number): Promise<void> {
  return callApi('[api.remote.setRemotePort]', () => SetRemotePort(port));
}

/**
 * Set remote host
 */
export function setRemoteHost(host: string): Promise<void> {
  return callApi('[api.remote.setRemoteHost]', () => SetRemoteHost(host));
}

/**
 * Set remote host + port in ONE backend transaction (Minor-02).
 * Either both persist or neither does — a failure never leaves a partial commit.
 */
export function setRemoteEndpoint(host: string, port: number): Promise<void> {
  return callApi('[api.remote.setRemoteEndpoint]', () => SetRemoteEndpoint(host, port));
}

/* ---------------------------------------------------------------------------
 * M1 配对 / 设备 / 安全事件 API（PG-05 桌面远程控制中心）
 * 绑定权威来源：frontend/wailsjs/go/main/App.d.ts + models.ts（自动生成，勿手改）
 * ------------------------------------------------------------------------- */

/** 创建配对窗口。confirmTerminalExposure 必须为用户显式勾选结果（P-02 不预勾选）。 */
export async function createRemotePairingWindow(
  confirmTerminalExposure: boolean,
): Promise<remote.PairingWindowInfo> {
  return await CreateRemotePairingWindow(confirmTerminalExposure);
}

/** 查询配对窗口状态（不含一次性配对码）。 */
export async function getRemotePairingWindow(): Promise<remote.PairingWindowStatus> {
  return await GetRemotePairingWindow();
}

/** 按 generation CAS 取消配对窗口；返回是否实际取消成功。 */
export async function cancelRemotePairingWindow(generation: number): Promise<boolean> {
  return await CancelRemotePairingWindow(generation);
}

/** 已配对设备列表。 */
export async function listRemoteDevices(): Promise<remote.DeviceInfo[]> {
  return await ListRemoteDevices();
}

/** 撤销设备。confirm 必须来自 PG-06 确认对话的显式确认。 */
export async function revokeRemoteDevice(
  deviceID: string,
  confirm: boolean,
): Promise<remote.RevokeDeviceResult> {
  return await RevokeRemoteDevice(deviceID, confirm);
}

/** 本地可见安全事件（sanitized 投影，newest-first）。limit 合法范围 1..500。 */
export async function listRemoteSecurityEvents(
  limit: number,
): Promise<remote.SecurityEventRecord[]> {
  return await ListRemoteSecurityEvents(limit);
}

/** 有界安全健康快照。 */
export async function getRemoteSecurityHealth(): Promise<remote.SecurityHealthSnapshot> {
  return await GetRemoteSecurityHealth();
}

/** 确认一个已关闭（非 active）的健康问题码；返回最新快照。 */
export async function acknowledgeRemoteSecurityHealth(
  code: string,
): Promise<remote.SecurityHealthSnapshot> {
  return await AcknowledgeRemoteSecurityHealth(code);
}

/* ---------------------------------------------------------------------------
 * M2-INT R12：外部进程清理恢复（legacy/uncertainty）API（R11-002 产品闭环）
 * 绑定权威来源：frontend/wailsjs/go/main/App.d.ts + models.ts（自动生成，勿手改）
 * 隐私语义：status 只含 sessionId/kind/reason/state/canConfirm，无 PID/路径/argv。
 * ------------------------------------------------------------------------- */

/** 隐私最小恢复状态：legacy/uncertain 外部进程清理项与全局锁定标记。 */
export async function getExternalCleanupRecoveryStatus(): Promise<remote.ExternalCleanupRecoveryStatus> {
  return await GetExternalCleanupRecoveryStatus();
}

/**
 * 显式确认恢复。confirmed 必须来自 PG-06 确认对话的显式确认（无 force-clear）。
 * 后端会再次核验进程活性：仍在运行 / 持久化失败 / 项不存在均抛错且不释放锁定。
 */
export async function confirmExternalCleanupRecovery(
  sessionID: string,
  confirmed: boolean,
): Promise<remote.ExternalCleanupRecoveryResult> {
  return await ConfirmExternalCleanupRecovery(sessionID, confirmed);
}

/** 启动警告（本次启动累积；含 legacy 外部清理提示）。仅展示，不当 toast 自动消失。 */
export async function getStartupWarnings(): Promise<string[]> {
  return await GetStartupWarnings();
}
