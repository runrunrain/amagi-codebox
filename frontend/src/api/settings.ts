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
  GetCommitSummaryPreset,
  SetCommitSummaryPreset,
  GetSettings,
  Load,
  Save,
} from '../../wailsjs/go/settings/Service';

import { settings } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// Type aliases
type DashboardDefaults = settings.DashboardDefaults;
type ShellEntry = settings.ShellEntry;
type TerminalSettings = settings.TerminalSettings;
type AppSettings = settings.AppSettings;

/**
 * Get dashboard defaults
 */
export function getDashboardDefaults(): Promise<DashboardDefaults> {
  return callApi('[api.settings.getDashboardDefaults]', () => GetDashboardDefaults());
}

/**
 * Set dashboard defaults
 */
export function setDashboardDefaults(defaults: DashboardDefaults): Promise<void> {
  return callApi('[api.settings.setDashboardDefaults]', () => SetDashboardDefaults(defaults));
}

/**
 * Get shell paths
 */
export function getShellPaths(): Promise<ShellEntry[]> {
  return callApi('[api.settings.getShellPaths]', () => GetShellPaths());
}

/**
 * Add shell path
 */
export function addShellPath(entry: ShellEntry): Promise<void> {
  return callApi('[api.settings.addShellPath]', () => AddShellPath(entry));
}

/**
 * Remove shell path
 */
export function removeShellPath(path: string): Promise<void> {
  return callApi('[api.settings.removeShellPath]', () => RemoveShellPath(path));
}

/**
 * Get terminal settings
 */
export function getTerminalSettings(): Promise<TerminalSettings> {
  return callApi('[api.settings.getTerminalSettings]', () => GetTerminalSettings());
}

/**
 * Set terminal settings
 */
export function setTerminalSettings(settings: TerminalSettings): Promise<void> {
  return callApi('[api.settings.setTerminalSettings]', () => SetTerminalSettings(settings));
}

/**
 * Get remote host
 */
export function getRemoteHost(): Promise<string> {
  return callApi('[api.settings.getRemoteHost]', () => GetRemoteHost());
}

/**
 * Set remote host
 */
export function setRemoteHost(host: string): Promise<void> {
  return callApi('[api.settings.setRemoteHost]', () => SetRemoteHost(host));
}

/**
 * Get remote port
 */
export function getRemotePort(): Promise<number> {
  return callApi('[api.settings.getRemotePort]', () => GetRemotePort());
}

/**
 * Set remote port
 */
export function setRemotePort(port: number): Promise<void> {
  return callApi('[api.settings.setRemotePort]', () => SetRemotePort(port));
}

/**
 * Get GitHub token
 */
export function getGitHubToken(): Promise<string> {
  return callApi('[api.settings.getGitHubToken]', () => GetGitHubToken());
}

/**
 * Set GitHub token
 */
export function setGitHubToken(token: string): Promise<void> {
  return callApi('[api.settings.setGitHubToken]', () => SetGitHubToken(token));
}

/**
 * Get mobile web root
 */
export function getMobileWebRoot(): Promise<string> {
  return callApi('[api.settings.getMobileWebRoot]', () => GetMobileWebRoot());
}

/**
 * Set mobile web root
 */
export function setMobileWebRoot(path: string): Promise<void> {
  return callApi('[api.settings.setMobileWebRoot]', () => SetMobileWebRoot(path));
}

/**
 * Get all settings
 */
export function getAllSettings(): Promise<AppSettings> {
  return callApi('[api.settings.getAllSettings]', () => GetSettings());
}

/**
 * Load settings from file
 */
export function loadSettings(): Promise<void> {
  return callApi('[api.settings.loadSettings]', () => Load());
}

/**
 * Save settings to file
 */
export function saveSettings(): Promise<void> {
  return callApi('[api.settings.saveSettings]', () => Save());
}

/**
 * Get commit summary preset（格式 "provider/preset名"，空=未设置）
 */
export function getCommitSummaryPreset(): Promise<string> {
  return callApi('[api.settings.getCommitSummaryPreset]', () => GetCommitSummaryPreset());
}

/**
 * Set commit summary preset（空字符串 = 未设置，禁用 AI 生成提交信息）
 */
export function setCommitSummaryPreset(value: string): Promise<void> {
  return callApi('[api.settings.setCommitSummaryPreset]', () => SetCommitSummaryPreset(value));
}
