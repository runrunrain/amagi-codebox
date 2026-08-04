<script setup lang="ts">
/**
 * OptionCard — 选项菜单（§4.3.1 / §6 OptionCard 组件契约）
 * ---------------------------------------------------------------------------
 * 编号选项列表；单选即答（点按发送该选项输入）。观察者禁用。
 * ---------------------------------------------------------------------------
 */
import type { OptionItem } from '../../lib/timeline';

defineProps<{
  item: OptionItem;
  canAnswer: boolean;
}>();

const emit = defineEmits<{
  answer: [input: string];
}>();
</script>

<template>
  <section class="option-card" aria-label="选项菜单">
    <p v-if="item.title" class="option-title">{{ item.title }}</p>
    <ul class="option-list">
      <li v-for="opt in item.options" :key="opt.key">
        <button
          type="button"
          class="option-btn"
          :disabled="!canAnswer"
          @click="emit('answer', opt.input)"
        >
          <span class="option-label">{{ opt.label }}</span>
          <svg class="option-chevron" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <polyline points="9 18 15 12 9 6" />
          </svg>
        </button>
      </li>
    </ul>
    <p v-if="!canAnswer" class="option-hint">你正在观察，获取控制权后才能选择</p>
  </section>
</template>

<style scoped>
.option-card {
  padding: 12px 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-radius: 10px;
}

.option-title {
  margin: 0 0 8px;
  font-size: 14px;
  font-weight: 600;
  color: var(--VT-text);
  line-height: 1.5;
  word-break: break-word;
}

.option-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.option-btn {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  min-height: 44px;
  padding: 6px 12px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  background: var(--VT-canvas);
  color: var(--VT-text);
  font-size: 14px;
  text-align: left;
  cursor: pointer;
}

.option-btn:disabled {
  color: var(--VT-text-disabled);
  border-color: var(--VT-border);
  cursor: not-allowed;
}

.option-btn:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

@media (hover: hover) {
  .option-btn:not(:disabled):hover {
    background: var(--VT-surface-raised);
  }
}

.option-label {
  word-break: break-word;
}

.option-chevron {
  flex-shrink: 0;
  color: var(--VT-text-secondary);
}

.option-hint {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--VT-text-secondary);
}
</style>
