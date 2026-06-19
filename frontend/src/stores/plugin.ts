/**
 * Plugin Store
 * Manages Claude and Codex plugin cache
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';

interface PluginDetail {
  ID: string;
  Name: string;
  Type: string;
  Version: string;
  Enabled: boolean;
  Scope: string;
  Developer?: string;
  Category?: string;
  Description: string;
  Resources?: {
    Skills?: number;
    Agents?: number;
    Commands?: number;
    Hooks?: number;
    MCP?: number;
  };
  // Codex specific
  Source?: string;
  Duplicate?: boolean;
  PluginId?: string;
  Capabilities?: string[];
}

interface Marketplace {
  Name: string;
  Description: string;
  URL: string;
}

export const usePluginStore = defineStore('plugin', () => {
  // Claude plugins
  const ccInstalled = ref<PluginDetail[]>([]);
  const ccMarkets = ref<Marketplace[]>([]);
  const ccAvailable = ref<PluginDetail[]>([]);

  // Codex plugins
  const cxInstalled = ref<PluginDetail[]>([]);
  const cxMarkets = ref<Marketplace[]>([]);
  const cxAvailable = ref<PluginDetail[]>([]);

  // Loading states
  const loadingCC = ref(false);
  const loadingCX = ref(false);

  // Computed
  const ccInstalledCount = computed(() => ccInstalled.value.length);
  const cxInstalledCount = computed(() => cxInstalled.value.length);
  const ccMarketCount = computed(() => ccMarkets.value.length);
  const cxMarketCount = computed(() => cxMarkets.value.length);

  // Actions
  function setCCInstalled(plugins: PluginDetail[]) {
    ccInstalled.value = plugins;
  }

  function setCCMarkets(markets: Marketplace[]) {
    ccMarkets.value = markets;
  }

  function setCCAvailable(plugins: PluginDetail[]) {
    ccAvailable.value = plugins;
  }

  function setCXInstalled(plugins: PluginDetail[]) {
    cxInstalled.value = plugins;
  }

  function setCXMarkets(markets: Marketplace[]) {
    cxMarkets.value = markets;
  }

  function setCXAvailable(plugins: PluginDetail[]) {
    cxAvailable.value = plugins;
  }

  function setLoadingCC(loading: boolean) {
    loadingCC.value = loading;
  }

  function setLoadingCX(loading: boolean) {
    loadingCX.value = loading;
  }

  return {
    // State
    ccInstalled,
    ccMarkets,
    ccAvailable,
    cxInstalled,
    cxMarkets,
    cxAvailable,
    loadingCC,
    loadingCX,

    // Computed
    ccInstalledCount,
    cxInstalledCount,
    ccMarketCount,
    cxMarketCount,

    // Actions
    setCCInstalled,
    setCCMarkets,
    setCCAvailable,
    setCXInstalled,
    setCXMarkets,
    setCXAvailable,
    setLoadingCC,
    setLoadingCX,
  };
});
