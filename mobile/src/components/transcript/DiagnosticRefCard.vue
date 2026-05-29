<script setup lang="ts">
import type { DiagnosticRefPart } from '../../types/transcript'

defineProps<{ part: DiagnosticRefPart }>()
</script>

<template>
  <article class="diagnostic-card" :data-reason="part.reason">
    <header class="diagnostic-header">有一段终端输出已放入诊断详情</header>
    <p class="diagnostic-summary">{{ part.summary }}</p>
    <p class="diagnostic-count">共 {{ part.count || 1 }} 条，详情请点顶部“诊断”打开诊断抽屉查看。</p>
    <p v-if="part.redacted" class="diagnostic-redacted">预览中已隐藏敏感片段</p>
  </article>
</template>

<style scoped>
.diagnostic-card {
  border-radius: 10px;
  border: 1px solid color-mix(in srgb, var(--session-warning, #fbbf24) 28%, transparent);
  background: color-mix(in srgb, var(--session-warning, #fbbf24) 8%, var(--session-surface-subtle, #121217));
  color: var(--session-text-soft, #dedee4);
  padding: 10px 12px;
}

.diagnostic-header {
  color: var(--session-warning, #fbbf24);
  font-size: 12px;
  font-weight: 700;
  margin-bottom: 6px;
}

.diagnostic-summary {
  margin: 0;
  color: var(--session-text-muted, #a1a1aa);
  font-size: 13px;
  line-height: 1.5;
}

.diagnostic-count,
.diagnostic-redacted {
  margin: 8px 0 0;
  color: var(--session-text-faint, #71717a);
  font-size: 12px;
}
</style>
