<template>
  <div v-if="visible" class="msg" :class="[`msg-${message.kind}`, { 'msg-human': message.isHuman }]">
    <!-- human（pi msg-user 同构）：control 底全宽块 + 左竖条，无角色标签 -->
    <template v-if="message.kind === 'user' && message.isHuman">
      <div class="msg-user">
        <span class="msg-user-bar" aria-hidden="true"></span>
        <div class="msg-user-body">
          <pre v-if="message.text" class="msg-text">{{ message.text }}</pre>

          <ToolCard v-for="card in message.tools" :key="card.id" :card="card" />

          <UsageLine v-if="message.usage" :usage="message.usage" />
        </div>
      </div>
    </template>

    <!-- system：居中弱化行 -->
    <template v-else-if="message.kind === 'system'">
      <div class="msg-system-line">{{ message.text }}</div>
    </template>

    <!-- assistant / tool_result 载体行：纸面直排（pi msg-assistant——无卡底） -->
    <template v-else>
      <!-- 思考折叠（design §7.2）：thinking 块默认折叠 details。 -->
      <details v-if="message.thinking" class="msg-thinking">
        <summary>思考过程</summary>
        <pre>{{ message.thinking }}</pre>
      </details>

      <pre v-if="message.text" class="msg-text">{{ message.text }}</pre>

      <ToolCard v-for="card in message.tools" :key="card.id" :card="card" />

      <UsageLine v-if="message.usage" :usage="message.usage" />
    </template>
  </div>
</template>

<script setup lang="ts">
/**
 * MessageItem.vue — 单条消息渲染（t7；版式对齐 pi webui 消息形态）。
 *
 * 形态分派：human user → pi msg-user（control 底 + 发话者竖条，无头像无气泡）；
 * assistant → 纸面直排（文本 + 思考折叠 + 工具卡 + 用量行，无卡底）；
 * 非 human user 行是 tool_result 载体（配对后工具卡挂在发起调用的 assistant
 * 消息上，本行通常无可视内容——仅 orphan 卡会留在本行，按直排渲染）；
 * system → 居中弱化系统行。
 */
import { computed } from 'vue'
import ToolCard from './ToolCard.vue'
import UsageLine from './UsageLine.vue'
import type { StreamMessage } from '../state/doc'

const props = defineProps<{ message: StreamMessage }>()

const visible = computed(() => {
  const m = props.message
  return Boolean(m.text || m.thinking || m.tools.length > 0)
})
</script>

<style scoped>
.msg {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}

/* 用户消息（pi §7.1）：control 底 + 发话者竖条，无头像无气泡 */
.msg-user {
  display: flex;
  background: var(--plane-block);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.msg-user-bar {
  flex: none;
  width: 3px;
  background: var(--plane-user-bar);
}

.msg-user-body {
  padding: 10px 14px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* 助手/工具行：纸面直排 */
.msg-text {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  overflow-wrap: anywhere;
  font-family: inherit;
  font-size: 14px;
}

.msg-system-line {
  text-align: center;
  font-size: 12px;
  color: var(--plane-text-dim);
  padding: 2px 0;
  overflow-wrap: anywhere;
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
  border-left: 2px solid var(--plane-hairline);
}
</style>
