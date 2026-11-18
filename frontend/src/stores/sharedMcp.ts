/**
 * Shared MCP Store（共用 MCP，契约 §4.3）
 * 数据流向单一：API → store → 组件（frontend-backend.md 范式，sharedSkills 同模式）。
 * 写动作成功后以响应 Overview 整体替换（门面返回最新状态，免二次请求）；
 * load 失败写 error 不 rethrow，其余动作失败 rethrow 供面板 toast / force 确认流。
 */

import { defineStore } from 'pinia';
import { ref } from 'vue';
import * as sharedMcpApi from '../api/sharedMcp';
import type {
  SharedMcpEngineCell,
  SharedMcpEngineMeta,
  SharedMcpEntry,
  SharedMcpEntryOverview,
  SharedMcpImportOutcome,
  SharedMcpOverview,
} from '../api/sharedMcp';

/**
 * 外改识别（契约 §4.3）：后端 ErrForeignModified 的 Kind 不跨 Wails 序列化，
 * 前端按错误信息片段识别（sharedmcp/errors.go 的 Msg 恒含「已被外部修改」，
 * writers.go 两处构造文案复核）——识别后面板弹 danger 确认以 force=true 重试。
 */
export function isForeignModifiedError(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return message.includes('已被外部修改');
}

export const useSharedMcpStore = defineStore('sharedMcp', () => {
  const entries = ref<SharedMcpEntryOverview[]>([]);
  const enginesMeta = ref<SharedMcpEngineMeta[]>([]);
  const registryExists = ref(true);
  const registryPath = ref('');
  const loading = ref(false);
  // 加载诊断：load 失败时写入，供面板 DiagnosticBanner / ErrorState 消费
  const error = ref('');
  // 首载标记：面板 onMounted 仅在未加载过时 load，切走再切回不重复加载
  const loaded = ref(false);

  function applyOverview(ov: SharedMcpOverview) {
    entries.value = ov.entries ?? [];
    enginesMeta.value = ov.enginesMeta ?? [];
    registryExists.value = !!ov.registryExists;
    registryPath.value = ov.registryPath ?? '';
  }

  /** 首载/刷新：失败写 error 不 rethrow（面板以 store 派生门控呈现，无需 catch） */
  async function load() {
    if (loading.value) return;
    loading.value = true;
    try {
      applyOverview(await sharedMcpApi.listSharedMcp());
      error.value = '';
      loaded.value = true;
    } catch (err) {
      error.value = `加载共用 MCP 失败：${err instanceof Error ? err.message : String(err)}`;
    } finally {
      loading.value = false;
    }
  }

  /** 新增：成功以响应 Overview 整体替换；失败 rethrow 供面板 toast */
  async function create(entry: SharedMcpEntry) {
    applyOverview(await sharedMcpApi.createSharedMcpEntry(entry));
  }

  /** 更新（name 不可改，不自动重分发）；失败 rethrow 供面板 toast */
  async function update(name: string, entry: SharedMcpEntry) {
    applyOverview(await sharedMcpApi.updateSharedMcpEntry(name, entry));
  }

  /** 删除（Applied 非空时后端先逐引擎还原）；失败 rethrow 供面板 toast / force 确认 */
  async function remove(name: string, force = false) {
    applyOverview(await sharedMcpApi.deleteSharedMcpEntry(name, force));
  }

  /** 条目 × 引擎开关（true = 分发/幂等 resync，false = 移除托管条目） */
  async function setEngineSelection(
    name: string,
    engine: string,
    selected: boolean,
    force = false,
  ) {
    applyOverview(
      await sharedMcpApi.setSharedMcpEngineSelection(name, engine, selected, force),
    );
  }

  /**
   * 导入（显式提交）：后端重新解析并批量登记（同名跳过逐条报告、默认无
   * 分发）；成功以响应 Overview 整体替换并返回结果报告供面板展示；失败
   * rethrow 供导入对话框展示错误。
   */
  async function importEntries(text: string, ticket?: string): Promise<SharedMcpImportOutcome> {
    const result = ticket
      ? await sharedMcpApi.importPickedSharedMcpFile(ticket)
      : await sharedMcpApi.importSharedMcpEntries(text);
    applyOverview(result.overview);
    return result;
  }

  /** 矩阵一格（引擎未返回时 undefined，消费方按未分发呈现） */
  function engineCell(entry: SharedMcpEntryOverview, engine: string): SharedMcpEngineCell | undefined {
    return entry.engines?.[engine];
  }

  return {
    entries,
    enginesMeta,
    registryExists,
    registryPath,
    loading,
    error,
    loaded,
    load,
    create,
    update,
    remove,
    setEngineSelection,
    importEntries,
    engineCell,
  };
});
