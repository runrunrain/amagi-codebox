<template>
  <Teleport to="body">
    <Transition name="dialog">
      <div v-if="open" class="dialog-overlay" @click="handleOverlayClick" @contextmenu.self.prevent>
        <div class="dialog-container" @click.stop>
          <div class="dialog-header">
            <h3 v-if="title" class="dialog-title">{{ title }}</h3>
            <p v-if="description" class="dialog-description">{{ description }}</p>
            <button class="dialog-close" @click="close">
              <span>×</span>
            </button>
          </div>
          <div class="dialog-body">
            <slot />
          </div>
          <div v-if="$slots.footer" class="dialog-footer">
            <slot name="footer" />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
interface Props {
  open?: boolean;
  title?: string;
  description?: string;
  closeOnOverlay?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  open: false,
  title: '',
  description: '',
  // 默认改为 false：遮罩点击不再关闭弹窗，只能通过 × / 取消 / 确认等按钮关闭。
  // 避免用户在弹窗外误点（含右键）意外丢失输入或关闭重要确认框。prop 保留以兼容已有传参。
  closeOnOverlay: false,
});

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void;
  (e: 'close'): void;
}>();

function close() {
  emit('update:open', false);
  emit('close');
}

function handleOverlayClick() {
  if (props.closeOnOverlay) {
    close();
  }
}

// Expose close method
defineExpose({
  close,
});
</script>

<style scoped>
.dialog-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  padding: 20px;
}

.dialog-container {
  background: var(--card);
  border-radius: 12px;
  box-shadow: 0 20px 40px rgba(0, 0, 0, 0.2);
  max-width: 500px;
  width: 100%;
  max-height: 90vh;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.dialog-header {
  position: relative;
  padding: 20px 24px 16px;
  border-bottom: 1px solid var(--separator);
}

.dialog-title {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin: 0 0 4px 0;
  padding-right: 32px;
}

.dialog-description {
  font-size: 13px;
  color: var(--secondary);
  margin: 0;
  padding-right: 32px;
  line-height: 1.5;
}

.dialog-close {
  position: absolute;
  top: 20px;
  right: 20px;
  width: 28px;
  height: 28px;
  border: none;
  background: var(--control);
  border-radius: 6px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 18px;
  color: var(--secondary);
  transition: all 0.15s;
}

.dialog-close:hover {
  background: var(--controlHover);
  color: var(--label);
}

.dialog-body {
  padding: 20px 24px;
  overflow-y: auto;
  flex: 1;
}

.dialog-footer {
  padding: 16px 24px;
  border-top: 1px solid var(--separator);
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

/* Transition */
.dialog-enter-active,
.dialog-leave-active {
  transition: opacity 0.2s;
}

.dialog-enter-active .dialog-container,
.dialog-leave-active .dialog-container {
  transition: transform 0.2s, opacity 0.2s;
}

.dialog-enter-from,
.dialog-leave-to {
  opacity: 0;
}

.dialog-enter-from .dialog-container,
.dialog-leave-to .dialog-container {
  transform: scale(0.95);
  opacity: 0;
}

/* Scrollbar styling */
.dialog-body::-webkit-scrollbar {
  width: 8px;
}

.dialog-body::-webkit-scrollbar-track {
  background: transparent;
}

.dialog-body::-webkit-scrollbar-thumb {
  background: var(--separator);
  border-radius: 4px;
}

.dialog-body::-webkit-scrollbar-thumb:hover {
  background: var(--tertiary);
}
</style>
