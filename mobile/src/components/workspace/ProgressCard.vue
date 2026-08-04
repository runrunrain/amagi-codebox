<script setup lang="ts">
/**
 * ProgressCard — 进度（§4.3.1 / §6 ProgressCard 组件契约）
 * ---------------------------------------------------------------------------
 * 进度文字化（不做动画 spinner；reduced-motion 友好）；控制者显式
 * 「停止运行」按钮（≥44px，danger），观察者只见文字。
 * ---------------------------------------------------------------------------
 */
import type { ProgressItem } from '../../lib/timeline';

defineProps<{
  item: ProgressItem;
  /** 控制者（control=you）才可见停止按钮。 */
  canControl: boolean;
  stopping: boolean;
}>();

const emit = defineEmits<{
  stop: [];
}>();
</script>

<template>
  <section class="progress-card" aria-label="运行进度">
    <div class="progress-row">
      <svg class="progress-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="10" /><polyline points="12 6 12 12 16 14" />
      </svg>
      <span class="progress-text">{{ item.text }}</span>
      <button
        v-if="canControl"
        type="button"
        class="progress-stop"
        :disabled="stopping"
        @click="emit('stop')"
      >
        {{ stopping ? '停止中…' : '停止运行' }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.progress-card {
  padding: 10px 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-radius: 10px;
}

.progress-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.progress-icon {
  flex-shrink: 0;
  color: var(--VT-accent);
}

.progress-text {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  color: var(--VT-text);
  line-height: 1.5;
  word-break: break-word;
}

.progress-stop {
  flex-shrink: 0;
  min-height: 44px;
  min-width: 44px;
  padding: 0 14px;
  border: none;
  border-radius: 8px;
  background: var(--VT-danger);
  color: var(--VT-canvas);
  font-size: 13px;
  font-weight: 700;
  cursor: pointer;
}

.progress-stop:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.progress-stop:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}
</style>
