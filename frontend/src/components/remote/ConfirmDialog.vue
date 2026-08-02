<!--
  ConfirmDialog（PG-06 确认对话系统模板 · 远程控制中心专用）
  契约：危险图标 + 一句话后果 + 不可逆性说明 + 动词化危险主按钮（VT-danger）+ 取消；
  焦点圈闭；Esc 取消；提交中防连点。与共享 ui/ConfirmDialog.vue 区分：本组件实现
  PG-06 完整结构（后果/不可逆分行、焦点圈闭、Esc、assertive 读屏），不动共享组件。
-->
<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="pg06-overlay"
      role="presentation"
      @mousedown.self="onCancel"
    >
      <div
        ref="dialogRef"
        class="pg06-dialog remote-cc"
        role="alertdialog"
        aria-modal="true"
        :aria-labelledby="titleId"
        :aria-describedby="descId"
        @keydown="onKeydown"
      >
        <div class="pg06-head">
          <span class="pg06-icon" aria-hidden="true">
            <svg width="22" height="22" viewBox="0 0 22 22" fill="none">
              <path
                d="M11 2.5 20 18.5H2L11 2.5Z"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linejoin="round"
              />
              <line x1="11" y1="8.5" x2="11" y2="13" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
              <circle cx="11" cy="15.8" r="1.1" fill="currentColor" />
            </svg>
          </span>
          <h3 :id="titleId" class="pg06-title">{{ title }}</h3>
        </div>

        <div :id="descId" class="pg06-body">
          <p class="pg06-consequence">{{ consequence }}</p>
          <p class="pg06-irreversible">{{ irreversibleNote }}</p>
        </div>

        <div class="pg06-actions">
          <button
            ref="cancelRef"
            type="button"
            class="pg06-btn pg06-cancel"
            :disabled="busy"
            @click="onCancel"
          >
            {{ cancelText }}
          </button>
          <button
            type="button"
            class="pg06-btn pg06-danger"
            :disabled="busy"
            :aria-busy="busy"
            @click="onConfirm"
          >
            {{ busy ? busyText : confirmText }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch, nextTick, onUnmounted } from 'vue';

interface Props {
  open: boolean;
  title: string;
  /** 一句话后果（如"该设备将立即失去访问"） */
  consequence: string;
  /** 不可逆性说明 */
  irreversibleNote: string;
  /** 动词化危险按钮文字（如"撤销设备"） */
  confirmText: string;
  cancelText?: string;
  /** 提交中：按钮置提交态防连点 */
  busy?: boolean;
  busyText?: string;
}

const props = withDefaults(defineProps<Props>(), {
  cancelText: '取消',
  busy: false,
  busyText: '处理中…',
});

const emit = defineEmits<{
  (e: 'confirm'): void;
  (e: 'cancel'): void;
}>();

const dialogRef = ref<HTMLElement | null>(null);
const cancelRef = ref<HTMLButtonElement | null>(null);
const titleId = `pg06-title-${Math.random().toString(36).slice(2, 8)}`;
const descId = `pg06-desc-${Math.random().toString(36).slice(2, 8)}`;

let previousActive: Element | null = null;

watch(
  () => props.open,
  async (val) => {
    if (val) {
      previousActive = document.activeElement;
      await nextTick();
      // 安全默认：初始焦点落在"取消"，危险按钮需用户主动移焦
      cancelRef.value?.focus();
    } else if (previousActive instanceof HTMLElement) {
      previousActive.focus();
      previousActive = null;
    }
  },
);

function focusables(): HTMLElement[] {
  if (!dialogRef.value) return [];
  return Array.from(
    dialogRef.value.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  );
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault();
    if (!props.busy) onCancel();
    return;
  }
  if (e.key !== 'Tab') return;
  // 焦点圈闭：Tab/Shift+Tab 在对话框内循环
  const list = focusables();
  if (list.length === 0) return;
  const first = list[0];
  const last = list[list.length - 1];
  const active = document.activeElement as HTMLElement | null;
  if (e.shiftKey) {
    if (!active || active === first || !dialogRef.value?.contains(active)) {
      e.preventDefault();
      last.focus();
    }
  } else if (!active || active === last || !dialogRef.value?.contains(active)) {
    e.preventDefault();
    first.focus();
  }
}

function onConfirm() {
  if (props.busy) return;
  emit('confirm');
}

function onCancel() {
  emit('cancel');
}

onUnmounted(() => {
  if (previousActive instanceof HTMLElement) previousActive.focus();
});
</script>

<style scoped>
.pg06-overlay {
  position: fixed;
  inset: 0;
  background: rgba(31, 30, 27, 0.42);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1200;
  padding: 20px;
}

.pg06-dialog {
  width: 100%;
  max-width: 440px;
  background: var(--vt-surface);
  border: 1px solid var(--vt-border);
  border-radius: 12px;
  padding: 22px 24px;
  font-family: var(--vt-font-ui);
  outline: none;
}

.pg06-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}

.pg06-icon {
  color: var(--vt-danger);
  display: inline-flex;
  flex-shrink: 0;
}

.pg06-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--vt-text);
}

.pg06-body {
  margin-bottom: 20px;
}

.pg06-consequence {
  margin: 0 0 8px;
  font-size: 14px;
  line-height: 1.55;
  color: var(--vt-text);
}

.pg06-irreversible {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--vt-text-secondary);
}

.pg06-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.pg06-btn {
  min-height: 44px;
  min-width: 88px;
  padding: 10px 18px;
  border-radius: 10px;
  font-size: 14px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: background 0.15s ease;
}

.pg06-btn:focus-visible {
  outline: 2px solid var(--vt-accent);
  outline-offset: 2px;
}

.pg06-cancel {
  background: transparent;
  border: 1px solid var(--vt-border-strong);
  color: var(--vt-text);
}

.pg06-cancel:hover:not(:disabled) {
  background: var(--vt-surface-raised);
}

.pg06-danger {
  background: var(--vt-danger);
  border: 1px solid var(--vt-danger);
  color: #fff;
}

.pg06-danger:hover:not(:disabled) {
  filter: brightness(0.94);
}

.pg06-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

@media (prefers-reduced-motion: reduce) {
  .pg06-btn {
    transition: none;
  }
}
</style>
