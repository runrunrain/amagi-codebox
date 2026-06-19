/**
 * Workspace Store
 * Manages workspaces and deployment operations
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import * as workspaceApi from '../api/workspace';
import { workspace } from '../../wailsjs/go/models';

// Types from wailsjs
type Workspace = workspace.Workspace;
type WorkspacePlugin = workspace.WorkspacePlugin;
type DeployResult = workspace.DeployResult;
type CleanResult = workspace.CleanResult;
type GlobalEnabled = workspace.GlobalEnabled;
type AvailablePlugin = workspace.AvailablePlugin;

// Tool type
type ToolType = string;

// Last action state
interface LastAction {
  kind: 'build' | 'sync' | 'clean';
  label: string;
  loading: boolean;
  result?: DeployResult | CleanResult;
}

export const useWorkspaceStore = defineStore('workspace', () => {
  // State
  const workspaces = ref<Workspace[]>([]);
  const activeWorkspaceId = ref<string | null>(null);
  const globalEnabled = ref<GlobalEnabled[]>([]);
  const availablePlugins = ref<AvailablePlugin[]>([]);
  const loading = ref(false);
  const lastAction = ref<LastAction | null>(null);

  // Computed
  const activeWorkspace = computed(() => {
    if (!activeWorkspaceId.value) return null;
    return workspaces.value.find(w => w.id === activeWorkspaceId.value) || null;
  });

  const workspaceCount = computed(() => workspaces.value.length);

  const busy = computed(() => loading.value || (lastAction.value?.loading ?? false));

  // Actions

  // Load all workspaces
  async function loadWorkspaces() {
    loading.value = true;
    try {
      const result = await workspaceApi.listWorkspaces();
      workspaces.value = result;
      // Set active workspace if none selected
      if (!activeWorkspaceId.value && result.length > 0) {
        activeWorkspaceId.value = result[0].id;
      }
    } catch (error) {
      console.error('[workspace.store.loadWorkspaces]', error);
      throw error;
    } finally {
      loading.value = false;
    }
  }

  // Load global enabled plugins
  async function loadGlobalEnabled() {
    try {
      const result = await workspaceApi.getGlobalEnabled();
      globalEnabled.value = result;
    } catch (error) {
      console.error('[workspace.store.loadGlobalEnabled]', error);
      throw error;
    }
  }

  // Load available plugins for a workspace
  async function loadAvailablePlugins(workspaceId: string) {
    try {
      const result = await workspaceApi.getAvailablePluginsForWorkspace(workspaceId);
      availablePlugins.value = result;
    } catch (error) {
      console.error('[workspace.store.loadAvailablePlugins]', error);
      throw error;
    }
  }

  // Create workspace
  async function createWorkspace(params: { name: string; path: string; tools?: ToolType[] }) {
    loading.value = true;
    try {
      const result = await workspaceApi.createWorkspace(params);
      workspaces.value.push(result);
      activeWorkspaceId.value = result.id;
      return result;
    } catch (error) {
      console.error('[workspace.store.createWorkspace]', error);
      throw error;
    } finally {
      loading.value = false;
    }
  }

  // Update workspace
  async function updateWorkspace(params: { id: string; name?: string; path?: string; tools?: ToolType[] }) {
    loading.value = true;
    try {
      const result = await workspaceApi.updateWorkspace(params);
      const index = workspaces.value.findIndex(w => w.id === params.id);
      if (index !== -1) {
        workspaces.value[index] = result;
      }
      return result;
    } catch (error) {
      console.error('[workspace.store.updateWorkspace]', error);
      throw error;
    } finally {
      loading.value = false;
    }
  }

  // Delete workspace
  async function deleteWorkspace(id: string) {
    loading.value = true;
    try {
      await workspaceApi.deleteWorkspace(id);
      workspaces.value = workspaces.value.filter(w => w.id !== id);
      if (activeWorkspaceId.value === id) {
        activeWorkspaceId.value = workspaces.value[0]?.id || null;
      }
    } catch (error) {
      console.error('[workspace.store.deleteWorkspace]', error);
      throw error;
    } finally {
      loading.value = false;
    }
  }

  // Build scaffold (deploy)
  async function buildScaffold(workspaceId: string) {
    setAction('build', '部署工作区');
    try {
      const result = await workspaceApi.buildScaffold(workspaceId);
      if (lastAction.value) {
        lastAction.value.result = result;
      }
      return result;
    } catch (error) {
      console.error('[workspace.store.buildScaffold]', error);
      if (lastAction.value) {
        lastAction.value.loading = false;
      }
      throw error;
    }
  }

  // Sync workspace
  async function syncWorkspace(workspaceId: string) {
    setAction('sync', '同步工作区');
    try {
      const result = await workspaceApi.syncWorkspace(workspaceId);
      if (lastAction.value) {
        lastAction.value.result = result;
      }
      return result;
    } catch (error) {
      console.error('[workspace.store.syncWorkspace]', error);
      if (lastAction.value) {
        lastAction.value.loading = false;
      }
      throw error;
    }
  }

  // Clean workspace
  async function cleanWorkspace(id: string) {
    setAction('clean', '清理工作区');
    try {
      const result = await workspaceApi.cleanWorkspace(id);
      if (lastAction.value) {
        lastAction.value.result = result;
      }
      return result;
    } catch (error) {
      console.error('[workspace.store.cleanWorkspace]', error);
      if (lastAction.value) {
        lastAction.value.loading = false;
      }
      throw error;
    }
  }

  // Set global enabled
  async function setGlobalEnabled(items: GlobalEnabled[]) {
    try {
      const result = await workspaceApi.setGlobalEnabled(items);
      globalEnabled.value = items;
      return result;
    } catch (error) {
      console.error('[workspace.store.setGlobalEnabled]', error);
      throw error;
    }
  }

  // Helper: set action state
  function setAction(kind: 'build' | 'sync' | 'clean', label: string) {
    lastAction.value = {
      kind,
      label,
      loading: true,
      result: undefined,
    };
  }

  // Helper: reset action state
  function resetAction() {
    lastAction.value = null;
  }

  // Helper: select workspace
  function selectWorkspace(id: string | null) {
    activeWorkspaceId.value = id;
  }

  // Helper: conflict label mapping
  function conflictLabel(type: string): string {
    const labels: Record<string, string> = {
      target_path: '目标路径冲突',
      user_file: '用户文件冲突',
      mcp_key: 'MCP 键冲突',
      modified_file: '托管文件已修改',
    };
    return labels[type] || type;
  }

  return {
    // State
    workspaces,
    activeWorkspaceId,
    globalEnabled,
    availablePlugins,
    loading,
    lastAction,

    // Computed
    activeWorkspace,
    workspaceCount,
    busy,

    // Actions
    loadWorkspaces,
    loadGlobalEnabled,
    loadAvailablePlugins,
    createWorkspace,
    updateWorkspace,
    deleteWorkspace,
    buildScaffold,
    syncWorkspace,
    cleanWorkspace,
    setGlobalEnabled,
    selectWorkspace,
    resetAction,
    conflictLabel,
  };
});
