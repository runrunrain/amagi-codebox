<!--
  卡⑤ 活动控制卡（PG-05）· M3 占位。
  诚实空态：M1 后端尚无可枚举的活动会话/控制权接口，此处不展示任何模拟数据、
  不提供伪装的"收回"按钮（C-02 假能力防御）。M3 接入真实控制后替换本占位。
-->
<template>
  <section class="rc-card" aria-labelledby="rc-act-title">
    <header class="rc-card-head">
      <h2 id="rc-act-title" class="rc-card-title">活动控制</h2>
      <p class="rc-card-sub">会话 × 当前控制者 × 收回控制</p>
    </header>

    <div class="act-empty" data-testid="activity-empty">
      <span class="act-empty-icon" aria-hidden="true">
        <svg width="40" height="40" viewBox="0 0 40 40" fill="none">
          <rect x="5" y="8" width="30" height="22" rx="3" stroke="currentColor" stroke-width="1.6" />
          <path d="M12 16l5 4-5 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
          <line x1="20" y1="24" x2="28" y2="24" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
        </svg>
      </span>
      <p class="act-empty-title">暂无活动会话</p>
      <p class="act-empty-desc">
        远程会话的控制权管理（观察 / 收回）将在后续里程碑提供；桌面本地操作恒为最高优先级。
      </p>
    </div>
  </section>
</template>

<script setup lang="ts">
// 纯展示占位，无数据依赖（M3 才接真实控制）。
</script>

<style scoped>
.act-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: 24px 12px;
  gap: 6px;
}

.act-empty-icon {
  color: var(--vt-text-secondary);
  display: inline-flex;
}

.act-empty-title {
  margin: 4px 0 0;
  font-size: 15px;
  font-weight: 600;
  font-family: var(--vt-font-display);
  color: var(--vt-text);
}

.act-empty-desc {
  margin: 0;
  max-width: 420px;
  font-size: 13px;
  line-height: 1.55;
  color: var(--vt-text-secondary);
}
</style>
