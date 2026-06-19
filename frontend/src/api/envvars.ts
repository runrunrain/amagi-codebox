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

type EnvVar = envvars.EnvVar;
type GlobalSyncStatus = envvars.GlobalSyncStatus;

/**
 * Get environment variables
 */
export async function getEnvVars(): Promise<EnvVar[]> {
  try {
    return await GetEnvVars();
  } catch (error) {
    console.error('Failed to get env vars:', error);
    throw error;
  }
}

/**
 * Set environment variable
 */
export async function setEnvVar(key: string, value: string): Promise<void> {
  try {
    await SetEnvVar(key, value);
  } catch (error) {
    console.error('Failed to set env var:', error);
    throw error;
  }
}

/**
 * Delete environment variable
 */
export async function deleteEnvVar(key: string): Promise<void> {
  try {
    await DeleteEnvVar(key);
  } catch (error) {
    console.error('Failed to delete env var:', error);
    throw error;
  }
}

/**
 * Get environment variables as JSON
 */
export async function getEnvVarsJSON(): Promise<string> {
  try {
    return await GetEnvVarsJSON();
  } catch (error) {
    console.error('Failed to get env vars JSON:', error);
    throw error;
  }
}

/**
 * Save environment variables from JSON
 */
export async function saveEnvVarsJSON(jsonStr: string): Promise<void> {
  try {
    await SaveEnvVarsJSON(jsonStr);
  } catch (error) {
    console.error('Failed to save env vars JSON:', error);
    throw error;
  }
}

/**
 * Import environment variables
 */
export async function importEnvVars(jsonStr: string): Promise<void> {
  try {
    await ImportEnvVars(jsonStr);
  } catch (error) {
    console.error('Failed to import env vars:', error);
    throw error;
  }
}

/**
 * Export environment variables
 */
export async function exportEnvVars(): Promise<string> {
  try {
    return await ExportEnvVars();
  } catch (error) {
    console.error('Failed to export env vars:', error);
    throw error;
  }
}

/**
 * Export environment variables to file
 */
export async function exportEnvVarsToFile(): Promise<void> {
  try {
    await ExportEnvVarsToFile();
  } catch (error) {
    console.error('Failed to export env vars to file:', error);
    throw error;
  }
}

/**
 * Import environment variables from file
 */
export async function importEnvVarsFromFile(): Promise<void> {
  try {
    await ImportEnvVarsFromFile();
  } catch (error) {
    console.error('Failed to import env vars from file:', error);
    throw error;
  }
}

/**
 * Get global sync status
 */
export async function getEnvVarsGlobalSyncStatus(): Promise<GlobalSyncStatus> {
  try {
    return await GetEnvVarsGlobalSyncStatus();
  } catch (error) {
    console.error('Failed to get global sync status:', error);
    throw error;
  }
}

/**
 * Set global sync enabled
 */
export async function setEnvVarsGlobalSyncEnabled(enabled: boolean): Promise<GlobalSyncStatus> {
  try {
    return await SetEnvVarsGlobalSyncEnabled(enabled);
  } catch (error) {
    console.error('Failed to set global sync enabled:', error);
    throw error;
  }
}
