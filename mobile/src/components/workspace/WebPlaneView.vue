<script setup lang="ts">
/**
 * WebPlaneView — pi/omp Web 会话平面 iframe 宿主视图（C2/C4）
 * ---------------------------------------------------------------------------
 * 权威依据：冻结契约 C1/C2/C4；参考桌面 WebPlaneHost.vue。
 *
 * 核心特征：
 *   · iframe 嵌入经 remote server 代理的 pi webui 页面；
 *   · sandbox="allow-scripts allow-forms"（同桌面端，不放行 allow-same-origin，
 *     opaque origin 跨源隔离是安全设计属性；allow-forms 放行内置输入台提交）；
 *   · Phase 状态机：loading → loaded | error；
 *   · 10s 看门狗超时兜底：移动端/网络拒连时如实进入 error 态，不无限转圈；
 *   · 会话结束态（ended）：保留最后画面 + 浮动 bar 提示「会话已结束」与切回动作；
 *   · open-url 桥：页面内链接经 postMessage 上抛，校验 capability token 后开外链
 *     （跨仓契约 v2.7.4；守卫纯函数在 lib/openUrlBridge，见该文件与实现报告）；
 *   · 视觉适配：VT 语义令牌、safe-area 边距、无障碍可访问名与无障碍反馈。
 * ---------------------------------------------------------------------------
 */
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import {
  extractCapabilityToken,
  openExternalUrl,
  parseOpenUrlMessage,
} from '../../lib/openUrlBridge';

const props = withDefaults(
  defineProps<{
    url: string;
    sessionId: string;
    ended?: boolean;
  }>(),
  { ended: false },
);

const emit = defineEmits<{
  error: [sessionId: string];
  retry: [sessionId: string];
  switchToTerminal: [];
  switchToTimeline: [];
}>();

export type WebPlanePhase = 'loading' | 'loaded' | 'error';

const phase = ref<WebPlanePhase>('loading');
// 浅色嵌入变体：移动端是浅色应用（iframe 背后铺 --VT-canvas 浅底），向 pi webui
// 页面 query 携带 skin=light —— 页面命中后叠加 webui-embedded-light（深色文字+
// 浅色半透明面板）。老版本 webui（不认识该参数）自然忽略，行为不劣化；
// fragment 契约（#/t=<token>）不动。插入点在 # 前，避免污染 fragment。
function withLightSkin(url: string): string {
  const hashIdx = url.indexOf('#');
  const base = hashIdx === -1 ? url : url.slice(0, hashIdx);
  const hash = hashIdx === -1 ? '' : url.slice(hashIdx);
  const sep = base.includes('?') ? '&' : '?';
  return `${base}${sep}skin=light${hash}`;
}
const frameSrc = ref(withLightSkin(props.url));
const frameKey = ref(0);

/**
 * webui → 宿主「打开外部链接」桥（跨仓契约 v2.7.4）：嵌入态 iframe sandbox 无
 * allow-popups，页面内 a[target=_blank] 被静默阻断——webui 改为消息上抛，宿主校验
 * 凭证后打开系统浏览器。ownToken 取当前 iframe src 的 fragment 凭证（严格相等判定
 * 来源，与桌面 WebPlaneHost.onFrameMessage 同构）；解析失败静默忽略。
 */
function onWindowMessage(event: MessageEvent): void {
  const url = parseOpenUrlMessage(event.data, extractCapabilityToken(frameSrc.value));
  if (url === null) return;
  openExternalUrl(url);
}

// 加载看门狗：iframe 对空响应/拒连不一定触发 @error，10s 超时兜底（对齐桌面）
const LOAD_TIMEOUT_MS = 10_000;
let watchdog: ReturnType<typeof setTimeout> | null = null;

function armWatchdog(): void {
  clearWatchdog();
  watchdog = setTimeout(() => {
    if (phase.value === 'loading') {
      phase.value = 'error';
      emit('error', props.sessionId);
    }
  }, LOAD_TIMEOUT_MS);
}

function clearWatchdog(): void {
  if (watchdog) {
    clearTimeout(watchdog);
    watchdog = null;
  }
}

function onFrameLoad(): void {
  clearWatchdog();
  phase.value = 'loaded';
}

function onFrameError(): void {
  clearWatchdog();
  phase.value = 'error';
  emit('error', props.sessionId);
}

function handleRetry(): void {
  emit('retry', props.sessionId);
  if (frameSrc.value === withLightSkin(props.url)) {
    phase.value = 'loading';
    frameKey.value++;
    armWatchdog();
  }
}

function onSwitchToTerminal(): void {
  emit('switchToTerminal');
}

watch(
  () => props.url,
  (newUrl) => {
    if (!newUrl) return;
    frameSrc.value = withLightSkin(newUrl);
    phase.value = 'loading';
    frameKey.value++;
    armWatchdog();
  },
);

armWatchdog();

onMounted(() => {
  window.addEventListener('message', onWindowMessage);
});

onBeforeUnmount(() => {
  clearWatchdog();
  window.removeEventListener('message', onWindowMessage);
});
</script>

<template>
  <div class="web-plane-view" data-testid="webplane-host">
    <!-- iframe 嵌入 WebUI 页面（C1/C4 冻结属性） -->
    <iframe
      v-if="frameSrc"
      :key="frameKey"
      class="web-plane-frame"
      :src="frameSrc"
      sandbox="allow-scripts allow-forms"
      title="Web 会话平面"
      data-testid="webplane-frame"
      @load="onFrameLoad"
      @error="onFrameError"
    />

    <!-- 加载中遮罩 -->
    <div
      v-if="phase === 'loading'"
      class="plane-overlay"
      role="status"
      data-testid="webplane-loading"
    >
      <div class="plane-spinner" aria-hidden="true" />
      <span class="plane-loading-text">Web 会话平面加载中…</span>
    </div>

    <!-- 加载失败遮罩 -->
    <div
      v-else-if="phase === 'error'"
      class="plane-overlay plane-overlay--error"
      role="alert"
      data-testid="webplane-error"
    >
      <div class="plane-error-icon" aria-hidden="true">
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="10" />
          <line x1="12" y1="8" x2="12" y2="12" />
          <line x1="12" y1="16" x2="12.01" y2="16" />
        </svg>
      </div>
      <strong class="plane-error-title">Web 会话平面加载失败</strong>
      <span class="plane-error-desc">pi webui 服务不可达或页面加载超时</span>
      <div class="plane-error-actions">
        <button
          type="button"
          class="plane-btn plane-btn--primary"
          @click="handleRetry"
        >
          重试
        </button>
        <button
          type="button"
          class="plane-btn"
          @click="onSwitchToTerminal"
        >
          切回终端
        </button>
      </div>
    </div>

    <!-- 会话已结束提示栏（保留画面 + 状态浮层） -->
    <div
      v-if="ended"
      class="plane-ended-bar"
      role="status"
      data-testid="webplane-ended-bar"
    >
      <span class="ended-badge">会话已结束</span>
      <button
        type="button"
        class="plane-btn-sm"
        @click="onSwitchToTerminal"
      >
        切回终端
      </button>
    </div>
  </div>
</template>

<style scoped>
.web-plane-view {
  position: relative;
  flex: 1;
  min-height: 0;
  width: 100%;
  display: flex;
  flex-direction: column;
  background: var(--VT-canvas);
  padding-bottom: env(safe-area-inset-bottom, 0px);
}

.web-plane-frame {
  flex: 1;
  width: 100%;
  height: 100%;
  min-height: 0;
  border: none;
  background: transparent;
}

.plane-overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  background: var(--VT-surface);
  backdrop-filter: blur(14px);
  z-index: 5;
  padding: 24px 16px;
  text-align: center;
}

.plane-spinner {
  width: 32px;
  height: 32px;
  border: 3px solid var(--VT-border);
  border-top-color: var(--VT-accent);
  border-radius: 50%;
  animation: plane-spin 0.8s linear infinite;
}

@keyframes plane-spin {
  to {
    transform: rotate(360deg);
  }
}

.plane-loading-text {
  font-size: 14px;
  color: var(--VT-text-secondary);
}

.plane-error-icon {
  color: var(--VT-danger);
}

.plane-error-title {
  font-size: 16px;
  font-weight: 700;
  color: var(--VT-text);
}

.plane-error-desc {
  font-size: 13px;
  color: var(--VT-text-secondary);
  max-width: 280px;
}

.plane-error-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 8px;
}

.plane-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 44px;
  padding: 0 16px;
  font-size: 14px;
  font-weight: 600;
  border-radius: 8px;
  border: 1px solid var(--VT-border-strong);
  background: var(--VT-surface-raised);
  color: var(--VT-text);
  cursor: pointer;
  touch-action: manipulation;
}

.plane-btn:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.plane-btn--primary {
  background: var(--VT-accent);
  border-color: var(--VT-accent-strong);
  color: #FFFFFF;
}

.plane-ended-bar {
  position: absolute;
  top: 10px;
  right: 12px;
  z-index: 10;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border-radius: 8px;
  background: var(--VT-surface-raised);
  border: 1px solid var(--VT-border);
  backdrop-filter: blur(10px);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
}

.ended-badge {
  font-size: 12px;
  font-weight: 600;
  color: var(--VT-text-secondary);
}

.plane-btn-sm {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 32px;
  padding: 0 10px;
  font-size: 12px;
  font-weight: 600;
  border-radius: 6px;
  border: 1px solid var(--VT-border-strong);
  background: var(--VT-surface);
  color: var(--VT-text);
  cursor: pointer;
  touch-action: manipulation;
}

@media (prefers-reduced-motion: reduce) {
  .plane-spinner {
    animation: none;
  }
}
</style>
