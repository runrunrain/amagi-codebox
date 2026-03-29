import { reactive } from 'vue';

export type ToastType = 'success' | 'error' | 'info';

export interface Toast {
  id: number;
  message: string;
  type: ToastType;
  duration: number;
}

// 全局单例状态
const toasts = reactive<Toast[]>([]);
let nextId = 0;

export function useToast() {
  const removeToast = (id: number) => {
    const index = toasts.findIndex(t => t.id === id);
    if (index > -1) {
      toasts.splice(index, 1);
    }
  };

  const showToast = (message: string, type: ToastType = 'info', duration: number = 3000) => {
    const id = nextId++;
    toasts.push({ id, message, type, duration });

    if (duration > 0) {
      setTimeout(() => {
        removeToast(id);
      }, duration);
    }
  };

  const showSuccess = (message: string, duration?: number) => showToast(message, 'success', duration);
  const showError = (message: string, duration?: number) => showToast(message, 'error', duration);
  const showInfo = (message: string, duration?: number) => showToast(message, 'info', duration);

  return {
    toasts,
    showSuccess,
    showError,
    showInfo,
    removeToast
  };
}
