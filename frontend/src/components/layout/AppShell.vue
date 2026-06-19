<template>
  <div class="app">
    <Sidebar />
    <main class="main">
      <SettingsView v-if="uiStore.isInSettingsMode" />
      <slot v-else />
    </main>
  </div>
</template>

<script setup lang="ts">
import Sidebar from './Sidebar.vue'
import SettingsView from '../../views/settings/SettingsView.vue'
import { useUIStore } from '../../stores/ui'

const uiStore = useUIStore()
</script>

<style scoped>
.app {
  display: flex;
  height: 100vh;
}

.main {
  flex: 1;
  display: flex;
  flex-direction: column;
  background: var(--card);
  position: relative;
  min-width: 0;
}
</style>
