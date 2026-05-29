<script setup lang="ts">
import { computed } from 'vue'
import PartRenderer from './PartRenderer.vue'
import type { KeyedTranscriptTurn, TranscriptPart, TranscriptTurn } from '../../types/transcript'

const props = defineProps<{
  turns?: TranscriptTurn[]
  turnOrder?: string[]
  turnsById?: Record<string, KeyedTranscriptTurn>
  partOrderByTurnId?: Record<string, string[]>
  partsById?: Record<string, TranscriptPart>
  loading?: boolean
  error?: string | null
}>()

const renderedTurns = computed<TranscriptTurn[]>(() => {
  if (props.turnOrder && props.turnsById && props.partOrderByTurnId && props.partsById) {
    return props.turnOrder.map((turnId) => {
      const turn = props.turnsById?.[turnId]
      if (!turn) return null
      return {
        ...turn,
        parts: (props.partOrderByTurnId?.[turnId] ?? []).map((partId) => props.partsById?.[partId]).filter(Boolean),
      } as TranscriptTurn
    }).filter(Boolean) as TranscriptTurn[]
  }
  return props.turns ?? []
})

function appTypeLabel(appType: TranscriptTurn['appType']) {
  switch (appType) {
    case 'claudecode':
      return 'Claude Code'
    case 'opencode':
      return 'OpenCode'
    case 'codex':
      return 'Codex'
    default:
      return 'Terminal'
  }
}

function statusLabel(status: TranscriptTurn['status']) {
  switch (status) {
    case 'streaming':
      return '正在处理'
    case 'completed':
      return '完成'
    case 'error':
      return '需要处理'
    default:
      return status
  }
}

function isTimelinePart(part: TranscriptPart) {
  if (part.type === 'raw-terminal') return false
  if (part.type === 'diagnostic-ref') {
    return part.visibility === 'summary-card' || part.visibility === 'error-card'
  }
  return true
}

function visibleParts(turn: TranscriptTurn) {
  return turn.parts.filter(isTimelinePart)
}

function partToUserText(part: TranscriptPart) {
  switch (part.type) {
    case 'text':
      return part.text
    case 'markdown':
      return part.markdown
    case 'reasoning':
      return part.text
    case 'error':
      return part.message
    case 'step':
      return part.title
    case 'file':
      return part.path
    default:
      return ''
  }
}

function userTurnText(turn: TranscriptTurn) {
  return visibleParts(turn).map(partToUserText).filter(Boolean).join('\n\n')
}

function assistantMeta(turn: TranscriptTurn) {
  return `${appTypeLabel(turn.appType)} · ${statusLabel(turn.status)}`
}
</script>

<template>
  <section class="session-timeline" aria-label="Session transcript">
    <div v-if="loading" class="timeline-state timeline-state--loading">正在连接会话，等待 Agent 输出...</div>
    <div v-else-if="error" class="timeline-state timeline-state--error">会话解析遇到问题，详情已放入诊断：{{ error }}</div>
    <div v-else-if="renderedTurns.length === 0" class="timeline-state">暂无会话输出，等待 Agent 响应。</div>

    <article v-for="turn in renderedTurns" :key="turn.id" class="timeline-turn" :class="`timeline-turn--${turn.role}`">
      <template v-if="turn.role === 'user'">
        <div v-if="userTurnText(turn)" class="user-bubble">{{ userTurnText(turn) }}</div>
      </template>

      <template v-else>
        <header class="assistant-meta" :class="{ 'assistant-meta--streaming': turn.status === 'streaming' }">
          <span v-if="turn.status === 'streaming'" class="assistant-meta-dot" aria-hidden="true"></span>
          <span>{{ assistantMeta(turn) }}</span>
        </header>
        <div class="assistant-flow">
          <PartRenderer v-for="part in visibleParts(turn)" :key="part.id" :part="part" />
        </div>
      </template>
    </article>

    <div class="timeline-bottom-spacer" aria-hidden="true"></div>
  </section>
</template>

<style scoped>
.session-timeline {
  display: flex;
  flex-direction: column;
  gap: 22px;
  color: var(--session-text, #f4f4f5);
}

.timeline-state {
  padding: 13px 14px;
  border: 1px solid var(--session-border-weak, rgba(255, 255, 255, 0.08));
  border-radius: 12px;
  color: var(--session-text-muted, #a1a1aa);
  background: var(--session-surface-subtle, #121217);
  font-size: 14px;
  line-height: 1.55;
}

.timeline-state--loading {
  border-color: color-mix(in srgb, var(--session-accent, #7aa2ff) 28%, transparent);
  color: var(--session-accent, #7aa2ff);
}

.timeline-state--error {
  border-color: color-mix(in srgb, var(--session-danger, #f87171) 30%, transparent);
  color: var(--session-danger, #f87171);
}

.timeline-turn {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}

.timeline-turn--user {
  align-items: flex-end;
}

.user-bubble {
  width: fit-content;
  max-width: min(82%, 64ch);
  margin-left: auto;
  padding: 10px 12px;
  border: 1px solid var(--session-border, rgba(255, 255, 255, 0.12));
  border-radius: 9px;
  background: var(--session-surface, #17171d);
  color: var(--session-text, #f4f4f5);
  font-size: 15px;
  line-height: 1.55;
  letter-spacing: -0.01em;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.assistant-meta {
  display: flex;
  align-items: center;
  gap: 7px;
  margin: 2px 0 0;
  color: var(--session-text-muted, #a1a1aa);
  font-size: 12.5px;
  font-weight: 650;
  line-height: 1.45;
}

.assistant-meta-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--session-accent, #7aa2ff);
  animation: assistant-meta-pulse 1.45s ease-in-out infinite;
}

.assistant-flow {
  display: grid;
  gap: 10px;
  min-width: 0;
  color: var(--session-text-soft, #dedee4);
  font-size: 15px;
  line-height: 1.68;
  overflow-wrap: anywhere;
}

.timeline-bottom-spacer {
  min-height: var(--session-composer-spacer, 156px);
}

@keyframes assistant-meta-pulse {
  0%, 100% { transform: scale(.75); opacity: .38; }
  50% { transform: scale(1); opacity: 1; }
}

@media (prefers-reduced-motion: reduce) {
  .assistant-meta-dot {
    animation: none;
  }
}
</style>
