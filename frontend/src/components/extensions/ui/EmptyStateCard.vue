<!-- 契约 §4 #6：空态卡片（--card 底 + --separator 边 + --radius-card，居中排版）。
     具名插槽 primary / secondary 各放一个 AppButton（按钮由父层传入）。 -->
<template>
  <div class="empty-state-card">
    <h3 class="esc-title">{{ title }}</h3>
    <p v-if="description" class="esc-desc">{{ description }}</p>
    <div class="esc-actions">
      <span class="esc-primary"><slot name="primary" /></span>
      <span v-if="$slots.secondary" class="esc-secondary"><slot name="secondary" /></span>
    </div>
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
.empty-state-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: var(--radius-card);
  padding: 44px 24px;
  text-align: center;
}

.esc-title {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin: 0 0 8px;
}

.esc-desc {
  font-size: 13px;
  color: var(--secondary);
  margin: 0 auto 16px;
  max-width: 400px;
  line-height: 1.6;
}

.esc-actions {
  display: inline-flex;
  align-items: center;
  gap: 10px;
}

.esc-primary,
.esc-secondary {
  display: inline-flex;
}
</style>
