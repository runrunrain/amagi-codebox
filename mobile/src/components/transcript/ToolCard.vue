<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ToolPart } from '../../types/transcript'

const props = defineProps<{ part: ToolPart }>()

const expanded = ref(false)

const stateLabel = computed(() => {
  switch (props.part.state) {
    case 'pending':
      return '等待'
    case 'running':
      return '运行中'
    case 'completed':
      return '完成'
    case 'error':
      return '出错'
    default:
      return props.part.state
  }
})

const displayName = computed(() => {
  const name = props.part.name.trim()
  const normalized = name.toLowerCase()
  if (/^(bash|shell|run)$/.test(normalized)) return '运行命令'
  if (/^(read|glob|grep)$/.test(normalized)) return '读取上下文'
  if (/^(edit|write|patch|apply_patch)$/.test(normalized)) return '修改文件'
  if (/^(todo|todowrite|plan)$/.test(normalized)) return '任务清单'
  return name || '工具活动'
})

const hasDetail = computed(() => Boolean(props.part.inputPreview || props.part.outputPreview))
</script>

<template>
  <article class="tool-card" :class="`tool-card--${part.state}`">
    <header class="tool-header">
      <div class="tool-heading">
        <span class="tool-name">{{ displayName }}</span>
        <span class="tool-title">{{ part.title }}</span>
      </div>
      <span class="tool-state">
        <span class="tool-state-dot" aria-hidden="true"></span>
        {{ stateLabel }}
      </span>
    </header>
    <button
      v-if="hasDetail"
      type="button"
      class="tool-detail-toggle"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >{{ expanded ? '收起详情' : '查看输入/输出摘要' }}</button>
    <div v-if="hasDetail && expanded" class="tool-detail-region">
      <pre v-if="part.inputPreview" class="tool-preview tool-preview--input">{{ part.inputPreview }}</pre>
      <pre v-if="part.outputPreview" class="tool-preview tool-preview--output">{{ part.outputPreview }}</pre>
    </div>
  </article>
</template>

<style scoped>
.tool-card {
  display: grid;
  gap: 7px;
  min-width: 0;
  padding: 9px 10px;
  border-radius: 10px;
  border: 1px solid var(--session-border-weak, rgba(255, 255, 255, 0.08));
  background: var(--session-surface-subtle, #121217);
  color: var(--session-text-muted, #a1a1aa);
  font-size: 12.5px;
  line-height: 1.45;
}

.tool-card--running,
.tool-card--pending {
  border-color: color-mix(in srgb, var(--session-accent, #7aa2ff) 24%, var(--session-border-weak, transparent));
}

.tool-card--error {
  border-color: color-mix(in srgb, var(--session-danger, #f87171) 34%, var(--session-border-weak, transparent));
}

.tool-header {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  justify-content: space-between;
  min-width: 0;
}

.tool-heading {
  display: grid;
  gap: 3px;
  min-width: 0;
}

.tool-name {
  color: var(--session-text-soft, #dedee4);
  font-weight: 720;
}

.tool-state {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--session-text-faint, #71717a);
  font-weight: 640;
  white-space: nowrap;
}

.tool-state-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.tool-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--session-text-faint, #71717a);
  font-family: "Cascadia Code", "JetBrains Mono", "SFMono-Regular", Consolas, monospace;
  font-size: 12px;
}

.tool-card--running .tool-state,
.tool-card--pending .tool-state {
  color: var(--session-accent, #7aa2ff);
}

.tool-card--running .tool-state-dot,
.tool-card--pending .tool-state-dot {
  animation: tool-running-pulse 1.35s ease-in-out infinite;
}

.tool-card--completed .tool-state {
  color: var(--session-success, #56d364);
}

.tool-card--error .tool-state {
  color: var(--session-danger, #f87171);
}

.tool-detail-toggle {
  justify-self: start;
  min-height: 30px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--session-text-muted, #a1a1aa);
  font: inherit;
  font-size: 12px;
  font-weight: 650;
}

.tool-detail-toggle:active {
  color: var(--session-text, #f4f4f5);
}

.tool-detail-region {
  display: grid;
  gap: 8px;
  min-width: 0;
}

.tool-preview {
  max-width: 100%;
  max-height: 240px;
  margin: 0;
  padding: 10px;
  border-radius: 10px;
  border: 1px solid var(--session-border-weak, rgba(255, 255, 255, 0.08));
  background: color-mix(in srgb, var(--session-surface-subtle, #121217) 88%, #000 12%);
  color: var(--session-text-muted, #a1a1aa);
  overflow-x: auto;
  overflow-y: auto;
  white-space: pre;
  word-break: normal;
  font-family: "Cascadia Code", "JetBrains Mono", "SFMono-Regular", Consolas, monospace;
  font-size: 12px;
  line-height: 1.5;
  -webkit-overflow-scrolling: touch;
  touch-action: pan-x pan-y;
}

.tool-preview--output {
  color: var(--session-text-faint, #71717a);
}

@keyframes tool-running-pulse {
  0%, 100% { transform: scale(.75); opacity: .38; }
  50% { transform: scale(1); opacity: 1; }
}

@media (prefers-reduced-motion: reduce) {
  .tool-card--running .tool-state-dot,
  .tool-card--pending .tool-state-dot {
    animation: none;
  }
}
</style>
