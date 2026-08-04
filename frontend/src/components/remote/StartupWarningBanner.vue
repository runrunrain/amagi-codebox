<!--
  StartupWarningBanner（M2-INT R12 · 启动警告的当前 App shell 消费）
  语义：
  - 挂载时拉取 GetStartupWarnings（含 legacy 外部清理提示），非空即展示。
  - 持久提醒：不自动消失（区别于 toast）；用户可手动关闭。
  - 关闭仅对当次启动生效（内存态，不写 localStorage）：下次启动若警告仍在会再次提醒。
  - 「前往处理」直达 设置 > 远程访问（恢复卡所在页）。
  隐私：后端警告串为固定文案，不含路径/凭据；本组件原样展示，不拼接任何环境信息。
-->
<template>
  <div v-if="visible && warnings.length > 0" class="remote-cc swb" role="status" data-testid="startup-warning-banner">
    <div class="swb-bar">
      <span class="swb-icon" aria-hidden="true">
        <svg width="18" height="18" viewBox="0 0 18 18" fill="none">
          <path d="M9 2 16.2 15H1.8L9 2Z" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" />
          <line x1="9" y1="7" x2="9" y2="11" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
          <circle cx="9" cy="13.2" r="1" fill="currentColor" />
        </svg>
      </span>
      <div class="swb-body">
        <p v-for="(msg, idx) in warnings" :key="idx" class="swb-text">{{ msg }}</p>
      </div>
      <button type="button" class="swb-action" data-testid="startup-warning-goto" @click="gotoRecovery">
        前往处理
      </button>
      <button
        type="button"
        class="swb-close"
        data-testid="startup-warning-dismiss"
        aria-label="关闭本次启动警告提醒"
        @click="dismiss"
      >
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
          <path d="M2 2 12 12M12 2 2 12" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
        </svg>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import '../../styles/vt-tokens.css';
import { getStartupWarnings } from '../../api/remote';
import { useUIStore } from '../../stores/ui';

const warnings = ref<string[]>([]);
/** 内存态可见性：关闭只对当次启动生效（不落 localStorage，下次启动重新提醒） */
const visible = ref(true);

const uiStore = useUIStore();

function dismiss() {
  visible.value = false;
}

function gotoRecovery() {
  uiStore.enterSettingsMode();
  uiStore.setActiveSettingKey('remote');
}

onMounted(async () => {
  try {
    const list = await getStartupWarnings();
    warnings.value = Array.isArray(list) ? list.filter((m) => typeof m === 'string' && m) : [];
  } catch {
    // 启动警告拉取失败不阻塞应用；保持无横幅（如实无数据，不伪造）
    warnings.value = [];
  }
});
</script>

<style scoped>
.swb {
  position: fixed;
  top: 12px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 1100;
  width: min(720px, calc(100vw - 32px));
  font-family: var(--vt-font-ui);
}

.swb-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  background: var(--vt-surface);
  border: 1px solid var(--vt-warning);
  border-left: 4px solid var(--vt-warning);
  border-radius: 12px;
}

.swb-icon {
  color: var(--vt-warning);
  display: inline-flex;
  flex-shrink: 0;
}

.swb-body {
  flex: 1;
  min-width: 0;
}

.swb-text {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--vt-text);
}

.swb-action {
  min-height: 44px;
  padding: 8px 14px;
  flex-shrink: 0;
  background: var(--vt-accent-strong);
  border: 1px solid var(--vt-accent-strong);
  border-radius: 10px;
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
}

.swb-action:hover {
  filter: brightness(1.05);
}

.swb-close {
  min-width: 44px;
  min-height: 44px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  background: transparent;
  border: none;
  border-radius: 10px;
  color: var(--vt-text-secondary);
  cursor: pointer;
}

.swb-close:hover {
  background: var(--vt-surface-raised);
  color: var(--vt-text);
}

.swb-action:focus-visible,
.swb-close:focus-visible {
  outline: 2px solid var(--vt-accent);
  outline-offset: 2px;
}

@media (prefers-reduced-motion: reduce) {
  .swb-action,
  .swb-close {
    transition: none;
  }
}
</style>
