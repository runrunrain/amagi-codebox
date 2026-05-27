<script setup lang="ts">
import type { ToolPart } from '../../types/transcript'

defineProps<{ part: ToolPart }>()
</script>

<template>
  <article class="tool-card" :class="`tool-card--${part.state}`">
    <header class="tool-header">
      <span class="tool-name">{{ part.name }}</span>
      <span class="tool-state">{{ part.state }}</span>
    </header>
    <div class="tool-title">{{ part.title }}</div>
    <pre v-if="part.inputPreview" class="tool-preview">{{ part.inputPreview }}</pre>
    <pre v-if="part.outputPreview" class="tool-preview tool-preview--output">{{ part.outputPreview }}</pre>
  </article>
</template>

<style scoped>
.tool-card {
  padding: 12px 14px;
  border-radius: 14px;
  border: 1px solid rgba(139, 148, 158, 0.22);
  background: rgba(22, 27, 34, 0.9);
}

.tool-card--running,
.tool-card--pending {
  border-color: rgba(210, 153, 34, 0.28);
}

.tool-card--error {
  border-color: rgba(248, 81, 73, 0.3);
}

.tool-header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}

.tool-name {
  color: #79c0ff;
  font-size: 12px;
  font-weight: 700;
  text-transform: uppercase;
}

.tool-state {
  color: #8b949e;
  font-size: 12px;
}

.tool-title {
  color: #f0f6fc;
  font-weight: 600;
}

.tool-preview {
  margin: 10px 0 0;
  padding: 10px;
  border-radius: 10px;
  background: #0d1117;
  color: #c9d1d9;
  overflow-x: auto;
  white-space: pre-wrap;
}

.tool-preview--output {
  color: #8b949e;
}
</style>
