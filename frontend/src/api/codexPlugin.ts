/**
 * Codex Plugin API
 * Encapsulates Codex plugin operations
 * Directly wraps wailsjs/go/codexplugin/Service
 */

import {
  ListMarketplaces,
  AddMarketplace,
  UpgradeMarketplace,
  RemoveMarketplace,
  ListPlugins,
  InstallPlugin,
  UninstallPlugin,
  SetPluginEnabled,
  GetPluginDetails,
  ListAvailablePlugins,
  RefreshPlugins,
} from '../../wailsjs/go/codexplugin/Service';

import { codexplugin } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// Type aliases for convenience
type CodexMarketplaceType = codexplugin.CodexMarketplace;
type CodexPluginType = codexplugin.CodexPlugin;
type CodexAvailablePluginType = codexplugin.CodexAvailablePlugin;
type CodexPluginDetailType = codexplugin.CodexPluginDetail;
type CodexPluginsDataType = codexplugin.CodexPluginsData;
type AddMarketplaceRequestType = codexplugin.AddMarketplaceRequest;
type CommandResultType = codexplugin.CommandResult;
type PluginSelectorType = codexplugin.PluginSelector;

/**
 * List marketplaces
 */
export function listCodexMarketplaces(): Promise<CodexMarketplaceType[]> {
  return callApi('[api.codexPlugin.listCodexMarketplaces]', () => ListMarketplaces());
}

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
 * List plugins in marketplace
 */
export function listCodexPlugins(marketplace: string): Promise<CodexPluginType[]> {
  return callApi('[api.codexPlugin.listCodexPlugins]', () => ListPlugins(marketplace));
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
 * List available plugins
 */
export function listAvailableCodexPlugins(): Promise<CodexAvailablePluginType[]> {
  return callApi('[api.codexPlugin.listAvailableCodexPlugins]', () => ListAvailablePlugins());
}

/**
 * Refresh plugins
 */
export function refreshCodexPlugins(): Promise<CodexPluginsDataType> {
  return callApi('[api.codexPlugin.refreshCodexPlugins]', () => RefreshPlugins());
}
