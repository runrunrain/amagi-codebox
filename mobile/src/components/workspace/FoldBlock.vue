<script setup lang="ts">
/**
 * FoldBlock — 长输出折叠（§4.3.1 / §6 FoldBlock 组件契约）
 * ---------------------------------------------------------------------------
 * 默认折叠：摘要行 + 行数；展开：等宽全文（pre）；复制按钮（≥44px）。
 * ---------------------------------------------------------------------------
 */
import { ref } from 'vue';
import type { FoldItem } from '../../lib/timeline';
import { copyText } from '../../utils/copyText';

const props = defineProps<{ item: FoldItem }>();

const expanded = ref(false);
const copied = ref(false);
let copiedTimer: ReturnType<typeof setTimeout> | null = null;

async function onCopy() {
  const ok = await copyText(props.item.fullText);
  if (!ok) return;
  copied.value = true;
  if (copiedTimer) clearTimeout(copiedTimer);
  copiedTimer = setTimeout(() => (copied.value = false), 1500);
}
</script>

<template>
  <section class="fold-block" :class="{ 'fold-block--expanded': expanded }">
    <div class="fold-header">
      <button
        type="button"
        class="fold-toggle"
        :aria-expanded="expanded"
        @click="expanded = !expanded"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" :class="{ 'chevron--down': expanded }">
          <polyline points="9 18 15 12 9 6" />
        </svg>
        <span class="fold-summary">{{ item.summary }}</span>
        <span class="fold-count">{{ item.lineCount }} 行</span>
      </button>
      <button type="button" class="fold-copy" :aria-label="copied ? '已复制' : '复制全部输出'" @click="onCopy">
        <svg v-if="!copied" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <rect x="9" y="9" width="13" height="13" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
        </svg>
        <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <polyline points="20 6 9 17 4 12" />
        </svg>
        {{ copied ? '已复制' : '复制' }}
      </button>
    </div>
    <pre v-if="expanded" class="fold-full">{{ item.fullText }}</pre>
  </section>
</template>

<style scoped>
.fold-block {
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-radius: 10px;
  overflow: hidden;
}

.fold-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
}

.fold-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
  min-height: 44px;
  padding: 0 8px;
  border: none;
  background: transparent;
  color: var(--VT-text);
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}

.fold-toggle:focus-visible,
.fold-copy:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
  border-radius: 8px;
}

.fold-toggle svg {
  flex-shrink: 0;
  color: var(--VT-text-secondary);
}

.chevron--down {
  transform: rotate(90deg);
}

.fold-summary {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
}

.fold-count {
  flex-shrink: 0;
  color: var(--VT-text-secondary);
  font-size: 12px;
}

.fold-copy {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-height: 44px;
  min-width: 44px;
  padding: 0 10px;
  border: none;
  background: transparent;
  color: var(--VT-accent-strong);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  flex-shrink: 0;
}

.fold-full {
  margin: 0;
  padding: 10px 14px;
  border-top: 1px solid var(--VT-border);
  background: var(--VT-surface-dark);
  color: var(--VT-on-dark);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 50vh;
  overflow-y: auto;
}

@media (prefers-reduced-motion: reduce) {
  .chevron--down {
    transition: none;
  }
}
</style>
