<script setup lang="ts">
/**
 * TimelineView — PG-03 内容转化时间线（§6 Timeline 虚拟滚动组件契约）
 * ---------------------------------------------------------------------------
 * · @tanstack/vue-virtual 虚拟滚动（动态测高 measureElement）；
 * · 七类内容转化组件 + 用户指令（coral 左边条）+ 边界/缺口标记原位渲染；
 * · 滚动跟随：贴底时自动跟随新输出；离底时显示「新输出」pill（点击回底）；
 * · 应答/停止/补齐/诊断入口事件上行给 store/page，本组件不直接写网络。
 * ---------------------------------------------------------------------------
 */
import { computed, nextTick, ref, watch } from 'vue';
import { useVirtualizer } from '@tanstack/vue-virtual';
import type { GapItem, TimelineItem } from '../../lib/timeline';
import PromptActionCard from './PromptActionCard.vue';
import OptionCard from './OptionCard.vue';
import FoldBlock from './FoldBlock.vue';
import ToolCallCard from './ToolCallCard.vue';
import ErrorCard from './ErrorCard.vue';
import ProgressCard from './ProgressCard.vue';
import MonoBlock from './MonoBlock.vue';
import GapMarker from './GapMarker.vue';
import BoundaryMarker from './BoundaryMarker.vue';

const props = defineProps<{
  items: TimelineItem[];
  /** 输出版本（store.latestSeq）：新帧到达的可靠信号（条目数可能因分块合并不变）。 */
  outputVersion: number;
  /** 控制者可答（PromptAction/OptionCard）。 */
  canAnswer: boolean;
  /** 控制者（停止运行按钮）。 */
  canControl: boolean;
  stopping: boolean;
  /** 正在补齐的 gap entryId 集合。 */
  fillingGapIds: ReadonlySet<string>;
}>();

const emit = defineEmits<{
  answer: [input: string];
  stop: [];
  'fill-gap': [entryId: string];
  'open-diagnostic': [];
}>();

const scrollEl = ref<HTMLElement | null>(null);

const virtualizer = useVirtualizer(
  computed(() => ({
    count: props.items.length,
    getScrollElement: () => scrollEl.value,
    estimateSize: () => 88,
    overscan: 8,
    getItemKey: (index: number) => props.items[index]?.id ?? index,
  })),
);

const virtualItems = computed(() => virtualizer.value.getVirtualItems());
const totalSize = computed(() => virtualizer.value.getTotalSize());

// --- 滚动跟随 + 新输出 pill ---

const NEAR_BOTTOM_PX = 96;
const nearBottom = ref(true);
const unseenCount = ref(0);

function onScroll(): void {
  const el = scrollEl.value;
  if (!el) return;
  const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM_PX;
  nearBottom.value = atBottom;
  if (atBottom) {
    unseenCount.value = 0;
  }
}

function scrollToBottom(): void {
  const el = scrollEl.value;
  if (!el) return;
  el.scrollTop = el.scrollHeight;
  nearBottom.value = true;
  unseenCount.value = 0;
}

watch(
  () => props.outputVersion,
  async () => {
    // 读实时滚动位置（不依赖 scroll 事件时序，避免竞态误判跟随状态）。
    await nextTick();
    const el = scrollEl.value;
    const atBottom = !el || el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM_PX;
    if (atBottom) {
      scrollToBottom();
    } else {
      unseenCount.value += 1;
    }
  },
);

/** E-07「跳到缺口」导航：按条目 id 滚动到原位标记（虚拟滚动 scrollToIndex）。 */
function scrollToItem(id: string): void {
  const idx = props.items.findIndex((i) => i.id === id);
  if (idx < 0) return;
  virtualizer.value.scrollToIndex(idx, { align: 'center' });
}

defineExpose({ scrollToBottom, scrollToItem });

/** 用户指令卡结算 chip 文案（M3-C outbox 可视化；不含 ID/payload）。 */
function deliveryChip(item: TimelineItem): { text: string; tone: 'sending' | 'settled' | 'halted' | 'unknown' } | null {
  if (item.kind !== 'user' || !item.delivery) return null;
  const attempts = item.attemptNo ?? 0;
  if (item.delivery === 'settled') return { text: '已确认', tone: 'settled' };
  if (item.delivery === 'halted') return { text: '已停发（控制权已变化）', tone: 'halted' };
  // sending：attempts 达上限仍未 ACK → terminal 未确认态（诚实，不伪造 confirmed）。
  if (attempts >= 8) return { text: '未确认，已停止自动重发', tone: 'unknown' };
  return { text: attempts > 1 ? `发送中（第 ${attempts} 次尝试）` : '发送中', tone: 'sending' };
}

function gapFilling(item: GapItem): boolean {
  return props.fillingGapIds.has(item.id);
}

/** 虚拟滚动动态测高（VNodeRef 可能为组件实例，仅元素测高）。 */
function measureRow(el: unknown): void {
  if (el instanceof Element) virtualizer.value.measureElement(el as HTMLElement);
}
</script>

<template>
  <div class="timeline" ref="scrollEl" aria-label="会话输出时间线" @scroll.passive="onScroll">
    <div v-if="items.length === 0" class="timeline-empty">
      <p>尚无输出。会话产生输出后，将以结构化卡片呈现于此。</p>
    </div>
    <div v-else class="timeline-inner" :style="{ height: `${totalSize}px` }">
      <div
        v-for="vItem in virtualItems"
        :key="String(vItem.key)"
        :data-index="vItem.index"
        :ref="measureRow"
        class="timeline-row"
        :style="{ transform: `translateY(${vItem.start}px)` }"
        data-testid="timeline-item"
        :data-seq="items[vItem.index]?.kind === 'boundary' ? (items[vItem.index] as { seq: number }).seq : undefined"
      >
        <template v-if="items[vItem.index]">
          <!-- 用户指令：coral 左边条 + outbox 结算 chip（M3-C） -->
          <div v-if="items[vItem.index].kind === 'user'" class="user-message">
            <span class="user-tag">你</span>
            <span class="user-text">{{ (items[vItem.index] as { text: string }).text }}</span>
            <span
              v-if="deliveryChip(items[vItem.index])"
              class="user-delivery"
              :class="`user-delivery--${deliveryChip(items[vItem.index])?.tone}`"
              role="status"
            >
              {{ deliveryChip(items[vItem.index])?.text }}
            </span>
          </div>
          <PromptActionCard
            v-else-if="items[vItem.index].kind === 'prompt-action'"
            :item="items[vItem.index] as any"
            :can-answer="canAnswer"
            @answer="(input: string) => emit('answer', input)"
          />
          <OptionCard
            v-else-if="items[vItem.index].kind === 'option'"
            :item="items[vItem.index] as any"
            :can-answer="canAnswer"
            @answer="(input: string) => emit('answer', input)"
          />
          <FoldBlock v-else-if="items[vItem.index].kind === 'fold'" :item="items[vItem.index] as any" />
          <ToolCallCard v-else-if="items[vItem.index].kind === 'tool'" :item="items[vItem.index] as any" />
          <ErrorCard v-else-if="items[vItem.index].kind === 'error'" :item="items[vItem.index] as any" />
          <ProgressCard
            v-else-if="items[vItem.index].kind === 'progress'"
            :item="items[vItem.index] as any"
            :can-control="canControl"
            :stopping="stopping"
            @stop="emit('stop')"
          />
          <MonoBlock
            v-else-if="items[vItem.index].kind === 'mono'"
            :item="items[vItem.index] as any"
            @open-diagnostic="emit('open-diagnostic')"
          />
          <BoundaryMarker v-else-if="items[vItem.index].kind === 'boundary'" :item="items[vItem.index] as any" />
          <GapMarker
            v-else-if="items[vItem.index].kind === 'gap'"
            :item="items[vItem.index] as any"
            :filling="gapFilling(items[vItem.index] as GapItem)"
            @fill="(id: string) => emit('fill-gap', id)"
          />
        </template>
      </div>
    </div>

    <!-- 新输出 pill（离底时） -->
    <button v-if="unseenCount > 0" type="button" class="new-output-pill" @click="scrollToBottom">
      {{ unseenCount }} 条新输出 ↓
    </button>
  </div>
</template>

<style scoped>
.timeline {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
}

.timeline-inner {
  position: relative;
  width: 100%;
}

.timeline-row {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  padding: 4px 12px;
  box-sizing: border-box;
}

/* M4-A safe-area：横屏（刘海/圆角在左右）时间线行不贴边；竖屏不变。 */
@media (orientation: landscape) {
  .timeline-row {
    padding-left: calc(12px + env(safe-area-inset-left, 0px));
    padding-right: calc(12px + env(safe-area-inset-right, 0px));
  }
  .timeline-empty {
    padding-left: calc(20px + env(safe-area-inset-left, 0px));
    padding-right: calc(20px + env(safe-area-inset-right, 0px));
  }
}

.timeline-empty {
  padding: 32px 20px;
  text-align: center;
  color: var(--VT-text-secondary);
  font-size: 13px;
  line-height: 1.6;
}

.user-message {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 12px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-accent);
  border-radius: 10px;
}

.user-tag {
  flex-shrink: 0;
  font-size: 12px;
  font-weight: 700;
  color: var(--VT-accent-strong);
  line-height: 1.5;
  padding-top: 1px;
}

.user-text {
  font-size: 14px;
  color: var(--VT-text);
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}

.user-delivery {
  flex-shrink: 0;
  align-self: center;
  margin-left: auto;
  padding: 2px 8px;
  border-radius: 999px;
  border: 1px solid var(--VT-border);
  font-size: 11px;
  font-weight: 600;
  white-space: nowrap;
}

.user-delivery--sending {
  border-color: var(--VT-warning);
  color: var(--VT-warning);
}

.user-delivery--settled {
  border-color: var(--VT-success);
  color: var(--VT-success);
}

.user-delivery--halted,
.user-delivery--unknown {
  border-color: var(--VT-danger);
  color: var(--VT-danger);
}

.new-output-pill {
  position: sticky;
  bottom: 12px;
  display: block;
  margin: 0 auto;
  width: fit-content;
  min-height: 44px;
  padding: 0 16px;
  border: none;
  border-radius: 999px;
  background: var(--VT-accent-strong);
  color: var(--VT-canvas);
  font-size: 13px;
  font-weight: 700;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.18);
}

.new-output-pill:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}
</style>
