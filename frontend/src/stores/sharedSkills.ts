/**
 * Shared Skills Store（共用技能，契约 §2.6）
 * 数据流向单一：API → store → 组件（frontend-backend.md 范式）。
 * 启停/删除成功后以响应列表整体替换 skills（门面返回最新列表，免二次请求）。
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import * as sharedSkillsApi from '../api/sharedSkills';
import type { SharedSkillView, SharedSkillsImportResult } from '../api/sharedSkills';

export const useSharedSkillsStore = defineStore('sharedSkills', () => {
  const skills = ref<SharedSkillView[]>([]);
  const loading = ref(false);
  // 加载诊断：load 失败时写入，供面板 DiagnosticBanner / ErrorState 消费
  const error = ref('');
  // 首载标记（契约 §2.6）：面板 onMounted 仅在未加载过时 load，切走再切回不重复加载
  const loaded = ref(false);

  const enabledCount = computed(() => skills.value.filter((s) => s.enabled).length);
  const disabledCount = computed(() => skills.value.filter((s) => !s.enabled).length);

  /** 首载/刷新：失败写 error 不 rethrow（面板以 store 派生门控呈现，无需 catch） */
  async function load() {
    if (loading.value) return;
    loading.value = true;
    try {
      skills.value = await sharedSkillsApi.listSharedSkills();
      error.value = '';
      loaded.value = true;
    } catch (err) {
      error.value = `加载共用技能失败：${err instanceof Error ? err.message : String(err)}`;
    } finally {
      loading.value = false;
    }
  }

  /** 启停（目录移动）：成功以响应列表整体替换；失败 rethrow 供面板 toast */
  async function setEnabled(name: string, enabled: boolean) {
    skills.value = await sharedSkillsApi.setSharedSkillEnabled(name, enabled);
  }

  /** 删除：成功以响应列表整体替换；失败 rethrow 供面板 toast */
  async function remove(name: string) {
    skills.value = await sharedSkillsApi.deleteSharedSkill(name);
  }

  // 导入互斥标记：zip/文件夹导入共享（选择器未关前禁用入口）
  const importing = ref(false);

  // 导入执行器：先弹选择器（取消 → null 不动作），再执行导入；成功以信封
  // views 整体替换列表并返回信封供面板双 toast；失败 rethrow 供面板 toast。
  async function runImport(
    pick: () => Promise<string>,
    run: (path: string) => Promise<SharedSkillsImportResult>,
  ): Promise<SharedSkillsImportResult | null> {
    if (importing.value) return null;
    importing.value = true;
    try {
      const path = await pick();
      if (!path) return null; // 用户取消
      const result = await run(path);
      skills.value = result.views;
      error.value = '';
      loaded.value = true;
      return result;
    } finally {
      importing.value = false;
    }
  }

  /** zip 导入：落点 ~/.agents/skills-disabled（默认停用，不自动分发） */
  async function importFromZip() {
    return runImport(sharedSkillsApi.pickSharedSkillsZip, sharedSkillsApi.importSharedSkillsZip);
  }

  /** 本地文件夹导入：语义同 zip 导入 */
  async function importFromFolder() {
    return runImport(sharedSkillsApi.pickSharedSkillsDir, sharedSkillsApi.importSharedSkillsDir);
  }

  return {
    skills,
    loading,
    error,
    loaded,
    importing,
    enabledCount,
    disabledCount,
    load,
    setEnabled,
    remove,
    importFromZip,
    importFromFolder,
  };
});
