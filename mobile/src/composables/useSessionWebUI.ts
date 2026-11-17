/**
 * composables/useSessionWebUI.ts — 会话 Web 会话平面（WebUI）探测与轮询（C2/C4）
 * ---------------------------------------------------------------------------
 * 权威依据：冻结契约 C2 / C4
 * 职责：
 *   · 针对 TUI 会话（pi/omp）探测 WebUI 可用状态（GET /api/remote/v1/session/{id}/webui）；
 *   · 探测中（probing）：高频轮询（800ms，处于冻结契约 0.5–1s 推荐区间）；
 *   · 可用后（available）：低频监测（5000ms，低频持续探测 ended / unavailable 状态迁移）；
 *   · 不可用（unavailable）：低频复活探测（10000ms，WSL 慢启动/网络瞬断自愈，
 *     非终态）；
 *   · 已结束（ended）/非平面会话（unknown）：停止轮询；
 *   · 会话切换 / 离开页面（unmount）时严格清理定时器，防泄露。
 * ---------------------------------------------------------------------------
 */
import { ref, computed, watch, onUnmounted, type Ref } from 'vue';
import { getSessionWebUI } from '../lib/api';
import type { WebUIState } from '../lib/contract';

export const PROBING_INTERVAL_MS = 800; // 0.5–1s 节奏轮询
export const MONITORING_INTERVAL_MS = 5000; // available 后的低频监测（5s）

/** unavailable 后的复活探测节奏。窗口耗尽只证明「此刻未就绪」不是永久
 * 属性：WSL+DrvFS 目标下 pi 扩展 webui server 就绪实测可达 3m42s+
 * （2026-11-17 会话 b5415434，超出 3min 探测窗口），若把 unavailable 当
 * 终态停探，服务晚就绪后无人再探测，Web 会话平面入口永不出现；网络
 * 瞬断同理可自愈。每轮成本仅一次同源请求，可忽略。 */
export const RESURRECT_INTERVAL_MS = 10000;

export function useSessionWebUI(
  sessionIdRef: Ref<string>,
  enabledRef: Ref<boolean>,
) {
  const state = ref<WebUIState>('unknown');
  const url = ref<string | undefined>(undefined);
  const isProbing = computed(() => state.value === 'probing');
  const isAvailable = computed(() => state.value === 'available');

  let pollTimer: ReturnType<typeof setTimeout> | null = null;
  let generation = 0;
  let inFlight: number | null = null;
  let disposed = false;

  function clearTimer(): void {
    if (pollTimer) {
      clearTimeout(pollTimer);
      pollTimer = null;
    }
  }

  async function poll(): Promise<void> {
    clearTimer();
    if (disposed || !enabledRef.value || !sessionIdRef.value || inFlight === generation) {
      return;
    }

    const currentSid = sessionIdRef.value;
    const requestGeneration = generation;
    inFlight = requestGeneration;

    try {
      const res = await getSessionWebUI(currentSid);
      if (disposed || !enabledRef.value || generation !== requestGeneration || sessionIdRef.value !== currentSid) return;

      state.value = res.state;
      url.value = res.url;

      scheduleNext(res.state);
    } catch {
      if (disposed || !enabledRef.value || generation !== requestGeneration || sessionIdRef.value !== currentSid) return;
      // 网络错误或服务不可达：标记为 unavailable，不持续高频空转；转低频
      // 复活探测（网络瞬断/晚就绪均可自愈，页面卸载/切换时严格清理）。
      state.value = 'unavailable';
      url.value = undefined;
      scheduleNext('unavailable');
    } finally {
      if (inFlight === requestGeneration) inFlight = null;
    }
  }

  /** 按状态排下一轮：probing 高频、available 低频保活、unavailable 低频
   * 复活；ended/unknown 为终态（会话平面已亡/非平面会话），停止轮询。 */
  function scheduleNext(nextState: WebUIState): void {
    if (nextState === 'probing') {
      pollTimer = setTimeout(poll, PROBING_INTERVAL_MS);
    } else if (nextState === 'available') {
      pollTimer = setTimeout(poll, MONITORING_INTERVAL_MS);
    } else if (nextState === 'unavailable') {
      pollTimer = setTimeout(poll, RESURRECT_INTERVAL_MS);
    }
  }

  function start(): void {
    generation++;
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
    generation++;
    clearTimer();
  }

  function refresh(): void {
    clearTimer();
    if (inFlight === generation) return;
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
