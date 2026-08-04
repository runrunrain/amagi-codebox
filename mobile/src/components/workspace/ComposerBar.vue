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
  border-top: 1px solid var(--VT-border);
  background: var(--VT-canvas);
  padding: 8px 12px calc(8px + env(safe-area-inset-bottom, 0px));
}

.composer-block-reason {
  margin: 0 0 6px;
  font-size: 12px;
  color: var(--VT-text-secondary);
  line-height: 1.5;
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
</style>
