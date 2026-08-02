<!--
  DeviceRow（P5 §6 组件契约）：名称 / 配对时间 / 撤销按钮（危险描边，≥44px）。
-->
<template>
  <div class="device-row" :data-device-id="device.id">
    <div class="device-info">
      <span class="device-name">{{ device.name || '未命名设备' }}</span>
      <span class="device-meta">
        配对于 {{ pairedAtText }}
        <template v-if="device.lastSeenAt"> · 最近活动 {{ lastSeenText }}</template>
      </span>
    </div>
    <button
      type="button"
      class="rc-btn rc-btn-danger-outline device-revoke"
      :data-testid="`revoke-${device.id}`"
      :disabled="busy"
      @click="$emit('revoke', device)"
    >
      撤销
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { remote } from '../../../wailsjs/go/models';
import { formatDateTime } from './remoteShared';

interface Props {
  device: remote.DeviceInfo;
  busy?: boolean;
}

const props = withDefaults(defineProps<Props>(), { busy: false });

defineEmits<{
  (e: 'revoke', device: remote.DeviceInfo): void;
}>();

const pairedAtText = computed(() => formatDateTime(props.device.pairedAt));
const lastSeenText = computed(() => formatDateTime(props.device.lastSeenAt));
</script>

<style scoped>
.device-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  min-height: 44px;
  padding: 10px 0;
  border-top: 1px solid var(--vt-border);
}

.device-row:first-child {
  border-top: none;
}

.device-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.device-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--vt-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.device-meta {
  font-size: 12px;
  color: var(--vt-text-secondary);
  font-variant-numeric: tabular-nums;
}

.device-revoke {
  flex-shrink: 0;
}
</style>
