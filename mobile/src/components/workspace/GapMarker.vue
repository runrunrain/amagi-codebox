<script setup lang="ts">
/**
 * GapMarker — 历史缺口标记（§6 GapMarker 组件契约；design §7.4 诚实能力边界）
 * ---------------------------------------------------------------------------
 * 原位标记缺口区间 [fromSeq, toSeq]；fillable 时提供「尝试补齐」（≥44px），
 * 补齐结果原位替换；exhausted（服务端 gap 变体确认不可补齐）如实呈现
 * 「已从最新继续」，不伪造可恢复承诺。
 * ---------------------------------------------------------------------------
 */
import type { GapItem } from '../../lib/timeline';

defineProps<{
  item: GapItem;
  filling: boolean;
}>();

const emit = defineEmits<{
  fill: [entryId: string];
}>();
</script>

<template>
  <div class="gap-marker" role="note" aria-label="历史缺口">
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <line x1="5" y1="12" x2="9" y2="12" /><line x1="15" y1="12" x2="19" y2="12" /><line x1="12" y1="5" x2="12" y2="9" opacity="0" />
      <path d="M7 4h10a2 2 0 0 1 2 2v3M17 20H7a2 2 0 0 1-2-2v-3" />
    </svg>
    <span class="gap-text">
      历史缺口：第 {{ item.fromSeq }}–{{ item.toSeq }} 段未保留
    </span>
    <button
      v-if="item.fillable && !item.exhausted"
      type="button"
      class="gap-fill"
      :disabled="filling"
      @click="emit('fill', item.id)"
    >
      {{ filling ? '补齐中…' : '尝试补齐' }}
    </button>
    <span v-else-if="item.exhausted" class="gap-exhausted">该段已不可补齐，从最新继续</span>
  </div>
</template>

<style scoped>
.gap-marker {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin: 4px 12px;
  padding: 8px 12px;
  border: 1px dashed var(--VT-gap);
  border-radius: 10px;
  color: var(--VT-text-secondary);
  font-size: 12px;
}

.gap-text {
  flex: 1;
  min-width: 0;
  line-height: 1.5;
}

.gap-fill {
  min-height: 44px;
  min-width: 44px;
  padding: 0 12px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  background: transparent;
  color: var(--VT-accent-strong);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.gap-fill:disabled {
  color: var(--VT-text-disabled);
  border-color: var(--VT-border);
  cursor: not-allowed;
}

.gap-fill:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.gap-exhausted {
  font-size: 12px;
}
</style>
