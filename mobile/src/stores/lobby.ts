/**
 * stores/lobby.ts — PG-02 会话大厅状态（M2-B）
 * ---------------------------------------------------------------------------
 * 权威依据：Task Contract M2-B；design §5.2 端点语义 / §8.1-§8.3 错误分类。
 * 职责：
 *   · 大厅加载：host/summary + sessions 列表（paired device 即可，观察者可读）；
 *   · 授权失效（auth.* 401）→ 清态，由页面负责踢回 PG-01；
 *   · 错误分类（禁笼统失败 AC-23/AC-24）：连接异常 / 宿主会话服务不可用 /
 *     授权失效 / 其他契约错误（保留 code+message）；
 *   · 启动失败四分类（AC-25）：workdir(400 bad_request) / capability / context /
 *     effect(422 session.launch_failed，固定服务端文案直接呈现，不复制冻结字符串)；
 *   · 控制权：acquire/release；control.busy(409)→冲突反馈（不静默覆盖），
 *     control.forbidden(403)→观察者语义说明。
 * 本 store 不持有任何凭据；Cookie 是唯一凭据载体（HttpOnly，脚本不可读）。
 * ---------------------------------------------------------------------------
 */

import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import {
  CLI_TYPE_CLAUDE_CODE,
  CLI_TYPE_CODEX,
  CLI_TYPE_OMP,
  CLI_TYPE_OPENCODE,
  CLI_TYPE_PI,
  ERROR_CODE_AUTH_REVOKED,
  ERROR_CODE_AUTH_UNPAIRED,
  ERROR_CODE_AUTH_WINDOW_EXPIRED,
  ERROR_CODE_BAD_REQUEST,
  ERROR_CODE_CONTROL_BUSY,
  ERROR_CODE_CONTROL_FORBIDDEN,
  ERROR_CODE_NET_UNREACHABLE,
  ERROR_CODE_RATE_LIMITED,
  ERROR_CODE_SERVICE_DOWN,
  ERROR_CODE_SESSION_LAUNCH_FAILED,
  type CLIType,
  type ControlSnapshot,
  type HostSummary,
  type CreateSessionRequest,
  type SessionID,
  type SessionSummary,
} from '../lib/contract';
import {
  acquireControl,
  createSession,
  getHostSummary,
  listSessions,
  releaseControl,
  removeSession,
  restartSession,
  stopSession,
  toApiRequestError,
  type ApiRequestError,
} from '../lib/api';
import { createTimingRecorder, type TimingRecorder, type TimingReportV1 } from '../lib/timing';

// --- 错误分类（展示层消费；禁笼统失败） ---

/** 大厅级错误分类：连接 / 宿主服务 / 授权 / 其他契约错误。 */
export type LobbyErrorKind = 'connection' | 'service' | 'auth' | 'other';

export interface ClassifiedError {
  kind: LobbyErrorKind;
  /** 分类标题（如「网络不可达」）。 */
  title: string;
  /** 说明与可执行动作指引。 */
  guidance: string;
  /** 服务端原始 message（契约错误体），供用户核对；非契约错误为本地描述。 */
  detail: string;
  /** 契约错误码（调试用小字；不是凭据）。 */
  code: string;
}

/** 启动失败四分类（AC-25，design §8.3）。 */
export type LaunchFailureKind = 'workdir' | 'launch-failed' | 'control' | 'connection' | 'other';

export interface ClassifiedLaunchError {
  kind: LaunchFailureKind;
  cliType: CLIType;
  title: string;
  /** 服务端固定分类文案（422/400 时原样呈现；design §8.3 冻结文案，前端不复制）。 */
  detail: string;
  guidance: string;
  code: string;
}

function isAuthError(err: ApiRequestError): boolean {
  return (
    err.code === ERROR_CODE_AUTH_UNPAIRED ||
    err.code === ERROR_CODE_AUTH_REVOKED ||
    err.code === ERROR_CODE_AUTH_WINDOW_EXPIRED
  );
}

/** 大厅请求错误统一分类。 */
export function classifyLobbyError(rawErr: unknown): ClassifiedError {
  const err = toApiRequestError(rawErr);
  if (err.code === ERROR_CODE_NET_UNREACHABLE && err.status === null) {
    return {
      kind: 'connection',
      title: '网络不可达',
      guidance: '请确认这台设备与桌面端在同一局域网，然后重试。',
      detail: err.message,
      code: err.code,
    };
  }
  if (
    err.code === ERROR_CODE_SERVICE_DOWN ||
    // 宿主未装配会话接口时 index 2-9 不可达（M2-A hardening gate 的安全默认）：
    // 真实服务器对未注册路径返回 404 + 契约 bad_request 体；纯 SPA fallback 则
    // 为非 JSON 404（客户端兜底 net.unreachable）。两者都如实归类为「宿主会话
    // 服务不可用」，不伪装成空列表或网络失败。
    (err.status === 404 && err.code === ERROR_CODE_BAD_REQUEST) ||
    (err.status === 404 && err.code === ERROR_CODE_NET_UNREACHABLE) ||
    (err.status !== null && err.status >= 500)
  ) {
    return {
      kind: 'service',
      title: '宿主会话服务不可用',
      guidance: '请回桌面端确认远程访问与会话服务已开启（设置 › 远程访问），然后重试。',
      detail: err.message,
      code: err.code,
    };
  }
  if (isAuthError(err)) {
    return {
      kind: 'auth',
      title: '授权已失效',
      guidance: '请重新配对恢复访问。',
      detail: err.message,
      code: err.code,
    };
  }
  return {
    kind: 'other',
    title: '请求未完成',
    guidance: '请稍后重试；若持续失败请回桌面端查看服务状态。',
    detail: err.message,
    code: err.code,
  };
}

/** 启动失败分类（AC-25）：400→workdir；422→launch-failed（服务端固定文案呈现）；control.*→控制权。 */
export function classifyLaunchError(rawErr: unknown, cliType: CLIType): ClassifiedLaunchError {
  const err = toApiRequestError(rawErr);
  if (err.code === ERROR_CODE_BAD_REQUEST) {
    return {
      kind: 'workdir',
      cliType,
      title: '工作目录无效',
      detail: err.message,
      guidance: '请在桌面端为该 CLI 配置有效的工作目录后重试。',
      code: err.code,
    };
  }
  if (err.code === ERROR_CODE_SESSION_LAUNCH_FAILED) {
    return {
      kind: 'launch-failed',
      cliType,
      // design §8.3：422 的 message 是冻结分类文案（capability/context/effect 三选一），
      // 原样呈现即为分类结果，前端不复制冻结字符串、不猜测子类。
      title: '启动失败',
      detail: err.message,
      guidance: '请回桌面端检查该 CLI 的安装与远程启动配置（设置 › 远程访问），然后重试。',
      code: err.code,
    };
  }
  if (err.code === ERROR_CODE_CONTROL_BUSY || err.code === ERROR_CODE_CONTROL_FORBIDDEN) {
    return {
      kind: 'control',
      cliType,
      title: '控制权不足',
      detail: err.message,
      guidance: '会话控制权在其他设备或桌面端；请在会话卡片上获取控制权后重试。',
      code: err.code,
    };
  }
  if (err.code === ERROR_CODE_NET_UNREACHABLE || err.code === ERROR_CODE_SERVICE_DOWN) {
    return {
      kind: 'connection',
      cliType,
      title: err.code === ERROR_CODE_NET_UNREACHABLE ? '网络不可达' : '宿主服务不可用',
      detail: err.message,
      guidance: '请确认连接后重试。',
      code: err.code,
    };
  }
  if (err.code === ERROR_CODE_RATE_LIMITED) {
    return {
      kind: 'other',
      cliType,
      title: '请求过于频繁',
      detail: err.message,
      guidance: '请稍后重试。',
      code: err.code,
    };
  }
  return {
    kind: 'other',
    cliType,
    title: '启动未完成',
    detail: err.message,
    guidance: '请稍后重试；若持续失败请回桌面端查看服务状态。',
    code: err.code,
  };
}

// --- CLI 展示元数据（图标+文字通道；颜色仅辅助） ---

export interface CliMeta {
  cliType: CLIType;
  label: string;
}

export const CLI_METAS: readonly CliMeta[] = [
  { cliType: CLI_TYPE_CLAUDE_CODE, label: 'Claude Code' },
  { cliType: CLI_TYPE_OPENCODE, label: 'OpenCode' },
  { cliType: CLI_TYPE_CODEX, label: 'Codex' },
  { cliType: CLI_TYPE_PI, label: 'Pi' },
  { cliType: CLI_TYPE_OMP, label: 'Oh My Pi' },
] as const;

export function cliLabel(cliType: CLIType): string {
  return CLI_METAS.find((m) => m.cliType === cliType)?.label ?? cliType;
}

// --- 危险操作回执 ---

export type DangerousOperation = 'stop' | 'restart' | 'remove';

export interface OperationReceipt {
  operation: DangerousOperation;
  sessionTitle: string;
  /** 如「已停止」；动词化回执。 */
  resultText: string;
}

export const useLobbyStore = defineStore('remote-lobby', () => {
  const loading = ref(false);
  /** 初次加载是否已完成（决定骨架 vs 内容）。 */
  const loaded = ref(false);
  const host = ref<HostSummary | null>(null);
  const sessions = ref<SessionSummary[]>([]);
  /** 大厅级错误（分类后）；null = 正常。 */
  const loadError = ref<ClassifiedError | null>(null);
  /** 授权失效信号：页面监听后踢回 PG-01。 */
  const authLost = ref<'revoked' | 'expired' | null>(null);

  /** 启动器：每个 CLI 独立的提交态（防连点）。 */
  const launching = ref<CLIType | null>(null);
  const launchError = ref<ClassifiedLaunchError | null>(null);

  /** 控制权冲突反馈（AC：冲突不静默覆盖）。 */
  const controlConflict = ref<{ sessionId: SessionID; message: string } | null>(null);

  /** 最近一次危险操作回执（含记录占位说明，M2-C 查询面预留）。 */
  const lastReceipt = ref<OperationReceipt | null>(null);

  // --- M3-006 T lane timing（design §6：T0=列表导航前 / T1=列表可交互渲染完成） ---
  // 每次 load()（导航/刷新/重试）创建新 recorder 并在起点打 T0——对齐
  // design §6「列表路由每次导航创建一个 recorder并完成 T lane」，避免重复
  // 导航触发 duplicate_mark fault。T1 由 SessionsPage 在 loading true→false
  // 后 nextTick（成功/空态/可操作错误态渲染完成）调用 markListTimingT1；
  // auth 失效踢回 PG-01 不打 T1（该导航无 T 样本，fail-closed）。
  // 消费面（design §6）：仅保留最近一次已完成 report 内存快照 + 显式
  // listTimingSnapshot() 供 Vitest/Playwright 读取；无 wire/HTTP/localStorage/
  // console 默认输出，report 固定 schema 不含 ID/URL/payload/时间戳。
  let listTiming: TimingRecorder | null = null;
  const lastListTimingReport = ref<TimingReportV1 | null>(null);

  /**
   * T1 锚点：列表成功/空态/可操作错误态渲染完成（SessionsPage nextTick 后调用）。
   * fail-closed：非法转换（缺 T0/duplicate）不落快照，保留上一份已完成 report。
   */
  function markListTimingT1(): void {
    const recorder = listTiming;
    if (recorder === null) return;
    listTiming = null;
    const transition = recorder.mark('T1');
    if (transition.accepted) lastListTimingReport.value = recorder.snapshot();
  }

  /** 最近一次已完成 T lane report（固定 schema 内存快照；无则 null）。 */
  function listTimingSnapshot(): TimingReportV1 | null {
    return lastListTimingReport.value;
  }

  const cliAvailability = computed(() => host.value?.cliAvailability ?? []);
  const visibleSessions = computed(() => sessions.value.filter((s) => s.state === 'running'));
  const runningCount = computed(() => visibleSessions.value.length);
  const controlledCount = computed(() => visibleSessions.value.filter((s) => s.control.state === 'you').length);

  function handleAuthFailure(err: ApiRequestError): boolean {
    if (err.code === ERROR_CODE_AUTH_REVOKED) {
      authLost.value = 'revoked';
      return true;
    }
    if (err.code === ERROR_CODE_AUTH_UNPAIRED || err.code === ERROR_CODE_AUTH_WINDOW_EXPIRED) {
      authLost.value = 'expired';
      return true;
    }
    return false;
  }

  /** 大厅加载/刷新：host/summary（含 CLI availability）+ 会话列表。 */
  async function load(): Promise<void> {
    // M3-006 T0：每次列表加载创建新 recorder 并在起点打 T0（design §6）。
    listTiming = createTimingRecorder();
    listTiming.mark('T0');
    loading.value = true;
    controlConflict.value = null;
    try {
      const nextHost = await getHostSummary();
      host.value = nextHost;
      try {
        const list = await listSessions();
        sessions.value = list.filter((item) => item.state === 'running');
        loadError.value = null;
      } catch (rawErr) {
        const err = toApiRequestError(rawErr);
        if (handleAuthFailure(err)) return;
        // 列表失败不清空已有投影：保留上次列表 + 分类错误呈现（不伪装空态）。
        loadError.value = classifyLobbyError(err);
      }
    } catch (rawErr) {
      const err = toApiRequestError(rawErr);
      if (handleAuthFailure(err)) return;
      host.value = null;
      loadError.value = classifyLobbyError(err);
    } finally {
      loading.value = false;
      loaded.value = true;
    }
  }

  /** 仅刷新会话列表（危险操作/控制权变更后）。 */
  async function refreshSessions(): Promise<void> {
    try {
      const list = await listSessions();
      sessions.value = list.filter((item) => item.state === 'running');
      if (loadError.value !== null) loadError.value = null;
    } catch (rawErr) {
      const err = toApiRequestError(rawErr);
      if (handleAuthFailure(err)) return;
      loadError.value = classifyLobbyError(err);
    }
  }

  /** 启动新会话；可覆盖宿主默认的安全会话设置。 */
  async function launch(req: CreateSessionRequest): Promise<boolean> {
    const cliType = req.cliType;
    if (launching.value !== null) return false; // 防连点：一次一个启动
    launching.value = cliType;
    launchError.value = null;
    try {
      await createSession(req);
      await refreshSessions();
      return true;
    } catch (rawErr) {
      const err = toApiRequestError(rawErr);
      if (handleAuthFailure(err)) return false;
      launchError.value = classifyLaunchError(err, cliType);
      return false;
    } finally {
      launching.value = null;
    }
  }

  /**
   * 危险操作统一入口（PG-06 确认之后调用；confirm 载荷在 api 层固定）。
   * 成功写回执并刷新列表；失败分类呈现（控制权类错误进冲突反馈，不笼统失败）。
   */
  async function runDangerousOperation(operation: DangerousOperation, session: SessionSummary): Promise<boolean> {
    controlConflict.value = null;
    try {
      if (operation === 'stop') await stopSession(session.id);
      else if (operation === 'restart') await restartSession(session.id);
      else await removeSession(session.id);
      lastReceipt.value = {
        operation,
        sessionTitle: session.title,
        resultText: operation === 'stop' ? '已停止' : operation === 'restart' ? '已重启' : '已移除',
      };
      await refreshSessions();
      return true;
    } catch (rawErr) {
      const err = toApiRequestError(rawErr);
      if (handleAuthFailure(err)) return false;
      if (err.code === ERROR_CODE_CONTROL_BUSY || err.code === ERROR_CODE_CONTROL_FORBIDDEN) {
        controlConflict.value = {
          sessionId: session.id,
          message:
            err.code === ERROR_CODE_CONTROL_BUSY
              ? '控制权正被其他设备或桌面端持有，操作未执行。'
              : '你没有该会话的控制权，操作未执行。',
        };
      } else {
        loadError.value = classifyLobbyError(err);
      }
      await refreshSessions(); // 冲突后如实刷新投影（控制者可能已变化）
      return false;
    }
  }

  /** 获取控制权；409 control.busy → 冲突反馈（不静默覆盖）。 */
  async function acquire(session: SessionSummary): Promise<boolean> {
    controlConflict.value = null;
    try {
      await acquireControl(session.id);
      await refreshSessions();
      return true;
    } catch (rawErr) {
      const err = toApiRequestError(rawErr);
      if (handleAuthFailure(err)) return false;
      if (err.code === ERROR_CODE_CONTROL_BUSY) {
        controlConflict.value = {
          sessionId: session.id,
          message: '控制权刚被其他设备或桌面端取得；已为你刷新最新状态。',
        };
      } else if (err.code === ERROR_CODE_CONTROL_FORBIDDEN) {
        controlConflict.value = {
          sessionId: session.id,
          message: '当前无法获取该会话的控制权；已为你刷新最新状态。',
        };
      } else {
        loadError.value = classifyLobbyError(err);
      }
      await refreshSessions();
      return false;
    }
  }

  /** 释放控制权（you→none）。 */
  async function release(session: SessionSummary): Promise<boolean> {
    controlConflict.value = null;
    try {
      await releaseControl(session.id);
      await refreshSessions();
      return true;
    } catch (rawErr) {
      const err = toApiRequestError(rawErr);
      if (handleAuthFailure(err)) return false;
      if (err.code === ERROR_CODE_CONTROL_FORBIDDEN) {
        controlConflict.value = {
          sessionId: session.id,
          message: '你已不再是该会话的控制者；已为你刷新最新状态。',
        };
        await refreshSessions();
      } else {
        loadError.value = classifyLobbyError(err);
      }
      return false;
    }
  }

  function dismissReceipt(): void {
    lastReceipt.value = null;
  }

  function dismissLaunchError(): void {
    launchError.value = null;
  }

  function dismissConflict(): void {
    controlConflict.value = null;
  }

  return {
    loading,
    loaded,
    host,
    sessions,
    loadError,
    authLost,
    launching,
    launchError,
    controlConflict,
    lastReceipt,
    cliAvailability,
    runningCount,
    controlledCount,
    load,
    refreshSessions,
    launch,
    runDangerousOperation,
    acquire,
    release,
    dismissReceipt,
    dismissLaunchError,
    dismissConflict,
    markListTimingT1,
    listTimingSnapshot,
  };
});

/** ControlSnapshot 四变体的展示投影（观察者语义）。 */
export function controlProjection(control: ControlSnapshot): {
  text: string;
  writable: boolean;
  reason: string | null;
} {
  switch (control.state) {
    case 'you':
      return { text: '你正在控制', writable: true, reason: null };
    case 'desktop':
      return { text: '桌面端控制中', writable: false, reason: '桌面端正在控制，你可观察但无法操作' };
    case 'other':
      return {
        text: `由 ${control.deviceName} 控制`,
        writable: false,
        reason: `控制权在 ${control.deviceName}，你可观察但无法操作`,
      };
    case 'none':
      return { text: '无人控制', writable: false, reason: '需要先获取控制权才能操作' };
  }
}
