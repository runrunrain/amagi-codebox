<template>
  <div class="web-plane-host">
    <!-- 契约 v1.0.2 §6.5：src = ${httpBase}/#/t=<token>（fragment 承载
         capability token，不入 HTTP 日志）；无 allow-same-origin，页面
         origin 为 opaque，跨源隔离是设计属性。M2 起页面含输入表单，
         sandbox 需允许表单提交（否则 Chrome 在激活行为层阻断 submit
         事件，发送按钮静默失效——T-2.4 实证）；allow-forms 不放行
         顶层导航/弹窗，页面为第一方，风险可控。 -->
    <iframe
      ref="frameRef"
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

    <!-- 会话已结束：保留最后画面 + 结束 badge（交互文档 §5）。
         设计 §7 要求 ended 时与额度条「融合避免双条堆叠」：实现取舍——
         ended-bar 是右上角浮动胶囊（非底栏），与底部 30px 常驻 strip 天然
         不堆叠，故不把额度按钮搬进 ended-bar；保留 strip 常驻，会话结束后
         仍可查看缓存额度（数据不随会话销毁）。 -->
    <div v-if="ended" class="plane-ended-bar" role="status">
      <span class="ended-badge">会话已结束</span>
      <button class="plane-btn" @click="emit('switchToTui')">切回终端</button>
    </div>

    <!-- 设计 §7：宿主额度细工具条（iframe 输入框下侧的同容器落点），
         常驻 30px；iframe flex:1 自适应，term-body 高度链路不变。
         TUI 平面的同款条由 TerminalView 直接挂载（v-show 互斥） -->
    <SessionQuotaStrip class="plane-quota-strip" :session-id="sessionId" />
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
import { ref, watch, onBeforeUnmount, onMounted } from 'vue';
// postToWebFrame / InsertInputPayload 来自同文件 <script> 块的具名导出
// （两个 script 块编译进同一模块，同名符号无需 import）。
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import SessionQuotaStrip from './SessionQuotaStrip.vue';
import {
  postToWebFrame,
  extractCapabilityToken,
  parseOpenUrlMessage,
  type InsertInputPayload,
} from './quickPathInsert';
import { openExternalURL } from '../../api/webui';

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
const frameRef = ref<HTMLIFrameElement | null>(null);

/**
 * 宿主 → webui 插入指令桥（父级经模板 ref 调用）。
 *
 * iframe 已 loaded、contentWindow 可用、且 props.url 携带合法 capability token 时，
 * 投递 { type: 'amagi:insert-input', token, text } 并返回 true；否则 false（不抛错）。
 * token = 构造 iframe URL（#/t=）时持有的凭证，接收端以「与自身 fragment 严格相等」
 * 校验来源（origin 不参与——event.origin 是发送方自身 origin）。
 */
function postToFrame(payload: InsertInputPayload): boolean {
  const token = extractCapabilityToken(props.url)
  if (!token) return false
  return postToWebFrame(frameRef.value, phase.value, { ...payload, token })
}

defineExpose({ postToFrame })

/**
 * webui → 宿主「打开外部链接」桥（v2.7.4）：嵌入态 sandbox 无 allow-popups，
 * 页面内 a[target=_blank] 被静默阻断——改为消息上抛，宿主校验 token 后经
 * Go BrowserOpenURL 走系统浏览器。守卫逻辑在 parseOpenUrlMessage（可单测）；
 * 打开失败尽力而为（console.warn，不打断页面）。
 */
function onFrameMessage(event: MessageEvent): void {
  const url = parseOpenUrlMessage(event.data, extractCapabilityToken(frameSrc.value))
  if (url === null) return
  openExternalURL(url).catch((err) => {
    console.warn('[WebPlaneHost] open external url failed:', err)
  })
}

onMounted(() => window.addEventListener('message', onFrameMessage))
onBeforeUnmount(() => window.removeEventListener('message', onFrameMessage))

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
  /* 透明：皮肤模式下透出全局皮肤层；webui 页面（#/t= 内嵌模式）body 自身透明 */
  background: transparent;
  min-height: 0;
}

.web-frame {
  flex: 1;
  width: 100%;
  border: none;
  min-height: 0;
  /* iframe 不设背景即可透出宿主皮肤层（iframe 默认透明） */
  background: transparent;
}

.plane-overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  /* 加载期/错误态可读优先：保留 --card 实底；皮肤模式下 --card 为半透明，
     叠 backdrop-filter 压花背景保证文字对比度 */
  background: var(--card, #fff);
  backdrop-filter: blur(14px) saturate(1.1);
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
  /* 透皮下 --control 为半透明：backdrop-filter 压花保证结束 badge 可读 */
  background: var(--control);
  backdrop-filter: blur(10px);
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

/* 额度 strip 自带 30px 高度与背景，此处仅钉住 flex 尺寸防被压缩 */
.plane-quota-strip {
  flex: 0 0 30px;
}

@media (prefers-reduced-motion: reduce) {
  .plane-btn {
    transition: none;
  }
}
</style>
