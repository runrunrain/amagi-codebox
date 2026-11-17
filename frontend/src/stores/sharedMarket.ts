/**
 * Shared Market Store（共用插件市场）
 * 数据流向单一：API → store → 组件。增删改成功后以响应列表整体替换
 * marketplaces（门面返回最新列表，免二次请求）。范式对齐 sharedSkills store。
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import * as sharedMarketApi from '../api/sharedMarket';
import type { SharedMarketplace } from '../api/sharedMarket';

export const useSharedMarketStore = defineStore('sharedMarket', () => {
  const marketplaces = ref<SharedMarketplace[]>([]);
  const loading = ref(false);
  // 加载诊断：load 失败时写入，供面板 DiagnosticBanner / ErrorState 消费
  const error = ref('');
  // 首载标记：面板 onMounted 仅在未加载过时 load，切走再切回不重复加载
  const loaded = ref(false);

  const totalCount = computed(() => marketplaces.value.length);
  const missingCount = computed(() => marketplaces.value.filter((m) => m.missing).length);

  /** 首载/刷新：失败写 error 不 rethrow（面板以 store 派生门控呈现） */
  async function load() {
    if (loading.value) return;
    loading.value = true;
    try {
      marketplaces.value = await sharedMarketApi.listSharedMarkets();
      error.value = '';
      loaded.value = true;
    } catch (err) {
      error.value = `加载共用市场失败：${err instanceof Error ? err.message : String(err)}`;
    } finally {
      loading.value = false;
    }
  }

  /** 新增市场：成功以响应列表整体替换；失败 rethrow 供面板 toast */
  async function add(source: string) {
    marketplaces.value = await sharedMarketApi.addSharedMarketplace(source);
    loaded.value = true;
  }

  /** 更新单个市场 */
  async function update(name: string) {
    marketplaces.value = await sharedMarketApi.updateSharedMarketplace(name);
  }

  /** 删除市场（danger 确认在面板层） */
  async function remove(name: string) {
    marketplaces.value = await sharedMarketApi.removeSharedMarketplace(name);
  }

  return {
    marketplaces,
    loading,
    error,
    loaded,
    totalCount,
    missingCount,
    load,
    add,
    update,
    remove,
  };
});
