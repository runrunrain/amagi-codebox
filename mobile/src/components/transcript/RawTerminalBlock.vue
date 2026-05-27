<script setup lang="ts">
import { computed } from 'vue'
import { cleanPtyText } from '../../utils/transcriptNormalizer'
import type { RawTerminalPart } from '../../types/transcript'

const props = defineProps<{ part: RawTerminalPart }>()
const safeText = computed(() => cleanPtyText(props.part.text || ' '))
</script>

<template>
  <article class="raw-block">
    <header class="raw-header">Raw fallback · {{ part.reason }}</header>
    <pre class="raw-content">{{ safeText || ' ' }}</pre>
  </article>
</template>

<style scoped>
.raw-block {
  border-radius: 14px;
  border: 1px solid rgba(139, 148, 158, 0.24);
  background: rgba(13, 17, 23, 0.92);
  overflow: hidden;
}

.raw-header {
  padding: 8px 12px;
  color: #8b949e;
  border-bottom: 1px solid rgba(139, 148, 158, 0.16);
  font-size: 12px;
}

.raw-content {
  margin: 0;
  padding: 12px;
  overflow-x: auto;
  color: #c9d1d9;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
