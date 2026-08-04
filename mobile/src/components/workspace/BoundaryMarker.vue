<script setup lang="ts">
/**
 * BoundaryMarker — 会话重启边界（PR-05：原位渲染）
 * ---------------------------------------------------------------------------
 * 边界占自身 Seq，原位插入时间线；上下段输出分属不同 run，不混排。
 * ---------------------------------------------------------------------------
 */
import { computed } from 'vue';
import type { BoundaryItem } from '../../lib/timeline';

const props = defineProps<{ item: BoundaryItem }>();

const timeText = computed(() => {
  const d = new Date(props.item.occurredAt);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString();
});
</script>

<template>
  <div class="boundary-marker" role="separator" :aria-label="`会话已于 ${timeText} 重启`">
    <span class="boundary-line" aria-hidden="true"></span>
    <span class="boundary-label">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <polyline points="1 4 1 10 7 10" /><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10" />
      </svg>
      会话已重启<template v-if="timeText"> · {{ timeText }}</template>
    </span>
    <span class="boundary-line" aria-hidden="true"></span>
  </div>
</template>

<style scoped>
.boundary-marker {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 8px 12px;
}

.boundary-line {
  flex: 1;
  height: 1px;
  background: var(--VT-border-strong);
}

.boundary-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--VT-text-secondary);
  white-space: nowrap;
}
</style>
