<template>
  <div class="usage">
    <span class="usage-item">输入 {{ fmt(usage.in) }}</span>
    <span class="usage-item">输出 {{ fmt(usage.out) }}</span>
    <span class="usage-item">缓存读 {{ fmt(usage.cacheRead) }}</span>
    <span class="usage-item">缓存写 {{ fmt(usage.cacheWrite) }}</span>
  </div>
</template>

<script setup lang="ts">
/**
 * UsageLine.vue — 用量四分项行（t7，design §6.3 usage：in/out/cacheRead/
 * cacheWrite，对齐 internal/appmeta/claude jsonl.go usage 三结构的第四分项）。
 */
import type { UsageWire } from '../data/source'

defineProps<{ usage: UsageWire }>()

function fmt(n: number): string {
  return typeof n === 'number' && Number.isFinite(n) ? n.toLocaleString('en-US') : '0'
}
</script>

<style scoped>
.usage {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  font-size: 11px;
  color: var(--plane-text-dim);
  font-family: var(--mono);
}
</style>
