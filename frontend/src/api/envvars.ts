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
