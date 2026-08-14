/**
 * Environment Check API
 * Encapsulates environment detection operations
 */

import {
  RunEnvCheck,
  CheckTool,
  InstallTool,
  UpdateTool,
  StartInstallToolAsync,
  StartUpdateToolAsync,
  GetEnvCheckSnapshot,
  RunEnvFixAction,
  InstallClaudeWithMethod,
  StartInstallClaudeWithMethodAsync,
  CleanClaudeInstall,
  UninstallClaudeCode,
  CheckClaudeConfig,
  FixClaudeConfig,
} from '../../wailsjs/go/main/App';

import { CleanHeadroom } from '../../wailsjs/go/envcheck/Service';

import { envcheck } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// Type aliases
type OverallStatus = envcheck.OverallStatus;
type CheckStatus = envcheck.CheckStatus;
type OperationState = envcheck.OperationState;
type EnvCheckSnapshot = envcheck.EnvCheckSnapshot;
type FixActionResult = envcheck.FixActionResult;
type InstallResult = envcheck.InstallResult;

/**
 * Run environment check
 */
export function runEnvCheck(): Promise<OverallStatus> {
  return callApi('[api.envcheck.runEnvCheck]', () => RunEnvCheck());
}

/**
 * Check specific tool
 */
export function checkTool(tool: string): Promise<CheckStatus> {
  return callApi('[api.envcheck.checkTool]', () => CheckTool(tool));
}

/**
 * Install tool
 */
export function installTool(tool: string): Promise<InstallResult> {
  return callApi('[api.envcheck.installTool]', () => InstallTool(tool));
}

/**
 * Update tool
 */
export function updateTool(tool: string): Promise<InstallResult> {
  return callApi('[api.envcheck.updateTool]', () => UpdateTool(tool));
}

/**
 * Start async tool install
 */
export function startInstallToolAsync(tool: string): Promise<OperationState> {
  return callApi('[api.envcheck.startInstallToolAsync]', () => StartInstallToolAsync(tool));
}

/**
 * Start async tool update
 */
export function startUpdateToolAsync(tool: string): Promise<OperationState> {
  return callApi('[api.envcheck.startUpdateToolAsync]', () => StartUpdateToolAsync(tool));
}

/**
 * Get env check snapshot
 */
export function getEnvCheckSnapshot(): Promise<EnvCheckSnapshot> {
  return callApi('[api.envcheck.getEnvCheckSnapshot]', () => GetEnvCheckSnapshot());
}

/**
 * Run fix action
 */
export function runEnvFixAction(action: string, tool: string, extraPath: string): Promise<FixActionResult> {
  return callApi('[api.envcheck.runEnvFixAction]', () => RunEnvFixAction(action, tool, extraPath));
}

/**
 * Install Claude with method
 */
export function installClaudeWithMethod(method: string): Promise<InstallResult> {
  return callApi('[api.envcheck.installClaudeWithMethod]', () => InstallClaudeWithMethod(method));
}

/**
 * Start async Claude install
 */
export function startInstallClaudeWithMethodAsync(method: string): Promise<OperationState> {
  return callApi('[api.envcheck.startInstallClaudeWithMethodAsync]', () => StartInstallClaudeWithMethodAsync(method));
}

/**
 * Clean Claude install
 */
export function cleanClaudeInstall(method: string): Promise<InstallResult> {
  return callApi('[api.envcheck.cleanClaudeInstall]', () => CleanClaudeInstall(method));
}

/**
 * Uninstall Claude Code
 */
export function uninstallClaudeCode(method: string): Promise<InstallResult> {
  return callApi('[api.envcheck.uninstallClaudeCode]', () => UninstallClaudeCode(method));
}

/**
 * Check Claude config
 */
export function checkClaudeConfig(): Promise<envcheck.ClaudeConfigStatus> {
  return callApi('[api.envcheck.checkClaudeConfig]', () => CheckClaudeConfig());
}

/**
 * Clean (uninstall) Headroom via pip.
 * Calls envcheck.Service.CleanHeadroom directly (no App-level wrapper exists).
 * R3-002: when active sessions still hold a headroom lease, the backend returns
 * the Go sentinel `headroom is in use by active sessions` (envcheck.ErrHeadroomInUse)
 * as the error string and does NOT remove the venv. We surface this as a typed
 * rejection so the UI can show a distinct "in use" message instead of a generic
 * uninstall failure.
 */
export function cleanHeadroom(): Promise<InstallResult> {
  return callApi('[api.envcheck.cleanHeadroom]', () => CleanHeadroom());
}

/**
 * Returns true when a cleanHeadroom rejection is the typed "headroom in use by
 * active sessions" rejection (R3-002). The backend wraps envcheck.ErrHeadroomInUse;
 * Wails surfaces the error string, so we match the sentinel message substring.
 * Callers should present a distinct confirm/reject message rather than retrying.
 */
export function isHeadroomInUseRejection(error: unknown): boolean {
  if (!error) return false;
  const msg = typeof error === 'string'
    ? error
    : ((error as { message?: unknown })?.message ?? String(error));
  return String(msg).includes('headroom is in use by active sessions');
}

/**
 * Fix Claude config
 */
export function fixClaudeConfig(key: string, value: string, filePath: string): Promise<envcheck.ConfigFixResult> {
  return callApi('[api.envcheck.fixClaudeConfig]', () => FixClaudeConfig(key, value, filePath));
}
