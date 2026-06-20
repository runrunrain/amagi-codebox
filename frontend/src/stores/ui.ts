/**
 * UI Store
 * Manages global UI state
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';

export const useUIStore = defineStore('ui', () => {
  // Settings mode state
  const settingsMode = ref(false);

  // Active setting page key (general/shell/terminal/remote/update/rules/about)
  // 注：envcheck 已升为主页导航（EnvCheckView），rules 下沉到设置页
  const activeSettingKey = ref<string>('general');

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
    // Reset to general when entering settings mode (unless already set)
    if (!activeSettingKey.value) {
      activeSettingKey.value = 'general';
    }
  }

  function exitSettingsMode() {
    settingsMode.value = false;
    // Keep activeSettingKey so re-entering resumes last page; reset optional.
  }

  function setActiveSettingKey(key: string) {
    activeSettingKey.value = key;
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
    activeSettingKey,
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
    setActiveSettingKey,
    setCurrentView,
    setGlobalLoading,
    toggleSidebar,
    setSidebarCollapsed,
  };
});
