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

import { envcheck } from '../../wailsjs/go/models';

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
export async function runEnvCheck(): Promise<OverallStatus> {
  try {
    return await RunEnvCheck();
  } catch (error) {
    console.error('Failed to run env check:', error);
    throw error;
  }
}

/**
 * Check specific tool
 */
export async function checkTool(tool: string): Promise<CheckStatus> {
  try {
    return await CheckTool(tool);
  } catch (error) {
    console.error('Failed to check tool:', error);
    throw error;
  }
}

/**
 * Install tool
 */
export async function installTool(tool: string): Promise<InstallResult> {
  try {
    return await InstallTool(tool);
  } catch (error) {
    console.error('Failed to install tool:', error);
    throw error;
  }
}

/**
 * Update tool
 */
export async function updateTool(tool: string): Promise<InstallResult> {
  try {
    return await UpdateTool(tool);
  } catch (error) {
    console.error('Failed to update tool:', error);
    throw error;
  }
}

/**
 * Start async tool install
 */
export async function startInstallToolAsync(tool: string): Promise<OperationState> {
  try {
    return await StartInstallToolAsync(tool);
  } catch (error) {
    console.error('Failed to start async install:', error);
    throw error;
  }
}

/**
 * Start async tool update
 */
export async function startUpdateToolAsync(tool: string): Promise<OperationState> {
  try {
    return await StartUpdateToolAsync(tool);
  } catch (error) {
    console.error('Failed to start async update:', error);
    throw error;
  }
}

/**
 * Get env check snapshot
 */
export async function getEnvCheckSnapshot(): Promise<EnvCheckSnapshot> {
  try {
    return await GetEnvCheckSnapshot();
  } catch (error) {
    console.error('Failed to get env check snapshot:', error);
    throw error;
  }
}

/**
 * Run fix action
 */
export async function runEnvFixAction(action: string, tool: string, extraPath: string): Promise<FixActionResult> {
  try {
    return await RunEnvFixAction(action, tool, extraPath);
  } catch (error) {
    console.error('Failed to run fix action:', error);
    throw error;
  }
}

/**
 * Install Claude with method
 */
export async function installClaudeWithMethod(method: string): Promise<InstallResult> {
  try {
    return await InstallClaudeWithMethod(method);
  } catch (error) {
    console.error('Failed to install Claude:', error);
    throw error;
  }
}

/**
 * Start async Claude install
 */
export async function startInstallClaudeWithMethodAsync(method: string): Promise<OperationState> {
  try {
    return await StartInstallClaudeWithMethodAsync(method);
  } catch (error) {
    console.error('Failed to start async Claude install:', error);
    throw error;
  }
}

/**
 * Clean Claude install
 */
export async function cleanClaudeInstall(method: string): Promise<InstallResult> {
  try {
    return await CleanClaudeInstall(method);
  } catch (error) {
    console.error('Failed to clean Claude install:', error);
    throw error;
  }
}

/**
 * Uninstall Claude Code
 */
export async function uninstallClaudeCode(method: string): Promise<InstallResult> {
  try {
    return await UninstallClaudeCode(method);
  } catch (error) {
    console.error('Failed to uninstall Claude Code:', error);
    throw error;
  }
}

/**
 * Check Claude config
 */
export async function checkClaudeConfig(): Promise<any> {
  try {
    return await CheckClaudeConfig();
  } catch (error) {
    console.error('Failed to check Claude config:', error);
    throw error;
  }
}

/**
 * Fix Claude config
 */
export async function fixClaudeConfig(key: string, value: string, filePath: string): Promise<any> {
  try {
    return await FixClaudeConfig(key, value, filePath);
  } catch (error) {
    console.error('Failed to fix Claude config:', error);
    throw error;
  }
}
