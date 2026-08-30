/**
 * System Proxy API
 * 全局设备显式代理（系统级 HTTP(S) 代理开关）
 *
 * 活状态与开关包装 wailsjs/go/main/App（注册表实时读写 + 可达性探测）；
 * 端点持久化包装 wailsjs/go/settings/Service（下次开启时写入的地址）。
 * 仅 Windows 支持（capabilities.systemProxyControlSupported 门控 UI）。
 */

import { GetSystemProxyStatus, SetSystemProxyEnabled } from '../../wailsjs/go/main/App'
import {
  GetSystemProxyEndpoint,
  SetSystemProxyEndpoint,
} from '../../wailsjs/go/settings/Service'
import { callApi } from './internal/call'

/** 系统显式代理完整快照（enabled/host/port 为系统实时值） */
export interface SystemProxyStatus {
  supported: boolean
  enabled: boolean
  host: string
  port: number
  reachable: boolean
  configuredHost: string
  configuredPort: number
}

/** 持久化的代理端点 */
export interface SystemProxyEndpoint {
  host: string
  port: number
}

/** 读取系统显式代理实时状态（含可达性探测与持久化端点） */
export function getSystemProxyStatus(): Promise<SystemProxyStatus> {
  return callApi('[api.systemProxy.getStatus]', () => GetSystemProxyStatus())
}

/**
 * 开启/关闭全局设备显式代理（即时生效并广播系统刷新）。
 * 开启时端点优先用持久化配置，为空则沿用系统现有地址；返回操作后快照。
 */
export function setSystemProxyEnabled(enabled: boolean): Promise<SystemProxyStatus> {
  return callApi('[api.systemProxy.setEnabled]', () => SetSystemProxyEnabled(enabled))
}

/** 读取持久化的代理端点（Load 时已归一，非零值） */
export function getSystemProxyEndpoint(): Promise<SystemProxyEndpoint> {
  return callApi('[api.systemProxy.getEndpoint]', () => GetSystemProxyEndpoint())
}

/** 校验并保存代理端点（去 scheme/拆内嵌端口/范围校验由后端完成） */
export function setSystemProxyEndpoint(host: string, port: number): Promise<void> {
  return callApi('[api.systemProxy.setEndpoint]', () => SetSystemProxyEndpoint(host, port))
}
