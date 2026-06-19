/**
 * Session Store
 * Manages session state
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import { session } from '../../wailsjs/go/models';

type SessionInfo = session.SessionInfo;

export const useSessionStore = defineStore('session', () => {
  // All sessions
  const sessions = ref<SessionInfo[]>([]);

  // Active session ID
  const activeSessionId = ref<string | null>(null);

  // Polling state
  const isPolling = ref(false);

  // Computed
  const runningSessions = computed(() => {
    return sessions.value.filter(s => s.status === 'running');
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

    // Computed
    runningSessions,
    activeSession,
    sessionCount,

    // Actions
    setSessions,
    addSession,
    updateSession,
    removeSession,
    setActiveSession,
    setPolling,
  };
});
