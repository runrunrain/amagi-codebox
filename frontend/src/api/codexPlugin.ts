/**
 * Codex Plugin API
 * Encapsulates Codex plugin operations
 * Directly wraps wailsjs/go/codexplugin/Service
 */

import {
  AddMarketplace,
  UpgradeMarketplace,
  RemoveMarketplace,
  InstallPlugin,
  UninstallPlugin,
  UpdatePlugin,
  SetPluginEnabled,
  GetPluginDetails,
  RefreshPlugins,
} from '../../wailsjs/go/codexplugin/Service';

import { codexplugin } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// Type aliases for convenience
type CodexPluginDetailType = codexplugin.CodexPluginDetail;
type CodexPluginsDataType = codexplugin.CodexPluginsData;
type AddMarketplaceRequestType = codexplugin.AddMarketplaceRequest;
type CommandResultType = codexplugin.CommandResult;
type PluginSelectorType = codexplugin.PluginSelector;

/**
 * Add marketplace
 */
export function addCodexMarketplace(req: AddMarketplaceRequestType): Promise<CommandResultType> {
  return callApi('[api.codexPlugin.addCodexMarketplace]', () => AddMarketplace(req));
}

/**
 * Upgrade marketplace
 */
export function upgradeCodexMarketplace(name: string): Promise<CommandResultType> {
  return callApi('[api.codexPlugin.upgradeCodexMarketplace]', () => UpgradeMarketplace(name));
}

/**
 * Remove marketplace
 */
export function removeCodexMarketplace(name: string): Promise<CommandResultType> {
  return callApi('[api.codexPlugin.removeCodexMarketplace]', () => RemoveMarketplace(name));
}

/**
 * Install plugin
 */
export function installCodexPlugin(selector: PluginSelectorType): Promise<CommandResultType> {
  return callApi('[api.codexPlugin.installCodexPlugin]', () => InstallPlugin(selector));
}

/**
 * Uninstall plugin
 */
export function uninstallCodexPlugin(selector: PluginSelectorType): Promise<CommandResultType> {
  return callApi('[api.codexPlugin.uninstallCodexPlugin]', () => UninstallPlugin(selector));
}

/**
 * Update a single plugin（P4）：市场刷新 + 卸载重装拉取最新版，并恢复原启用状态。
 * 后端实现见 internal/codexplugin service.UpdatePlugin（codex CLI 无原生单插件 update）。
 */
export function updateCodexPlugin(selector: PluginSelectorType): Promise<CommandResultType> {
  return callApi('[api.codexPlugin.updateCodexPlugin]', () => UpdatePlugin(selector));
}

/**
 * Set plugin enabled
 */
export function setCodexPluginEnabled(selector: PluginSelectorType, enabled: boolean): Promise<CommandResultType> {
  return callApi('[api.codexPlugin.setCodexPluginEnabled]', () => SetPluginEnabled(selector, enabled));
}

/**
 * Get plugin details
 */
export function getCodexPluginDetails(selector: PluginSelectorType): Promise<CodexPluginDetailType> {
  return callApi('[api.codexPlugin.getCodexPluginDetails]', () => GetPluginDetails(selector));
}

/**
 * Refresh plugins
 */
export function refreshCodexPlugins(): Promise<CodexPluginsDataType> {
  return callApi('[api.codexPlugin.refreshCodexPlugins]', () => RefreshPlugins());
}
