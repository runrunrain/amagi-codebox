<template>
  <div class="progress-bar">
    <div class="pb-fill" :style="{ width: `${percent}%` }" />
  </div>
</template>

<script setup lang="ts">
interface Props {
  percent: number;
}

withDefaults(defineProps<Props>(), {
  percent: 0,
});
</script>

<style scoped>
.progress-bar {
  height: 6px;
  background: var(--control);
  border-radius: 999px;
  overflow: hidden;
  margin: 10px 0;
}

.pb-fill {
  height: 100%;
  background: var(--accent);
  border-radius: 999px;
  transition: width 0.3s ease;
}
</style>
