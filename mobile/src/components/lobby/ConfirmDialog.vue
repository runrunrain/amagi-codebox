<script setup lang="ts">
/**
 * ConfirmDialog — PG-06 危险操作确认对话（M2-B）
 * ---------------------------------------------------------------------------
 * 契约（P5 §6 组件契约 / Task Contract M2-B）：
 *   · 动词化确认按钮（「停止会话」而非「确定」）+ 后果说明 + 不可逆标记；
 *   · 焦点圈闭（Tab/Shift+Tab 循环）+ Esc 取消 + 打开时焦点落在取消按钮（安全默认）；
 *   · 提交态（submitting）禁用双按钮防连点；
 *   · role="alertdialog" + aria-modal + aria-labelledby/describedby。
 * ---------------------------------------------------------------------------
 */
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue';

const props = defineProps<{
  title: string;
  /** 动词化确认按钮文案，如「停止会话」。 */
  verb: string;
  /** 后果说明（逐条）。 */
  consequences: string[];
  /** 不可逆操作标记（移除）：追加醒目说明。 */
  irreversible?: boolean;
  /** 提交中：禁用按钮防连点。 */
  submitting?: boolean;
}>();

const emit = defineEmits<{
  confirm: [];
  cancel: [];
}>();

const dialogRef = ref<HTMLElement | null>(null);
const cancelRef = ref<HTMLButtonElement | null>(null);

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    if (!props.submitting) emit('cancel');
    return;
  }
  if (event.key !== 'Tab') return;
  // 焦点圈闭：仅在对话框内的可聚焦元素间循环。
  const root = dialogRef.value;
  if (!root) return;
  const focusables = Array.from(
    root.querySelectorAll<HTMLElement>('button:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'),
  );
  if (focusables.length === 0) return;
  const first = focusables[0];
  const last = focusables[focusables.length - 1];
  const active = document.activeElement;
  if (event.shiftKey && active === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && active === last) {
    event.preventDefault();
    first.focus();
  }
}

onMounted(async () => {
  await nextTick();
  // 安全默认：焦点落在取消按钮，避免误触危险动词。
  cancelRef.value?.focus();
  document.addEventListener('keydown', onKeydown, true);
});

onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown, true);
});
</script>

<template>
  <Teleport to="body">
    <div class="confirm-overlay" @click.self="!submitting && emit('cancel')">
      <div
        ref="dialogRef"
        class="confirm-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        aria-describedby="confirm-desc"
      >
        <h2 id="confirm-title" class="confirm-title">{{ title }}</h2>
        <div id="confirm-desc" class="confirm-body">
          <ul class="consequence-list">
            <li v-for="(line, i) in consequences" :key="i">{{ line }}</li>
          </ul>
          <p v-if="irreversible" class="irreversible-note">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
              <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
              <line x1="12" y1="9" x2="12" y2="13" />
              <line x1="12" y1="17" x2="12.01" y2="17" />
            </svg>
            此操作不可撤销，执行后无法恢复。
          </p>
        </div>
        <div class="confirm-actions">
          <button ref="cancelRef" type="button" class="btn-cancel" :disabled="submitting" @click="emit('cancel')">
            取消
          </button>
          <button type="button" class="btn-danger" :disabled="submitting" @click="emit('confirm')">
            {{ submitting ? '执行中…' : verb }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.confirm-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: flex-end;
  justify-content: center;
  z-index: 300;
}

.confirm-dialog {
  width: 100%;
  background: var(--VT-canvas);
  border-radius: 16px 16px 0 0;
  padding: 20px 20px calc(20px + env(safe-area-inset-bottom, 0));
  border-top: 1px solid var(--VT-border);
}

.confirm-title {
  margin: 0 0 12px;
  font-size: 17px;
  font-weight: 700;
  color: var(--VT-text);
}

.consequence-list {
  margin: 0;
  padding-left: 20px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 14px;
  line-height: 1.55;
  color: var(--VT-text);
}

.irreversible-note {
  margin: 12px 0 0;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: var(--VT-danger);
}

.confirm-actions {
  display: flex;
  gap: 10px;
  margin-top: 20px;
}

.btn-cancel,
.btn-danger {
  flex: 1;
  min-height: 44px;
  border-radius: 10px;
  font-size: 16px;
  font-weight: 600;
  cursor: pointer;
}

.btn-cancel {
  border: 1px solid var(--VT-border-strong);
  background: transparent;
  color: var(--VT-text);
}

.btn-danger {
  border: none;
  background: var(--VT-danger);
  /* VT-on-dark 白字对 VT-danger 复算 5.38:1（≥4.5 PASS） */
  color: var(--VT-on-dark);
}

.btn-cancel:disabled,
.btn-danger:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.btn-cancel:focus-visible,
.btn-danger:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

@media (hover: hover) {
  .btn-cancel:hover:not(:disabled) {
    background: var(--VT-surface-raised);
  }
  .btn-danger:hover:not(:disabled) {
    background: var(--VT-accent-strong);
  }
}

@media (hover: hover) and (pointer: fine) {
  .confirm-overlay {
    align-items: center;
  }
  .confirm-dialog {
    max-width: 440px;
    border-radius: 16px;
    border: 1px solid var(--VT-border);
  }
}

@media (prefers-reduced-motion: reduce) {
  .confirm-overlay,
  .confirm-dialog {
    transition: none;
  }
}
</style>
