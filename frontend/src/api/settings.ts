/**
 * Settings API
 * Encapsulates application settings operations
 * Directly wraps wailsjs/go/settings/Service
 */

import {
  GetDashboardDefaults,
  SetDashboardDefaults,
  GetShellPaths,
  AddShellPath,
  RemoveShellPath,
  GetTerminalSettings,
  SetTerminalSettings,
  GetRemoteHost,
  SetRemoteHost,
  GetRemotePort,
  SetRemotePort,
  GetGitHubToken,
  SetGitHubToken,
  GetMobileWebRoot,
  SetMobileWebRoot,
  GetSettings,
  Load,
  Save,
} from '../../wailsjs/go/settings/Service';

import { settings } from '../../wailsjs/go/models';

// Type aliases
type DashboardDefaults = settings.DashboardDefaults;
type ShellEntry = settings.ShellEntry;
type TerminalSettings = settings.TerminalSettings;
type AppSettings = settings.AppSettings;

/**
 * Get dashboard defaults
 */
export async function getDashboardDefaults(): Promise<DashboardDefaults> {
  try {
    return await GetDashboardDefaults();
  } catch (error) {
    console.error('[api.settings.getDashboardDefaults]', error);
    throw error;
  }
}

/**
 * Set dashboard defaults
 */
export async function setDashboardDefaults(defaults: DashboardDefaults): Promise<void> {
  try {
    await SetDashboardDefaults(defaults);
  } catch (error) {
    console.error('[api.settings.setDashboardDefaults]', error);
    throw error;
  }
}

/**
 * Get shell paths
 */
export async function getShellPaths(): Promise<ShellEntry[]> {
  try {
    return await GetShellPaths();
  } catch (error) {
    console.error('[api.settings.getShellPaths]', error);
    throw error;
  }
}

/**
 * Add shell path
 */
export async function addShellPath(entry: ShellEntry): Promise<void> {
  try {
    await AddShellPath(entry);
  } catch (error) {
    console.error('[api.settings.addShellPath]', error);
    throw error;
  }
}

/**
 * Remove shell path
 */
export async function removeShellPath(path: string): Promise<void> {
  try {
    await RemoveShellPath(path);
  } catch (error) {
    console.error('[api.settings.removeShellPath]', error);
    throw error;
  }
}

/**
 * Get terminal settings
 */
export async function getTerminalSettings(): Promise<TerminalSettings> {
  try {
    return await GetTerminalSettings();
  } catch (error) {
    console.error('[api.settings.getTerminalSettings]', error);
    throw error;
  }
}

/**
 * Set terminal settings
 */
export async function setTerminalSettings(settings: TerminalSettings): Promise<void> {
  try {
    await SetTerminalSettings(settings);
  } catch (error) {
    console.error('[api.settings.setTerminalSettings]', error);
    throw error;
  }
}

/**
 * Get remote host
 */
export async function getRemoteHost(): Promise<string> {
  try {
    return await GetRemoteHost();
  } catch (error) {
    console.error('[api.settings.getRemoteHost]', error);
    throw error;
  }
}

/**
 * Set remote host
 */
export async function setRemoteHost(host: string): Promise<void> {
  try {
    await SetRemoteHost(host);
  } catch (error) {
    console.error('[api.settings.setRemoteHost]', error);
    throw error;
  }
}

/**
 * Get remote port
 */
export async function getRemotePort(): Promise<number> {
  try {
    return await GetRemotePort();
  } catch (error) {
    console.error('[api.settings.getRemotePort]', error);
    throw error;
  }
}

/**
 * Set remote port
 */
export async function setRemotePort(port: number): Promise<void> {
  try {
    await SetRemotePort(port);
  } catch (error) {
    console.error('[api.settings.setRemotePort]', error);
    throw error;
  }
}

/**
 * Get GitHub token
 */
export async function getGitHubToken(): Promise<string> {
  try {
    return await GetGitHubToken();
  } catch (error) {
    console.error('[api.settings.getGitHubToken]', error);
    throw error;
  }
}

/**
 * Set GitHub token
 */
export async function setGitHubToken(token: string): Promise<void> {
  try {
    await SetGitHubToken(token);
  } catch (error) {
    console.error('[api.settings.setGitHubToken]', error);
    throw error;
  }
}

/**
 * Get mobile web root
 */
export async function getMobileWebRoot(): Promise<string> {
  try {
    return await GetMobileWebRoot();
  } catch (error) {
    console.error('[api.settings.getMobileWebRoot]', error);
    throw error;
  }
}

/**
 * Set mobile web root
 */
export async function setMobileWebRoot(path: string): Promise<void> {
  try {
    await SetMobileWebRoot(path);
  } catch (error) {
    console.error('[api.settings.setMobileWebRoot]', error);
    throw error;
  }
}

/**
 * Get all settings
 */
export async function getAllSettings(): Promise<AppSettings> {
  try {
    return await GetSettings();
  } catch (error) {
    console.error('[api.settings.getAllSettings]', error);
    throw error;
  }
}

/**
 * Load settings from file
 */
export async function loadSettings(): Promise<void> {
  try {
    await Load();
  } catch (error) {
    console.error('[api.settings.loadSettings]', error);
    throw error;
  }
}

/**
 * Save settings to file
 */
export async function saveSettings(): Promise<void> {
  try {
    await Save();
  } catch (error) {
    console.error('[api.settings.saveSettings]', error);
    throw error;
  }
}
