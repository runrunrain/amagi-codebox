/**
 * TEST-ONLY window.go.main.App 场景 stub（M1-C PG-05 浏览器证据用）。
 * 模拟 internal/remote M1 后端语义：
 * - 服务启停 / 监听配置（写事件）
 * - 配对窗口：一次性短码、generation CAS 取消、过期自动关闭、状态轮询
 * - 设备：配对列表、撤销（confirm=false 拒绝）
 * - 安全事件：sanitized 投影（newest-first）
 * - 健康快照：有界 issue + acknowledge
 * 场景控制经 window.__remoteHarness 暴露（供 Playwright 驱动）；
 * 所有调用记录于 calls[]（供断言，如 RevokeRemoteDevice 的 confirm=true）。
 * Major-06 对齐：security ready ≠ server running——设备列表/撤销/安全事件
 * 在服务关闭时仍可用（与 internal/remote 真实语义一致）。
 * R2-N01 对齐：配对窗口三方法中仅 CreateWindow 有 accepting 门
 * （device.go CreateWindow 检查 accepting）；GetPairingWindow/CancelPairingWindow
 * 只查 requireSecurity（server_security.go:307-320），stopped 时分别返回
 * Active:false / false，不抛错。
 * 本文件不进入生产构建。
 */

interface HarnessEvent {
  eventId: string;
  kind: string;
  occurredAt: string;
  outcome?: string;
}

interface HarnessDevice {
  id: string;
  name: string;
  pairedAt: string;
  lastSeenAt: string;
  credentialExpiresAt: string;
  revokedAt?: string;
}

interface HarnessIssue {
  code: string;
  active: boolean;
  acknowledged: boolean;
  firstObservedAt: string;
  lastObservedAt: string;
  occurrences: number;
  droppedEventIds: number[];
  recentEventIds: string[];
}

interface HarnessWindow {
  generation: number;
  code: string;
  expiresAt: number;
  baseUrl: string;
  addressRequired: boolean;
}

/** M2-INT R12：外部进程清理恢复场景项（对齐 backend privacy status 语义：
 *  processAlive 是 stub 侧的“OS 活性”真相；status/confirm 均按它复检）。 */
interface HarnessRecoveryItem {
  sessionId: string;
  kind: number;
  reason: string;
  processAlive: boolean;
}

interface HarnessState {
  running: boolean;
  host: string;
  port: number;
  generation: number;
  window: HarnessWindow | null;
  devices: HarnessDevice[];
  events: HarnessEvent[];
  issues: HarnessIssue[];
  nextWindowTtlMs: number;
  windowAddressRequired: boolean;
  eventSeq: number;
  recoveryItems: HarnessRecoveryItem[];
  startupWarnings: string[];
  failNextConfirmWithPersistence: boolean;
}

declare global {
  interface Window {
    __remoteHarness?: {
      state: () => HarnessState;
      calls: Array<{ method: string; args: unknown[] }>;
      setNextWindowTtl: (ms: number) => void;
      expireActiveWindow: () => void;
      setWindowAddressRequired: (v: boolean) => void;
      pushHealthIssue: (code: string, active: boolean) => void;
      setRecoveryProcessAlive: (sessionId: string, alive: boolean) => void;
      failNextConfirmWithPersistence: () => void;
      pushStartupWarning: (msg: string) => void;
      reset: () => void;
    };
  }
}

function seedDevices(): HarnessDevice[] {
  return [
    {
      id: 'DEVpixel8AAAAAAAAAAAA0',
      name: 'Pixel 8',
      pairedAt: '2026-08-01T14:05:00+08:00',
      lastSeenAt: '2026-08-02T09:40:00+08:00',
      credentialExpiresAt: '2026-11-01T14:05:00+08:00',
    },
    {
      id: 'DEVipadAirAAAAAAAAAAA1',
      name: 'iPad Air',
      pairedAt: '2026-07-30T10:12:00+08:00',
      lastSeenAt: '2026-08-01T22:03:00+08:00',
      credentialExpiresAt: '2026-10-30T10:12:00+08:00',
    },
  ];
}

function initialState(): HarnessState {
  return {
    running: false,
    host: '0.0.0.0',
    port: 8680,
    generation: 0,
    window: null,
    devices: seedDevices(),
    events: [],
    issues: [],
    nextWindowTtlMs: 90_000,
    windowAddressRequired: false,
    eventSeq: 0,
    recoveryItems: [],
    startupWarnings: [],
    failNextConfirmWithPersistence: false,
  };
}

export function installRemoteStub(): void {
  let state = initialState();
  const calls: Array<{ method: string; args: unknown[] }> = [];

  // 场景种子：?seedHealth=1 预置一个已关闭未确认的健康问题
  const params = new URLSearchParams(location.search);
  if (params.get('seedHealth') === '1') {
    state.issues.push({
      code: 'durable_sink_degraded',
      active: false,
      acknowledged: false,
      firstObservedAt: new Date(Date.now() - 3600_000).toISOString(),
      lastObservedAt: new Date(Date.now() - 600_000).toISOString(),
      occurrences: 3,
      droppedEventIds: [],
      recentEventIds: [],
    });
  }

  // 场景种子（M2-INT R12）：?seedRecovery=running|awaiting|two
  //   running  → 旧进程仍存活（status 返回 running，confirm 被 live 拒绝）
  //   awaiting → 旧进程已退出（status 返回 awaiting_confirmation，可确认）
  //   two      → 一项存活一项已退出（多项计数/fence 不完全释放）
  const seedRecovery = params.get('seedRecovery');
  if (seedRecovery === 'running' || seedRecovery === 'awaiting' || seedRecovery === 'two') {
    state.recoveryItems.push({
      sessionId: 'SES-LEGACY-0001',
      kind: 2,
      reason: 'legacy_process_identity',
      processAlive: seedRecovery === 'running',
    });
    if (seedRecovery === 'two') {
      state.recoveryItems.push({
        sessionId: 'SES-LEGACY-0002',
        kind: 3,
        reason: 'identity_inspection_uncertain',
        processAlive: true,
      });
    }
  }

  // 场景种子：?seedStartupWarning=1 预置与 app.go Startup 完全同文的启动警告
  if (params.get('seedStartupWarning') === '1') {
    state.startupWarnings.push(
      '检测到未完成的外部进程清理；请先关闭旧外部终端，再通过恢复确认 API 重新核验并解锁 Headroom',
    );
  }

  const delay = () => new Promise((r) => setTimeout(r, 30));

  function pushEvent(kind: string, outcome?: string) {
    state.eventSeq += 1;
    state.events.unshift({
      eventId: `EV-${String(state.eventSeq).padStart(4, '0')}`,
      kind,
      occurredAt: new Date().toISOString(),
      ...(outcome ? { outcome } : {}),
    });
  }

  /** 轮询语义：过期窗口在下一次状态查询时关闭并记录事件（对齐后端惰性过期） */
  function sweepExpiredWindow() {
    if (state.window && Date.now() >= state.window.expiresAt) {
      state.window = null;
      pushEvent('pairing_window_expired');
    }
  }

  async function call<T>(method: string, args: unknown[], fn: () => T): Promise<T> {
    calls.push({ method, args });
    await delay();
    return fn();
  }

  const app = {
    GetRemoteStatus: () =>
      call('GetRemoteStatus', [], () => ({
        host: state.host,
        port: state.port,
        // token 字段存在（与真实后端一致），页面按硬规则不得展示/提供复制
        token: 'STUB-TOKEN-MUST-NOT-BE-DISPLAYED',
        running: state.running,
      })),

    ToggleRemoteServer: (enabled: boolean) =>
      call('ToggleRemoteServer', [enabled], () => {
        state.running = !!enabled;
        pushEvent(enabled ? 'remote_service_started' : 'remote_service_stopped');
        if (!enabled) state.window = null;
      }),

    SetRemoteHost: (host: string) =>
      call('SetRemoteHost', [host], () => {
        state.host = host;
        pushEvent('remote_listen_configuration_changed');
      }),

    SetRemotePort: (port: number) =>
      call('SetRemotePort', [port], () => {
        state.port = port;
        pushEvent('remote_listen_configuration_changed');
      }),

    // Minor-02：与真实 App.SetRemoteEndpoint 对齐——单次事务，两项均校验通过
    // 才提交；任一不合法则整体拒绝且无副作用（无部分提交）。
    SetRemoteEndpoint: (host: string, port: number) =>
      call('SetRemoteEndpoint', [host, port], () => {
        const h = (host || '').trim();
        if (!h) throw new Error('remote host must not be empty');
        if (!Number.isInteger(port) || port < 1024 || port > 65535) {
          throw new Error(`port ${port} out of valid range [1024, 65535]`);
        }
        state.host = h;
        state.port = port;
        pushEvent('remote_listen_configuration_changed');
      }),

    GetRemoteToken: () => call('GetRemoteToken', [], () => 'STUB-TOKEN-MUST-NOT-BE-DISPLAYED'),

    RegenerateRemoteToken: () =>
      call('RegenerateRemoteToken', [], () => {
        pushEvent('legacy_token_rotated');
        return 'STUB-TOKEN-ROTATED';
      }),

    CreateRemotePairingWindow: (confirmTerminalExposure: boolean) =>
      call('CreateRemotePairingWindow', [confirmTerminalExposure], () => {
        if (!confirmTerminalExposure || !state.running) {
          throw new Error('security state unavailable');
        }
        sweepExpiredWindow();
        // 与后端一致：创建新窗口会取代旧窗口
        state.generation += 1;
        const win: HarnessWindow = {
          generation: state.generation,
          code: `ABCD-EFGH-${String(state.generation).padStart(2, '0')}`,
          expiresAt: Date.now() + state.nextWindowTtlMs,
          baseUrl: state.windowAddressRequired ? '' : `http://192.168.1.20:${state.port}`,
          addressRequired: state.windowAddressRequired,
        };
        state.window = win;
        pushEvent('pairing_window_opened');
        return {
          generation: win.generation,
          code: win.code,
          expiresAt: new Date(win.expiresAt).toISOString(),
          baseUrl: win.baseUrl || undefined,
          addressRequired: win.addressRequired,
        };
      }),

    // R2-N01：与生产对齐（server_security.go:307-313）——GetPairingWindow 只查
    // requireSecurity，不检查 running；stopped 且无活跃窗口时返回 Active:false。
    GetRemotePairingWindow: () =>
      call('GetRemotePairingWindow', [], () => {
        sweepExpiredWindow();
        if (!state.window) return { active: false };
        return {
          active: true,
          generation: state.window.generation,
          expiresAt: new Date(state.window.expiresAt).toISOString(),
          remainingAttempts: 3,
        };
      }),

    // R2-N01：与生产对齐（server_security.go:315-320 + device.go CancelWindow）——
    // 只查 requireSecurity；stopped 且无活跃窗口时 CAS 落空返回 false，不抛错。
    CancelRemotePairingWindow: (generation: number) =>
      call('CancelRemotePairingWindow', [generation], () => {
        sweepExpiredWindow();
        if (state.window && state.window.generation === generation) {
          state.window = null;
          pushEvent('pairing_window_canceled');
          return true;
        }
        return false;
      }),

    // Major-06：与真实后端对齐——ListDevices 只要求 security ready，不要求
    // server running（internal/remote/server_security.go requireSecurity 不检查
    // running）；服务关闭时仍返回真实持久设备列表。
    ListRemoteDevices: () =>
      call('ListRemoteDevices', [], () => state.devices.filter((d) => !d.revokedAt)),

    // Major-06：撤销同样不受 running 门控（真实后端 RevokeDevice 只校验
    // confirm/id/security ready）；服务关闭时撤销真实生效并写事件。
    RevokeRemoteDevice: (deviceID: string, confirm: boolean) =>
      call('RevokeRemoteDevice', [deviceID, confirm], () => {
        if (!confirm) throw new Error('security state unavailable');
        const dev = state.devices.find((d) => d.id === deviceID);
        if (!dev) throw new Error('invalid id');
        if (dev.revokedAt) {
          return {
            device: dev,
            alreadyRevoked: true,
            terminationRequestedConnections: 0,
            eventOutcome: 'accepted',
            durabilityDegraded: false,
          };
        }
        dev.revokedAt = new Date().toISOString();
        pushEvent('device_revoked');
        return {
          device: dev,
          alreadyRevoked: false,
          terminationRequestedConnections: 1,
          eventOutcome: 'accepted',
          durabilityDegraded: false,
        };
      }),

    ListRemoteSecurityEvents: (limit: number) =>
      call('ListRemoteSecurityEvents', [limit], () => state.events.slice(0, limit)),

    // Major-06：SecurityReady 是 device store 就绪闩（与 running 无关），
    // stub 模拟安全状态已加载的健康后端 → 恒为 true。
    GetRemoteSecurityHealth: () =>
      call('GetRemoteSecurityHealth', [], () => ({
        securityReady: true,
        issues: state.issues,
      })),

    AcknowledgeRemoteSecurityHealth: (code: string) =>
      call('AcknowledgeRemoteSecurityHealth', [code], () => {
        const issue = state.issues.find((i) => i.code === code);
        if (!issue) throw new Error('invalid id');
        if (issue.active) throw new Error('security state unavailable');
        issue.acknowledged = true;
        return { securityReady: true, issues: state.issues };
      }),

    OpenRemoteWebUI: () => call('OpenRemoteWebUI', [], () => ({ ok: true })),

    /* ------------------------------------------------------------------
     * M2-INT R12：恢复 status/confirm/startup warnings stub。
     * 语义对齐 headroom_facade.go：status 每次调用复检活性；
     * confirm 走 confirmed=true + 活性复检 + 持久化，无 force-clear。
     * ------------------------------------------------------------------ */
    GetExternalCleanupRecoveryStatus: () =>
      call('GetExternalCleanupRecoveryStatus', [], () => ({
        version: 1,
        blocked: state.recoveryItems.length > 0,
        items: state.recoveryItems.map((item) => ({
          sessionId: item.sessionId,
          kind: item.kind,
          reason: item.reason,
          state: item.processAlive ? 'running' : 'awaiting_confirmation',
          canConfirm: !item.processAlive,
        })),
      })),

    ConfirmExternalCleanupRecovery: (sessionID: string, confirmed: boolean) =>
      call('ConfirmExternalCleanupRecovery', [sessionID, confirmed], () => {
        const item = state.recoveryItems.find((i) => i.sessionId === sessionID);
        if (!item) throw new Error('external cleanup recovery: item not found');
        if (!confirmed) {
          throw new Error('external cleanup recovery: explicit confirmation required');
        }
        if (item.processAlive) {
          throw new Error('external cleanup recovery: process is still running');
        }
        if (state.failNextConfirmWithPersistence) {
          state.failNextConfirmWithPersistence = false;
          throw new Error('confirm external cleanup persistence: external cleanup store: not ready');
        }
        state.recoveryItems = state.recoveryItems.filter((i) => i.sessionId !== sessionID);
        return {
          sessionId: sessionID,
          cleared: true,
          fenceReleased: state.recoveryItems.length === 0,
        };
      }),

    GetStartupWarnings: () => call('GetStartupWarnings', [], () => [...state.startupWarnings]),
  };

  (window as unknown as { go: { main: { App: typeof app } } }).go = { main: { App: app } };

  window.__remoteHarness = {
    state: () => JSON.parse(JSON.stringify(state)) as HarnessState,
    calls,
    setNextWindowTtl: (ms: number) => {
      state.nextWindowTtlMs = ms;
    },
    expireActiveWindow: () => {
      if (state.window) state.window.expiresAt = Date.now() - 1;
    },
    setWindowAddressRequired: (v: boolean) => {
      state.windowAddressRequired = v;
    },
    pushHealthIssue: (code: string, active: boolean) => {
      state.issues.push({
        code,
        active,
        acknowledged: false,
        firstObservedAt: new Date().toISOString(),
        lastObservedAt: new Date().toISOString(),
        occurrences: 1,
        droppedEventIds: [],
        recentEventIds: [],
      });
    },
    setRecoveryProcessAlive: (sessionId: string, alive: boolean) => {
      const item = state.recoveryItems.find((i) => i.sessionId === sessionId);
      if (item) item.processAlive = alive;
    },
    failNextConfirmWithPersistence: () => {
      state.failNextConfirmWithPersistence = true;
    },
    pushStartupWarning: (msg: string) => {
      state.startupWarnings.push(msg);
    },
    reset: () => {
      state = initialState();
      calls.length = 0;
      try {
        localStorage.clear();
      } catch {
        /* ignore */
      }
    },
  };
}
