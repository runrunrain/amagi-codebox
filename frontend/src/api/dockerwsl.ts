/**
 * Docker Desktop ↔ WSL Integration Health API
 *
 * 健康检查与一键自愈（2026-09-25 运维复盘 §1.2：Docker Desktop 与用户发行版
 * 同时冷启动时 /mnt/wsl/docker-desktop 共享挂载竞态挂坏的实证修复序列固化）。
 * Wraps the App-bound methods GetDockerWSLHealth / SelfHealDockerWSLIntegration.
 */

import { GetDockerWSLHealth, SelfHealDockerWSLIntegration } from '../../wailsjs/go/main/App';
import { dockerwsl } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

export type DockerWSLHealth = dockerwsl.HealthReport;
export type DockerWSLSelfHealReport = dockerwsl.SelfHealReport;

/** 拉取集成健康快照（挂载字节数/状态、引擎、工具发行版、问题清单、自愈建议）。 */
export function getDockerWSLHealth(): Promise<DockerWSLHealth> {
  return callApi('[api.dockerwsl.getDockerWSLHealth]', () => GetDockerWSLHealth());
}

/**
 * 执行一键自愈（全退 Desktop → 仅 terminate docker-desktop 工具发行版 → 全新启动
 * → 等引擎就绪（冷启动最长 3-4 分钟）→ 复核挂载）。同步返回步骤回执。
 */
export function selfHealDockerWSLIntegration(): Promise<DockerWSLSelfHealReport> {
  return callApi('[api.dockerwsl.selfHealDockerWSLIntegration]', () => SelfHealDockerWSLIntegration());
}
