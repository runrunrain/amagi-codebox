<!--
  ControlBadge（RC3-3 桌面端互联 · 控制权四态徽标）
  视觉风格 §4 冻结规则：四态有且仅有四套表达，同一语义任何页面同一表达——
  none（无标记，不渲染）/ you（绿「你持有」）/ other（琥珀「他人持有」）/
  desktop（蓝「桌面持有」）。带文字 + 点状图标，不靠颜色单独表达（色弱可读）。
  使用处：远程会话列表行、远程终端状态条。数据源 = store 控制权投影。
-->
<template>
  <span
    v-if="tone !== 'none'"
    class="control-badge"
    :class="`ctl-${tone}`"
    :title="tooltip"
  >
    <span class="cb-dot" aria-hidden="true" />
    <span class="cb-text">{{ label }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { controlStateLabel, controlStateTone } from './remoteClientShared';

const props = withDefaults(defineProps<{ state: string; deviceName?: string }>(), {
  deviceName: '',
});

const tone = computed(() => controlStateTone(props.state));
const label = computed(() => controlStateLabel(props.state));
const tooltip = computed(() => {
  if (props.state === 'other' && props.deviceName) {
    return `控制权由「${props.deviceName}」持有`;
  }
  if (props.state === 'desktop') return '控制权由桌面端持有，本终端只读';
  return label.value;
});
</script>

<style scoped>
.control-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 999px;
  white-space: nowrap;
  flex-shrink: 0;
}

.cb-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}

/* you：绿（成功色系） */
.control-badge.ctl-you {
  color: var(--success-strong);
  background: rgba(52, 199, 89, 0.14);
}
.control-badge.ctl-you .cb-dot {
  background: var(--success);
}

/* other：琥珀（警告色系） */
.control-badge.ctl-other {
  color: var(--warning-strong);
  background: rgba(255, 149, 0, 0.14);
}
.control-badge.ctl-other .cb-dot {
  background: var(--warning, #ff9500);
}

/* desktop：蓝（强调色系） */
.control-badge.ctl-desktop {
  color: var(--accent-strong);
  background: rgba(0, 122, 255, 0.1);
}
.control-badge.ctl-desktop .cb-dot {
  background: var(--accent);
}
</style>
