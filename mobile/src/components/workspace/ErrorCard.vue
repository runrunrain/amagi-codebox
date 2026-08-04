<script setup lang="ts">
/**
 * ErrorCard — 错误（§4.3.1 / §6 ErrorCard 组件契约）
 * ---------------------------------------------------------------------------
 * danger 左边条 + 原因 + 下一步指引；详情可展开。
 * ---------------------------------------------------------------------------
 */
import { ref } from 'vue';
import type { ErrorItem } from '../../lib/timeline';

defineProps<{ item: ErrorItem }>();

const expanded = ref(false);
</script>

<template>
  <section class="error-card" role="alert">
    <div class="error-head">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="10" /><line x1="12" y1="8" x2="12" y2="12" /><line x1="12" y1="16" x2="12.01" y2="16" />
      </svg>
      <span class="error-reason">{{ item.reason }}</span>
    </div>
    <p class="error-next">{{ item.nextStep }}</p>
    <template v-if="item.detail">
      <button type="button" class="error-toggle" :aria-expanded="expanded" @click="expanded = !expanded">
        {{ expanded ? '收起详情' : '查看详情' }}
      </button>
      <pre v-if="expanded" class="error-detail">{{ item.detail }}</pre>
    </template>
  </section>
</template>

<style scoped>
.error-card {
  padding: 12px 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-danger);
  border-left: 4px solid var(--VT-danger);
  border-radius: 10px;
}

.error-head {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  color: var(--VT-danger);
}

.error-reason {
  font-size: 14px;
  font-weight: 600;
  line-height: 1.5;
  word-break: break-word;
}

.error-next {
  margin: 8px 0 0;
  font-size: 13px;
  color: var(--VT-text);
  line-height: 1.6;
}

.error-toggle {
  margin-top: 8px;
  min-height: 44px;
  min-width: 44px;
  padding: 0 12px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  background: transparent;
  color: var(--VT-text);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.error-toggle:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.error-detail {
  margin: 8px 0 0;
  padding: 8px 10px;
  background: var(--VT-surface-dark);
  color: var(--VT-on-dark);
  border-radius: 8px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
