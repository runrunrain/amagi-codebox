/**
 * Environment Variables API
 * Encapsulates environment variable operations
 */

import {
  GetEnvVars,
  SetEnvVar,
  DeleteEnvVar,
  GetEnvVarsJSON,
  SaveEnvVarsJSON,
  ImportEnvVars,
  ExportEnvVars,
  ExportEnvVarsToFile,
  ImportEnvVarsFromFile,
  GetEnvVarsGlobalSyncStatus,
  SetEnvVarsGlobalSyncEnabled,
  SetEnvVarPathAllow,
} from '../../wailsjs/go/main/App';

import { envvars } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

type EnvVar = envvars.EnvVar;
type GlobalSyncStatus = envvars.GlobalSyncStatus;

/**
 * Get environment variables
 */
export function getEnvVars(): Promise<EnvVar[]> {
  return callApi('[api.envvars.getEnvVars]', () => GetEnvVars());
}

/**
 * Set environment variable
 */
export function setEnvVar(key: string, value: string): Promise<void> {
  return callApi('[api.envvars.setEnvVar]', () => SetEnvVar(key, value));
}

/**
 * Delete environment variable
 */
export function deleteEnvVar(key: string): Promise<void> {
  return callApi('[api.envvars.deleteEnvVar]', () => DeleteEnvVar(key));
}

/**
 * Get environment variables as JSON
 */
export function getEnvVarsJSON(): Promise<string> {
  return callApi('[api.envvars.getEnvVarsJSON]', () => GetEnvVarsJSON());
}

/**
 * Save environment variables from JSON
 */
export function saveEnvVarsJSON(jsonStr: string): Promise<void> {
  return callApi('[api.envvars.saveEnvVarsJSON]', () => SaveEnvVarsJSON(jsonStr));
}

/**
 * Import environment variables
 */
export function importEnvVars(jsonStr: string): Promise<void> {
  return callApi('[api.envvars.importEnvVars]', () => ImportEnvVars(jsonStr));
}

/**
 * Export environment variables
 */
export function exportEnvVars(): Promise<string> {
  return callApi('[api.envvars.exportEnvVars]', () => ExportEnvVars());
}

/**
 * Export environment variables to file
 */
export function exportEnvVarsToFile(): Promise<void> {
  return callApi('[api.envvars.exportEnvVarsToFile]', () => ExportEnvVarsToFile());
}

/**
 * Import environment variables from file
 */
export function importEnvVarsFromFile(): Promise<void> {
  return callApi('[api.envvars.importEnvVarsFromFile]', () => ImportEnvVarsFromFile());
}

/**
 * Get global sync status
 */
export function getEnvVarsGlobalSyncStatus(): Promise<GlobalSyncStatus> {
  return callApi('[api.envvars.getEnvVarsGlobalSyncStatus]', () => GetEnvVarsGlobalSyncStatus());
}

/**
 * Set global sync enabled
 */
export function setEnvVarsGlobalSyncEnabled(enabled: boolean): Promise<GlobalSyncStatus> {
  return callApi('[api.envvars.setEnvVarsGlobalSyncEnabled]', () => SetEnvVarsGlobalSyncEnabled(enabled));
}

/**
 * Set per-key path-value forwarding allowance (P4-A2 WSL path gate).
 * 开启后该键的 Windows 盘符路径值不再被 WSL 启动链剥离，并经 WSLENV /p
 * 标志自动翻译为 /mnt 路径；保留键（WSLENV/PATH/PATHEXT）后端拒绝。
 */
export function setEnvVarPathAllow(key: string, allow: boolean): Promise<void> {
  return callApi('[api.envvars.setEnvVarPathAllow]', () => SetEnvVarPathAllow(key, allow));
}

// --- P4-A2 路径值放行门（E6）与 UE5 模板（E5）的纯前端判定面 ---

/**
 * WSL WSLENV 保留键：永不跨界转发，也不适用路径值放行门
 * （后端 platform.IsWSLENVReservedKey / 保存校验拒绝，前端同步禁用操作）。
 */
export const WSL_RESERVED_ENV_KEYS: readonly string[] = ['WSLENV', 'PATH', 'PATHEXT'];

/** 键是否为 WSL 保留键（大小写不敏感，容忍首尾空白） */
export function isWSLReservedEnvKey(key: string): boolean {
  return WSL_RESERVED_ENV_KEYS.includes(key.trim().toUpperCase());
}

/**
 * 值是否为 Windows 盘符绝对路径（C:\\... 或 C:/...）——WSL 启动链
 * 默认剥离的形态；仅此类键需要用户显式开启放行门。
 */
export function isWindowsDrivePathValue(value: string): boolean {
  return /^[a-zA-Z]:[\\/]/.test(value.trim());
}

/** UE5 模板条目：一键添加为待填键（值留空，用户自行填写） */
export interface EnvVarTemplateEntry {
  key: string;
  description: string;
  /** 值预期为 Windows 盘符路径（放行门适用面） */
  pathLike: boolean;
}

/**
 * UE5 工作流常用变量模板（P4-A2 E5）：纯前端静态清单，不含本机路径；
 * 添加后不自动启用、不含默认放行标记（allowPathValue 恒为缺省 false）。
 * UE_ROOT 为引擎根目录（路径值，如需跨界需在行内显式开启放行门）；
 * 版本三件套为标量键，直接转发即可。
 */
export const UE5_ENV_TEMPLATE: readonly EnvVarTemplateEntry[] = [
  {
    key: 'UE_ROOT',
    description: 'UE5 引擎根目录（Windows 盘符路径，值自填，例如 C:\\Program Files\\Epic Games\\UE_5.4）',
    pathLike: true,
  },
  { key: 'UE_VERSION_MAJOR', description: '引擎主版本号（如 5）', pathLike: false },
  { key: 'UE_VERSION_MINOR', description: '引擎次版本号（如 4）', pathLike: false },
  { key: 'UE_VERSION_HOTFIX', description: '引擎修订号（如 4，对应 5.4.4）', pathLike: false },
];
