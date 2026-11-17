/**
 * 公共 MCP 面板交互逻辑（CommonMCPPanel 的可测内核）。
 *
 * 职责：server 清单加载、启停、targets 勾选、删除确认流。
 * 导入对话框流程独立在 useMCPImportDialog（预览→确认两步交互）。
 */

import { computed, ref } from 'vue';
import { useSharedExtStore } from '../../../stores/sharedExt';
import { useToast } from '../../../composables/useToast';
import { SHARED_EXT_ENGINES } from '../../../api/sharedExt';
import type { SharedExtEngine } from '../../../api/sharedExt';
import { mcpRowModel, nextTargets } from './commonCenterModel';
import type { McpRowModel } from './commonCenterModel';

export function useCommonMCPPanel() {
  const store = useSharedExtStore();
  const toast = useToast();

  const initialLoading = ref(true);
  const initialError = ref('');

  const rows = computed<McpRowModel[]>(() => store.mcpServers.map(mcpRowModel));

  const deleting = ref(false);

  async function refresh(): Promise<void> {
    await store.loadMCPServers();
  }

  async function initialLoad(): Promise<void> {
    initialLoading.value = true;
    try {
      await refresh();
      initialError.value = store.error;
    } finally {
      initialLoading.value = false;
    }
  }

  async function handleToggleEnabled(name: string, enabled: boolean): Promise<void> {
    if (store.loading) return;
    try {
      await store.setMCPServerEnabled(name, enabled);
      toast.showSuccess(enabled ? `已启用「${name}」` : `已停用「${name}」`);
    } catch (e) {
      console.error('[CommonMCPPanel] toggle enabled failed:', e);
      toast.showError(`操作失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function handleToggleTarget(name: string, engine: SharedExtEngine): Promise<void> {
    const server = store.mcpServers.find((s) => s.name === name);
    const next = nextTargets(server?.targets ?? [], engine);
    try {
      await store.setMCPServerTargets(name, next);
    } catch (e) {
      console.error('[CommonMCPPanel] set targets failed:', e);
      toast.showError(`保存分发目标失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  // ----- 删除确认流 -----
  const deleteTarget = ref<McpRowModel | null>(null);
  const showDeleteDialog = computed({
    get: () => deleteTarget.value !== null,
    set: (open: boolean) => {
      if (!open) deleteTarget.value = null;
    },
  });

  function requestDelete(row: McpRowModel): void {
    deleteTarget.value = row;
  }

  async function confirmDelete(): Promise<void> {
    const row = deleteTarget.value;
    if (!row || deleting.value) return;
    deleting.value = true;
    try {
      await store.deleteMCPServer(row.name);
      toast.showSuccess(`已删除「${row.name}」`);
      deleteTarget.value = null;
    } catch (e) {
      console.error('[CommonMCPPanel] delete failed:', e);
      toast.showError(`删除失败: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      deleting.value = false;
    }
  }

  return {
    rows,
    store,
    initialLoading,
    initialError,
    engines: SHARED_EXT_ENGINES,
    deleting,
    initialLoad,
    handleRetry: initialLoad,
    refresh,
    handleToggleEnabled,
    handleToggleTarget,
    requestDelete,
    confirmDelete,
    deleteTarget,
    showDeleteDialog,
  };
}
