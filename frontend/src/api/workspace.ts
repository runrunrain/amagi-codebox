/**
 * Workspace API
 * Encapsulates workspace operations
 * Directly wraps wailsjs/go/workspace/Service
 */

import {
  ListWorkspaces,
  GetWorkspace,
  CreateWorkspace,
  UpdateWorkspace,
  DeleteWorkspace,
  SetWorkspacePlugins,
  GetAvailablePluginsForWorkspace,
  GetDeploymentManifest,
  CleanWorkspace,
  BuildScaffold,
  SyncWorkspace,
  GetGlobalEnabled,
  SetGlobalEnabled,
  Load,
  Save,
} from '../../wailsjs/go/workspace/Service';

import { workspace } from '../../wailsjs/go/models';

// Type aliases
type Workspace = workspace.Workspace;
type WorkspacePlugin = workspace.WorkspacePlugin;
type AvailablePlugin = workspace.AvailablePlugin;
type GlobalEnabled = workspace.GlobalEnabled;
type DeploymentManifest = workspace.DeploymentManifest;
type CleanResult = workspace.CleanResult;
type DeployResult = workspace.DeployResult;
// wailsjs CreateWorkspace/UpdateWorkspace declare workspace.ToolType but the
// generated models.ts does not emit that class; fall back to string.
type ToolType = string;

/**
 * List workspaces
 */
export async function listWorkspaces(): Promise<Workspace[]> {
  try {
    return await ListWorkspaces();
  } catch (error) {
    console.error('[api.workspace.listWorkspaces]', error);
    throw error;
  }
}

/**
 * Get workspace
 */
export async function getWorkspace(id: string): Promise<Workspace> {
  try {
    return await GetWorkspace(id);
  } catch (error) {
    console.error('[api.workspace.getWorkspace]', error);
    throw error;
  }
}

/**
 * Create workspace
 */
export async function createWorkspace(params: {
  name: string;
  path: string;
  tools?: ToolType[];
}): Promise<Workspace> {
  try {
    return await CreateWorkspace(params.name, params.path, params.tools || []);
  } catch (error) {
    console.error('[api.workspace.createWorkspace]', error);
    throw error;
  }
}

/**
 * Update workspace
 */
export async function updateWorkspace(params: {
  id: string;
  name?: string;
  path?: string;
  tools?: ToolType[];
}): Promise<Workspace> {
  try {
    return await UpdateWorkspace(params.id, params.name || '', params.path || '', params.tools || []);
  } catch (error) {
    console.error('[api.workspace.updateWorkspace]', error);
    throw error;
  }
}

/**
 * Delete workspace
 */
export async function deleteWorkspace(id: string): Promise<void> {
  try {
    await DeleteWorkspace(id);
  } catch (error) {
    console.error('[api.workspace.deleteWorkspace]', error);
    throw error;
  }
}

/**
 * Set workspace plugins
 */
export async function setWorkspacePlugins(
  workspaceID: string,
  items: WorkspacePlugin[]
): Promise<void> {
  try {
    await SetWorkspacePlugins(workspaceID, items);
  } catch (error) {
    console.error('[api.workspace.setWorkspacePlugins]', error);
    throw error;
  }
}

/**
 * Get available plugins for workspace
 */
export async function getAvailablePluginsForWorkspace(
  workspaceID: string
): Promise<AvailablePlugin[]> {
  try {
    return await GetAvailablePluginsForWorkspace(workspaceID);
  } catch (error) {
    console.error('[api.workspace.getAvailablePluginsForWorkspace]', error);
    throw error;
  }
}

/**
 * Get deployment manifest
 */
export async function getDeploymentManifest(workspaceID: string): Promise<DeploymentManifest> {
  try {
    return await GetDeploymentManifest(workspaceID);
  } catch (error) {
    console.error('[api.workspace.getDeploymentManifest]', error);
    throw error;
  }
}

/**
 * Clean workspace
 */
export async function cleanWorkspace(id: string): Promise<CleanResult> {
  try {
    return await CleanWorkspace(id);
  } catch (error) {
    console.error('[api.workspace.cleanWorkspace]', error);
    throw error;
  }
}

/**
 * Build scaffold
 */
export async function buildScaffold(workspaceID: string): Promise<DeployResult> {
  try {
    return await BuildScaffold(workspaceID);
  } catch (error) {
    console.error('[api.workspace.buildScaffold]', error);
    throw error;
  }
}

/**
 * Sync workspace
 */
export async function syncWorkspace(workspaceID: string): Promise<DeployResult> {
  try {
    return await SyncWorkspace(workspaceID);
  } catch (error) {
    console.error('[api.workspace.syncWorkspace]', error);
    throw error;
  }
}

/**
 * Get global enabled plugins
 */
export async function getGlobalEnabled(): Promise<GlobalEnabled[]> {
  try {
    return await GetGlobalEnabled();
  } catch (error) {
    console.error('[api.workspace.getGlobalEnabled]', error);
    throw error;
  }
}

/**
 * Set global enabled plugins
 */
export async function setGlobalEnabled(items: GlobalEnabled[]): Promise<DeployResult> {
  try {
    return await SetGlobalEnabled(items);
  } catch (error) {
    console.error('[api.workspace.setGlobalEnabled]', error);
    throw error;
  }
}

/**
 * Load workspace data
 */
export async function loadWorkspace(): Promise<void> {
  try {
    await Load();
  } catch (error) {
    console.error('[api.workspace.loadWorkspace]', error);
    throw error;
  }
}

/**
 * Save workspace data
 */
export async function saveWorkspace(): Promise<void> {
  try {
    await Save();
  } catch (error) {
    console.error('[api.workspace.saveWorkspace]', error);
    throw error;
  }
}
