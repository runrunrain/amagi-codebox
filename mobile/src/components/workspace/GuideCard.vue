<script setup lang="ts">
/**
 * GuideCard — E-09 首次进入工作区引导（M2-C）
 * ---------------------------------------------------------------------------
 * 可关闭引导卡：说明内容转化形态（结构化卡片为主形态）、控制/观察语义、
 * 诊断视图按需入口。关闭状态记入 localStorage（不存任何凭据/敏感信息）。
 * ---------------------------------------------------------------------------
 */
const emit = defineEmits<{
  dismiss: [];
  'open-diagnostic': [];
}>();
</script>

<template>
  <section class="guide-card" aria-label="工作区引导">
    <div class="guide-head">
      <h2 class="guide-title">这是你的会话工作区</h2>
      <button type="button" class="guide-close" aria-label="关闭引导" @click="emit('dismiss')">×</button>
    </div>
    <ul class="guide-points">
      <li><strong>结构化阅读：</strong>终端输出已转化为卡片（确认、选项、工具、错误、进度等），可直接点按应答；不再需要读原始终端。</li>
      <li><strong>控制与观察：</strong>取得控制权后可输入与应答；其他设备控制时你可实时观察。</li>
      <li><strong>诊断视图：</strong>需要终端级原始细节时，从右上角菜单按需打开（不影响权限）。</li>
    </ul>
    <div class="guide-actions">
      <button type="button" class="guide-diagnostic" @click="emit('open-diagnostic')">了解诊断视图</button>
      <button type="button" class="guide-gotit" @click="emit('dismiss')">知道了</button>
    </div>
  </section>
</template>

<style scoped>
.guide-card {
  margin: 12px;
  padding: 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-accent);
  border-radius: 10px;
}

.guide-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.guide-title {
  flex: 1;
  margin: 0;
  font-size: 15px;
  font-weight: 700;
  color: var(--VT-text);
}

.guide-close {
  min-width: 44px;
  min-height: 44px;
  margin: -8px -8px -8px 0;
  border: none;
  background: transparent;
  color: var(--VT-text-secondary);
  font-size: 18px;
  cursor: pointer;
}

.guide-close:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
  border-radius: 8px;
}

.guide-points {
  margin: 8px 0 0;
  padding-left: 18px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
  color: var(--VT-text);
  line-height: 1.6;
}

.guide-actions {
  display: flex;
  gap: 8px;
  margin-top: 12px;
}

.guide-diagnostic,
.guide-gotit {
  min-height: 44px;
  padding: 0 14px;
  border-radius: 8px;
  font-size: 13px;
  font-weight: 700;
  cursor: pointer;
}

.guide-diagnostic {
  border: 1px solid var(--VT-border-strong);
  background: transparent;
  color: var(--VT-text);
}

.guide-gotit {
  border: none;
  background: var(--VT-accent-strong);
  color: var(--VT-canvas);
}

.guide-diagnostic:focus-visible,
.guide-gotit:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}
</style>
