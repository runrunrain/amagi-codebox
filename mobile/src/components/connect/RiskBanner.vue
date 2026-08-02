<script setup lang="ts">
/**
 * RiskBanner — LAN 明文 HTTP 风险提示（NFR-18/AC-22）
 * 契约（P5 §6）：警告左边条 + 图标 + 动作；启用类提示**不可**关成"不再提示"。
 * 本组件无关闭按钮、无"不再提示"——每次进入 PG-01 配对流程均可见。
 */
</script>

<template>
  <aside class="risk-banner" role="note" aria-label="安全提示">
    <span class="risk-icon" aria-hidden="true">
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
        <line x1="12" y1="9" x2="12" y2="13" />
        <line x1="12" y1="17" x2="12.01" y2="17" />
      </svg>
    </span>
    <p class="risk-text">
      远程连接使用<strong>局域网明文 HTTP</strong>：同一网络内的其他设备可能截获通信内容。
      请只在可信的家庭/办公网络中配对；配对后这台设备即可查看终端输出。
    </p>
  </aside>
</template>

<style scoped>
.risk-banner {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  padding: 12px 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-warning);
  border-radius: 10px;
}

.risk-icon {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  color: var(--VT-warning);
}

.risk-text {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--VT-text);
}

.risk-text strong {
  font-weight: 700;
}
</style>
