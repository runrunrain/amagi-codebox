/**
 * AgentCliSettings.vue 的可测试交互逻辑（安装状态映射 + 控制器状态机）。
 *
 * 结构对齐 envcheckOperationTracking.ts：视图层只做渲染与事件接线，行为
 * 全部收敛到这里。无 DOM 依赖；Wails 调用经 api/agentcli（wailsjs 绑定
 * 边界可打桩），轮换令牌的二次确认经 confirm 回调注入——测试可在
 * wailsjs 边界驱动开关/安装/清理/轮换全部分支，不必挂载组件。
 *
 * 秘密边界：任何状态/文案都不含令牌；轮换只反馈结果，不回显令牌。
 */
import { reactive, ref } from 'vue';
import {
  getAgentCLIStatus,
  setAgentCLIEnabled,
  rotateAgentCLIToken,
  getAgentCLIInstallStatus,
  installAgentCLIShim,
  removeAgentCLIShim,
  installAgentCLISkill,
  removeAgentCLISkill,
  type AgentCLISkillTarget,
  type AgentCLIInstallState,
} from '../../api/agentcli';
import { main } from '../../../wailsjs/go/models';

type AgentCLIStatus = main.AgentCLIStatus;
type AgentCLIInstallStatus = main.AgentCLIInstallStatus;
export type { AgentCLIStatus, AgentCLIInstallStatus };

export type { AgentCLISkillTarget, AgentCLIInstallState } from '../../api/agentcli';

/** 技能安装目标展示元数据（顺序即视图渲染顺序，对齐后端 install.SkillTargets）。 */
export const SKILL_TARGETS: ReadonlyArray<{ target: AgentCLISkillTarget; label: string }> = [
  { target: 'agents', label: '通用 agents（~/.agents/skills）' },
  { target: 'claude', label: 'Claude Code（~/.claude/skills）' },
  { target: 'codex', label: 'Codex（~/.codex/skills）' },
];

/** 轮换令牌二次确认文案（旧令牌立即失效是不可逆动作，必须先行确认）。 */
export const ROTATE_CONFIRM_MESSAGE =
  '轮换后旧令牌立即失效，正在使用它的 CLI 会话需重新执行才能读到新令牌。确定轮换吗？';

/** modified-by-user 的明确提示（任务要求：应用不会覆盖用户手动改过的文件）。 */
export const MODIFIED_BY_USER_HINT = '已被手动修改，应用不会覆盖';

/** 单个安装状态（shim 或技能）的展示映射。 */
export interface InstallStateMeta {
  /** 徽标文案。 */
  text: string;
  /** 徽标 class（badge-ok / badge-warn / badge-missing，对齐 WSLCLISettings）。 */
  badgeClass: string;
  /** 额外行内提示；空串表示无。 */
  hint: string;
  /** 安装/更新按钮文案。 */
  installLabel: string;
}

/**
 * 安装状态 → 展示映射。unknown 状态按 invalid 处理（后端新增枚举时前端
 * 不静默伪装成正常态）。
 */
export function installStateMeta(state: string): InstallStateMeta {
  switch (state as AgentCLIInstallState) {
    case 'up-to-date':
      return { text: '已是最新', badgeClass: 'badge-ok', hint: '', installLabel: '重装' };
    case 'outdated':
      return { text: '有新版本', badgeClass: 'badge-warn', hint: '内容与当前应用版本不一致，建议更新', installLabel: '更新' };
    case 'modified-by-user':
      return { text: '已被手动修改', badgeClass: 'badge-warn', hint: MODIFIED_BY_USER_HINT, installLabel: '重新安装' };
    case 'invalid':
      return { text: '状态异常', badgeClass: 'badge-missing', hint: '目标路径不可用，无法读写该文件', installLabel: '安装' };
    case 'not-installed':
      return { text: '未安装', badgeClass: 'badge-missing', hint: '', installLabel: '安装' };
    default:
      // 后端新增枚举时按异常态展示，不静默伪装成正常态。
      return { text: '状态异常', badgeClass: 'badge-missing', hint: '未知安装状态：' + state, installLabel: '安装' };
  }
}

/** 技能目标短名（按钮/提示用）。 */
export function skillTargetName(target: AgentCLISkillTarget): string {
  const found = SKILL_TARGETS.find((t) => t.target === target);
  return found ? found.label.split('（')[0] : target;
}

/**
 * 可复制的使用示例：`<shim 路径> status --json`。路径含空白时加引号，
 * 保证整行复制到任意 shell 都可直接执行；shim 尚未安装时回退命令名。
 */
export function usageExample(shimPath: string | undefined, command: string | undefined): string {
  const path = (shimPath || command || 'codebox-agent').trim();
  const quoted = /\s/.test(path) ? `"${path}"` : path;
  return `${quoted} status --json`;
}

/** RFC3339 启动时间 → 本地可读文案；空/非法返回空串（视图按空隐藏）。 */
export function formatStartedAt(iso: string | undefined): string {
  if (!iso) return '';
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${t.getFullYear()}-${pad(t.getMonth() + 1)}-${pad(t.getDate())} ${pad(t.getHours())}:${pad(t.getMinutes())}:${pad(t.getSeconds())}`;
}

/**
 * 最近一次启动/发布失败原因的展示值：空白视为无异常（视图按空隐藏告警）。
 * 非空时以告警样式展示（如「端点被其它实例占用」），不静默吞掉。
 */
export function lastErrorText(lastError: string | undefined): string {
  return (lastError ?? '').trim();
}

/** 错误对象 → 用户可读消息（沿用视图层 err?.message || err 惯例）。 */
export function errMessage(err: unknown): string {
  const e = err as { message?: string } | null | undefined;
  return String(e?.message ?? err ?? '未知错误');
}

/** 控制器依赖注入面（测试与视图各接各的，控制器不直接碰 DOM/toast）。 */
export interface AgentCliControllerDeps {
  /** 轮换令牌二次确认门；返回 false 则放弃操作。 */
  confirm: (message: string) => boolean | Promise<boolean>;
  /** 结果提示（视图接 toast）；缺省静默，测试可不传。 */
  notify?: (kind: 'success' | 'error', message: string) => void;
}

/** 单个安装动作的忙碌标记：'install' | 'remove' | null。 */
export type InstallAction = 'install' | 'remove' | null;

/**
 * agent-cli 设置页控制器：状态快照 + 全部操作。
 *
 * 行为约定：
 *  - 开关失败后回读真实状态（开关不停留在乐观值，对齐 GeneralSettings 系统代理）；
 *  - 轮换令牌必须先过 confirm 门；取消不产生任何后端调用；
 *  - 安装/清理按后端返回值整体刷新安装面状态（返回值即权威）；
 *  - 操作失败写入 actionError 并保持忙碌标记复位，不中断页面。
 */
export function createAgentCliSettingsController(deps: AgentCliControllerDeps) {
  const status = ref<AgentCLIStatus | null>(null);
  const install = ref<AgentCLIInstallStatus | null>(null);
  const statusLoading = ref(false);
  const toggling = ref(false);
  const rotating = ref(false);
  const actionError = ref('');
  const shimBusy = ref<InstallAction>(null);
  const skillBusy = reactive<Record<AgentCLISkillTarget, InstallAction>>({
    agents: null,
    claude: null,
    codex: null,
  });

  function notify(kind: 'success' | 'error', message: string) {
    deps.notify?.(kind, message);
  }

  /** 拉取运行状态（开关/轮换后调用；失败不覆盖已有快照，只记错误）。 */
  async function refreshStatus(): Promise<void> {
    statusLoading.value = true;
    try {
      status.value = await getAgentCLIStatus();
    } catch (err) {
      actionError.value = `读取命令行访问状态失败: ${errMessage(err)}`;
    } finally {
      statusLoading.value = false;
    }
  }

  /** 拉取本机分发状态（安装/清理后调用）。 */
  async function refreshInstall(): Promise<void> {
    try {
      install.value = await getAgentCLIInstallStatus();
    } catch (err) {
      actionError.value = `读取本机分发状态失败: ${errMessage(err)}`;
    }
  }

  async function refresh(): Promise<void> {
    await Promise.all([refreshStatus(), refreshInstall()]);
  }

  /** 开关命令行访问；失败回读真实状态。 */
  async function toggleEnabled(enabled: boolean): Promise<void> {
    if (toggling.value) return;
    toggling.value = true;
    try {
      await setAgentCLIEnabled(enabled);
      notify('success', enabled ? '命令行访问已开启' : '命令行访问已关闭');
      await refreshStatus();
    } catch (err) {
      actionError.value = `切换命令行访问失败: ${errMessage(err)}`;
      notify('error', actionError.value);
      await refreshStatus();
    } finally {
      toggling.value = false;
    }
  }

  /** 轮换令牌：二次确认通过后才调用后端；取消是零副作用。 */
  async function rotateToken(): Promise<void> {
    if (rotating.value) return;
    let ok: boolean;
    try {
      ok = await deps.confirm(ROTATE_CONFIRM_MESSAGE);
    } catch {
      ok = false;
    }
    if (!ok) return;
    rotating.value = true;
    try {
      await rotateAgentCLIToken();
      notify('success', 'CLI 令牌已轮换，旧令牌立即失效');
      await refreshStatus();
    } catch (err) {
      actionError.value = `轮换令牌失败: ${errMessage(err)}`;
      notify('error', actionError.value);
    } finally {
      rotating.value = false;
    }
  }

  /** 应用安装/清理动作的统一收尾：成功覆盖快照 + 提示，失败记错误。 */
  async function applyInstallAction(
    busy: { value: InstallAction },
    action: Exclude<InstallAction, null>,
    fn: () => Promise<AgentCLIInstallStatus>,
    successMessage: string,
    failPrefix: string,
  ): Promise<void> {
    if (busy.value !== null) return;
    busy.value = action;
    try {
      install.value = await fn();
      notify('success', successMessage);
    } catch (err) {
      actionError.value = `${failPrefix}: ${errMessage(err)}`;
      notify('error', actionError.value);
      await refreshInstall();
    } finally {
      busy.value = null;
    }
  }

  function installShim(): Promise<void> {
    return applyInstallAction(
      shimBusy,
      'install',
      () => installAgentCLIShim(),
      'shim 已安装',
      '安装 shim 失败',
    );
  }

  function removeShim(): Promise<void> {
    return applyInstallAction(
      shimBusy,
      'remove',
      () => removeAgentCLIShim(),
      'shim 已清理（启动不再自动装回）',
      '清理 shim 失败',
    );
  }

  /** reactive 记录项的 busy 适配（getter/setter 单元，语义同 ref.value）。 */
  function skillBusyCell(target: AgentCLISkillTarget): { value: InstallAction } {
    return {
      get value() {
        return skillBusy[target];
      },
      set value(v: InstallAction) {
        skillBusy[target] = v;
      },
    };
  }

  function installSkill(target: AgentCLISkillTarget): Promise<void> {
    return applyInstallAction(
      skillBusyCell(target),
      'install',
      () => installAgentCLISkill(target),
      `${skillTargetName(target)}技能已安装`,
      `安装技能失败（${skillTargetName(target)}）`,
    );
  }

  function removeSkill(target: AgentCLISkillTarget): Promise<void> {
    return applyInstallAction(
      skillBusyCell(target),
      'remove',
      () => removeAgentCLISkill(target),
      `${skillTargetName(target)}技能已清理`,
      `清理技能失败（${skillTargetName(target)}）`,
    );
  }

  return {
    // 状态
    status,
    install,
    statusLoading,
    toggling,
    rotating,
    actionError,
    shimBusy,
    skillBusy,
    // 动作
    refresh,
    refreshStatus,
    refreshInstall,
    toggleEnabled,
    rotateToken,
    installShim,
    removeShim,
    installSkill,
    removeSkill,
  };
}

export type AgentCliSettingsController = ReturnType<typeof createAgentCliSettingsController>;
