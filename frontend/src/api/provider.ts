/**
 * Provider API
 * Encapsulates provider and preset operations
 * Wraps main/App methods and config.ConfigService for CRUD
 */

import {
  GetProvidersByType,
  GetProviderExportJSON,
  SaveProviderFromJSON,
  UpdateProvider,
  GetUrlHistory,
  AddUrlToHistory,
  RemoveUrlFromHistory,
  GetTerminalPresets,
  SaveTerminalPreset,
  DeleteTerminalPreset,
  GetMergedTerminalPresets,
  ResolveTerminalPreset,
  GetOpenCodeConfig,
  GetOpenCodeConfigPath,
  SaveOpenCodeConfig,
  GetConfigService,
} from '../../wailsjs/go/main/App';

import { config } from '../../wailsjs/go/models';

// Type aliases
type Provider = config.Provider;
type TerminalPreset = config.TerminalPreset;
type MergedTerminalPreset = config.MergedTerminalPreset;

let configService: any = null;

/**
 * Initialize config service
 */
async function getService() {
  if (!configService) {
    configService = await GetConfigService();
  }
  return configService;
}

/**
 * Get providers by type
 */
export async function getProvidersByType(providerType: string): Promise<Record<string, Provider>> {
  try {
    return await GetProvidersByType(providerType);
  } catch (error) {
    console.error('[api.provider.getProvidersByType]', error);
    throw error;
  }
}

/**
 * Get provider export as JSON
 */
export async function getProviderExportJSON(providerName: string): Promise<string> {
  try {
    return await GetProviderExportJSON(providerName);
  } catch (error) {
    console.error('[api.provider.getProviderExportJSON]', error);
    throw error;
  }
}

/**
 * Save provider from JSON
 */
export async function saveProviderFromJSON(providerName: string, jsonStr: string): Promise<void> {
  try {
    await SaveProviderFromJSON(providerName, jsonStr);
  } catch (error) {
    console.error('[api.provider.saveProviderFromJSON]', error);
    throw error;
  }
}

/**
 * Update provider（统一编辑入口：改名 + 属性 + 密钥）
 *
 * 后端 App.UpdateProvider 行为（详见设计文档第四节 + 鲁班实现报告）：
 * - oldName == newName：仅更新属性，复用 SaveProviderFromJSON 路径（零副作用）。
 * - oldName != newName（改名）：config 内 Models key + 三 map TerminalPresets stable key +
 *   Provider 字段 + OpenCodePresets bindings 同步迁移；secrets 密钥迁移；新属性覆盖。
 *
 * providerJSON 为完整的 ExportProvider JSON 字符串。约定：
 * - api_key 为空字符串（或省略）= 保持当前密钥不变（后端走"迁移旧密钥"分支）；
 *   填入新值 = 更新密钥。
 * - presets 字段应从 getProviderExportJSON 返回原样保留，避免覆盖清空 legacy presets。
 */
export async function updateProvider(
  oldName: string,
  newName: string,
  providerJSON: string
): Promise<void> {
  try {
    await UpdateProvider(oldName, newName, providerJSON);
  } catch (error) {
    console.error('[api.provider.updateProvider]', error);
    throw error;
  }
}

/**
 * Get URL history for a provider
 */
export async function getUrlHistory(providerID: string): Promise<string[]> {
  try {
    return await GetUrlHistory(providerID);
  } catch (error) {
    console.error('[api.provider.getUrlHistory]', error);
    throw error;
  }
}

/**
 * Add URL to history
 */
export async function addUrlToHistory(providerID: string, url: string): Promise<void> {
  try {
    await AddUrlToHistory(providerID, url);
  } catch (error) {
    console.error('[api.provider.addUrlToHistory]', error);
    throw error;
  }
}

/**
 * Remove URL from history
 */
export async function removeUrlFromHistory(providerID: string, url: string): Promise<void> {
  try {
    await RemoveUrlFromHistory(providerID, url);
  } catch (error) {
    console.error('[api.provider.removeUrlFromHistory]', error);
    throw error;
  }
}

/**
 * Get terminal presets
 */
export async function getTerminalPresets(terminalType: string): Promise<Record<string, TerminalPreset>> {
  try {
    return await GetTerminalPresets(terminalType);
  } catch (error) {
    console.error('[api.provider.getTerminalPresets]', error);
    throw error;
  }
}

/**
 * Save terminal preset
 */
export async function saveTerminalPreset(
  terminalType: string,
  presetName: string,
  preset: TerminalPreset
): Promise<void> {
  try {
    await SaveTerminalPreset(terminalType, presetName, preset);
  } catch (error) {
    console.error('[api.provider.saveTerminalPreset]', error);
    throw error;
  }
}

/**
 * Delete terminal preset
 */
export async function deleteTerminalPreset(terminalType: string, presetName: string): Promise<void> {
  try {
    await DeleteTerminalPreset(terminalType, presetName);
  } catch (error) {
    console.error('[api.provider.deleteTerminalPreset]', error);
    throw error;
  }
}

/**
 * Get merged terminal presets
 */
export async function getMergedTerminalPresets(terminalType: string): Promise<MergedTerminalPreset[]> {
  try {
    return await GetMergedTerminalPresets(terminalType);
  } catch (error) {
    console.error('[api.provider.getMergedTerminalPresets]', error);
    throw error;
  }
}

/**
 * Get OpenCode global config.json content
 */
export async function getOpenCodeConfig(): Promise<string> {
  try {
    return await GetOpenCodeConfig();
  } catch (error) {
    console.error('[api.provider.getOpenCodeConfig]', error);
    throw error;
  }
}

/**
 * Get OpenCode global config.json file path
 */
export async function getOpenCodeConfigPath(): Promise<string> {
  try {
    return await GetOpenCodeConfigPath();
  } catch (error) {
    console.error('[api.provider.getOpenCodeConfigPath]', error);
    throw error;
  }
}

/**
 * Save OpenCode global config.json content
 */
export async function saveOpenCodeConfig(content: string): Promise<void> {
  try {
    await SaveOpenCodeConfig(content);
  } catch (error) {
    console.error('[api.provider.saveOpenCodeConfig]', error);
    throw error;
  }
}

/**
 * Resolve terminal preset
 */
export async function resolveTerminalPreset(
  terminalType: string,
  key: string
): Promise<{
  providerName: string;
  model: string;
  openCodeCfgJSON: string;
  found: boolean;
}> {
  try {
    const jsonStr = await ResolveTerminalPreset(terminalType, key);
    const parsed = JSON.parse(jsonStr);
    return parsed;
  } catch (error) {
    console.error('[api.provider.resolveTerminalPreset]', error);
    throw error;
  }
}

/**
 * Get provider (via ConfigService)
 */
export async function getProvider(id: string): Promise<Provider> {
  try {
    const service = await getService();
    return await service.GetProvider(id);
  } catch (error) {
    console.error('[api.provider.getProvider]', error);
    throw error;
  }
}

/**
 * Save provider (via ConfigService)
 */
export async function saveProvider(id: string, provider: Provider): Promise<void> {
  try {
    const service = await getService();
    await service.SaveProvider(id, provider);
  } catch (error) {
    console.error('[api.provider.saveProvider]', error);
    throw error;
  }
}

/**
 * Delete provider (via ConfigService)
 */
export async function deleteProvider(id: string): Promise<void> {
  try {
    const service = await getService();
    await service.DeleteProvider(id);
  } catch (error) {
    console.error('[api.provider.deleteProvider]', error);
    throw error;
  }
}

/**
 * Get preset (via ConfigService)
 */
export async function getPreset(terminalType: string, presetName: string): Promise<any> {
  try {
    const service = await getService();
    return await service.GetPreset(terminalType, presetName);
  } catch (error) {
    console.error('[api.provider.getPreset]', error);
    throw error;
  }
}

/**
 * Save preset (via ConfigService)
 */
export async function savePreset(terminalType: string, presetName: string, preset: any): Promise<void> {
  try {
    const service = await getService();
    await service.SavePreset(terminalType, presetName, preset);
  } catch (error) {
    console.error('[api.provider.savePreset]', error);
    throw error;
  }
}

/**
 * Delete preset (via ConfigService)
 */
export async function deletePreset(terminalType: string, presetName: string): Promise<void> {
  try {
    const service = await getService();
    await service.DeletePreset(terminalType, presetName);
  } catch (error) {
    console.error('[api.provider.deletePreset]', error);
    throw error;
  }
}
