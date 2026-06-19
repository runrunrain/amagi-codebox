<template>
  <div class="page-head">
    <h1>{{ title }}</h1>
    <p v-if="description">{{ description }}</p>
  </div>
</template>

<script setup lang="ts">
interface Props {
  title: string;
  description?: string;
}

withDefaults(defineProps<Props>(), {
  description: '',
});
</script>

<style scoped>
.page-head h1 {
  font-size: 28px;
  font-weight: 600;
  letter-spacing: -0.4px;
  color: var(--label);
}

.page-head p {
  font-size: 13px;
  color: var(--secondary);
  margin-top: 4px;
}
</style>
