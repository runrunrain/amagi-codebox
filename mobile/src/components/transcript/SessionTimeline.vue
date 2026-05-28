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
</script>

<template>
  <section class="session-timeline" aria-label="Session transcript">
    <div v-if="loading" class="timeline-state timeline-state--loading">正在建立结构化输出...</div>
    <div v-else-if="error" class="timeline-state timeline-state--error">结构化解析失败，已切换 raw fallback：{{ error }}</div>
    <div v-else-if="renderedTurns.length === 0" class="timeline-state">暂无输出，等待终端数据。</div>

    <article v-for="turn in renderedTurns" :key="turn.id" class="timeline-turn" :class="`timeline-turn--${turn.role}`">
      <header class="turn-header">
        <span class="turn-role">{{ turn.role }}</span>
        <span class="turn-meta">{{ turn.appType }} · {{ turn.status }}</span>
      </header>
      <PartRenderer v-for="part in turn.parts" :key="part.id" :part="part" />
    </article>
  </section>
</template>

<style scoped>
.session-timeline {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.timeline-state {
  padding: 14px;
  border: 1px solid rgba(139, 148, 158, 0.22);
  border-radius: 14px;
  color: #8b949e;
  background: rgba(22, 27, 34, 0.72);
}

.timeline-state--loading {
  border-color: rgba(88, 166, 255, 0.24);
  color: #79c0ff;
}

.timeline-state--error {
  border-color: rgba(248, 81, 73, 0.28);
  color: #ff7b72;
}

.timeline-turn {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.turn-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  color: #8b949e;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.08em;
}

.turn-role {
  color: #f0f6fc;
  font-weight: 700;
}

.turn-meta {
  font-size: 11px;
}
</style>
