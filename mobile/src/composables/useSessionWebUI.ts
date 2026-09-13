/**
 * composables/useSessionWebUI.ts — 会话 Web 会话平面（WebUI）探测与轮询（C2/C4）
 * ---------------------------------------------------------------------------
 * 权威依据：冻结契约 C2 / C4
 * 职责：
 *   · 针对 TUI 会话（pi/omp）探测 WebUI 可用状态（GET /api/remote/v1/session/{id}/webui）；
 *   · 探测中（probing）：高频轮询（800ms，处于冻结契约 0.5–1s 推荐区间）；
 *   · 可用后（available）：低频监测（5000ms，低频持续探测 ended / unavailable 状态迁移）；
 *   · 不可用（unavailable / unknown）或已结束（ended）：停止高频轮询；
 *   · 会话切换 / 离开页面（unmount）时严格清理定时器，防泄露。
 * ---------------------------------------------------------------------------
 */
import { ref, computed, watch, onUnmounted, type Ref } from 'vue';
import { getSessionWebUI } from '../lib/api';
import type { WebUIState } from '../lib/contract';

export const PROBING_INTERVAL_MS = 800; // 0.5–1s 节奏轮询
export const MONITORING_INTERVAL_MS = 5000; // available 后的低频监测（5s）

export function useSessionWebUI(
  sessionIdRef: Ref<string>,
  enabledRef: Ref<boolean>,
) {
  const state = ref<WebUIState>('unknown');
  const url = ref<string | undefined>(undefined);
  const isProbing = computed(() => state.value === 'probing');
  const isAvailable = computed(() => state.value === 'available');

  let pollTimer: ReturnType<typeof setTimeout> | null = null;
  let inFlight = false;
  let disposed = false;

  function clearTimer(): void {
    if (pollTimer) {
      clearTimeout(pollTimer);
      pollTimer = null;
    }
  }

  async function poll(): Promise<void> {
    clearTimer();
    if (disposed || !enabledRef.value || !sessionIdRef.value || inFlight) {
      return;
    }

    const currentSid = sessionIdRef.value;
    inFlight = true;

    try {
      const res = await getSessionWebUI(currentSid);
      if (disposed || sessionIdRef.value !== currentSid) return;

      state.value = res.state;
      url.value = res.url;

      if (res.state === 'probing') {
        pollTimer = setTimeout(poll, PROBING_INTERVAL_MS);
      } else if (res.state === 'available') {
        pollTimer = setTimeout(poll, MONITORING_INTERVAL_MS);
      }
    } catch {
      if (disposed || sessionIdRef.value !== currentSid) return;
      // 网络错误或服务不可达：标记为 unavailable，不持续高频空转
      state.value = 'unavailable';
      url.value = undefined;
    } finally {
      inFlight = false;
    }
  }

  function start(): void {
    clearTimer();
    const sid = sessionIdRef.value;
    if (!sid || !enabledRef.value) {
      state.value = 'unknown';
      url.value = undefined;
      return;
    }
    state.value = 'probing';
    url.value = undefined;
    void poll();
  }

  function stop(): void {
    clearTimer();
  }

  function refresh(): void {
    clearTimer();
    inFlight = false;
    void poll();
  }

  // 监听 sessionId 与 enabledRef 状态变化
  watch(
    [sessionIdRef, enabledRef],
    ([newSid, newEnabled], [oldSid, oldEnabled]) => {
      if (!newEnabled || !newSid) {
        stop();
        state.value = 'unknown';
        url.value = undefined;
      } else if (newSid !== oldSid || !oldEnabled) {
        start();
      }
    },
    { immediate: true },
  );

  onUnmounted(() => {
    disposed = true;
    stop();
  });

  return {
    state,
    url,
    isProbing,
    isAvailable,
    start,
    stop,
    refresh,
  };
}
