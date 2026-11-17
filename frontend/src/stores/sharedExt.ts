/**
 * Shared Extensions Store（扩展管理页「共用」中心）
 *
 * 管理公共 skill / 公共 MCP 仓库清单与聚合 skill 视图。错误约定：
 *  - 加载类 action（load*）失败写 error 状态但不抛错，UI 读 error 渲染错误态；
 *  - 变更类 action 失败写 error 并重新抛出，调用方可选 toast；成功后刷新对应
 *    列表（聚合视图无需手动刷新——registry 写路径会内建失效 sharedext 的
 *    30s TTL 缓存，插件侧变化则由 P4 的 invalidateCache 挂点处理）。
 *  - importMCPServers 的部分成功（skipped 非空）不抛错，返回结果供 UI 提示。
 */

import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import * as sharedExtApi from '../api/sharedExt';
import type {
  AggregatedSkill,
  ImportSharedMCPResult,
  SharedExtEngine,
  SharedMCPServer,
  SharedSkill,
} from '../api/sharedExt';

export const useSharedExtStore = defineStore('sharedExt', () => {
  // ----- state -----
  const skills = ref<SharedSkill[]>([]);
  const mcpServers = ref<SharedMCPServer[]>([]);
  const aggregated = ref<AggregatedSkill[]>([]);
  const error = ref('');

  // loading 是跨 load 的合并 busy 态（内部计数，任一在飞即 true；
  // 计数必须用 ref，否则 computed 无法追踪）
  const loadingCount = ref(0);
  const loading = computed(() => loadingCount.value > 0);

  function beginLoad() {
    loadingCount.value += 1;
  }
  function endLoad() {
    loadingCount.value = Math.max(0, loadingCount.value - 1);
  }
  function setError(e: unknown) {
    error.value = e instanceof Error ? e.message : String(e);
  }

  // ----- 加载类 -----
  async function loadSkills(): Promise<void> {
    beginLoad();
    try {
      skills.value = await sharedExtApi.getSharedSkills();
      error.value = '';
    } catch (e) {
      console.error('[sharedExt.store.loadSkills]', e);
      setError(e);
    } finally {
      endLoad();
    }
  }

  async function loadMCPServers(): Promise<void> {
    beginLoad();
    try {
      mcpServers.value = await sharedExtApi.getSharedMCPServers();
      error.value = '';
    } catch (e) {
      console.error('[sharedExt.store.loadMCPServers]', e);
      setError(e);
    } finally {
      endLoad();
    }
  }

  async function loadAggregated(force = false): Promise<void> {
    beginLoad();
    try {
      aggregated.value = await sharedExtApi.getAggregatedSkills(force);
      error.value = '';
    } catch (e) {
      console.error('[sharedExt.store.loadAggregated]', e);
      setError(e);
    } finally {
      endLoad();
    }
  }

  // ----- 公共 skill 变更 -----
  async function importSkillFromZip(path: string): Promise<SharedSkill[]> {
    try {
      const imported = await sharedExtApi.importSkillFromZip(path);
      await loadSkills();
      return imported;
    } catch (e) {
      console.error('[sharedExt.store.importSkillFromZip]', e);
      setError(e);
      throw e;
    }
  }

  async function importSkillFromDir(path: string): Promise<SharedSkill[]> {
    try {
      const imported = await sharedExtApi.importSkillFromDir(path);
      await loadSkills();
      return imported;
    } catch (e) {
      console.error('[sharedExt.store.importSkillFromDir]', e);
      setError(e);
      throw e;
    }
  }

  async function deleteSkill(name: string): Promise<void> {
    try {
      await sharedExtApi.deleteSkill(name);
      await loadSkills();
    } catch (e) {
      console.error('[sharedExt.store.deleteSkill]', e);
      setError(e);
      throw e;
    }
  }

  async function setSkillEnabled(name: string, enabled: boolean): Promise<void> {
    try {
      await sharedExtApi.setSkillEnabled(name, enabled);
      await loadSkills();
    } catch (e) {
      console.error('[sharedExt.store.setSkillEnabled]', e);
      setError(e);
      throw e;
    }
  }

  async function setSkillTargets(name: string, targets: SharedExtEngine[]): Promise<void> {
    try {
      await sharedExtApi.setSkillTargets(name, targets);
      await loadSkills();
    } catch (e) {
      console.error('[sharedExt.store.setSkillTargets]', e);
      setError(e);
      throw e;
    }
  }

  // ----- 公共 MCP 变更 -----
  /** 纯预览（不落库）：非法 JSON 抛后端报错，由调用方就地展示，不写全局 error。 */
  async function previewMCPJSON(text: string): Promise<SharedMCPServer[]> {
    return await sharedExtApi.previewMCPJSON(text);
  }

  /** 批量导入：部分成功（skipped 非空）不抛错，返回结果供 UI 提示。 */
  async function importMCPServers(servers: SharedMCPServer[]): Promise<ImportSharedMCPResult> {
    try {
      const result = await sharedExtApi.importSharedMCPServers(servers);
      if (result.imported.length > 0) {
        await loadMCPServers();
      }
      return result;
    } catch (e) {
      console.error('[sharedExt.store.importMCPServers]', e);
      setError(e);
      throw e;
    }
  }

  async function deleteMCPServer(name: string): Promise<void> {
    try {
      await sharedExtApi.deleteMCPServer(name);
      await loadMCPServers();
    } catch (e) {
      console.error('[sharedExt.store.deleteMCPServer]', e);
      setError(e);
      throw e;
    }
  }

  async function setMCPServerEnabled(name: string, enabled: boolean): Promise<void> {
    try {
      await sharedExtApi.setMCPServerEnabled(name, enabled);
      await loadMCPServers();
    } catch (e) {
      console.error('[sharedExt.store.setMCPServerEnabled]', e);
      setError(e);
      throw e;
    }
  }

  async function setMCPServerTargets(name: string, targets: SharedExtEngine[]): Promise<void> {
    try {
      await sharedExtApi.setMCPServerTargets(name, targets);
      await loadMCPServers();
    } catch (e) {
      console.error('[sharedExt.store.setMCPServerTargets]', e);
      setError(e);
      throw e;
    }
  }

  return {
    // state
    skills,
    mcpServers,
    aggregated,
    loading,
    error,
    // 加载类
    loadSkills,
    loadMCPServers,
    loadAggregated,
    // skill 变更
    importSkillFromZip,
    importSkillFromDir,
    deleteSkill,
    setSkillEnabled,
    setSkillTargets,
    // MCP
    previewMCPJSON,
    importMCPServers,
    deleteMCPServer,
    setMCPServerEnabled,
    setMCPServerTargets,
  };
});
