<script setup lang="ts">
import type { DiagnosticRefPart } from '../../types/transcript'

defineProps<{ part: DiagnosticRefPart }>()
</script>

<template>
  <article class="diagnostic-card" :data-reason="part.reason">
    <header class="diagnostic-header">诊断输出已隔离 · {{ part.reason }}</header>
    <p class="diagnostic-summary">{{ part.summary }}</p>
    <pre v-if="part.preview" class="diagnostic-preview">{{ part.preview }}</pre>
    <p v-if="part.redacted" class="diagnostic-redacted">预览中已隐藏敏感片段</p>
  </article>
</template>

<style scoped>
.diagnostic-card {
  border-radius: 14px;
  border: 1px solid rgba(210, 153, 34, 0.28);
  background: rgba(210, 153, 34, 0.08);
  color: #e6edf3;
  padding: 12px 14px;
}

.diagnostic-header {
  color: #ffd866;
  font-size: 12px;
  font-weight: 600;
  margin-bottom: 8px;
}

.diagnostic-summary {
  margin: 0;
  color: #c9d1d9;
}

.diagnostic-preview {
  margin: 10px 0 0;
  max-height: 140px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  color: #8b949e;
  font-size: 12px;
}

.diagnostic-redacted {
  margin: 8px 0 0;
  color: #8b949e;
  font-size: 12px;
}
</style>
