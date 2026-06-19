/**
 * Provider Store
 * Manages provider and preset cache
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import { config } from '../../wailsjs/go/models';

type Provider = config.Provider;
type TerminalPreset = config.TerminalPreset;
type MergedTerminalPreset = config.MergedTerminalPreset;

export const useProviderStore = defineStore('provider', () => {
  // Providers cache
  const providers = ref<Record<string, Provider>>({});

  // Presets cache by terminal type
  const presets = ref<Record<string, Record<string, TerminalPreset>>>({
    claude: {},
    codex: {},
    opencode: {},
  });

  // Merged presets cache
  const mergedPresets = ref<Record<string, MergedTerminalPreset[]>>({
    claude: [],
    codex: [],
    opencode: [],
  });

  // Loading states
  const loadingProviders = ref(false);
  const loadingPresets = ref(false);

  // Computed
  const providerList = computed(() => Object.values(providers.value));
  const claudePresets = computed(() => mergedPresets.value.claude || []);
  const codexPresets = computed(() => mergedPresets.value.codex || []);
  const opencodePresets = computed(() => mergedPresets.value.opencode || []);

  // Actions
  function setProviders(newProviders: Record<string, Provider>) {
    providers.value = newProviders;
  }

  function setTerminalPresets(terminalType: string, newPresets: Record<string, TerminalPreset>) {
    presets.value[terminalType] = newPresets;
  }

  function setMergedPresets(terminalType: string, newPresets: MergedTerminalPreset[]) {
    mergedPresets.value[terminalType] = newPresets;
  }

  function setLoadingProviders(loading: boolean) {
    loadingProviders.value = loading;
  }

  function setLoadingPresets(loading: boolean) {
    loadingPresets.value = loading;
  }

  return {
    // State
    providers,
    presets,
    mergedPresets,
    loadingProviders,
    loadingPresets,

    // Computed
    providerList,
    claudePresets,
    codexPresets,
    opencodePresets,

    // Actions
    setProviders,
    setTerminalPresets,
    setMergedPresets,
    setLoadingProviders,
    setLoadingPresets,
  };
});
