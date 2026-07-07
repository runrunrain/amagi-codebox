/**
 * Plugin API (Claude)
 * Encapsulates Claude plugin operations
 * Directly wraps wailsjs/go/plugin/Service
 */

import {
  GetMarketplaces,
  GetInstalledPlugins,
  GetAvailablePlugins,
  GetPluginDetail,
  GetPluginSubItemStates,
  GetPluginSubItems,
  AnalyzePluginType,
  InstallPlugin,
  UninstallPlugin,
  EnablePlugin,
  DisablePlugin,
  UpdatePlugin,
  UpdateMarketplace,
  AddMarketplace,
  RemoveMarketplace,
  SetSubItemEnabled,
  RefreshPlugins,
} from '../../wailsjs/go/plugin/Service';

// SetPluginSubItemEnabled 走 main.App 统一入口（按 pluginId 是否含 '@' 自动分派 Codex/Claude）
// 不导入 plugin.Service 版本（那是 Claude 专用），保证 Codex/Claude 两路径都不回归
import { SetPluginSubItemEnabled as AppSetPluginSubItemEnabled } from '../../wailsjs/go/main/App';

import { plugin } from '../../wailsjs/go/models';

// Type aliases
type Marketplace = plugin.Marketplace;
type InstalledPlugin = plugin.InstalledPlugin;
type PluginDetail = plugin.PluginDetail;
type SubItem = plugin.SubItem;
type PluginSubItemState = plugin.PluginSubItemState;
// wailsjs AnalyzePluginType declares plugin.PluginType but the generated
// models.ts does not emit that class; fall back to string.
type PluginType = string;
type CommandResult = plugin.CommandResult;
type SubItemRef = plugin.SubItemRef;

/**
 * Get marketplaces
 */
export async function getMarketplaces(): Promise<Marketplace[]> {
  try {
    return await GetMarketplaces();
  } catch (error) {
    console.error('[api.plugin.getMarketplaces]', error);
    throw error;
  }
}

/**
 * Get installed plugins
 */
export async function getInstalledPlugins(): Promise<InstalledPlugin[]> {
  try {
    return await GetInstalledPlugins();
  } catch (error) {
    console.error('[api.plugin.getInstalledPlugins]', error);
    throw error;
  }
}

/**
 * Get available plugins
 */
export async function getAvailablePlugins(): Promise<any[]> {
  try {
    return await GetAvailablePlugins();
  } catch (error) {
    console.error('[api.plugin.getAvailablePlugins]', error);
    throw error;
  }
}

/**
 * Get plugin detail
 */
export async function getPluginDetail(pluginId: string): Promise<PluginDetail> {
  try {
    return await GetPluginDetail(pluginId);
  } catch (error) {
    console.error('[api.plugin.getPluginDetail]', error);
    throw error;
  }
}

/**
 * Get plugin sub items
 */
export async function getPluginSubItems(pluginId: string): Promise<SubItem[]> {
  try {
    return await GetPluginSubItems(pluginId);
  } catch (error) {
    console.error('[api.plugin.getPluginSubItems]', error);
    throw error;
  }
}

/**
 * Get plugin sub item states
 */
export async function getPluginSubItemStates(pluginId: string): Promise<PluginSubItemState> {
  try {
    return await GetPluginSubItemStates(pluginId);
  } catch (error) {
    console.error('[api.plugin.getPluginSubItemStates]', error);
    throw error;
  }
}

/**
 * Analyze plugin type
 */
export async function analyzePluginType(pluginId: string): Promise<PluginType> {
  try {
    return await AnalyzePluginType(pluginId);
  } catch (error) {
    console.error('[api.plugin.analyzePluginType]', error);
    throw error;
  }
}

/**
 * Install plugin
 */
export async function installPlugin(pluginName: string): Promise<CommandResult> {
  try {
    return await InstallPlugin(pluginName);
  } catch (error) {
    console.error('[api.plugin.installPlugin]', error);
    throw error;
  }
}

/**
 * Uninstall plugin
 */
export async function uninstallPlugin(pluginId: string): Promise<CommandResult> {
  try {
    return await UninstallPlugin(pluginId);
  } catch (error) {
    console.error('[api.plugin.uninstallPlugin]', error);
    throw error;
  }
}

/**
 * Enable plugin
 */
export async function enablePlugin(pluginId: string): Promise<CommandResult> {
  try {
    return await EnablePlugin(pluginId);
  } catch (error) {
    console.error('[api.plugin.enablePlugin]', error);
    throw error;
  }
}

/**
 * Disable plugin
 */
export async function disablePlugin(pluginId: string): Promise<CommandResult> {
  try {
    return await DisablePlugin(pluginId);
  } catch (error) {
    console.error('[api.plugin.disablePlugin]', error);
    throw error;
  }
}

/**
 * Update plugin
 */
export async function updatePlugin(pluginId: string): Promise<CommandResult> {
  try {
    return await UpdatePlugin(pluginId);
  } catch (error) {
    console.error('[api.plugin.updatePlugin]', error);
    throw error;
  }
}

/**
 * Update marketplace
 */
export async function updateMarketplace(name: string): Promise<CommandResult> {
  try {
    return await UpdateMarketplace(name);
  } catch (error) {
    console.error('[api.plugin.updateMarketplace]', error);
    throw error;
  }
}

/**
 * Add marketplace
 */
export async function addMarketplace(source: string): Promise<CommandResult> {
  try {
    return await AddMarketplace(source);
  } catch (error) {
    console.error('[api.plugin.addMarketplace]', error);
    throw error;
  }
}

/**
 * Remove marketplace
 */
export async function removeMarketplace(name: string): Promise<CommandResult> {
  try {
    return await RemoveMarketplace(name);
  } catch (error) {
    console.error('[api.plugin.removeMarketplace]', error);
    throw error;
  }
}

/**
 * Set sub item enabled
 *
 * 注意：此函数调的是 plugin.Service.SetSubItemEnabled（Claude 专用、对象参数版本）。
 * 仅适用于 Claude 引擎。Codex 引擎或需要 Codex/Claude 双路兼容时，请使用 setPluginSubItemEnabled。
 */
export async function setSubItemEnabled(
  pluginId: string,
  subItemRef: SubItemRef,
  enabled: boolean
): Promise<void> {
  try {
    await SetSubItemEnabled(pluginId, subItemRef, enabled);
  } catch (error) {
    console.error('[api.plugin.setSubItemEnabled]', error);
    throw error;
  }
}

/**
 * Set plugin sub item enabled (统一入口)
 *
 * 调 main.App.SetPluginSubItemEnabled：基于 Claude 已安装插件注册表自动分派到 Codex/Claude 服务。
 * 后端 isClaudePlugin 通过 a.Plugins.GetInstalledPlugins() 查询 pluginId 是否在 Claude 注册表中：
 * - 在 Claude 注册表中 → plugin.Service.SetPluginSubItemEnabled（Claude 引擎，真正落盘到 plugin-subitems.json）
 * - 不在 Claude 注册表 → codexplugin.Service.SetPluginSubItemEnabled（Codex 引擎，当前 no-op）
 * - a.Plugins == nil 或 GetInstalledPlugins 失败 → 保守按 Codex 分派并告警
 *
 * 注意：两引擎 pluginId 都用 `name@marketplace` 格式（都含 '@'），不可用字符启发式区分；
 * 详见 app.go isClaudePlugin 与 internal/plugin/service.go:380 pluginID 构造逻辑。
 *
 * 参数 subItemType 取值为后端 SubItemType 单数：skill / agent / command / hook / mcp。
 * Codex 与 Claude 两条路径都走此入口，避免直调 window.go.main.App 造成状态散落。
 */
export async function setPluginSubItemEnabled(
  pluginId: string,
  subItemType: string,
  subItemId: string,
  enabled: boolean
): Promise<void> {
  try {
    await AppSetPluginSubItemEnabled(pluginId, subItemType, subItemId, enabled);
  } catch (error) {
    console.error('[api.plugin.setPluginSubItemEnabled]', error);
    throw error;
  }
}

/**
 * Refresh plugins
 */
export async function refreshPlugins(): Promise<void> {
  try {
    await RefreshPlugins();
  } catch (error) {
    console.error('[api.plugin.refreshPlugins]', error);
    throw error;
  }
}
