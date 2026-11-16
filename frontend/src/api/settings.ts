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
  GetCommitSummaryPreset,
  SetCommitSummaryPreset,
  GetWebPlaneSkin,
  SetWebPlaneSkin,
  GetClaudeWebUIEnabled,
  SetClaudeWebUIEnabled
} from '../../wailsjs/go/settings/Service';

import { settings } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// Type aliases
type DashboardDefaults = settings.DashboardDefaults;
type ShellEntry = settings.ShellEntry;
type TerminalSettings = settings.TerminalSettings;
/** 会话 Web 平面配色方向（设置→外观）：dark（缺省，深底浅字）/ light（浅底深字）。 */
export type WebPlaneSkin = 'dark' | 'light';

/** 归一化：后端对空/未知值 fail-safe 回落 dark，前端同规则（双端一致）。 */
function normalizeWebPlaneSkin(v: string): WebPlaneSkin {
  return v === 'light' ? 'light' : 'dark';
}

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

/**
 * Get 会话 Web 平面配色方向（后端已归一为 dark/light）
 */
export function getWebPlaneSkin(): Promise<WebPlaneSkin> {
  return callApi('[api.settings.getWebPlaneSkin]', async () =>
    normalizeWebPlaneSkin(await GetWebPlaneSkin()));
}

/**
 * Set 会话 Web 平面配色方向（后端对未知值 fail-safe 归一 dark）
 */
export function setWebPlaneSkin(value: WebPlaneSkin): Promise<void> {
  return callApi('[api.settings.setWebPlaneSkin]', () => SetWebPlaneSkin(value));
}

/**
 * Get Claude Code Web 视图开关（后端三态：未设置 = 默认开启）
 */
export function getClaudeWebUIEnabled(): Promise<boolean> {
  return callApi('[api.settings.getClaudeWebUIEnabled]', () => GetClaudeWebUIEnabled());
}

/**
 * Set Claude Code Web 视图开关（只在会话启动时读取，运行中切换不影响已启动会话）
 */
export function setClaudeWebUIEnabled(enabled: boolean): Promise<void> {
  return callApi('[api.settings.setClaudeWebUIEnabled]', () => SetClaudeWebUIEnabled(enabled));
}
