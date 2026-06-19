<template>
  <div class="app">
    <Sidebar />
    <main class="main">
      <router-view />
    </main>
  </div>
</template>

<script setup lang="ts">
import Sidebar from './Sidebar.vue';
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
