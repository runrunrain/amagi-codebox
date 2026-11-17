<!-- 契约 §4 #8：两列标签-值网格（值可 mono），空 value 项由父层过滤。 -->
<template>
  <dl class="meta-grid">
    <div v-for="(item, index) in items" :key="index" class="meta-cell">
      <dt class="meta-label">{{ item.label }}</dt>
      <dd :class="['meta-value', { mono: item.mono }]">{{ item.value }}</dd>
    </div>
  </dl>
</template>

<script setup lang="ts">
interface MetaItem {
  label: string;
  value: string;
  mono?: boolean;
}

interface Props {
  items: MetaItem[];
}

defineProps<Props>();
</script>

<style scoped>
.meta-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 10px 16px;
  margin: 0;
}

.meta-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.meta-label {
  font-size: 11px;
  color: var(--tertiary);
}

.meta-value {
  margin: 0;
  font-size: 13px;
  color: var(--label);
  word-break: break-all;
}

.meta-value.mono {
  font-family: var(--mono);
}
</style>
