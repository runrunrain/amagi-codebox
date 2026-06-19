/**
 * Terminal Engine Composable (P0 Skeleton)
 * P1 will integrate full xterm functionality from legacy Terminals.vue
 */

import { ref, computed, onUnmounted } from 'vue';

export function useTerminalEngine() {
  // Terminal instance map
  const terminals = ref<Map<string, any>>(new Map());
  const activeSessionId = ref<string | null>(null);

  // Active terminal instance
  const activeTerminal = computed(() => {
    if (!activeSessionId.value) return null;
    return terminals.value.get(activeSessionId.value) || null;
  });

  /**
   * Mount terminal to DOM element (P1: actual xterm instantiation)
   */
  function mountTerminal(sessionId: string, element: HTMLElement) {
    // P1: Will create Terminal instance, load addons, register events
    // For now, just track the session
    if (!terminals.value.has(sessionId)) {
      terminals.value.set(sessionId, {
        sessionId,
        element,
        ready: false,
      });
    }
  }

  /**
   * Write input to terminal (P1: PtyWrite integration)
   */
  function writeInput(sessionId: string, data: string) {
    const term = terminals.value.get(sessionId);
    if (term && term.ready) {
      // P1: term.write(data)
      console.log(`[P1 Skeleton] Writing to terminal ${sessionId}:`, data);
    }
  }

  /**
   * Dispose terminal (P1: actual cleanup)
   */
  function disposeTerminal(sessionId: string) {
    const term = terminals.value.get(sessionId);
    if (term) {
      // P1: term.dispose()
      terminals.value.delete(sessionId);
      if (activeSessionId.value === sessionId) {
        activeSessionId.value = null;
      }
    }
  }

  /**
   * Resize all terminals (P1: actual resize)
   */
  function resizeAll(cols: number, rows: number) {
    // P1: Iterate and call PtyResize for each active terminal
    console.log(`[P1 Skeleton] Resizing all terminals to ${cols}x${rows}`);
  }

  /**
   * Switch active session
   */
  function switchSession(sessionId: string | null) {
    activeSessionId.value = sessionId;
  }

  // Cleanup on unmount
  onUnmounted(() => {
    terminals.value.forEach((_, sessionId) => {
      disposeTerminal(sessionId);
    });
  });

  return {
    terminals,
    activeSessionId,
    activeTerminal,
    mountTerminal,
    writeInput,
    disposeTerminal,
    resizeAll,
    switchSession,
  };
}
