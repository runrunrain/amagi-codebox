<script setup lang="ts">
import MarkdownRenderer from './MarkdownRenderer.vue'
import ToolCard from './ToolCard.vue'
import DiagnosticRefCard from './DiagnosticRefCard.vue'
import type { TranscriptPart } from '../../types/transcript'

defineProps<{ part: TranscriptPart }>()
</script>

<template>
  <MarkdownRenderer v-if="part.type === 'markdown'" :markdown="part.markdown" :streaming="part.streaming" />
  <ToolCard v-else-if="part.type === 'tool'" :part="part" />
  <DiagnosticRefCard v-else-if="part.type === 'diagnostic-ref'" :part="part" />
  <template v-else-if="part.type === 'raw-terminal'"></template>
  <article v-else-if="part.type === 'diff'" class="part part--diff-card">
    <header class="diff-header">
      <span class="diff-title">{{ part.filename || '文件变更' }}</span>
      <span class="diff-stats"><span class="diff-add">+{{ part.additions }}</span><span class="diff-del">-{{ part.deletions }}</span></span>
    </header>
    <pre class="part--diff">{{ part.diff }}</pre>
  </article>
  <div v-else-if="part.type === 'error'" class="part part--error">{{ part.message }}</div>
  <div v-else-if="part.type === 'reasoning'" class="part part--reasoning">{{ part.text }}</div>
  <div v-else-if="part.type === 'file'" class="part part--file">{{ part.action || 'file' }} · {{ part.path }}</div>
  <div v-else-if="part.type === 'step'" class="part part--step">{{ part.title }} · {{ part.status }}</div>
  <div v-else class="part-text">{{ part.text }}</div>
</template>

<style scoped>
.part {
  border-radius: 10px;
  border: 1px solid var(--session-border-weak, rgba(255, 255, 255, 0.08));
  background: var(--session-surface-subtle, #121217);
  color: var(--session-text-soft, #dedee4);
  padding: 10px 12px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  min-width: 0;
}

.part-text {
  color: var(--session-text-soft, #dedee4);
  font-size: 15px;
  line-height: 1.68;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  min-width: 0;
}

.part--diff {
  max-width: 100%;
  margin: 8px 0 0;
  padding: 10px;
  border-radius: 9px;
  border: 1px solid var(--session-border-weak, rgba(255, 255, 255, 0.08));
  overflow-x: auto;
  white-space: pre;
  word-break: normal;
  background: color-mix(in srgb, var(--session-surface-subtle, #121217) 88%, #000 12%);
  color: var(--session-text-muted, #a1a1aa);
  font-family: "Cascadia Code", "JetBrains Mono", "SFMono-Regular", Consolas, monospace;
  font-size: 12px;
  line-height: 1.5;
  -webkit-overflow-scrolling: touch;
  touch-action: pan-x;
}

.part--diff-card {
  padding: 10px;
}

.diff-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.diff-title {
  min-width: 0;
  color: var(--session-text, #f4f4f5);
  font-size: 13px;
  font-weight: 720;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.diff-stats {
  display: inline-flex;
  flex: none;
  gap: 8px;
  font-size: 12px;
  font-weight: 800;
}

.diff-add {
  color: var(--session-success, #56d364);
}

.diff-del {
  color: var(--session-danger, #f87171);
}

.part--error {
  color: var(--session-danger, #f87171);
  border-color: color-mix(in srgb, var(--session-danger, #f87171) 32%, var(--session-border-weak, transparent));
}

.part--reasoning {
  color: var(--session-text-muted, #a1a1aa);
  font-size: 13px;
}

.part--file,
.part--step {
  color: var(--session-text-muted, #a1a1aa);
  font-size: 13px;
}
</style>
