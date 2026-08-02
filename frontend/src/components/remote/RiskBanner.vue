<!--
  RiskBanner（P5 §6 组件契约 / P4 §6.1 风险提示面板）
  VT-warning 4px 左边条 + 警告图标 + 内容；启用类风险不可关成"不再提示"（无关闭钮）。
-->
<template>
  <div class="risk-banner" role="note">
    <span class="risk-icon" aria-hidden="true">
      <svg width="18" height="18" viewBox="0 0 18 18" fill="none">
        <path
          d="M9 2 16.5 15.5h-15L9 2Z"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linejoin="round"
        />
        <line x1="9" y1="7" x2="9" y2="10.8" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
        <circle cx="9" cy="13.2" r="1" fill="currentColor" />
      </svg>
    </span>
    <div class="risk-content">
      <p class="risk-title">{{ title }}</p>
      <div class="risk-body"><slot /></div>
      <div v-if="$slots.action" class="risk-action"><slot name="action" /></div>
    </div>
  </div>
</template>

<script setup lang="ts">
interface Props {
  title: string;
}

defineProps<Props>();
</script>

<style scoped>
.risk-banner {
  display: flex;
  gap: 10px;
  padding: 12px 14px;
  background: var(--vt-surface);
  border: 1px solid var(--vt-warning);
  border-left: 4px solid var(--vt-warning);
  border-radius: 8px;
}

.risk-icon {
  color: var(--vt-warning);
  display: inline-flex;
  flex-shrink: 0;
  margin-top: 1px;
}

.risk-content {
  flex: 1;
  min-width: 0;
}

.risk-title {
  margin: 0 0 4px;
  font-size: 13px;
  font-weight: 600;
  color: var(--vt-warning);
}

.risk-body {
  font-size: 13px;
  line-height: 1.55;
  color: var(--vt-text);
}

.risk-action {
  margin-top: 10px;
}
</style>
