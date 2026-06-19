/**
 * Session List Composable
 * P1: 真实轮询 GetSessions，写入 session store；管理 activeSessionId。
 *
 * 使用方式：
 *   const { startPolling, stopPolling, refresh, removeAndRefresh, stopAndRefresh } = useSessionList()
 *   onMounted(() => { refresh(); startPolling() })
 *   onUnmounted(() => stopPolling())
 *
 * 数据真相源：useSessionStore.sessions / activeSessionId。
 */

import { onUnmounted } from 'vue'
import { useSessionStore } from '../stores/session'
import * as sessionApi from '../api/session'

export function useSessionList() {
  const store = useSessionStore()

  let pollTimer: ReturnType<typeof setInterval> | null = null

  /**
   * 立即从后端拉取会话列表并写入 store。
   * 静默处理错误（runtime 缺失等），仅在控制台记录。
   */
  async function refresh(): Promise<void> {
    try {
      const list = await sessionApi.getSessions()
      store.setSessions(list || [])
    } catch (err) {
      console.error('[useSessionList] refresh failed:', err)
    }
  }

  /**
   * 启动周期性轮询（默认 2000ms）。
   * 重复调用安全：已运行则忽略。
   */
  function startPolling(intervalMs = 2000): void {
    if (pollTimer) return
    store.setPolling(true)
    pollTimer = setInterval(() => {
      refresh()
    }, intervalMs)
  }

  /**
   * 停止轮询。
   */
  function stopPolling(): void {
    if (pollTimer) {
      clearInterval(pollTimer)
      pollTimer = null
    }
    store.setPolling(false)
  }

  /** 选中会话 */
  function selectSession(sessionId: string | null): void {
    store.setActiveSession(sessionId)
  }

  /** 清除当前选中 */
  function clearActive(): void {
    store.setActiveSession(null)
  }

  /** 停止单个会话后刷新 */
  async function stopAndRefresh(sessionId: string): Promise<void> {
    await sessionApi.stopSession(sessionId)
    await refresh()
  }

  /** 移除单个会话后刷新（并清理 activeSessionId 关联） */
  async function removeAndRefresh(sessionId: string): Promise<void> {
    await sessionApi.removeSession(sessionId)
    store.removeSession(sessionId)
    await refresh()
  }

  // 默认在组件卸载时停止轮询
  onUnmounted(() => {
    stopPolling()
  })

  return {
    refresh,
    startPolling,
    stopPolling,
    selectSession,
    clearActive,
    stopAndRefresh,
    removeAndRefresh,
  }
}
