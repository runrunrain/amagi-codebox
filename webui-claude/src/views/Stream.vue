<template>
  <div ref="scrollRef" class="stream-scroll">
    <div v-if="ended" class="stream-ended" role="status">
      会话已结束——可切回终端平面查看
    </div>

    <MessageItem v-for="message in doc.messages" :key="message.uuid" :message="message" />

    <!-- busy「生成中」动效（design §7.2/R3：长生成期间转录静默是数据源硬边界，
         用动效诚实告知；prefers-reduced-motion 降级为静态文案）。 -->
    <div v-if="doc.busy" class="stream-busy" role="status">
      <span class="busy-dots" aria-hidden="true">
        <span class="busy-dot"></span>
        <span class="busy-dot"></span>
        <span class="busy-dot"></span>
      </span>
      <span class="busy-label">生成中…</span>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * Stream.vue — 消息流视图（t7）：消息列表 + ended 横幅 + busy 动效 +
 * 跟底自动滚动（用户上翻阅读时不打扰）。
 */
import { nextTick, ref, watch } from 'vue'
import MessageItem from '../components/MessageItem.vue'
import type { StreamDoc } from '../state/doc'

const props = defineProps<{ doc: StreamDoc; ended: boolean }>()

const scrollRef = ref<HTMLElement | null>(null)

function nearBottom(el: HTMLElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight < 80
}

watch(
  () => [props.doc.messages.length, props.doc.busy, props.ended] as const,
  async () => {
    const el = scrollRef.value
    if (!el) return
    const pinned = nearBottom(el)
    await nextTick()
    if (pinned) el.scrollTop = el.scrollHeight
  },
)
</script>

<style scoped>
.stream-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.stream-ended {
  align-self: center;
  padding: 4px 14px;
  font-size: 12px;
  color: var(--plane-text-dim);
  border: 1px solid var(--plane-border);
  border-radius: var(--radius-sm);
}

.stream-busy {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 4px;
  color: var(--plane-text-dim);
}

.busy-dots {
  display: inline-flex;
  gap: 4px;
}

.busy-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--plane-accent);
  animation: busy-pulse 1.2s ease-in-out infinite;
}

.busy-dot:nth-child(2) {
  animation-delay: 0.2s;
}

.busy-dot:nth-child(3) {
  animation-delay: 0.4s;
}

@keyframes busy-pulse {
  0%,
  100% {
    opacity: 0.25;
    transform: translateY(0);
  }
  50% {
    opacity: 1;
    transform: translateY(-2px);
  }
}

/* 动效降级（design §7.2）：可访问性偏好下停用动画，仅保留静态指示。 */
@media (prefers-reduced-motion: reduce) {
  .busy-dot {
    animation: none;
    opacity: 0.8;
  }
}
</style>
