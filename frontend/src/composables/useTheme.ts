/**
 * Theme Composable
 * Manages theme state and provides theme-related utilities
 */

import { ref, computed } from 'vue';

type Theme = 'light' | 'dark' | 'auto';

const currentTheme = ref<Theme>('light');

export function useTheme() {
  const theme = computed(() => currentTheme.value);

  const isDark = computed(() => {
    if (currentTheme.value === 'auto') {
      return window.matchMedia('(prefers-color-scheme: dark)').matches;
    }
    return currentTheme.value === 'dark';
  });

  const isLight = computed(() => !isDark.value);

  function setTheme(newTheme: Theme) {
    currentTheme.value = newTheme;
    // Apply theme class to body
    if (newTheme === 'dark' || (newTheme === 'auto' && window.matchMedia('(prefers-color-scheme: dark)').matches)) {
      document.body.classList.add('dark-theme');
    } else {
      document.body.classList.remove('dark-theme');
    }
  }

  function toggleTheme() {
    setTheme(isDark.value ? 'light' : 'dark');
  }

  // Initialize theme
  function init() {
    const saved = localStorage.getItem('theme') as Theme;
    if (saved && (saved === 'light' || saved === 'dark' || saved === 'auto')) {
      setTheme(saved);
    } else {
      setTheme('light');
    }

    // Listen for system theme changes when in auto mode
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
      if (currentTheme.value === 'auto') {
        setTheme('auto');
      }
    });
  }

  return {
    theme,
    isDark,
    isLight,
    setTheme,
    toggleTheme,
    init,
  };
}
