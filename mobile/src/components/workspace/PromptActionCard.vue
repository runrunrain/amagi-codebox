<script setup lang="ts">
/**
 * PromptActionCard — 权限确认（§4.3.1 / §6 PromptAction 组件契约）
 * ---------------------------------------------------------------------------
 * 问题 + 按钮组（≥44px 触控）；点按即答（发送对应输入）。观察者禁用并明示
 * （disabled + 原因由 Composer 层说明，此处以 aria-disabled 呈现）。
 * ---------------------------------------------------------------------------
 */
import type { PromptActionItem } from '../../lib/timeline';

defineProps<{
  item: PromptActionItem;
  /** 控制者可答；观察者禁用。 */
  canAnswer: boolean;
}>();

const emit = defineEmits<{
  answer: [input: string];
}>();
</script>

<template>
  <section class="prompt-action" aria-label="权限确认">
    <p class="prompt-question">{{ item.question }}</p>
    <div class="prompt-buttons" role="group" aria-label="确认选项">
      <button
        v-for="opt in item.options"
        :key="opt.key"
        type="button"
        class="prompt-btn"
        :disabled="!canAnswer"
        @click="emit('answer', opt.input)"
      >
        {{ opt.label }}
      </button>
    </div>
    <p v-if="!canAnswer" class="prompt-hint">你正在观察，获取控制权后才能确认</p>
  </section>
</template>

<style scoped>
.prompt-action {
  padding: 12px 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-warning);
  border-left: 4px solid var(--VT-warning);
  border-radius: 10px;
}

.prompt-question {
  margin: 0 0 10px;
  font-size: 14px;
  font-weight: 600;
  color: var(--VT-text);
  line-height: 1.5;
  word-break: break-word;
}

.prompt-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.prompt-btn {
  min-height: 44px;
  min-width: 44px;
  padding: 0 16px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  background: var(--VT-canvas);
  color: var(--VT-text);
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
}

.prompt-btn:disabled {
  color: var(--VT-text-disabled);
  border-color: var(--VT-border);
  cursor: not-allowed;
}

.prompt-btn:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

@media (hover: hover) {
  .prompt-btn:not(:disabled):hover {
    background: var(--VT-surface-raised);
  }
}

.prompt-hint {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--VT-text-secondary);
}
</style>
