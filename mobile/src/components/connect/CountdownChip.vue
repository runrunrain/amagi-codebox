<script setup lang="ts">
/**
 * CountdownChip — 配对窗口倒计时芯片（PG-01 配对窗口状态区）
 * 等宽数字（tabular-nums），mm:ss；颜色+文字+图标三通道；<60s 转警告色。
 * isEstimate=true 时表示服务端未给出 expiresAt，按 P6 暂定窗口（3min）本地估算，
 * 文案诚实标注"预计"——真实过期以服务端 auth.window_expired 为准。
 */
import { computed } from 'vue';

interface Props {
  remainingMs: number;
  isEstimate?: boolean;
}

const props = withDefaults(defineProps<Props>(), { isEstimate: false });

const text = computed(() => {
  const totalSeconds = Math.max(0, Math.ceil(props.remainingMs / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
});

const urgent = computed(() => props.remainingMs > 0 && props.remainingMs < 60_000);
</script>

<template>
  <div class="countdown-chip" :class="{ 'countdown-chip--urgent': urgent }" role="timer" aria-live="off">
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
      <circle cx="12" cy="12" r="10" />
      <polyline points="12 6 12 12 16 14" />
    </svg>
    <span class="countdown-label">{{ isEstimate ? '配对窗口预计剩余' : '配对窗口剩余' }}</span>
    <span class="countdown-time">{{ text }}</span>
  </div>
</template>

<style scoped>
.countdown-chip {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 44px;
  padding: 0 16px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-radius: 999px;
  color: var(--VT-text);
  font-size: 14px;
}

.countdown-chip--urgent {
  border-color: var(--VT-warning);
  color: var(--VT-warning);
}

.countdown-label {
  font-weight: 500;
}

.countdown-time {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace;
  font-variant-numeric: tabular-nums;
  font-size: 16px;
  font-weight: 700;
  letter-spacing: 0.04em;
}
</style>
