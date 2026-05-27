<script setup lang="ts">
import MarkdownRenderer from './MarkdownRenderer.vue'
import ToolCard from './ToolCard.vue'
import RawTerminalBlock from './RawTerminalBlock.vue'
import DiagnosticRefCard from './DiagnosticRefCard.vue'
import type { TranscriptPart } from '../../types/transcript'

defineProps<{ part: TranscriptPart }>()
</script>

<template>
  <MarkdownRenderer v-if="part.type === 'markdown'" :markdown="part.markdown" :streaming="part.streaming" />
  <ToolCard v-else-if="part.type === 'tool'" :part="part" />
  <DiagnosticRefCard v-else-if="part.type === 'diagnostic-ref'" :part="part" />
  <RawTerminalBlock v-else-if="part.type === 'raw-terminal'" :part="part" />
  <pre v-else-if="part.type === 'diff'" class="part part--diff">{{ part.diff }}</pre>
  <div v-else-if="part.type === 'error'" class="part part--error">{{ part.message }}</div>
  <div v-else-if="part.type === 'reasoning'" class="part part--reasoning">{{ part.text }}</div>
  <div v-else-if="part.type === 'file'" class="part part--file">{{ part.action || 'file' }} · {{ part.path }}</div>
  <div v-else-if="part.type === 'step'" class="part part--step">{{ part.title }} · {{ part.status }}</div>
  <div v-else class="part part--text">{{ part.text }}</div>
</template>

<style scoped>
.part {
  border-radius: 14px;
  border: 1px solid rgba(139, 148, 158, 0.2);
  background: rgba(22, 27, 34, 0.78);
  color: #e6edf3;
  padding: 12px 14px;
  white-space: pre-wrap;
  word-break: break-word;
}

.part--text {
  border-left: 2px solid rgba(88, 166, 255, 0.5);
}

.part--diff {
  overflow-x: auto;
  border-color: rgba(88, 166, 255, 0.2);
  background: #0d1117;
}

.part--error {
  color: #ff7b72;
  border-color: rgba(248, 81, 73, 0.28);
}

.part--reasoning {
  color: #a5d6ff;
}

.part--file,
.part--step {
  color: #8b949e;
}
</style>
