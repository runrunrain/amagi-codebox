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
import { EventsOn } from '../../wailsjs/runtime/runtime';
import * as remoteClientApi from '../api/remoteClient';
import type { HostEntry, RemoteSessionSummary, LegacyProviderSummary } from '../api/remoteClient';
import { remoteErrorText } from '../api/remoteClient';
import { prepareRemoteProviderUpload, prepareRemoteSettingsUpload } from '../utils/remoteMask';

export type LoadState = 'idle' | 'loading' | 'ready' | 'error';
export type ConnectState = 'disconnected' | 'connecting' | 'connected' | 'failed';

/** 本机 scope 常量。 */
export const LOCAL_SCOPE = 'local';

/** 远端会话列表轮询节奏（远程 REST 短连接，比本机 PTY 轮询低频）。 */
const SESSION_POLL_INTERVAL_MS = 4000;

/** 主机探活最小间隔（菜单打开时触发，防频繁出网）。 */
const HOST_PROBE_MIN_INTERVAL_MS = 15000;

/** M-6 修复：offline 主机周期探活节奏（交互稿 §3 offline 行「自动周期探活」）。 */
const HOST_PROBE_PERIODIC_MS = 30000;

/** 终端连接状态投影（internal/remoteclient/conn.go ConnState 冻结五态）。 */
export type RemoteConnState = 'disconnected' | 'connecting' | 'attached' | 'readonly' | 'degraded';

/**
 * RC4 远程配置域装载状态：idle/loading/ready + 两个专属降级态——
 * needs-token（未配置 legacy token → EmptyState + 配置入口）与 error
 * （含 401：已配 token 但被拒 → 提示重填）。authRejected 置位时视图
 * 文案切换为「令牌无效/被拒，请重新配置」。
 */
export type RemoteConfigLoadState = 'idle' | 'loading' | 'needs-token' | 'error' | 'ready';

export interface RemoteConfigSnapshot {
  state: RemoteConfigLoadState;
  error: string;
  /** true = auth.unpaired（宿主拒 token / token 未配置），文案引导重填。 */
  authRejected: boolean;
}

/** 单个远程终端会话的客户端侧状态（rc:* 事件聚合）。 */
export interface RemoteTerminalState {
  connState: RemoteConnState | '';
  /** 当前重连轮次（attach 成功清零）。 */
  attempt: number;
  /** 下一轮重连退避毫秒数。 */
  nextRetryMs: number;
  detail: string;
  /** 控制权四态（contract.ControlState：you/other/desktop/none）。 */
  controlState: string;
  /**
   * 降级检测（RC3-3）：本设备曾持有控制权且尚未恢复。事件流中离开 you 置位、
   * 回到 you 复位；主动释放（release 响应）也复位——自愿放手不是被抢占。
   */
  controlWasYou: boolean;
  /** state=other 时的持有设备名（契约条件字段，供「他人持有」文案）。 */
  controlDeviceName: string;
  /** 远端会话五态（contract.SessionState）。 */
  sessionState: string;
  /** attach 绑定已调用且未 detach。 */
  attached: boolean;
}

/** rc:terminal-output 分发载荷：输出帧或 history.gap 缺口通知（如实透出，不吞不改）。 */
export type RemoteTerminalOutputEvent =
  | { kind: 'output'; seq: number; data: string }
  | { kind: 'gap'; fromSeq: number; toSeq: number; source: string };

/** rc:session-state 分发载荷。 */
export interface RemoteSessionStateEvent {
  state: string;
  restartBoundary?: boolean;
}

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

  // ---- RC2-5 远程终端：attach 状态 + rc:* 事件聚合 ----
  const remoteTerminalStates = ref<Record<string, RemoteTerminalState>>({});
  /** 终端页当前展示的远程会话（远程模式下）。 */
  const activeRemoteTerminalId = ref<string | null>(null);
  /** rc:revoked fail-closed：当前主机授权已被撤销（交互稿 §3 revoked 行）。 */
  const connectRevoked = ref(false);

  // ---- RC4 远程配置域（legacy 接口：providers 列表/详情 + settings + token）----
  /** 远程提供商摘要列表（LegacyProviderSummary 五字段投影）。 */
  const remoteProviders = ref<LegacyProviderSummary[]>([]);
  const remoteProvidersView = ref<RemoteConfigSnapshot>({
    state: 'idle',
    error: '',
    authRejected: false,
  });
  /** 当前打开的远程提供商详情（净化后 JSON 字符串，凭据字段为掩码占位）。 */
  const remoteProviderName = ref('');
  const remoteProviderDoc = ref('');
  const remoteProviderDocView = ref<RemoteConfigSnapshot>({
    state: 'idle',
    error: '',
    authRejected: false,
  });
  /** 远程应用设置（净化后 JSON 字符串，remoteToken 等为掩码占位）。 */
  const remoteSettingsDoc = ref('');
  const remoteSettingsView = ref<RemoteConfigSnapshot>({
    state: 'idle',
    error: '',
    authRejected: false,
  });

  /** 当前主机是否已配置 legacy token（登记簿布尔投影，token 本体永不出 Keychain）。 */
  const currentHasLegacyToken = computed(() => currentHost.value?.hasLegacyToken === true);

  let pollTimer: ReturnType<typeof setTimeout> | null = null;
  let probeTimer: ReturnType<typeof setTimeout> | null = null;

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
      // M-6：登记簿非空即启动周期探活（offline 主机自动恢复检测；
      // probeHosts 自身 15s throttle，30s 节奏保证每轮真实出网一次）。
      if (hosts.value.length > 0) startHostProbing();
    } catch (err) {
      hostsState.value = hosts.value.length > 0 ? 'ready' : 'error';
      hostsError.value = remoteErrorText(err);
    }
  }

  /**
   * M-6 周期探活（setTimeout 接力单飞，与会话轮询同纪律）。一旦启动随应用
   * 生命周期运行——主机状态灯（含切换器灰/绿）依赖它恢复，不限于远程模式。
   */
  function startHostProbing(): void {
    if (probeTimer) return;
    const tick = async () => {
      try {
        await probeHosts();
      } finally {
        if (probeTimer !== null) {
          probeTimer = setTimeout(tick, HOST_PROBE_PERIODIC_MS);
        }
      }
    };
    probeTimer = setTimeout(tick, HOST_PROBE_PERIODIC_MS);
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
    resetRemoteConfigState();
    remoteSessions.value = [];
    remoteSessionsState.value = 'idle';
    remoteSessionsStale.value = false;
    remoteSessionsError.value = '';
    // RC2-5：终端状态随连接一并清空（Go 侧 Disconnect 已 DetachAll）。
    remoteTerminalStates.value = {};
    activeRemoteTerminalId.value = null;
    connectRevoked.value = false;
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
    connectRevoked.value = false;
    // 换主机：上一宿主的终端状态随之作废（Go 侧 Connect 顶替已 DetachAll）。
    remoteTerminalStates.value = {};
    activeRemoteTerminalId.value = null;
    try {
      await remoteClientApi.connectRemoteHost(hostID);
      scope.value = hostID;
      connectState.value = 'connected';
      remoteSessions.value = [];
      remoteSessionsState.value = 'idle';
      remoteSessionsStale.value = false;
      resetRemoteConfigState();
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

  /* -------------------------------------------------------------------------
   * RC2-5 远程终端：rc:* 事件桥（conn.go 文件头冻结清单）+ attach 生命周期
   *
   * 事件名与 payload（Go 侧 EventsEmit，map[string]any）：
   *   rc:conn-state      {sessionId, hostId, state, attempt, nextRetryMs, detail}
   *   rc:session-state   {sessionId, state, restartBoundary?, seq?, occurredAt}
   *   rc:terminal-output {sessionId, seq, data} | {sessionId, gap:{fromSeq,toSeq,source}}
   *   rc:control-state   {sessionId, state, deviceName?, reason, occurredAt}
   *   rc:revoked         {hostId, sessionId?}
   *   rc:host-health     {hostId, state}
   * ----------------------------------------------------------------------- */

  // 输出/会话态订阅表（非响应式：高频输出不进 ref，避免每帧触发依赖追踪）。
  const outputSubs = new Map<string, Set<(ev: RemoteTerminalOutputEvent) => void>>();
  const sessionStateSubs = new Map<string, Set<(ev: RemoteSessionStateEvent) => void>>();
  let rcBridgeReady = false;

  function ensureTermState(sessionID: string): RemoteTerminalState {
    const existing = remoteTerminalStates.value[sessionID];
    if (existing) return existing;
    const created: RemoteTerminalState = {
      connState: '',
      attempt: 0,
      nextRetryMs: 0,
      detail: '',
      controlState: '',
      controlWasYou: false,
      controlDeviceName: '',
      sessionState: '',
      attached: false,
    };
    remoteTerminalStates.value[sessionID] = created;
    return created;
  }

  /** 订阅某会话的终端输出（含 gap 缺口通知）；返回解除订阅函数。 */
  function subscribeRemoteTerminalOutput(
    sessionID: string,
    cb: (ev: RemoteTerminalOutputEvent) => void,
  ): () => void {
    ensureRcEventBridge();
    let set = outputSubs.get(sessionID);
    if (!set) {
      set = new Set();
      outputSubs.set(sessionID, set);
    }
    set.add(cb);
    return () => {
      set.delete(cb);
      if (set.size === 0) outputSubs.delete(sessionID);
    };
  }

  /** 订阅某会话的 rc:session-state；返回解除订阅函数。 */
  function subscribeRemoteSessionState(
    sessionID: string,
    cb: (ev: RemoteSessionStateEvent) => void,
  ): () => void {
    ensureRcEventBridge();
    let set = sessionStateSubs.get(sessionID);
    if (!set) {
      set = new Set();
      sessionStateSubs.set(sessionID, set);
    }
    set.add(cb);
    return () => {
      set.delete(cb);
      if (set.size === 0) sessionStateSubs.delete(sessionID);
    };
  }

  /** 注册六类 rc:* 事件监听（幂等；store 首次使用时即就绪）。 */
  function ensureRcEventBridge(): void {
    if (rcBridgeReady) return;
    rcBridgeReady = true;
    try {
      EventsOn('rc:conn-state', (p: any) => {
        if (!p || typeof p.sessionId !== 'string') return;
        const st = ensureTermState(p.sessionId);
        st.connState = String(p.state ?? '') as RemoteTerminalState['connState'];
        st.attempt = Number(p.attempt ?? 0);
        st.nextRetryMs = Number(p.nextRetryMs ?? 0);
        st.detail = String(p.detail ?? '');
      });
      EventsOn('rc:session-state', (p: any) => {
        if (!p || typeof p.sessionId !== 'string') return;
        const st = ensureTermState(p.sessionId);
        st.sessionState = String(p.state ?? '');
        if (st.sessionState === 'removed') {
          // 会话移除：幂等 scope 终结（Go 侧已停止重连）。
          st.connState = 'disconnected';
          st.detail = '会话已移除';
        }
        const ev: RemoteSessionStateEvent = {
          state: st.sessionState,
          restartBoundary: p.restartBoundary === true,
        };
        sessionStateSubs.get(p.sessionId)?.forEach((cb) => cb(ev));
      });
      EventsOn('rc:control-state', (p: any) => {
        if (!p || typeof p.sessionId !== 'string') return;
        applyControlSnapshot(p.sessionId, {
          state: String(p.state ?? ''),
          deviceName: typeof p.deviceName === 'string' ? p.deviceName : '',
        });
      });
      EventsOn('rc:terminal-output', (p: any) => {
        if (!p || typeof p.sessionId !== 'string') return;
        const subs = outputSubs.get(p.sessionId);
        if (!subs || subs.size === 0) return;
        let ev: RemoteTerminalOutputEvent;
        if (p.gap && typeof p.gap === 'object') {
          ev = {
            kind: 'gap',
            fromSeq: Number(p.gap.fromSeq ?? 0),
            toSeq: Number(p.gap.toSeq ?? 0),
            source: String(p.gap.source ?? ''),
          };
        } else if (typeof p.data === 'string') {
          ev = { kind: 'output', seq: Number(p.seq ?? 0), data: p.data };
        } else {
          return;
        }
        subs.forEach((cb) => cb(ev));
      });
      EventsOn('rc:revoked', (p: any) => {
        if (!p || typeof p.hostId !== 'string') return;
        // fail-closed（交互稿 §3 revoked 行）：清连接视图 + 终端全部断开；
        // Go 侧已丢弃连接并把登记簿健康置 revoked，这里同步前端投影。
        if (scope.value === p.hostId) {
          connectState.value = 'disconnected';
          connectRevoked.value = true;
          connectError.value = '';
          stopSessionPolling();
          for (const st of Object.values(remoteTerminalStates.value)) {
            st.connState = 'disconnected';
            st.detail = '授权已撤销';
          }
        }
        void loadHosts();
      });
      EventsOn('rc:host-health', (p: any) => {
        if (!p || typeof p.hostId !== 'string') return;
        const h = hosts.value.find((x) => x.id === p.hostId);
        if (h && typeof p.state === 'string') h.health = p.state;
      });
    } catch (err) {
      // 非 Wails 环境（单测等）：事件桥不可用，终端功能降级但不影响其余域。
      console.error('[stores.remoteClient] rc:* event bridge unavailable:', err);
    }
  }

  /** 打开某会话的远程终端（终端页展示目标；attach 由视图挂载时发起）。 */
  function openRemoteTerminal(sessionID: string): void {
    ensureRcEventBridge();
    activeRemoteTerminalId.value = sessionID;
  }

  /** attach（或复用）会话终端连接；幂等。 */
  async function attachRemoteTerminal(sessionID: string): Promise<void> {
    ensureRcEventBridge();
    const res = await remoteClientApi.attachRemoteTerminal(sessionID);
    const st = ensureTermState(sessionID);
    st.attached = true;
    if (res && typeof res.state === 'string' && res.state) {
      st.connState = res.state as RemoteTerminalState['connState'];
    }
  }

  /** detach 会话终端连接（停止重连、销毁 outbox）；视图卸载时调用。 */
  async function detachRemoteTerminal(sessionID: string): Promise<void> {
    const st = remoteTerminalStates.value[sessionID];
    if (st) st.attached = false;
    try {
      await remoteClientApi.detachRemoteTerminal(sessionID);
    } catch (err) {
      // 宿主连接已断开（切回本机/换主机/revoked）时 detach 必然失败：记录即可。
      console.error('[stores.remoteClient] detach terminal failed:', err);
    }
    delete remoteTerminalStates.value[sessionID];
    if (activeRemoteTerminalId.value === sessionID) {
      activeRemoteTerminalId.value = null;
    }
  }

  /* -------------------------------------------------------------------------
   * RC3-3 控制权域：acquire/release + 四态投影 + 被抢占降级判定
   *
   * 写权威在服务端（ControlView / rc:control-state 事件均为服务端快照），
   * store 是唯一落点：终端状态条与会话列表行都从这里读，组件不得自算控制态。
   * ----------------------------------------------------------------------- */

  /** 应用服务端控制权快照：更新终端状态投影并同步会话列表条目（同一数据源）。 */
  function applyControlSnapshot(
    sessionID: string,
    view: { state: string; deviceName?: string },
  ): void {
    const st = ensureTermState(sessionID);
    if (view.state !== st.controlState) {
      if (st.controlState === 'you') st.controlWasYou = true;
      if (view.state === 'you') st.controlWasYou = false;
      st.controlState = view.state;
    }
    st.controlDeviceName = view.deviceName ?? '';
    const s = remoteSessions.value.find((x) => x.id === sessionID);
    if (s && s.control) {
      s.control.state = view.state;
      s.control.deviceName = view.deviceName;
    }
  }

  /**
   * 控制权四态投影：rc:control-state 事件流优先，回退会话列表快照
   * （列表行的 control 来自服务端 SessionSummary.control，同为权威）。
   */
  function controlStateOf(sessionID: string): string {
    const st = remoteTerminalStates.value[sessionID];
    if (st && st.controlState) return st.controlState;
    return remoteSessions.value.find((s) => s.id === sessionID)?.control?.state ?? '';
  }

  /** state=other 时的持有设备名（事件流优先，回退列表快照）。 */
  function controlDeviceNameOf(sessionID: string): string {
    const st = remoteTerminalStates.value[sessionID];
    if (st && st.controlState) return st.controlDeviceName;
    return remoteSessions.value.find((s) => s.id === sessionID)?.control?.deviceName ?? '';
  }

  /**
   * 被抢占降级判定（交互稿 §3 只读行 + ControlBanner）：本设备曾持有控制权、
   * 事件流中被 desktop/other 夺走，且终端仍处于 attach 生命周期。
   * 恢复 you（重新获取成功）后自动回到 false。
   */
  function isControlDegraded(sessionID: string): boolean {
    const st = remoteTerminalStates.value[sessionID];
    if (!st || !st.attached || !st.controlWasYou) return false;
    return st.controlState === 'other' || st.controlState === 'desktop';
  }

  /**
   * 会话的控制权操作：running 会话四态下均可 acquire（none/other/desktop →
   * 「获取控制权」，desktop 持有时服务端可 409 拒绝，由调用方提示）；you → release。
   */
  function controlActionOf(sessionID: string): 'acquire' | 'release' | '' {
    const session = remoteSessions.value.find((s) => s.id === sessionID);
    if (!session || session.state !== 'running') return '';
    return controlStateOf(sessionID) === 'you' ? 'release' : 'acquire';
  }

  /**
   * 获取控制权：成功落 ControlView 权威投影；错误（含 409 control.busy）抛调用方。
   * 契约要求 acquire 持有 attach 租约（同进程 WS，见 conn.go/RC3-go 报告）：
   * 未 attach（如从会话列表行直接获取）时先幂等 attach，再走 acquire。
   */
  async function acquireControl(sessionID: string): Promise<void> {
    if (!remoteTerminalStates.value[sessionID]?.attached) {
      await attachRemoteTerminal(sessionID);
    }
    const view = await remoteClientApi.acquireRemoteControl(sessionID);
    applyControlSnapshot(sessionID, { state: view.state ?? '', deviceName: view.deviceName });
  }

  /** 释放控制权：主动放手不是被抢占，复位降级标记。 */
  async function releaseControl(sessionID: string): Promise<void> {
    const view = await remoteClientApi.releaseRemoteControl(sessionID);
    applyControlSnapshot(sessionID, { state: view.state ?? '', deviceName: view.deviceName });
    ensureTermState(sessionID).controlWasYou = false;
  }

  /* -------------------------------------------------------------------------
   * RC4 远程配置域（legacy 接口）：providers 列表/详情、settings 读写、token 配置。
   *
   * 错误分流（验收 5 + 空态/错误态）：auth.unpaired + 未配 token → needs-token
   * （EmptyState + 配置入口）；auth.unpaired + 已配 token → error + authRejected
   * （401 文案引导重填）；其余错误 → error + 原文。提交前统一经 utils/remoteMask
   * 剥离掩码占位（store 层第二道防线，视图为第一道）。
   * ----------------------------------------------------------------------- */

  /** RC4 域全部状态复位（切主机/切回本机时调用：远端配置快照随连接作废）。 */
  function resetRemoteConfigState(): void {
    remoteProviders.value = [];
    remoteProvidersView.value = { state: 'idle', error: '', authRejected: false };
    remoteProviderName.value = '';
    remoteProviderDoc.value = '';
    remoteProviderDocView.value = { state: 'idle', error: '', authRejected: false };
    remoteSettingsDoc.value = '';
    remoteSettingsView.value = { state: 'idle', error: '', authRejected: false };
  }

  /** 将 legacy 域错误归入 RemoteConfigSnapshot（需 scope 内当前主机已就绪）。 */
  function classifyConfigError(err: unknown): RemoteConfigSnapshot {
    const code = remoteClientApi.extractRemoteErrorCode(err);
    if (code === 'auth.unpaired' && !currentHasLegacyToken.value) {
      return { state: 'needs-token', error: remoteErrorText(err), authRejected: false };
    }
    return { state: 'error', error: remoteErrorText(err), authRejected: code === 'auth.unpaired' };
  }

  /** 拉取远程提供商列表（仅远程模式；远程模式下 ProviderCenter 数据源）。 */
  async function loadRemoteProviders(): Promise<void> {
    if (!isRemoteMode.value) return;
    remoteProvidersView.value = { state: 'loading', error: '', authRejected: false };
    try {
      remoteProviders.value = await remoteClientApi.listRemoteProviders(scope.value);
      remoteProvidersView.value = { state: 'ready', error: '', authRejected: false };
    } catch (err) {
      remoteProviders.value = [];
      remoteProvidersView.value = classifyConfigError(err);
    }
  }

  /** 打开远程提供商详情（净化后 JSON；密钥字段已由 Go 掩码为占位）。 */
  async function openRemoteProvider(name: string): Promise<void> {
    if (!isRemoteMode.value) return;
    remoteProviderName.value = name;
    remoteProviderDoc.value = '';
    remoteProviderDocView.value = { state: 'loading', error: '', authRejected: false };
    try {
      remoteProviderDoc.value = await remoteClientApi.getRemoteProviderJSON(scope.value, name);
      remoteProviderDocView.value = { state: 'ready', error: '', authRejected: false };
    } catch (err) {
      remoteProviderDoc.value = '';
      remoteProviderDocView.value = classifyConfigError(err);
    }
  }

  /** 关闭远程提供商详情。 */
  function closeRemoteProvider(): void {
    remoteProviderName.value = '';
    remoteProviderDoc.value = '';
    remoteProviderDocView.value = { state: 'idle', error: '', authRejected: false };
  }

  /**
   * 保存远程提供商（全量替换语义）：入参为视图编辑后的完整导出 JSON（含
   * 掩码占位）。内部先经 prepareRemoteProviderUpload 剥离掩码占位 +
   * 阻断含非统一密钥凭据字段的提交，再上传；成功后刷新列表与详情。
   */
  async function saveRemoteProvider(name: string, editedDocJSON: string): Promise<void> {
    if (!isRemoteMode.value) return;
    const payload = prepareRemoteProviderUpload(editedDocJSON);
    await remoteClientApi.putRemoteProviderJSON(scope.value, name, payload);
    await Promise.all([loadRemoteProviders(), openRemoteProvider(name)]);
  }

  /** 拉取远程应用设置（净化后 JSON）。 */
  async function loadRemoteSettings(): Promise<void> {
    if (!isRemoteMode.value) return;
    remoteSettingsView.value = { state: 'loading', error: '', authRejected: false };
    try {
      remoteSettingsDoc.value = await remoteClientApi.getRemoteSettingsJSON(scope.value);
      remoteSettingsView.value = { state: 'ready', error: '', authRejected: false };
    } catch (err) {
      remoteSettingsDoc.value = '';
      remoteSettingsView.value = classifyConfigError(err);
    }
  }

  /**
   * 保存远程应用设置：入参为编辑后的完整设置 JSON（含掩码占位）。
   * prepareRemoteSettingsUpload 一律剥离掩码占位（宿主仅消费非密钥字段）。
   */
  async function saveRemoteSettings(editedDocJSON: string): Promise<void> {
    if (!isRemoteMode.value) return;
    const payload = prepareRemoteSettingsUpload(editedDocJSON);
    await remoteClientApi.putRemoteSettingsJSON(scope.value, payload);
    await loadRemoteSettings();
  }

  /** 配置/替换当前主机的 legacy 访问令牌；成功后刷新登记簿布尔投影。 */
  async function setRemoteLegacyToken(token: string): Promise<void> {
    await remoteClientApi.setRemoteLegacyToken(scope.value, token);
    await loadHosts();
  }

  /** 清除当前主机的 legacy 访问令牌（幂等）；成功后刷新登记簿。 */
  async function clearRemoteLegacyToken(): Promise<void> {
    await remoteClientApi.clearRemoteLegacyToken(scope.value);
    await loadHosts();
  }

  // 事件桥随 store 创建即就绪：rc:host-health / rc:revoked 不依赖终端页打开。
  ensureRcEventBridge();

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
    remoteTerminalStates,
    activeRemoteTerminalId,
    connectRevoked,
    // RC4 远程配置域
    remoteProviders,
    remoteProvidersView,
    remoteProviderName,
    remoteProviderDoc,
    remoteProviderDocView,
    remoteSettingsDoc,
    remoteSettingsView,
    currentHasLegacyToken,
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
    openRemoteTerminal,
    attachRemoteTerminal,
    detachRemoteTerminal,
    subscribeRemoteTerminalOutput,
    subscribeRemoteSessionState,
    controlStateOf,
    controlDeviceNameOf,
    isControlDegraded,
    controlActionOf,
    acquireControl,
    releaseControl,
    // RC4 远程配置域
    loadRemoteProviders,
    openRemoteProvider,
    closeRemoteProvider,
    saveRemoteProvider,
    loadRemoteSettings,
    saveRemoteSettings,
    setRemoteLegacyToken,
    clearRemoteLegacyToken,
  };
});
