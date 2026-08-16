<template>
  <div class="web-plane-host">
    <!-- 契约 v1.0.2 §6.5：src = ${httpBase}/#/t=<token>（fragment 承载
         capability token，不入 HTTP 日志）；无 allow-same-origin，页面
         origin 为 opaque，跨源隔离是设计属性。M2 起页面含输入表单，
         sandbox 需允许表单提交（否则 Chrome 在激活行为层阻断 submit
         事件，发送按钮静默失效——T-2.4 实证）；allow-forms 不放行
         顶层导航/弹窗，页面为第一方，风险可控。 -->
    <iframe
      v-if="frameSrc"
      :key="frameKey"
      class="web-frame"
      :src="frameSrc"
      sandbox="allow-scripts allow-forms"
      title="Pi Web 会话平面"
      @load="onFrameLoad"
      @error="onFrameError"
    />

    <!-- 加载中 -->
    <div v-if="phase === 'loading'" class="plane-overlay">
      <LoadingState message="Web 平面加载中…" />
    </div>

    <!-- 加载失败 / 失联（交互文档 §5：提供"重试 / 切回 TUI"两个动作） -->
    <div v-else-if="phase === 'error'" class="plane-overlay">
      <ErrorState
        title="Web 平面加载失败"
        message="pi webui 服务不可达或页面加载超时"
        :on-retry="handleRetry"
      />
      <button class="plane-btn" @click="emit('switchToTui')">切回终端</button>
    </div>

    <!-- 会话已结束：保留最后画面 + 结束 badge（交互文档 §5） -->
    <div v-if="ended" class="plane-ended-bar" role="status">
      <span class="ended-badge">会话已结束</span>
      <button class="plane-btn" @click="emit('switchToTui')">切回终端</button>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * WebPlaneHost — pi Web 平面 iframe 容器（蓝图 T-1.6，交互文档 §4.2）。
 *
 * props: url（OpenWebPlane 返回的 ${httpBase}/#/t=<token>，v1.0.2）、sessionId、
 * ended（会话结束态由父级从 webui 探测状态推导传入）。
 * emits: error（iframe 加载失败/超时）、retry（用户点重试）、
 * switchToTui（用户点切回终端）。
 *
 * codebox 不拦截页面内部交互（输入/滚动/复制由页面自理）。
 */
import { ref, watch, onBeforeUnmount } from 'vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';

const props = withDefaults(
  defineProps<{ url: string; sessionId: string; ended?: boolean }>(),
  { ended: false },
);

const emit = defineEmits<{
  error: [sessionId: string];
  retry: [sessionId: string];
  switchToTui: [];
}>();

type Phase = 'loading' | 'loaded' | 'error';

const phase = ref<Phase>('loading');
const frameSrc = ref(props.url);
const frameKey = ref(0);

// 加载看门狗：iframe 对拒连/空响应不保证触发 error，超时兜底。
const LOAD_TIMEOUT_MS = 10_000;
let watchdog: ReturnType<typeof setTimeout> | null = null;

function armWatchdog() {
  clearWatchdog();
  watchdog = setTimeout(() => {
    if (phase.value === 'loading') {
      phase.value = 'error';
      emit('error', props.sessionId);
    }
  }, LOAD_TIMEOUT_MS);
}

function clearWatchdog() {
  if (watchdog) {
    clearTimeout(watchdog);
    watchdog = null;
  }
}

function onFrameLoad() {
  clearWatchdog();
  phase.value = 'loaded';
}

function onFrameError() {
  clearWatchdog();
  phase.value = 'error';
  emit('error', props.sessionId);
}

function handleRetry() {
  // 先让父级重新解析 URL（可能端口已变）；url 变化由 watch 接管重载。
  emit('retry', props.sessionId);
  if (frameSrc.value === props.url) {
    // URL 未变也要强制重载：重置状态并 bump key 重建 iframe。
    phase.value = 'loading';
    frameKey.value++;
    armWatchdog();
  }
}

watch(
  () => props.url,
  (url) => {
    if (!url) return;
    frameSrc.value = url;
    phase.value = 'loading';
    frameKey.value++;
    armWatchdog();
  },
);

armWatchdog();

onBeforeUnmount(() => clearWatchdog());
</script>

<style scoped>
.web-plane-host {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  background: var(--card, #fff);
  min-height: 0;
}

.web-frame {
  flex: 1;
  width: 100%;
  border: none;
  min-height: 0;
  background: var(--card, #fff);
}

.plane-overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  background: var(--card, #fff);
}

.plane-ended-bar {
  position: absolute;
  top: 10px;
  right: 12px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border-radius: 9px;
  background: var(--control);
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.12);
}

.ended-badge {
  font-size: 12px;
  color: var(--secondary);
}

.plane-btn {
  border: none;
  border-radius: 8px;
  padding: 5px 12px;
  font-size: 12px;
  font-family: inherit;
  cursor: pointer;
  background: var(--control);
  color: var(--label);
  transition: background 0.15s;
}

.plane-btn:hover {
  background: var(--controlHover);
}

@media (prefers-reduced-motion: reduce) {
  .plane-btn {
    transition: none;
  }
}
</style>
