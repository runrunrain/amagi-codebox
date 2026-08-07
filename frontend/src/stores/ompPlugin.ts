import { ref } from 'vue';
import { defineStore } from 'pinia';
import * as api from '../api/ompPlugin';
import type {
  OmpPlugin,
  OmpCommandResult,
} from '../api/ompPlugin';

const CACHE_TTL = 5 * 60 * 1000;

/**
 * OMP 插件 Store（简化版 PiPlugin Store：list JSON 已含全部字段，无 detail 二级拉取）。
 * 写操作（install/uninstall/setEnabled/upgrade）后清空 loadedAt 并强制刷新列表，
 * 保证 UI 与 omp CLI 实际状态一致；写操作返回 CommandResult 供 UI 展示。
 */
export const useOmpPluginStore = defineStore('ompPlugin', () => {
  const installed = ref<OmpPlugin[]>([]);
  const warnings = ref<string[]>([]);
  const loading = ref(false);
  const loadError = ref('');
  const loadedAt = ref<number | null>(null);

  async function refresh(force = false) {
    if (!force && loadedAt.value && Date.now() - loadedAt.value < CACHE_TTL) {
      return;
    }
    loading.value = true;
    loadError.value = '';
    try {
      const data = await api.refreshOmpPlugins();
      installed.value = data.installed || [];
      warnings.value = data.warnings || [];
      loadedAt.value = Date.now();
    } catch (err) {
      loadError.value = err instanceof Error ? err.message : String(err);
    } finally {
      loading.value = false;
    }
  }

  async function install(spec: string): Promise<OmpCommandResult> {
    const result = await api.installOmpPlugin(spec);
    loadedAt.value = null;
    await refresh(true);
    return result;
  }

  async function uninstall(id: string): Promise<OmpCommandResult> {
    const result = await api.uninstallOmpPlugin(id);
    loadedAt.value = null;
    await refresh(true);
    return result;
  }

  async function setEnabled(id: string, enabled: boolean): Promise<OmpCommandResult> {
    const result = await api.setOmpPluginEnabled(id, enabled);
    loadedAt.value = null;
    await refresh(true);
    return result;
  }

  async function upgrade(id: string): Promise<OmpCommandResult> {
    const result = await api.upgradeOmpPlugin(id);
    loadedAt.value = null;
    await refresh(true);
    return result;
  }

  return {
    installed,
    warnings,
    loading,
    loadError,
    loadedAt,
    refresh,
    install,
    uninstall,
    setEnabled,
    upgrade,
  };
});
