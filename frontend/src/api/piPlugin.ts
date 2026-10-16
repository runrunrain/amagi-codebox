/**
 * Pi Package API
 * Wraps the dedicated backend PiPlugins service (internal/piplugin).
 * Install/remove/update delegate to the official pi CLI, which manages the
 * packages[] array in ~/.pi/agent/settings.json and the on-disk package
 * entities under ~/.pi/agent/npm/ and ~/.pi/agent/git/.
 */

import {
  GetPackageDetails,
  InstallPackage,
  RefreshPackages,
  RemovePackage,
  UpdatePackage,
} from '../../wailsjs/go/piplugin/Service';

export interface PiPackage {
  id: string;
  source: string;
  sourceType: 'npm' | 'git' | 'local';
  name: string;
  version?: string;
  description?: string;
  author?: string;
  repository?: string;
  scope: 'user';
  enabled: boolean;
  installPath?: string;
  manifestPath?: string;
  lastUpdated?: string;
  pinned?: boolean;
  extensions?: string[];
  skills?: string[];
  prompts?: string[];
  themes?: string[];
}

export interface PiResourceInfo {
  name: string;
  filePath: string;
  type: 'extension' | 'skill' | 'prompt' | 'theme';
}

export interface PiPackageDetail extends PiPackage {
  resources: PiResourceInfo[];
  manifestDeclared: boolean;
}

export interface PiPackagesData {
  installed: PiPackage[];
  warnings?: string[];
}

export interface PiCommandResult {
  success: boolean;
  output: string;
  error?: string;
}

export async function refreshPiPackages(): Promise<PiPackagesData> {
  return await RefreshPackages() as PiPackagesData;
}

export async function getPiPackageDetails(source: string): Promise<PiPackageDetail> {
  return await GetPackageDetails(source) as PiPackageDetail;
}

export async function installPiPackage(source: string): Promise<PiCommandResult> {
  return await InstallPackage(source) as PiCommandResult;
}

export async function updatePiPackage(source: string): Promise<PiCommandResult> {
  return await UpdatePackage(source) as PiCommandResult;
}

export async function removePiPackage(source: string): Promise<PiCommandResult> {
  return await RemovePackage(source) as PiCommandResult;
}

// ---------------------------------------------------------------------------
// P3-S5：插件操作目标化（Windows / WSL / 同时应用）与 WSL 侧状态面。
// App 绑定方法（frontend/wailsjs/go/main/App，生成物不手改）。
// ---------------------------------------------------------------------------

import {
  InstallPiPackageTargeted,
  UpdatePiPackageTargeted,
  RemovePiPackageTargeted,
  GetPiPluginWSLStatus,
  CheckPiPluginWSLStatus,
  GetWSLPluginTargetSupport,
} from '../../wailsjs/go/main/App';

/** 操作目标：Windows（默认）/ 当前 WSL distro / 同时应用。 */
export type PiPluginTarget = 'windows' | 'wsl' | 'both';

export interface PiPluginOpOutcome {
  target: 'windows' | 'wsl';
  ok: boolean;
  outcome: string; // installed/updated/removed/already-ok/blocked/failed/skipped
  errCode?: string;
  detail?: string;
  output?: string;
  pinnedRef?: string;
  refKind?: string;
}

export interface PiPluginTargetedResult {
  target: PiPluginTarget;
  results: PiPluginOpOutcome[];
}

export interface WSLPluginItem {
  source: string;
  identity: string;
  sourceType: string;
  name?: string;
  version?: string;
  commit?: string;
  managed: boolean;
  state: string; // installed / mismatch / local-item / registered-missing / frozen
  detail?: string;
  entityPresent: boolean;
  settingsPresent: boolean;
  entityPath?: string;
  pinnedRef?: string;
  refKind?: string;
  frozen?: boolean;
  frozenReason?: string;
  lastOp?: string;
  lastErrorCode?: string;
}

export interface WSLPluginSnapshot {
  distro: string;
  available: boolean;
  reason?: string;
  checked: boolean;
  checkedAt?: string;
  piResolve?: string;
  items: WSLPluginItem[];
  managed: number;
  installed: number;
  mismatched: number;
  local: number;
}

export interface PluginRegistrationSummary {
  managed: number;
  installed: number;
  failed: number;
  frozen: number;
  missing: number;
  lastOpAt?: string;
}

export interface PiPluginWSLStatus {
  distro: string;
  available: boolean;
  reason?: string;
  snapshot: WSLPluginSnapshot;
  summary: PluginRegistrationSummary;
}

export interface WSLPluginTargetEntry {
  cli: string;
  support: string; // operations / status-placeholder / excluded
  detail: string;
}

/** 目标化安装（WSL 侧重放：npm 固定版本、git 固定 ref；每目标独立结果）。 */
export function installPiPackageTargeted(source: string, target: PiPluginTarget): Promise<PiPluginTargetedResult> {
  return InstallPiPackageTargeted(source, target) as Promise<PiPluginTargetedResult>;
}

/** 目标化更新（仅本系统登记管理的 WSL 插件可更新；脏仓库冻结）。 */
export function updatePiPackageTargeted(source: string, target: PiPluginTarget): Promise<PiPluginTargetedResult> {
  return UpdatePiPackageTargeted(source, target) as Promise<PiPluginTargetedResult>;
}

/** 目标化移除（WSL 本地手动项不动；实体按 pi 语义保留）。 */
export function removePiPackageTargeted(source: string, target: PiPluginTarget): Promise<PiPluginTargetedResult> {
  return RemovePiPackageTargeted(source, target) as Promise<PiPluginTargetedResult>;
}

/** WSL 侧插件状态（缓存快照，只读不唤醒停机 distro）。 */
export function getPiPluginWSLStatus(): Promise<PiPluginWSLStatus> {
  return GetPiPluginWSLStatus() as Promise<PiPluginWSLStatus>;
}

/** 显式检查 WSL 侧插件（允许唤醒当前 distro；只读不写）。 */
export function checkPiPluginWSLStatus(): Promise<PiPluginWSLStatus> {
  return CheckPiPluginWSLStatus() as Promise<PiPluginWSLStatus>;
}

/** 各 CLI 插件面板的 WSL 目标支持描述（pi=operations；其他=占位；omp 排除）。 */
export function getWSLPluginTargetSupport(): Promise<WSLPluginTargetEntry[]> {
  return GetWSLPluginTargetSupport() as Promise<WSLPluginTargetEntry[]>;
}
