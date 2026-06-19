<template>
  <div class="empty-state">
    <div class="empty-icon">{{ icon }}</div>
    <h3>{{ title }}</h3>
    <p>{{ description }}</p>
    <slot name="action" />
  </div>
</template>

<script setup lang="ts">
interface Props {
  icon?: string;
  title: string;
  description: string;
}

withDefaults(defineProps<Props>(), {
  icon: '—',
});
</script>

<style scoped>
.empty-state {
  text-align: center;
  padding: 44px 20px;
  color: var(--tertiary);
  font-size: 13px;
  line-height: 1.6;
}

.empty-icon {
  font-size: 48px;
  margin-bottom: 16px;
  opacity: 0.5;
}

.empty-state h3 {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin-bottom: 8px;
}

.empty-state p {
  font-size: 14px;
  color: var(--secondary);
  max-width: 400px;
  margin: 0 auto;
}
</style>
