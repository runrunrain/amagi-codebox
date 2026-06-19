<template>
  <div class="config-card">
    <slot />
  </div>
</template>

<script setup lang="ts">
// 无状态卡片容器组件
</script>

<style scoped>
.config-card {
  background: var(--card);
  border-radius: 14px;
  box-shadow: var(--shadow);
  padding: 20px 24px 10px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
</style>
