/**
 * Session List Composable (P0 Skeleton)
 * P1 will integrate GetSessions polling and active session tracking
 */

import { ref, computed, onUnmounted } from 'vue';
import { session } from '../../wailsjs/go/models';

type SessionInfo = session.SessionInfo;

export function useSessionList() {
  const sessions = ref<SessionInfo[]>([]);
  const activeSessionId = ref<string | null>(null);
  const polling = ref(false);
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  // Running sessions only
  const runningSessions = computed(() => {
    return sessions.value.filter(s => s.status === 'running');
  });

  // Active session
  const activeSession = computed(() => {
    if (!activeSessionId.value) return null;
    return sessions.value.find(s => s.id === activeSessionId.value) || null;
  });

  /**
   * Start polling for sessions (P1: actual GetSessions call)
   */
  function startPolling(intervalMs: number = 2000) {
    if (polling.value) return;
    polling.value = true;

    // P1: Replace with actual GetSessions call from api
    pollTimer = setInterval(async () => {
      try {
        // const newSessions = await getSessions();
        // sessions.value = newSessions;
        console.log('[P1 Skeleton] Polling sessions...');
      } catch (error) {
        console.error('Failed to poll sessions:', error);
      }
    }, intervalMs);
  }

  /**
   * Stop polling
   */
  function stopPolling() {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
    polling.value = false;
  }

  /**
   * Set active session
   */
  function setActiveSession(sessionId: string | null) {
    activeSessionId.value = sessionId;
  }

  /**
   * Refresh sessions immediately
   */
  async function refresh() {
    // P1: await getSessions()
    console.log('[P1 Skeleton] Refreshing sessions...');
  }

  // Cleanup on unmount
  onUnmounted(() => {
    stopPolling();
  });

  return {
    sessions,
    runningSessions,
    activeSessionId,
    activeSession,
    polling,
    startPolling,
    stopPolling,
    setActiveSession,
    refresh,
  };
}
