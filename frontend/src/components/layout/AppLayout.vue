<script lang="ts" setup>
import Sidebar from './Sidebar.vue'
</script>

<template>
  <div class="app-layout">
    <Sidebar />
    <main class="main-content">
      <router-view v-slot="{ Component, route }">
        <keep-alive :include="['TerminalsPage']">
          <component :is="Component" :key="route.path" />
        </keep-alive>
      </router-view>
    </main>
  </div>
</template>

<style scoped>
.app-layout {
  display: flex;
  height: 100vh;
  overflow: hidden;
}

.main-content {
  flex: 1;
  overflow-y: auto;
  padding: 24px;
  background: #0f1219;
}

/* 终端页面不需要 padding，通过 deep 选择器覆盖 */
.main-content :deep(.terminals-page) {
  margin: -24px;
  height: calc(100% + 48px);
}
</style>
