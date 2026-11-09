/**
 * Agent CLI API
 * 命令行访问（agent-cli / codebox-agent）设置面的封装。
 * 直接包装 wailsjs/go/main/App 中 agent-cli 门面方法（app_agentcli.go 与
 * app_agentcli_install.go）。
 *
 * 秘密边界（DESIGN.md §3）：CLI 令牌只存在于 0600 端点文件与请求头中，
 * 门面返回值不含令牌；前端同样不展示、不记录、不复制任何令牌内容。
 */

import {
  GetAgentCLIStatus,
  SetAgentCLIEnabled,
  RotateAgentCLIToken,
  GetAgentCLIInstallStatus,
  InstallAgentCLIShim,
  RemoveAgentCLIShim,
  InstallAgentCLISkill,
  RemoveAgentCLISkill,
} from '../../wailsjs/go/main/App';

import { main } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// Type aliases
type AgentCLIStatus = main.AgentCLIStatus;
type AgentCLIInstallStatus = main.AgentCLIInstallStatus;

/** 技能安装目标（后端 install.SkillTarget：agents|claude|codex）。 */
export type AgentCLISkillTarget = 'agents' | 'claude' | 'codex';

/** 安装状态枚举（后端 install.State 字符串）。 */
export type AgentCLIInstallState =
  | 'not-installed'
  | 'up-to-date'
  | 'outdated'
  | 'modified-by-user'
  | 'invalid';

/**
 * 获取命令行访问运行状态（enabled/running/url/pid/startedAt，不含令牌）
 */
export function getAgentCLIStatus(): Promise<AgentCLIStatus> {
  return callApi('[api.agentcli.getAgentCLIStatus]', () => GetAgentCLIStatus());
}

/**
 * 开关命令行访问（关闭 = 令牌作废 + 撤销端点文件；开启 = 启动/恢复 + 发布端点）
 */
export function setAgentCLIEnabled(enabled: boolean): Promise<void> {
  return callApi('[api.agentcli.setAgentCLIEnabled]', () => SetAgentCLIEnabled(enabled));
}

/**
 * 轮换 CLI 令牌（旧令牌立即失效并重写端点文件；仅服务运行且启用时可用）
 */
export function rotateAgentCLIToken(): Promise<void> {
  return callApi('[api.agentcli.rotateAgentCLIToken]', () => RotateAgentCLIToken());
}

/**
 * 获取本机分发状态（shim 路径/状态、三个技能目标状态、命令数，不含令牌）
 */
export function getAgentCLIInstallStatus(): Promise<AgentCLIInstallStatus> {
  return callApi('[api.agentcli.getAgentCLIInstallStatus]', () => GetAgentCLIInstallStatus());
}

/**
 * 安装 shim（幂等；外部改动的文件保留并报冲突），返回更新后的状态
 */
export function installAgentCLIShim(): Promise<AgentCLIInstallStatus> {
  return callApi('[api.agentcli.installAgentCLIShim]', () => InstallAgentCLIShim());
}

/**
 * 清理 shim（记录用户清理偏好，启动不再自动装回），返回更新后的状态
 */
export function removeAgentCLIShim(): Promise<AgentCLIInstallStatus> {
  return callApi('[api.agentcli.removeAgentCLIShim]', () => RemoveAgentCLIShim());
}

/**
 * 安装指定目标的 agent 技能（agents|claude|codex），返回更新后的状态
 */
export function installAgentCLISkill(target: AgentCLISkillTarget): Promise<AgentCLIInstallStatus> {
  return callApi('[api.agentcli.installAgentCLISkill]', () => InstallAgentCLISkill(target));
}

/**
 * 清理指定目标的 agent 技能（记录用户清理偏好），返回更新后的状态
 */
export function removeAgentCLISkill(target: AgentCLISkillTarget): Promise<AgentCLIInstallStatus> {
  return callApi('[api.agentcli.removeAgentCLISkill]', () => RemoveAgentCLISkill(target));
}
