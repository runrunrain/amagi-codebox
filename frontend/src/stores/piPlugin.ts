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

  async function refresh(force = false) {
    if (!force && loadedAt.value && Date.now() - loadedAt.value < CACHE_TTL) {
      return;
    }
    loading.value = true;
    try {
      const data = await api.refreshPiPackages();
      installed.value = data.installed || [];
      warnings.value = data.warnings || [];
      loadedAt.value = Date.now();
      if (activeSource.value && !installed.value.some(pkg => pkg.source === activeSource.value)) {
        activeSource.value = null;
      }
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
    activeSource,
    details,
    loading,
    loadingDetail,
    activePackage,
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
