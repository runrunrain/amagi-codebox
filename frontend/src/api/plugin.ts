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
import { callApi } from './internal/call';

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
export function getMarketplaces(): Promise<Marketplace[]> {
  return callApi('[api.plugin.getMarketplaces]', () => GetMarketplaces());
}

/**
 * Get installed plugins
 */
export function getInstalledPlugins(): Promise<InstalledPlugin[]> {
  return callApi('[api.plugin.getInstalledPlugins]', () => GetInstalledPlugins());
}

/**
 * Get available plugins
 *
 * 后端 GetAvailablePlugins 返回 []interface{}（claude CLI JSON 归一化为
 * internal/plugin.AvailablePlugin 形状，未导出到 wailsjs models），生成的绑定
 * 类型为 Array<any>；store 层按 PluginDetail[] 消费，此处对齐该事实契约。
 */
export function getAvailablePlugins(): Promise<PluginDetail[]> {
  return callApi('[api.plugin.getAvailablePlugins]', async () => {
    const plugins = await GetAvailablePlugins();
    return plugins as PluginDetail[];
  });
}

/**
 * Get plugin detail
 */
export function getPluginDetail(pluginId: string): Promise<PluginDetail> {
  return callApi('[api.plugin.getPluginDetail]', () => GetPluginDetail(pluginId));
}

/**
 * Get plugin sub items
 */
export function getPluginSubItems(pluginId: string): Promise<SubItem[]> {
  return callApi('[api.plugin.getPluginSubItems]', () => GetPluginSubItems(pluginId));
}

/**
 * Get plugin sub item states
 */
export function getPluginSubItemStates(pluginId: string): Promise<PluginSubItemState> {
  return callApi('[api.plugin.getPluginSubItemStates]', () => GetPluginSubItemStates(pluginId));
}

/**
 * Analyze plugin type
 */
export function analyzePluginType(pluginId: string): Promise<PluginType> {
  return callApi('[api.plugin.analyzePluginType]', () => AnalyzePluginType(pluginId));
}

/**
 * Install plugin
 */
export function installPlugin(pluginName: string): Promise<CommandResult> {
  return callApi('[api.plugin.installPlugin]', () => InstallPlugin(pluginName));
}

/**
 * Uninstall plugin
 */
export function uninstallPlugin(pluginId: string): Promise<CommandResult> {
  return callApi('[api.plugin.uninstallPlugin]', () => UninstallPlugin(pluginId));
}

/**
 * Enable plugin
 */
export function enablePlugin(pluginId: string): Promise<CommandResult> {
  return callApi('[api.plugin.enablePlugin]', () => EnablePlugin(pluginId));
}

/**
 * Disable plugin
 */
export function disablePlugin(pluginId: string): Promise<CommandResult> {
  return callApi('[api.plugin.disablePlugin]', () => DisablePlugin(pluginId));
}

/**
 * Update plugin
 */
export function updatePlugin(pluginId: string): Promise<CommandResult> {
  return callApi('[api.plugin.updatePlugin]', () => UpdatePlugin(pluginId));
}

/**
 * Update marketplace
 */
export function updateMarketplace(name: string): Promise<CommandResult> {
  return callApi('[api.plugin.updateMarketplace]', () => UpdateMarketplace(name));
}

/**
 * Add marketplace
 */
export function addMarketplace(source: string): Promise<CommandResult> {
  return callApi('[api.plugin.addMarketplace]', () => AddMarketplace(source));
}

/**
 * Remove marketplace
 */
export function removeMarketplace(name: string): Promise<CommandResult> {
  return callApi('[api.plugin.removeMarketplace]', () => RemoveMarketplace(name));
}

/**
 * Set sub item enabled
 *
 * 注意：此函数调的是 plugin.Service.SetSubItemEnabled（Claude 专用、对象参数版本）。
 * 仅适用于 Claude 引擎。Codex 引擎或需要 Codex/Claude 双路兼容时，请使用 setPluginSubItemEnabled。
 */
export function setSubItemEnabled(
  pluginId: string,
  subItemRef: SubItemRef,
  enabled: boolean
): Promise<void> {
  return callApi('[api.plugin.setSubItemEnabled]', () => SetSubItemEnabled(pluginId, subItemRef, enabled));
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
export function setPluginSubItemEnabled(
  pluginId: string,
  subItemType: string,
  subItemId: string,
  enabled: boolean
): Promise<void> {
  return callApi('[api.plugin.setPluginSubItemEnabled]', () => AppSetPluginSubItemEnabled(pluginId, subItemType, subItemId, enabled));
}

/**
 * Refresh plugins
 */
export function refreshPlugins(): Promise<void> {
  return callApi('[api.plugin.refreshPlugins]', () => RefreshPlugins());
}
