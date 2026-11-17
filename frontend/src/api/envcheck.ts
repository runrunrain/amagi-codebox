/**
 * Environment Check API
 * Encapsulates environment detection operations
 */

import {
  RunEnvCheck,
  CheckTool,
  StartInstallToolAsync,
  StartUpdateToolAsync,
  GetEnvCheckSnapshot,
  RunEnvFixAction,
  StartInstallClaudeWithMethodAsync,
  CleanClaudeInstall,
  UninstallClaudeCode,
  FixClaudeConfig,
} from '../../wailsjs/go/main/App';

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
 * Fix Claude config
 */
export function fixClaudeConfig(key: string, value: string, filePath: string): Promise<envcheck.ConfigFixResult> {
  return callApi('[api.envcheck.fixClaudeConfig]', () => FixClaudeConfig(key, value, filePath));
}
