/**
 * OpenCode Plugin API
 * Wraps the dedicated backend service. Install/update always use the official
 * Update discovery resolves a moving GitHub/npm reference to an immutable
 * tag or exact version before invoking the official OpenCode CLI.
 */

import {
  GetPluginDetails,
  InstallPlugin,
  RefreshPlugins,
  UninstallPlugin,
  UpdatePlugin,
} from '../../wailsjs/go/opencodeplugin/Service';

export interface OpenCodePlugin {
  id: string;
  spec: string;
  name: string;
  version?: string;
  description?: string;
  author?: string;
  repository?: string;
  source: string;
  scope: string;
  enabled: boolean;
  installPath?: string;
  manifestPath?: string;
  lastUpdated?: string;
  targets: string[];
}

export interface OpenCodeResourceInfo {
  name: string;
  filePath: string;
}

export interface OpenCodePluginDetail extends OpenCodePlugin {
  skills: OpenCodeResourceInfo[];
  agents: OpenCodeResourceInfo[];
  commands: OpenCodeResourceInfo[];
  hooks: OpenCodeResourceInfo[];
  hasMcp: boolean;
}

export interface OpenCodePluginsData {
  installed: OpenCodePlugin[];
  warnings?: string[];
}

export interface OpenCodeCommandResult {
  success: boolean;
  output: string;
  error?: string;
}

export async function refreshOpenCodePlugins(): Promise<OpenCodePluginsData> {
  return await RefreshPlugins() as OpenCodePluginsData;
}

export async function getOpenCodePluginDetails(spec: string): Promise<OpenCodePluginDetail> {
  return await GetPluginDetails(spec) as OpenCodePluginDetail;
}

export async function installOpenCodePlugin(spec: string): Promise<OpenCodeCommandResult> {
  return await InstallPlugin(spec) as OpenCodeCommandResult;
}

export async function updateOpenCodePlugin(spec: string): Promise<OpenCodeCommandResult> {
  return await UpdatePlugin(spec) as OpenCodeCommandResult;
}

export async function uninstallOpenCodePlugin(spec: string): Promise<OpenCodeCommandResult> {
  return await UninstallPlugin(spec) as OpenCodeCommandResult;
}
