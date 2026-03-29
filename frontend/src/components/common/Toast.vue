<template>
  <Teleport to="body">
    <div class="toast-container">
      <TransitionGroup name="toast-list">
        <div
          v-for="toast in toasts"
          :key="toast.id"
          class="toast-item"
          :class="`toast-${toast.type}`"
        >
          <div class="toast-icon">
            <svg v-if="toast.type === 'success'" viewBox="0 0 24 24" width="20" height="20" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="20 6 9 17 4 12"></polyline>
            </svg>
            <svg v-else-if="toast.type === 'error'" viewBox="0 0 24 24" width="20" height="20" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="10"></circle>
              <line x1="15" y1="9" x2="9" y2="15"></line>
              <line x1="9" y1="9" x2="15" y2="15"></line>
            </svg>
            <svg v-else viewBox="0 0 24 24" width="20" height="20" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="10"></circle>
              <line x1="12" y1="16" x2="12" y2="12"></line>
              <line x1="12" y1="8" x2="12.01" y2="8"></line>
            </svg>
          </div>
          <div class="toast-content">{{ toast.message }}</div>
          <button class="toast-close" @click="removeToast(toast.id)">
            <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
              <line x1="18" y1="6" x2="6" y2="18"></line>
              <line x1="6" y1="6" x2="18" y2="18"></line>
            </svg>
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { useToast } from '../../composables/useToast';

const { toasts, removeToast } = useToast();
</script>

<style scoped>
.toast-container {
  position: fixed;
  top: 20px;
  right: 20px;
  z-index: 9999;
  display: flex;
  flex-direction: column;
  gap: 10px;
  pointer-events: none;
}

.toast-item {
  display: flex;
  align-items: center;
  padding: 12px 16px;
  border-radius: 8px;
  background-color: #1a1f2e;
  color: #fff;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
  pointer-events: auto;
  min-width: 280px;
  max-width: 400px;
  border-left: 4px solid transparent;
}

.toast-success {
  border-left-color: #4fc3f7;
}
.toast-success .toast-icon {
  color: #4fc3f7;
}

.toast-error {
  border-left-color: #ef5350;
}
.toast-error .toast-icon {
  color: #ef5350;
}

.toast-info {
  border-left-color: #42a5f5;
}
.toast-info .toast-icon {
  color: #42a5f5;
}

.toast-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  margin-right: 12px;
  flex-shrink: 0;
}

.toast-content {
  flex: 1;
  font-size: 14px;
  line-height: 1.4;
  word-break: break-word;
}

.toast-close {
  background: transparent;
  border: none;
  color: #888;
  cursor: pointer;
  padding: 4px;
  margin-left: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 4px;
  transition: all 0.2s;
}

.toast-close:hover {
  color: #fff;
  background-color: rgba(255, 255, 255, 0.1);
}

/* Transition animations */
.toast-list-enter-active,
.toast-list-leave-active {
  transition: all 0.3s ease;
}

.toast-list-enter-from {
  opacity: 0;
  transform: translateX(30px);
}

.toast-list-leave-to {
  opacity: 0;
  transform: translateX(30px);
}
</style>
