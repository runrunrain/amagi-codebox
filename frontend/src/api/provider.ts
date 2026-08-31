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
  DeleteProvider,
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
} from '../../wailsjs/go/main/App';

import {
  GetProvider,
  GetPresets,
  SavePreset,
  DeletePreset,
} from '../../wailsjs/go/config/ConfigService';

import { config } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// Type aliases
type Provider = config.Provider;
type TerminalPreset = config.TerminalPreset;
type MergedTerminalPreset = config.MergedTerminalPreset;

/**
 * Get providers by type
 */
export function getProvidersByType(providerType: string): Promise<Record<string, Provider>> {
  return callApi('[api.provider.getProvidersByType]', () => GetProvidersByType(providerType));
}

/**
 * Get provider export as JSON
 */
export function getProviderExportJSON(providerName: string): Promise<string> {
  return callApi('[api.provider.getProviderExportJSON]', () => GetProviderExportJSON(providerName));
}

/**
 * Save provider from JSON
 */
export function saveProviderFromJSON(providerName: string, jsonStr: string): Promise<void> {
  return callApi('[api.provider.saveProviderFromJSON]', () => SaveProviderFromJSON(providerName, jsonStr));
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
export function updateProvider(
  oldName: string,
  newName: string,
  providerJSON: string
): Promise<void> {
  return callApi('[api.provider.updateProvider]', () => UpdateProvider(oldName, newName, providerJSON));
}

/**
 * Get URL history for a provider
 */
export function getUrlHistory(providerID: string): Promise<string[]> {
  return callApi('[api.provider.getUrlHistory]', () => GetUrlHistory(providerID));
}

/**
 * Add URL to history
 */
export function addUrlToHistory(providerID: string, url: string): Promise<void> {
  return callApi('[api.provider.addUrlToHistory]', () => AddUrlToHistory(providerID, url));
}

/**
 * Remove URL from history
 */
export function removeUrlFromHistory(providerID: string, url: string): Promise<void> {
  return callApi('[api.provider.removeUrlFromHistory]', () => RemoveUrlFromHistory(providerID, url));
}

/**
 * Get terminal presets
 */
export function getTerminalPresets(terminalType: string): Promise<Record<string, TerminalPreset>> {
  return callApi('[api.provider.getTerminalPresets]', () => GetTerminalPresets(terminalType));
}

/**
 * Save terminal preset
 */
export function saveTerminalPreset(
  terminalType: string,
  presetName: string,
  preset: TerminalPreset
): Promise<void> {
  return callApi('[api.provider.saveTerminalPreset]', () => SaveTerminalPreset(terminalType, presetName, preset));
}

/**
 * Delete terminal preset
 */
export function deleteTerminalPreset(terminalType: string, presetName: string): Promise<void> {
  return callApi('[api.provider.deleteTerminalPreset]', () => DeleteTerminalPreset(terminalType, presetName));
}

/**
 * Get merged terminal presets
 */
export function getMergedTerminalPresets(terminalType: string): Promise<MergedTerminalPreset[]> {
  return callApi('[api.provider.getMergedTerminalPresets]', () => GetMergedTerminalPresets(terminalType));
}

/**
 * Get OpenCode global config.json content
 */
export function getOpenCodeConfig(): Promise<string> {
  return callApi('[api.provider.getOpenCodeConfig]', () => GetOpenCodeConfig());
}

/**
 * Get OpenCode global config.json file path
 */
export function getOpenCodeConfigPath(): Promise<string> {
  return callApi('[api.provider.getOpenCodeConfigPath]', () => GetOpenCodeConfigPath());
}

/**
 * Save OpenCode global config.json content
 */
export function saveOpenCodeConfig(content: string): Promise<void> {
  return callApi('[api.provider.saveOpenCodeConfig]', () => SaveOpenCodeConfig(content));
}

/**
 * Resolve terminal preset
 */
export function resolveTerminalPreset(
  terminalType: string,
  key: string
): Promise<{
  providerName: string;
  model: string;
  openCodeCfgJSON: string;
  found: boolean;
}> {
  return callApi('[api.provider.resolveTerminalPreset]', async () => {
    const raw = await ResolveTerminalPreset(terminalType, key);
    if (!raw) {
      return { providerName: '', model: '', openCodeCfgJSON: '', found: false };
    }
    try {
      const parsed = JSON.parse(raw);
      if (typeof parsed === 'object' && parsed !== null) {
        return {
          providerName: parsed.providerName ?? parsed.provider ?? raw,
          model: parsed.model ?? '',
          openCodeCfgJSON: parsed.openCodeCfgJSON ?? '',
          found: parsed.found ?? true,
        };
      }
    } catch {
      // Backend App.ResolveTerminalPreset returns provName directly on Wails v2 multi-value return truncation
    }
    return {
      providerName: raw,
      model: '',
      openCodeCfgJSON: '',
      found: Boolean(raw),
    };
  });
}

/**
 * Get provider (via ConfigService)
 */
export function getProvider(id: string): Promise<Provider> {
  return callApi('[api.provider.getProvider]', () => GetProvider(id));
}

/**
 * Save provider (via ConfigService)
 */
export function saveProvider(id: string, provider: Provider): Promise<void> {
  return callApi('[api.provider.saveProvider]', () => SaveProviderFromJSON(id, JSON.stringify(provider)));
}

/**
 * Delete provider (via ConfigService)
 */
export function deleteProvider(id: string): Promise<void> {
  return callApi('[api.provider.deleteProvider]', () => DeleteProvider(id));
}

/**
 * Get presets for a provider (via ConfigService)
 */
export function getPresets(providerName: string): Promise<Record<string, config.Preset>> {
  return callApi('[api.provider.getPresets]', () => GetPresets(providerName));
}

/**
 * Get preset (via ConfigService)
 */
export function getPreset(providerName: string, presetName: string): Promise<config.Preset | null> {
  return callApi('[api.provider.getPreset]', async () => {
    const presets = await GetPresets(providerName);
    return presets?.[presetName] ?? null;
  });
}

/**
 * Save preset (via ConfigService)
 */
export function savePreset(terminalType: string, presetName: string, preset: config.Preset): Promise<void> {
  return callApi('[api.provider.savePreset]', () => SavePreset(terminalType, presetName, preset));
}

/**
 * Delete preset (via ConfigService)
 */
export function deletePreset(terminalType: string, presetName: string): Promise<void> {
  return callApi('[api.provider.deletePreset]', () => DeletePreset(terminalType, presetName));
}
