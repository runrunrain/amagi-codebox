import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import * as api from '../api/opencodePlugin';
import type {
  OpenCodePlugin,
  OpenCodePluginDetail,
} from '../api/opencodePlugin';

const CACHE_TTL = 5 * 60 * 1000;

export const useOpenCodePluginStore = defineStore('opencodePlugin', () => {
  const installed = ref<OpenCodePlugin[]>([]);
  const warnings = ref<string[]>([]);
  const activeSpec = ref<string | null>(null);
  const details = ref<Record<string, OpenCodePluginDetail>>({});
  const loading = ref(false);
  const loadingDetail = ref(false);
  const loadedAt = ref<number | null>(null);

  const activePlugin = computed(() => {
    if (!activeSpec.value) return null;
    return details.value[activeSpec.value]
      || installed.value.find(plugin => plugin.spec === activeSpec.value)
      || null;
  });

  async function refresh(force = false) {
    if (!force && loadedAt.value && Date.now() - loadedAt.value < CACHE_TTL) {
      return;
    }
    loading.value = true;
    try {
      const data = await api.refreshOpenCodePlugins();
      installed.value = data.installed || [];
      warnings.value = data.warnings || [];
      loadedAt.value = Date.now();
      if (activeSpec.value && !installed.value.some(plugin => plugin.spec === activeSpec.value)) {
        activeSpec.value = null;
      }
    } finally {
      loading.value = false;
    }
  }

  async function selectPlugin(spec: string) {
    activeSpec.value = spec;
    loadingDetail.value = true;
    try {
      details.value[spec] = await api.getOpenCodePluginDetails(spec);
    } finally {
      loadingDetail.value = false;
    }
  }

  async function install(spec: string) {
    const result = await api.installOpenCodePlugin(spec);
    loadedAt.value = null;
    await refresh(true);
    await selectPlugin(spec);
    return result;
  }

  async function update(spec: string) {
    const identity = pluginIdentity(spec);
    const result = await api.updateOpenCodePlugin(spec);
    delete details.value[spec];
    loadedAt.value = null;
    await refresh(true);
    const updated = installed.value.find(plugin => pluginIdentity(plugin.spec) === identity);
    if (updated) {
      await selectPlugin(updated.spec);
    } else {
      activeSpec.value = null;
    }
    return result;
  }

  async function uninstall(spec: string) {
    const result = await api.uninstallOpenCodePlugin(spec);
    delete details.value[spec];
    if (activeSpec.value === spec) {
      activeSpec.value = null;
    }
    loadedAt.value = null;
    await refresh(true);
    return result;
  }

  return {
    installed,
    warnings,
    activeSpec,
    details,
    loading,
    loadingDetail,
    activePlugin,
    refresh,
    selectPlugin,
    install,
    update,
    uninstall,
  };
});

function pluginIdentity(spec: string) {
  const value = spec.trim().toLowerCase();
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
