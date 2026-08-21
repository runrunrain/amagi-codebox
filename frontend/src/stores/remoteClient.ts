/**
 * Remote Client Store（RC1-6 桌面端互联 · 客户端域）
 *
 * 全局 host scope：'local' | hostID。切换到某主机 = 全应用进入远程模式，
 * 会话页数据面指向该主机（RemoteClientConnect 单连接模型：顶替既有连接）。
 * 状态模型按交互稿 §3：loading / empty / error(可重试，保留最后成功数据 +
 * 过期标记) / offline / revoked。
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import * as remoteClientApi from '../api/remoteClient';
import type { HostEntry, RemoteSessionSummary } from '../api/remoteClient';
import { remoteErrorText } from '../api/remoteClient';

export type LoadState = 'idle' | 'loading' | 'ready' | 'error';
export type ConnectState = 'disconnected' | 'connecting' | 'connected' | 'failed';

/** 本机 scope 常量。 */
export const LOCAL_SCOPE = 'local';

/** 远端会话列表轮询节奏（远程 REST 短连接，比本机 PTY 轮询低频）。 */
const SESSION_POLL_INTERVAL_MS = 4000;

/** 主机探活最小间隔（菜单打开时触发，防频繁出网）。 */
const HOST_PROBE_MIN_INTERVAL_MS = 15000;

export const useRemoteClientStore = defineStore('remoteClient', () => {
  // ---- host scope ----
  const scope = ref<string>(LOCAL_SCOPE);

  // ---- 主机登记簿 + 健康投影 ----
  const hosts = ref<HostEntry[]>([]);
  const hostsState = ref<LoadState>('idle');
  const hostsError = ref('');
  let lastProbeAt = 0;

  // ---- 当前连接态 ----
  const connectState = ref<ConnectState>('disconnected');
  const connectError = ref('');

  // ---- 远端会话列表 ----
  const remoteSessions = ref<RemoteSessionSummary[]>([]);
  const remoteSessionsState = ref<LoadState>('idle');
  /** error(可重试)：保留最后成功数据时置 true（过期标记）。 */
  const remoteSessionsStale = ref(false);
  const remoteSessionsError = ref('');
  const lastSyncedAt = ref<number | null>(null);

  // ---- 配对向导 ----
  const pairingWizardOpen = ref(false);

  let pollTimer: ReturnType<typeof setTimeout> | null = null;

  const isRemoteMode = computed(() => scope.value !== LOCAL_SCOPE);
  const currentHost = computed<HostEntry | null>(
    () => hosts.value.find((h) => h.id === scope.value) ?? null,
  );
  const currentHostName = computed(() => currentHost.value?.displayName || '远程主机');

  /** 装载主机登记簿。 */
  async function loadHosts(): Promise<void> {
    if (hostsState.value === 'idle') hostsState.value = 'loading';
    try {
      hosts.value = (await remoteClientApi.listRemoteHosts()) ?? [];
      hostsState.value = 'ready';
      hostsError.value = '';
    } catch (err) {
      hostsState.value = hosts.value.length > 0 ? 'ready' : 'error';
      hostsError.value = remoteErrorText(err);
    }
  }

  /**
   * 后台探活全部登记主机（throttle）。后端把健康结论写回登记簿，
   * 前端随后重读列表刷新状态灯。
   */
  async function probeHosts(force = false): Promise<void> {
    if (hosts.value.length === 0) return;
    const now = Date.now();
    if (!force && now - lastProbeAt < HOST_PROBE_MIN_INTERVAL_MS) return;
    lastProbeAt = now;
    for (const h of hosts.value) {
      try {
        await remoteClientApi.probeRemoteHost(h.hostPort);
      } catch {
        // 单次探活失败不阻断后续主机（callApi 已打日志）。
      }
    }
    try {
      hosts.value = (await remoteClientApi.listRemoteHosts()) ?? hosts.value;
    } catch {
      // 保留现有列表。
    }
  }

  /** 切回本机：断开远程连接视图（尽力而为），清空远端会话数据面。 */
  async function switchToLocal(): Promise<void> {
    const prev = scope.value;
    scope.value = LOCAL_SCOPE;
    stopSessionPolling();
    remoteSessions.value = [];
    remoteSessionsState.value = 'idle';
    remoteSessionsStale.value = false;
    remoteSessionsError.value = '';
    if (prev !== LOCAL_SCOPE && connectState.value === 'connected') {
      connectState.value = 'disconnected';
      try {
        await remoteClientApi.disconnectRemoteHost(prev);
      } catch (err) {
        console.error('[stores.remoteClient] disconnect failed:', err);
      }
    }
    connectError.value = '';
  }

  /**
   * 切换到指定主机：Connect（凭据恢复 + 验证）→ 进入远程模式 → 拉取会话列表。
   * 失败时不切换 scope，抛出错误由调用方展示（revoked/unreachable 等健康
   * 结论已由后端写回登记簿，这里顺手刷新主机列表让状态灯即时反映）。
   */
  async function switchToHost(hostID: string): Promise<void> {
    if (hostID === scope.value && connectState.value === 'connected') return;
    connectState.value = 'connecting';
    connectError.value = '';
    try {
      await remoteClientApi.connectRemoteHost(hostID);
      scope.value = hostID;
      connectState.value = 'connected';
      remoteSessions.value = [];
      remoteSessionsState.value = 'idle';
      remoteSessionsStale.value = false;
      await loadHosts();
      await refreshRemoteSessions();
      startSessionPolling();
    } catch (err) {
      connectState.value = 'failed';
      connectError.value = remoteErrorText(err);
      try {
        await loadHosts();
      } catch {
        // 忽略列表刷新失败。
      }
      throw err;
    }
  }

  /**
   * 刷新远端会话列表。首屏 loading 不渲染旧数据冒充；已有数据时失败走
   * error(可重试)：保留最后成功数据 + stale 过期标记。
   */
  async function refreshRemoteSessions(): Promise<void> {
    if (!isRemoteMode.value) return;
    const firstLoad = remoteSessionsState.value === 'idle';
    if (firstLoad) remoteSessionsState.value = 'loading';
    try {
      remoteSessions.value = await remoteClientApi.listRemoteSessions();
      remoteSessionsState.value = 'ready';
      remoteSessionsStale.value = false;
      remoteSessionsError.value = '';
      lastSyncedAt.value = Date.now();
    } catch (err) {
      remoteSessionsError.value = remoteErrorText(err);
      if (firstLoad || remoteSessions.value.length === 0) {
        remoteSessionsState.value = 'error';
      } else {
        remoteSessionsStale.value = true;
      }
    }
  }

  /** 启动会话列表轮询（setTimeout 接力单飞，杜绝慢请求堆积）。 */
  function startSessionPolling(intervalMs = SESSION_POLL_INTERVAL_MS): void {
    if (pollTimer) return;
    const tick = async () => {
      await refreshRemoteSessions();
      if (pollTimer !== null) {
        pollTimer = setTimeout(tick, intervalMs);
      }
    };
    pollTimer = setTimeout(tick, intervalMs);
  }

  /** 停止会话列表轮询。 */
  function stopSessionPolling(): void {
    if (pollTimer) {
      clearTimeout(pollTimer);
      pollTimer = null;
    }
  }

  // ---- 会话行操作（调用绑定后刷新列表；错误抛给调用方展示）----

  async function stopRemoteSession(sessionID: string): Promise<void> {
    await remoteClientApi.stopRemoteSession(sessionID);
    await refreshRemoteSessions();
  }

  async function restartRemoteSession(sessionID: string): Promise<void> {
    await remoteClientApi.restartRemoteSession(sessionID);
    await refreshRemoteSessions();
  }

  async function deleteRemoteSession(sessionID: string): Promise<void> {
    await remoteClientApi.deleteRemoteSession(sessionID);
    await refreshRemoteSessions();
  }

  function openPairingWizard(): void {
    pairingWizardOpen.value = true;
  }

  return {
    // State
    scope,
    hosts,
    hostsState,
    hostsError,
    connectState,
    connectError,
    remoteSessions,
    remoteSessionsState,
    remoteSessionsStale,
    remoteSessionsError,
    lastSyncedAt,
    pairingWizardOpen,
    // Computed
    isRemoteMode,
    currentHost,
    currentHostName,
    // Actions
    loadHosts,
    probeHosts,
    switchToLocal,
    switchToHost,
    refreshRemoteSessions,
    startSessionPolling,
    stopSessionPolling,
    stopRemoteSession,
    restartRemoteSession,
    deleteRemoteSession,
    openPairingWizard,
  };
});
