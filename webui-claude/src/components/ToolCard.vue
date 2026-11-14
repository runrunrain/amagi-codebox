<template>
  <div class="tool" :data-status="card.status">
    <div class="tool-head">
      <span class="tool-status" aria-hidden="true">{{ statusIcon }}</span>
      <span class="tool-name">{{ card.name || '未知工具' }}</span>
      <span class="tool-state">{{ statusLabel }}</span>
    </div>

    <details v-if="inputPreview" class="tool-sec">
      <summary>输入</summary>
      <pre>{{ inputPreview }}</pre>
    </details>

    <!-- failed 默认展开（错误可见性优先），成功态默认折叠。 -->
    <details v-if="card.result" class="tool-sec" :open="card.status === 'failed'">
      <summary :class="{ 'tool-sec-err': card.status === 'failed' }">
        {{ card.status === 'failed' ? '执行失败' : '执行结果' }}
      </summary>
      <pre>{{ card.result.content || '(空输出)' }}</pre>
    </details>
  </div>
</template>

<script setup lang="ts">
/**
 * ToolCard.vue — 工具卡四态（t7，design §6.3/§7.2）：
 * running（执行中，脉冲指示）/ success / failed（默认展开结果）/
 * orphan（结果无对应调用——缓冲丢头残留，渲染为未知工具）。
 */
import { computed } from 'vue'
import type { ToolCardModel, ToolStatus } from '../state/doc'

const props = defineProps<{ card: ToolCardModel }>()

const STATUS_META: Record<ToolStatus, { icon: string; label: string }> = {
  running: { icon: '◌', label: '执行中' },
  success: { icon: '✓', label: '完成' },
  failed: { icon: '✕', label: '失败' },
  orphan: { icon: '?', label: '未知调用' },
}

const statusIcon = computed(() => STATUS_META[props.card.status].icon)
const statusLabel = computed(() => STATUS_META[props.card.status].label)

const inputPreview = computed(() => {
  if (props.card.input === undefined || props.card.input === null) return ''
  if (typeof props.card.input === 'string') return props.card.input
  try {
    return JSON.stringify(props.card.input, null, 2)
  } catch {
    return String(props.card.input)
  }
})
</script>

<style scoped>
.tool {
  border: 1px solid var(--plane-border);
  border-radius: var(--radius-sm);
  font-size: 12px;
  overflow: hidden;
}

.tool-head {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 8px;
  background: var(--plane-card);
}

.tool-status {
  width: 1.2em;
  text-align: center;
  font-weight: 600;
}

.tool-name {
  font-family: var(--mono);
  font-weight: 600;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-state {
  margin-left: auto;
  color: var(--plane-text-dim);
  white-space: nowrap;
}

.tool[data-status='running'] .tool-status {
  color: var(--plane-accent);
  animation: tool-pulse 1.2s ease-in-out infinite;
}

.tool[data-status='running'] .tool-state {
  color: var(--plane-accent);
}

.tool[data-status='success'] .tool-status,
.tool[data-status='success'] .tool-state {
  color: var(--plane-success);
}

.tool[data-status='failed'] .tool-status,
.tool[data-status='failed'] .tool-state {
  color: var(--plane-danger);
}

.tool[data-status='orphan'] .tool-status,
.tool[data-status='orphan'] .tool-state {
  color: var(--plane-text-dim);
}

@keyframes tool-pulse {
  0%,
  100% {
    opacity: 0.3;
  }
  50% {
    opacity: 1;
  }
}

@media (prefers-reduced-motion: reduce) {
  .tool[data-status='running'] .tool-status {
    animation: none;
  }
}

.tool-sec summary {
  cursor: pointer;
  padding: 3px 8px;
  color: var(--plane-text-dim);
  user-select: none;
  border-top: 1px solid var(--plane-border);
}

.tool-sec-err {
  color: var(--plane-danger);
}

.tool-sec pre {
  margin: 0;
  padding: 6px 8px;
  max-height: 240px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--mono);
  color: var(--plane-text-dim);
  border-top: 1px solid var(--plane-border);
}
</style>
