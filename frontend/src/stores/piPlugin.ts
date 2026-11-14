import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import * as api from '../api/piPlugin';
import type {
  PiPackage,
  PiPackageDetail,
} from '../api/piPlugin';

const CACHE_TTL = 5 * 60 * 1000;

export const usePiPluginStore = defineStore('piPlugin', () => {
  const installed = ref<PiPackage[]>([]);
  const warnings = ref<string[]>([]);
  // 契约 §7（plugin-ui-redesign）：refresh 失败写入 / 成功清空；refresh 维持
  // 既有 rethrow 语义（动作签名不变），面板首屏门控与诊断横幅改由该 state 派生。
  const loadError = ref('');
  const activeSource = ref<string | null>(null);
  const details = ref<Record<string, PiPackageDetail>>({});
  const loading = ref(false);
  const loadingDetail = ref(false);
  const loadedAt = ref<number | null>(null);

  const activePackage = computed(() => {
    if (!activeSource.value) return null;
    return details.value[activeSource.value]
      || installed.value.find(pkg => pkg.source === activeSource.value)
      || null;
  });

  // 契约 §3.1/§7：PiPackage 自带四个可选资源数组（列表级可能未填充），Σ=0
  // 时面板隐藏对应标签（宁可少显示不显示假零）。
  const skillCount = computed(() => installed.value.reduce((sum, pkg) => sum + (pkg.skills?.length ?? 0), 0));
  const extensionCount = computed(() => installed.value.reduce((sum, pkg) => sum + (pkg.extensions?.length ?? 0), 0));
  const promptCount = computed(() => installed.value.reduce((sum, pkg) => sum + (pkg.prompts?.length ?? 0), 0));
  const themeCount = computed(() => installed.value.reduce((sum, pkg) => sum + (pkg.themes?.length ?? 0), 0));

  async function refresh(force = false) {
    if (!force && loadedAt.value && Date.now() - loadedAt.value < CACHE_TTL) {
      return;
    }
    loading.value = true;
    try {
      const data = await api.refreshPiPackages();
      installed.value = data.installed || [];
      warnings.value = data.warnings || [];
      loadError.value = '';
      loadedAt.value = Date.now();
      if (activeSource.value && !installed.value.some(pkg => pkg.source === activeSource.value)) {
        activeSource.value = null;
      }
    } catch (error) {
      loadError.value = error instanceof Error ? error.message : String(error);
      throw error;
    } finally {
      loading.value = false;
    }
  }

  async function selectPackage(source: string) {
    activeSource.value = source;
    loadingDetail.value = true;
    try {
      details.value[source] = await api.getPiPackageDetails(source);
    } finally {
      loadingDetail.value = false;
    }
  }

  async function install(source: string) {
    const result = await api.installPiPackage(source);
    loadedAt.value = null;
    await refresh(true);
    // The registered source may be normalized by the pi CLI; reselect the
    // matching entry when possible, otherwise clear the selection.
    const match = installed.value.find(pkg => pkg.source === source)
      || installed.value.find(pkg => packageIdentity(pkg.source) === packageIdentity(source));
    if (match) {
      await selectPackage(match.source);
    } else {
      activeSource.value = null;
    }
    return result;
  }

  async function update(source: string) {
    const result = await api.updatePiPackage(source);
    delete details.value[source];
    loadedAt.value = null;
    await refresh(true);
    // Pi keeps the same source string in settings.json after update, so the
    // selection can simply be re-resolved.
    if (installed.value.some(pkg => pkg.source === source)) {
      await selectPackage(source);
    } else {
      activeSource.value = null;
    }
    return result;
  }

  async function remove(source: string) {
    const result = await api.removePiPackage(source);
    delete details.value[source];
    if (activeSource.value === source) {
      activeSource.value = null;
    }
    loadedAt.value = null;
    await refresh(true);
    return result;
  }

  return {
    installed,
    warnings,
    loadError,
    activeSource,
    details,
    loading,
    loadingDetail,
    activePackage,
    skillCount,
    extensionCount,
    promptCount,
    themeCount,
    refresh,
    selectPackage,
    install,
    update,
    remove,
  };
});

function packageIdentity(source: string) {
  const value = source.trim().toLowerCase();
  const github = value.match(
    /^(?:github:|git@github\.com:|(?:git\+)?https?:\/\/github\.com\/)([^#]+)(?:#.*)?$/,
  );
  if (github) {
    return `github:${github[1].replace(/\.git$/, '').replace(/^\/+|\/+$/g, '')}`;
  }
  if (value.startsWith('file://') || value.startsWith('/') || /^[a-z]:[\\/]/i.test(value)) {
    return value;
  }
  if (value.startsWith('@')) {
    const slash = value.indexOf('/');
    const versionAt = value.lastIndexOf('@');
    return versionAt > slash ? value.slice(0, versionAt) : value;
  }
  const versionAt = value.lastIndexOf('@');
  return versionAt > 0 ? value.slice(0, versionAt) : value;
}
