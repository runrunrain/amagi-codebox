<template>
  <aside class="sidebar" :class="{ 'settings-mode': isInSettingsMode }">
    <!-- Normal Mode: SidebarNormal -->
    <SidebarNormal v-if="!isInSettingsMode"/>
    <!-- Settings Mode: SidebarSettings -->
    <SidebarSettings v-else/>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useUIStore } from '../../stores/ui'
import SidebarNormal from './SidebarNormal.vue'
import SidebarSettings from './SidebarSettings.vue'

const uiStore = useUIStore()
const isInSettingsMode = computed(() => uiStore.isInSettingsMode)
</script>

<style scoped>
.sidebar {
  width: 264px;
  flex-shrink: 0;
  background: var(--sidebar);
  border-right: 1px solid var(--separator);
  display: flex;
  flex-direction: column;
  padding: 18px 14px 14px;
  gap: 16px;
}
</style>
