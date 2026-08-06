<script setup lang="ts">
/**
 * ComposerBar — PG-03 输入区（§6 Composer 组件契约 + CHG-20260801-05）
 * ---------------------------------------------------------------------------
 * · 多行自动增高（上限 ~5 行）；草稿保留（store.draft，发送成功才清空）；
 * · 历史指令复用入口（≥44px；点选回填草稿，不直接发送）；
 * · 发送态防连点（sending 期间禁用；空草稿禁用）；
 * · 「停止运行」显式按钮（控制者 ≥44px，danger；点击 → ConfirmDialog）；
 * · 写操作经控制权过滤：观察者禁用输入并明示原因（writeBlockReason）。
 * · 禁模拟键盘键帽 KeyTray（CHG-20260801-05）：不提供任何键帽式快捷条。
 * ---------------------------------------------------------------------------
 */
import { nextTick, ref, watch } from 'vue';

/** outbox 可视投影（M3-C；store.outboxView 形状）。 */
export interface OutboxStripView {
  pendingCount: number;
  haltedCount: number;
  maxAttemptNo: number;
  resending: boolean;
  exhaustedUnconfirmed: boolean;
}

const props = defineProps<{
  draft: string;
  sending: boolean;
  stopping: boolean;
  canWrite: boolean;
  /** 控制者（停止运行按钮可见性）。 */
  canControl: boolean;
  /** 观察者/不可写原因（null = 可写）。 */
  blockReason: string | null;
  history: string[];
  /** outbox 待确认/停发投影（可选；无 capability 时全 0 不渲染）。 */
  outbox?: OutboxStripView;
}>();

const emit = defineEmits<{
  'update:draft': [value: string];
  send: [];
  stop: [];
  reuse: [text: string];
}>();

const textareaEl = ref<HTMLTextAreaElement | null>(null);
const historyOpen = ref(false);

const MAX_HEIGHT_PX = 132; // ~5 行

async function autoGrow(): Promise<void> {
  await nextTick();
  const el = textareaEl.value;
  if (!el) return;
  el.style.height = 'auto';
  el.style.height = `${Math.min(el.scrollHeight, MAX_HEIGHT_PX)}px`;
}

watch(
  () => props.draft,
  () => autoGrow(),
);

function onInput(ev: Event): void {
  emit('update:draft', (ev.target as HTMLTextAreaElement).value);
}

function onSend(): void {
  emit('send');
}

function onReuse(text: string): void {
  emit('reuse', text);
  historyOpen.value = false;
}
</script>

<template>
  <div class="composer">
    <p v-if="blockReason" class="composer-block-reason" role="note">{{ blockReason }}</p>

    <!-- outbox 可视条（M3-C）：待确认/自动重发反馈/停发态，如实呈现不伪造 confirmed -->
    <div
      v-if="outbox && (outbox.pendingCount > 0 || outbox.haltedCount > 0)"
      class="outbox-strip"
      role="status"
      aria-live="polite"
      data-testid="outbox-strip"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M22 2 11 13" /><path d="M22 2 15 22l-4-9-9-4z" />
      </svg>
      <span v-if="outbox.resending" class="outbox-text outbox-text--resending">
        已恢复，正在自动重发 {{ outbox.pendingCount }} 条未确认指令…
      </span>
      <span v-else-if="outbox.pendingCount > 0" class="outbox-text">
        {{ outbox.pendingCount }} 条指令待确认<template v-if="outbox.maxAttemptNo > 1">（第 {{ outbox.maxAttemptNo }} 次尝试）</template>
      </span>
      <span v-if="outbox.exhaustedUnconfirmed" class="outbox-text outbox-text--warn">
        已达重试上限仍未确认：发送状态未知，请人工判断后重发
      </span>
      <span v-if="outbox.haltedCount > 0" class="outbox-text outbox-text--warn">
        {{ outbox.haltedCount }} 条指令已停止发送（控制权已变化），内容保留在时间线
      </span>
    </div>

    <div v-if="historyOpen && history.length > 0" class="history-panel" role="menu" aria-label="历史指令">
      <button
        v-for="(cmd, i) in history"
        :key="i"
        type="button"
        class="history-item"
        role="menuitem"
        @click="onReuse(cmd)"
      >
        {{ cmd }}
      </button>
    </div>

    <div class="composer-row">
      <button
        type="button"
        class="composer-history"
        aria-label="历史指令"
        :aria-expanded="historyOpen"
        :disabled="history.length === 0"
        @click="historyOpen = !historyOpen"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M3 3v5h5" /><path d="M3.05 13A9 9 0 1 0 6 5.3L3 8" /><path d="M12 7v5l4 2" />
        </svg>
      </button>
      <textarea
        ref="textareaEl"
        class="composer-input"
        :value="draft"
        :disabled="!canWrite"
        rows="1"
        placeholder="输入指令…"
        aria-label="输入指令"
        @input="onInput"
      ></textarea>
      <button
        type="button"
        class="composer-send"
        :disabled="!canWrite || sending || draft.trim().length === 0"
        @click="onSend"
      >
        {{ sending ? '发送中' : '发送' }}
      </button>
      <button
        v-if="canControl"
        type="button"
        class="composer-stop"
        :disabled="stopping"
        @click="emit('stop')"
      >
        停止运行
      </button>
    </div>
  </div>
</template>

<style scoped>
.composer {
  /* M4-R3（谛听 M4-006）：显式不收缩——窄屏首次 Guide 态下由 Guide/时间线
     收缩让位，Composer（含停止运行）必须完整保持在视口底部。 */
  flex-shrink: 0;
  border-top: 1px solid var(--VT-border);
  background: var(--VT-canvas);
  padding: 8px 12px calc(8px + env(safe-area-inset-bottom, 0px));
  /* M4-A safe-area：横屏下 Composer 不贴刘海边（左右内边距） */
  padding-left: calc(12px + env(safe-area-inset-left, 0px));
  padding-right: calc(12px + env(safe-area-inset-right, 0px));
}

/* M4-A 横屏紧凑模式：矮视口压缩输入区节奏，保持 44px 目标不变。 */
@media (orientation: landscape) and (max-height: 500px) {
  .composer {
    padding-top: 4px;
    padding-bottom: calc(4px + env(safe-area-inset-bottom, 0px));
  }
  .history-panel {
    max-height: 30vh;
  }
}

.composer-block-reason {
  margin: 0 0 6px;
  font-size: 12px;
  color: var(--VT-text-secondary);
  line-height: 1.5;
}

.outbox-strip {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 8px;
  margin: 0 0 6px;
  padding: 6px 10px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-warning);
  border-radius: 8px;
  font-size: 12px;
  color: var(--VT-text);
  line-height: 1.5;
}

.outbox-strip > svg {
  flex-shrink: 0;
  color: var(--VT-warning);
}

.outbox-text--resending {
  color: var(--VT-accent-strong);
  font-weight: 600;
}

.outbox-text--warn {
  color: var(--VT-warning);
  font-weight: 600;
}

.history-panel {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 40vh;
  overflow-y: auto;
  margin-bottom: 8px;
  padding: 6px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-radius: 10px;
}

.history-item {
  min-height: 44px;
  padding: 6px 10px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--VT-text);
  font-size: 13px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  text-align: left;
  word-break: break-word;
  cursor: pointer;
}

@media (hover: hover) {
  .history-item:hover {
    background: var(--VT-surface-raised);
  }
}

.history-item:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.composer-row {
  display: flex;
  align-items: flex-end;
  gap: 8px;
}

.composer-history {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 44px;
  min-height: 44px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  background: transparent;
  color: var(--VT-text);
  cursor: pointer;
}

.composer-history:disabled {
  color: var(--VT-text-disabled);
  border-color: var(--VT-border);
  cursor: not-allowed;
}

.composer-input {
  flex: 1;
  min-width: 0;
  min-height: 44px;
  max-height: 132px;
  padding: 10px 12px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 10px;
  background: var(--VT-surface);
  color: var(--VT-text);
  font-size: 14px;
  line-height: 1.5;
  font-family: inherit;
  resize: none;
  box-sizing: border-box;
}

.composer-input:disabled {
  color: var(--VT-text-disabled);
  background: var(--VT-surface);
  border-color: var(--VT-border);
}

.composer-input:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 1px;
}

.composer-send {
  flex-shrink: 0;
  min-height: 44px;
  min-width: 44px;
  padding: 0 16px;
  border: none;
  border-radius: 8px;
  background: var(--VT-accent-strong);
  color: var(--VT-canvas);
  font-size: 14px;
  font-weight: 700;
  cursor: pointer;
}

.composer-send:disabled {
  background: var(--VT-border);
  color: var(--VT-text-disabled);
  cursor: not-allowed;
}

.composer-stop {
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

.composer-stop:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.composer-history:focus-visible,
.composer-send:focus-visible,
.composer-stop:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

/* M4-R2（谛听 M4-006）：≤240px 超窄逻辑视口（含 200% 缩放等效 180px）
   Composer 防裁切。单行 flex 放不下 历史(44)+输入+发送(min-content~60)
   +停止(min-content~80)+三 gap——flex 溢出被右缘裁切且不产生文档级
   scrollWidth（实测 180px 停止控制被裁，谛听 R2 读图取证）。拆三层：
   输入独占首行（order -1 + 100% basis），历史+发送第二行，停止独占
   第三行（danger 主操作不缩字不缩高，保持 ≥44px 与完整文案）。 */
@media (max-width: 240px) {
  .composer-row {
    flex-wrap: wrap;
  }
  .composer-input {
    flex: 1 1 100%;
    order: -1;
  }
  .composer-send {
    flex: 1 1 auto;
  }
  .composer-stop {
    flex: 1 1 100%;
  }
}
</style>
