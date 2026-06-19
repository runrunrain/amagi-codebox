<!--
  ConfirmDialog - 通用确认弹窗（对照交接说明 §8.2 项17）。
  基于 Dialog.vue，支持 danger 模式（删除类操作）。
  emit confirm/cancel，父组件通过 v-model:open 控制显示。
-->
<template>
  <Dialog
    :open="open"
    :title="title"
    :description="message"
    @update:open="handleClose"
  >
    <div class="confirm-dialog-body">
      <p class="confirm-message">{{ message }}</p>
    </div>
    <template #footer>
      <div class="confirm-actions">
        <AppButton variant="ghost" @click="handleCancel">{{ cancelText }}</AppButton>
        <AppButton
          :variant="danger ? 'danger' : 'primary'"
          @click="handleConfirm"
        >{{ confirmText }}</AppButton>
      </div>
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import Dialog from './Dialog.vue';
import AppButton from './AppButton.vue';

interface Props {
  open?: boolean;
  title?: string;
  message?: string;
  danger?: boolean;
  confirmText?: string;
  cancelText?: string;
}

const props = withDefaults(defineProps<Props>(), {
  open: false,
  title: '确认',
  message: '',
  danger: false,
  confirmText: '确认',
  cancelText: '取消',
});

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void;
  (e: 'confirm'): void;
  (e: 'cancel'): void;
}>();

function handleClose() {
  emit('update:open', false);
}

function handleConfirm() {
  emit('confirm');
  handleClose();
}

function handleCancel() {
  emit('cancel');
  handleClose();
}
</script>

<style scoped>
.confirm-dialog-body {
  padding: 4px 0;
}

.confirm-message {
  margin: 0;
  color: var(--secondary);
  line-height: 1.6;
  font-size: 14px;
}

.confirm-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
