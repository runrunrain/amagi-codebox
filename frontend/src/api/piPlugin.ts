/**
 * Pi Package API
 * Wraps the dedicated backend PiPlugins service (internal/piplugin).
 * Install/remove/update delegate to the official pi CLI, which manages the
 * packages[] array in ~/.pi/agent/settings.json and the on-disk package
 * entities under ~/.pi/agent/npm/ and ~/.pi/agent/git/.
 */

import {
  GetPackageDetails,
  InstallPackage,
  RefreshPackages,
  RemovePackage,
  UpdatePackage,
} from '../../wailsjs/go/piplugin/Service';

export interface PiPackage {
  id: string;
  source: string;
  sourceType: 'npm' | 'git' | 'local';
  name: string;
  version?: string;
  description?: string;
  author?: string;
  repository?: string;
  scope: 'user';
  enabled: boolean;
  installPath?: string;
  manifestPath?: string;
  lastUpdated?: string;
  pinned?: boolean;
  extensions?: string[];
  skills?: string[];
  prompts?: string[];
  themes?: string[];
}

export interface PiResourceInfo {
  name: string;
  filePath: string;
  type: 'extension' | 'skill' | 'prompt' | 'theme';
}

export interface PiPackageDetail extends PiPackage {
  resources: PiResourceInfo[];
  manifestDeclared: boolean;
}

export interface PiPackagesData {
  installed: PiPackage[];
  warnings?: string[];
}

export interface PiCommandResult {
  success: boolean;
  output: string;
  error?: string;
}

export async function refreshPiPackages(): Promise<PiPackagesData> {
  return await RefreshPackages() as PiPackagesData;
}

export async function getPiPackageDetails(source: string): Promise<PiPackageDetail> {
  return await GetPackageDetails(source) as PiPackageDetail;
}

export async function installPiPackage(source: string): Promise<PiCommandResult> {
  return await InstallPackage(source) as PiCommandResult;
}

export async function updatePiPackage(source: string): Promise<PiCommandResult> {
  return await UpdatePackage(source) as PiCommandResult;
}

export async function removePiPackage(source: string): Promise<PiCommandResult> {
  return await RemovePackage(source) as PiCommandResult;
}
