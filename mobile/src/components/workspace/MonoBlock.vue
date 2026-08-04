<script setup lang="ts">
/**
 * MonoBlock — 等宽兜底（§4.3.1 / §6 MonoBlock 组件契约）
 * ---------------------------------------------------------------------------
 * 无法进一步转化的文本以等宽呈现；含终端控制序列时给诊断视图指引
 * （诊断视图本体 M2-D，本组件只留入口事件）。
 * ---------------------------------------------------------------------------
 */
import type { MonoItem } from '../../lib/timeline';

defineProps<{ item: MonoItem }>();

const emit = defineEmits<{
  'open-diagnostic': [];
}>();
</script>

<template>
  <div class="mono-block">
    <pre class="mono-text">{{ item.text }}</pre>
    <button
      v-if="item.hadControlChars"
      type="button"
      class="mono-diagnostic"
      @click="emit('open-diagnostic')"
    >
      此段含终端控制序列，以等宽文本呈现；需要终端级细节可打开诊断视图
    </button>
  </div>
</template>

<style scoped>
.mono-block {
  padding: 8px 12px;
}

.mono-text {
  margin: 0;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 13px;
  line-height: 1.6;
  color: var(--VT-text);
  white-space: pre-wrap;
  word-break: break-word;
}

.mono-diagnostic {
  margin-top: 6px;
  min-height: 44px;
  padding: 4px 10px;
  border: 1px dashed var(--VT-border-strong);
  border-radius: 8px;
  background: transparent;
  color: var(--VT-text-secondary);
  font-size: 12px;
  text-align: left;
  line-height: 1.5;
  cursor: pointer;
}

.mono-diagnostic:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}
</style>
