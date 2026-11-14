<template>
  <div v-if="visible" class="msg" :class="[`msg-${message.kind}`, { 'msg-human': message.isHuman }]">
    <span class="msg-role">{{ roleLabel }}</span>
    <div class="msg-body">
      <!-- 思考折叠（design §7.2）：thinking 块默认折叠 details。 -->
      <details v-if="message.thinking" class="msg-thinking">
        <summary>思考过程</summary>
        <pre>{{ message.thinking }}</pre>
      </details>

      <pre v-if="message.text" class="msg-text">{{ message.text }}</pre>

      <ToolCard v-for="card in message.tools" :key="card.id" :card="card" />

      <UsageLine v-if="message.usage" :usage="message.usage" />
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * MessageItem.vue — 单条消息渲染（t7）。
 *
 * 形态分派：human user → 用户气泡；assistant → 文本 + 思考折叠 + 工具卡 +
 * 用量行；非 human user 行是 tool_result 载体（配对后工具卡挂在发起调用的
 * assistant 消息上，本行通常无可视内容——仅 orphan 卡会留在本行）；
 * system → 弱化系统行。
 */
import { computed } from 'vue'
import ToolCard from './ToolCard.vue'
import UsageLine from './UsageLine.vue'
import type { StreamMessage } from '../state/doc'

const props = defineProps<{ message: StreamMessage }>()

const roleLabel = computed(() => {
  if (props.message.kind === 'assistant') return '助手'
  if (props.message.kind === 'system') return '系统'
  return props.message.isHuman ? '用户' : '工具'
})

const visible = computed(() => {
  const m = props.message
  return Boolean(m.text || m.thinking || m.tools.length > 0)
})
</script>

<style scoped>
.msg {
  max-width: 92%;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.msg-assistant,
.msg-system {
  align-self: flex-start;
}

.msg-human {
  align-self: flex-end;
  align-items: flex-end;
}

.msg-role {
  font-size: 11px;
  color: var(--plane-text-dim);
  padding: 0 2px;
}

.msg-body {
  background: var(--plane-card);
  border: 1px solid var(--plane-border);
  border-radius: var(--radius-md);
  padding: 8px 12px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.msg-human .msg-body {
  background: var(--plane-user-card);
  border-color: var(--plane-accent);
}

.msg-system .msg-body {
  color: var(--plane-text-dim);
  font-size: 12px;
}

.msg-text {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: inherit;
}

.msg-thinking summary {
  cursor: pointer;
  color: var(--plane-text-dim);
  font-size: 12px;
  user-select: none;
}

.msg-thinking pre {
  margin: 6px 0 0;
  padding: 8px;
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--mono);
  color: var(--plane-text-dim);
  border-left: 2px solid var(--plane-border);
}
</style>
