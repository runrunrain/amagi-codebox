/**
 * Session Store
 * Manages session state
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import { session } from '../../wailsjs/go/models';
import { probeWebUI, type WebUIStatus } from '../api/webui';

type SessionInfo = session.SessionInfo;

/** webui 探测轮询节奏（契约 §4.1 建议 0.5–1s）。 */
const WEBUI_PROBE_INTERVAL_MS = 800;

/** available 后的保活节奏：跟随 /resume、/new、fork、reload 导致的
 * sessionId/端口演进（老扩展端口漂移场景经 url 字段传导）。 */
const WEBUI_KEEPALIVE_INTERVAL_MS = 3000;

export const useSessionStore = defineStore('session', () => {
  // All sessions
  const sessions = ref<SessionInfo[]>([]);

  // Active session ID
  const activeSessionId = ref<string | null>(null);

  // Polling state
  const isPolling = ref(false);

  // ---- webui 探测（蓝图 T-1.6）----
  // 按 sessionId 隔离的 pi Web 平面状态（unknown/probing/available/
  // unavailable/ended）。仅 embedded pi 会话有值；内存持有，不持久化（TD-9）。
  const webuiStatus = ref<Record<string, WebUIStatus>>({});
  // 单飞 await 链（Major3）：setTimeout 接力而非 setInterval，上一轮探测
  // 完成后才排下一轮，杜绝慢探测堆积并发。
  const webuiProbeTimers = new Map<string, ReturnType<typeof setTimeout>>();
  // generation（Minor6）：stop/retry 换代，迟到结果按代际校验丢弃。
  const webuiProbeGen = new Map<string, number>();

  /**
   * 启动（幂等）某 pi 会话的 webui 探测轮询；unavailable/ended 终态后自动
   * 停止，available 后转低频保活（跟随会话切换导致的 url 演进）。
   */
  function ensureWebUIProbe(sessionId: string) {
    if (webuiProbeTimers.has(sessionId)) return;
    const gen = (webuiProbeGen.get(sessionId) ?? 0) + 1;
    webuiProbeGen.set(sessionId, gen);
    const isCurrent = () =>
      webuiProbeGen.get(sessionId) === gen && webuiProbeTimers.has(sessionId);
    const tick = async () => {
      let terminal = false;
      let nextDelay = WEBUI_PROBE_INTERVAL_MS;
      try {
        const st = await probeWebUI(sessionId);
        // Minor6：轮询已停止/重试换代（含会话被 removeSession）→ 丢弃迟到结果。
        if (!isCurrent()) return;
        webuiStatus.value = { ...webuiStatus.value, [sessionId]: st };
        if (st.state === 'unavailable' || st.state === 'ended') {
          terminal = true;
        } else if (st.state === 'available') {
          // available 非终态：转低频保活，跟随 /resume 等会话切换
          //（sessionId 演进/端口漂移经 webuiStatus.url 传导）。
          nextDelay = WEBUI_KEEPALIVE_INTERVAL_MS;
        }
      } catch {
        // 单次探测失败不改变状态（callApi 已打日志）；下轮重试。
      }
      if (!isCurrent()) return;
      if (terminal) {
        stopWebUIProbe(sessionId);
        return;
      }
      webuiProbeTimers.set(sessionId, setTimeout(tick, nextDelay));
    };
    // 立即首轮（0ms timer 保持 map 占位语义统一）。
    webuiProbeTimers.set(sessionId, setTimeout(tick, 0));
  }

  /** 停止某会话的 webui 探测轮询；进行中的探测结果按代际丢弃。 */
  function stopWebUIProbe(sessionId: string) {
    webuiProbeGen.set(sessionId, (webuiProbeGen.get(sessionId) ?? 0) + 1);
    const timer = webuiProbeTimers.get(sessionId);
    if (timer) {
      clearTimeout(timer);
      webuiProbeTimers.delete(sessionId);
    }
  }

  /** 重新探测（用户手动重试场景）：清除终态并重启轮询。 */
  function retryWebUIProbe(sessionId: string) {
    stopWebUIProbe(sessionId);
    const { [sessionId]: _dropped, ...rest } = webuiStatus.value;
    void _dropped;
    webuiStatus.value = rest;
    ensureWebUIProbe(sessionId);
  }

  // Computed
  // "stopping" remains an active backend-owned process until Wait/terminal.
  // Keep it in the desktop list/count so users do not see a false stopped state.
  const runningSessions = computed(() => {
    return sessions.value.filter(s => s.status === 'running' || s.status === 'stopping');
  });

  const stoppingSessions = computed(() => {
    return sessions.value.filter(s => s.status === 'stopping');
  });

  const activeSession = computed(() => {
    if (!activeSessionId.value) return null;
    return sessions.value.find(s => s.id === activeSessionId.value) || null;
  });

  const sessionCount = computed(() => runningSessions.value.length);

  // Actions
  function setSessions(newSessions: SessionInfo[]) {
    sessions.value = newSessions;
  }

  function addSession(session: SessionInfo) {
    sessions.value.unshift(session);
  }

  function updateSession(session: SessionInfo) {
    const index = sessions.value.findIndex(s => s.id === session.id);
    if (index >= 0) {
      sessions.value[index] = session;
    }
  }

  function removeSession(sessionId: string) {
    stopWebUIProbe(sessionId);
    webuiProbeGen.delete(sessionId);
    const { [sessionId]: _droppedStatus, ...restStatus } = webuiStatus.value;
    void _droppedStatus;
    webuiStatus.value = restStatus;
    const index = sessions.value.findIndex(s => s.id === sessionId);
    if (index >= 0) {
      sessions.value.splice(index, 1);
    }
    if (activeSessionId.value === sessionId) {
      activeSessionId.value = null;
    }
  }

  function setActiveSession(sessionId: string | null) {
    activeSessionId.value = sessionId;
  }

  function setPolling(polling: boolean) {
    isPolling.value = polling;
  }

  return {
    // State
    sessions,
    activeSessionId,
    isPolling,
    webuiStatus,

    // Computed
    runningSessions,
    stoppingSessions,
    activeSession,
    sessionCount,

    // Actions
    setSessions,
    addSession,
    updateSession,
    removeSession,
    setActiveSession,
    setPolling,
    ensureWebUIProbe,
    stopWebUIProbe,
    retryWebUIProbe,
  };
});
