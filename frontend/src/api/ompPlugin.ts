/**
 * OMP Plugin API
 * Wraps the dedicated backend ompplugin service (internal/ompplugin).
 * Install/uninstall/enable/disable/upgrade delegate to the official omp CLI,
 * which manages plugins under ~/.omp/plugins (npm packages, GitHub sources,
 * marketplace references and local paths).
 */

import {
  InstallPlugin,
  ListPlugins,
  RefreshPlugins,
  SetPluginEnabled,
  UninstallPlugin,
  UpgradePlugin,
} from '../../wailsjs/go/ompplugin/Service';

export interface OmpPlugin {
  id: string;
  name: string;
  version?: string;
  kind: 'npm' | 'marketplace';
  enabled: boolean;
  enabledFeatures?: string[];
  description?: string;
  scope?: string;
  installPath?: string;
}

export interface OmpPluginsData {
  installed: OmpPlugin[];
  warnings?: string[];
}

export interface OmpCommandResult {
  success: boolean;
  output: string;
  error?: string;
}

/** 包装 wails 调用：失败时抛出带操作上下文的中文错误（透传后端消息，如 omp 未安装提示） */
async function callOmp<T>(context: string, fn: () => Promise<T>): Promise<T> {
  try {
    return await fn();
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    throw new Error(`${context}：${message}`, { cause: err });
  }
}

export async function listOmpPlugins(): Promise<OmpPlugin[]> {
  return await callOmp('获取 OMP 插件列表失败', () => ListPlugins()) as OmpPlugin[];
}

export async function refreshOmpPlugins(): Promise<OmpPluginsData> {
  return await callOmp('获取 OMP 插件列表失败', () => RefreshPlugins()) as OmpPluginsData;
}

export async function installOmpPlugin(spec: string): Promise<OmpCommandResult> {
  return await callOmp('安装 OMP 插件失败', () => InstallPlugin(spec)) as OmpCommandResult;
}

export async function uninstallOmpPlugin(id: string): Promise<OmpCommandResult> {
  return await callOmp('卸载 OMP 插件失败', () => UninstallPlugin(id)) as OmpCommandResult;
}

export async function setOmpPluginEnabled(id: string, enabled: boolean): Promise<OmpCommandResult> {
  return await callOmp(enabled ? '启用 OMP 插件失败' : '禁用 OMP 插件失败', () => SetPluginEnabled(id, enabled)) as OmpCommandResult;
}

export async function upgradeOmpPlugin(id: string): Promise<OmpCommandResult> {
  return await callOmp('升级 OMP 插件失败', () => UpgradePlugin(id)) as OmpCommandResult;
}
