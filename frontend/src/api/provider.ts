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
  DeleteTerminalPreset,
  GetMergedTerminalPresets,
  GetOpenCodeConfig,
  GetOpenCodeConfigPath
} from '../../wailsjs/go/main/App';
import { config } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// Type aliases
type Provider = config.Provider;
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
