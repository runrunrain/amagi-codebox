/**
 * UI Store
 * Manages global UI state
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';

export const useUIStore = defineStore('ui', () => {
  // Settings mode state
  const settingsMode = ref(false);

  // Current view ID
  const currentViewId = ref('settings');

  // Global loading state
  const globalLoading = ref(false);

  // Global loading message
  const globalLoadingMessage = ref('');

  // Sidebar collapsed state
  const sidebarCollapsed = ref(false);

  // Computed
  const isInSettingsMode = computed(() => settingsMode.value);
  const isLoading = computed(() => globalLoading.value);

  // Actions
  function enterSettingsMode() {
    settingsMode.value = true;
  }

  function exitSettingsMode() {
    settingsMode.value = false;
  }

  function setCurrentView(viewId: string) {
    currentViewId.value = viewId;
  }

  function setGlobalLoading(loading: boolean, message = '') {
    globalLoading.value = loading;
    globalLoadingMessage.value = message;
  }

  function toggleSidebar() {
    sidebarCollapsed.value = !sidebarCollapsed.value;
  }

  function setSidebarCollapsed(collapsed: boolean) {
    sidebarCollapsed.value = collapsed;
  }

  return {
    // State
    settingsMode,
    currentViewId,
    globalLoading,
    globalLoadingMessage,
    sidebarCollapsed,

    // Computed
    isInSettingsMode,
    isLoading,

    // Actions
    enterSettingsMode,
    exitSettingsMode,
    setCurrentView,
    setGlobalLoading,
    toggleSidebar,
    setSidebarCollapsed,
  };
});
