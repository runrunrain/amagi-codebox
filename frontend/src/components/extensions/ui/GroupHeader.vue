<!-- 契约 §4 #4：粘性小节标题（沿用 .plg-group-header 视觉：11px/tertiary/大写）。
     具名插槽 actions 放组级操作（如「更新市场」小按钮）。 -->
<template>
  <div class="group-header">
    <span class="gh-title">{{ title }}</span>
    <span v-if="count !== undefined" class="gh-count">{{ count }}</span>
    <div class="gh-actions">
      <slot name="actions" />
    </div>
  </div>
</template>

<script setup lang="ts">
interface Props {
  title: string;
  count?: number;
}

defineProps<Props>();
</script>

<style scoped>
.group-header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  font-size: 11px;
  font-weight: 600;
  color: var(--tertiary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  position: sticky;
  top: 0;
  background: var(--window);
  z-index: 1;
  margin-top: 8px;
}

.group-header:first-child {
  margin-top: 0;
}

.gh-title {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.gh-count {
  font-size: 10px;
  font-weight: 600;
  color: var(--secondary);
  background: var(--control);
  border-radius: 999px;
  padding: 1px 6px;
  line-height: 1.4;
  flex-shrink: 0;
}

.gh-actions {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
</style>
