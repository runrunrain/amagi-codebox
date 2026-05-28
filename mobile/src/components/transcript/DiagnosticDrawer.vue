<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { DiagnosticRecordV2 } from '../../types/transcript'

const props = defineProps<{
  open: boolean
  records: DiagnosticRecordV2[]
  count: number
  returnFocusTo?: HTMLElement | null
}>()

const emit = defineEmits<{ close: [] }>()

const titleId = 'diagnostic-drawer-title'
const drawerRef = ref<HTMLElement>()
const closeButtonRef = ref<HTMLButtonElement>()
let previouslyFocusedElement: HTMLElement | null = null

function getFocusableElements(): HTMLElement[] {
  if (!drawerRef.value) return []

  return Array.from(drawerRef.value.querySelectorAll<HTMLElement>([
    'a[href]',
    'button:not([disabled])',
    'textarea:not([disabled])',
    'input:not([disabled])',
    'select:not([disabled])',
    '[tabindex]:not([tabindex="-1"])',
  ].join(','))).filter((element) => !element.hasAttribute('aria-hidden'))
}

function focusInitialElement() {
  const target = closeButtonRef.value ?? drawerRef.value
  target?.focus()
}

function restoreFocus() {
  const target = props.returnFocusTo ?? previouslyFocusedElement
  if (target && document.contains(target)) {
    target.focus()
  }
}

function requestClose() {
  emit('close')
}

function trapTabFocus(event: KeyboardEvent) {
  const focusableElements = getFocusableElements()
  if (focusableElements.length === 0) {
    event.preventDefault()
    drawerRef.value?.focus()
    return
  }

  const first = focusableElements[0]
  const last = focusableElements[focusableElements.length - 1]
  const active = document.activeElement

  if (event.shiftKey && active === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && active === last) {
    event.preventDefault()
    first.focus()
  }
}

function onDialogKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    requestClose()
    return
  }

  if (event.key === 'Tab') {
    trapTabFocus(event)
  }
}

watch(() => props.open, async (open, wasOpen) => {
  if (open) {
    previouslyFocusedElement = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null
    await nextTick()
    focusInitialElement()
    return
  }

  if (wasOpen) {
    await nextTick()
    restoreFocus()
  }
}, { immediate: true })

onBeforeUnmount(() => {
  if (props.open) {
    restoreFocus()
  }
})
</script>

<template>
  <div
    v-if="open"
    class="diagnostic-drawer-backdrop"
    @click.self="requestClose"
  >
    <aside
      ref="drawerRef"
      class="diagnostic-drawer"
      role="dialog"
      aria-modal="true"
      :aria-labelledby="titleId"
      tabindex="-1"
      @keydown="onDialogKeydown"
    >
      <header class="diagnostic-drawer-header">
        <div>
          <h2 :id="titleId">诊断详情</h2>
          <p>{{ count }} 条诊断事件，按原因合并显示。</p>
        </div>
        <button
          ref="closeButtonRef"
          type="button"
          class="diagnostic-drawer-close"
          aria-label="关闭诊断详情"
          @click="requestClose"
        >关闭</button>
      </header>
      <div v-if="records.length === 0" class="diagnostic-drawer-empty">暂无诊断详情。</div>
      <article v-for="record in records" :key="record.id" class="diagnostic-drawer-item" :class="`diagnostic-drawer-item--${record.visibility}`">
        <header>
          <strong>{{ record.reason }}</strong>
          <span>{{ record.visibility }} · {{ record.severity }} · ×{{ record.count }}</span>
        </header>
        <p>{{ record.summary }}</p>
        <pre v-if="record.preview">{{ record.preview }}</pre>
        <small v-if="record.redacted">预览中已隐藏敏感片段</small>
      </article>
    </aside>
  </div>
</template>

<style scoped>
.diagnostic-drawer-backdrop {
  position: fixed;
  inset: 0;
  z-index: 40;
  background: rgba(1, 4, 9, 0.58);
  display: flex;
  align-items: flex-end;
}

.diagnostic-drawer {
  width: 100%;
  max-height: 72vh;
  overflow: auto;
  border-radius: 18px 18px 0 0;
  border: 1px solid rgba(139, 148, 158, 0.28);
  background: #0d1117;
  color: #e6edf3;
  padding: 14px;
  box-shadow: 0 -18px 60px rgba(0, 0, 0, 0.35);
}

.diagnostic-drawer-header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: flex-start;
  margin-bottom: 12px;
}

.diagnostic-drawer-header h2,
.diagnostic-drawer-header p {
  margin: 0;
}

.diagnostic-drawer-header p {
  margin-top: 4px;
  color: #8b949e;
  font-size: 12px;
}

.diagnostic-drawer-close {
  border: 1px solid rgba(139, 148, 158, 0.28);
  background: rgba(22, 27, 34, 0.9);
  color: #c9d1d9;
  border-radius: 999px;
  padding: 6px 12px;
}

.diagnostic-drawer-empty,
.diagnostic-drawer-item {
  border-radius: 12px;
  border: 1px solid rgba(139, 148, 158, 0.22);
  background: rgba(22, 27, 34, 0.78);
  padding: 12px;
}

.diagnostic-drawer-item + .diagnostic-drawer-item {
  margin-top: 10px;
}

.diagnostic-drawer-item--error-card {
  border-color: rgba(248, 81, 73, 0.36);
}

.diagnostic-drawer-item header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  color: #ffd866;
  font-size: 12px;
}

.diagnostic-drawer-item pre {
  max-height: 180px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  color: #8b949e;
}

.diagnostic-drawer-item small {
  color: #8b949e;
}
</style>
