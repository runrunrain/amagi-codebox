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
export async function listCodexMarketplaces(): Promise<CodexMarketplaceType[]> {
  try {
    return await ListMarketplaces();
  } catch (error) {
    console.error('[api.codexPlugin.listCodexMarketplaces]', error);
    throw error;
  }
}

/**
 * Add marketplace
 */
export async function addCodexMarketplace(req: AddMarketplaceRequestType): Promise<CommandResultType> {
  try {
    return await AddMarketplace(req);
  } catch (error) {
    console.error('[api.codexPlugin.addCodexMarketplace]', error);
    throw error;
  }
}

/**
 * Upgrade marketplace
 */
export async function upgradeCodexMarketplace(name: string): Promise<CommandResultType> {
  try {
    return await UpgradeMarketplace(name);
  } catch (error) {
    console.error('[api.codexPlugin.upgradeCodexMarketplace]', error);
    throw error;
  }
}

/**
 * Remove marketplace
 */
export async function removeCodexMarketplace(name: string): Promise<CommandResultType> {
  try {
    return await RemoveMarketplace(name);
  } catch (error) {
    console.error('[api.codexPlugin.removeCodexMarketplace]', error);
    throw error;
  }
}

/**
 * List plugins in marketplace
 */
export async function listCodexPlugins(marketplace: string): Promise<CodexPluginType[]> {
  try {
    return await ListPlugins(marketplace);
  } catch (error) {
    console.error('[api.codexPlugin.listCodexPlugins]', error);
    throw error;
  }
}

/**
 * Install plugin
 */
export async function installCodexPlugin(selector: PluginSelectorType): Promise<CommandResultType> {
  try {
    return await InstallPlugin(selector);
  } catch (error) {
    console.error('[api.codexPlugin.installCodexPlugin]', error);
    throw error;
  }
}

/**
 * Uninstall plugin
 */
export async function uninstallCodexPlugin(selector: PluginSelectorType): Promise<CommandResultType> {
  try {
    return await UninstallPlugin(selector);
  } catch (error) {
    console.error('[api.codexPlugin.uninstallCodexPlugin]', error);
    throw error;
  }
}

/**
 * Set plugin enabled
 */
export async function setCodexPluginEnabled(selector: PluginSelectorType, enabled: boolean): Promise<CommandResultType> {
  try {
    return await SetPluginEnabled(selector, enabled);
  } catch (error) {
    console.error('[api.codexPlugin.setCodexPluginEnabled]', error);
    throw error;
  }
}

/**
 * Get plugin details
 */
export async function getCodexPluginDetails(selector: PluginSelectorType): Promise<CodexPluginDetailType> {
  try {
    return await GetPluginDetails(selector);
  } catch (error) {
    console.error('[api.codexPlugin.getCodexPluginDetails]', error);
    throw error;
  }
}

/**
 * List available plugins
 */
export async function listAvailableCodexPlugins(): Promise<CodexAvailablePluginType[]> {
  try {
    return await ListAvailablePlugins();
  } catch (error) {
    console.error('[api.codexPlugin.listAvailableCodexPlugins]', error);
    throw error;
  }
}

/**
 * Refresh plugins
 */
export async function refreshCodexPlugins(): Promise<CodexPluginsDataType> {
  try {
    return await RefreshPlugins();
  } catch (error) {
    console.error('[api.codexPlugin.refreshCodexPlugins]', error);
    throw error;
  }
}
