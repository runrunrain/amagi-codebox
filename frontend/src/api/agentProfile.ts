// agentProfile - Agent 配置档 API 封装。
// 直接包装 wailsjs 绑定的 agentprofile.Service 方法（数据量小，无需 Pinia store）。
// 后端存储：~/.amagi-codebox/agent-profiles.json。
import {
  ListAgentProfiles,
  GetAgentProfile,
  CaptureAgentProfile,
  SaveAgentProfile,
  ApplyAgentProfile,
  DeleteAgentProfile,
} from '../../wailsjs/go/agentprofile/Service';

/** 单个配置档：pi 为 ~/.pi/agent/amagi.json 全文（JSON），
 *  omp 为 ~/.omp/agent/config.yml 全文（YAML，空串=不管理该侧）。 */
export interface AgentProfile {
  pi: string;
  omp: string;
  updatedAt: number;
}

/** 配置档存储根结构。 */
export interface AgentProfileStore {
  version: number;
  profiles: Record<string, AgentProfile>;
  lastApplied: string;
}

/** 列出全部配置档（文件缺失时返回空骨架）。 */
export async function listAgentProfiles(): Promise<AgentProfileStore> {
  const raw = await ListAgentProfiles();
  const parsed = JSON.parse(raw) as Partial<AgentProfileStore>;
  return {
    version: parsed.version ?? 1,
    profiles: parsed.profiles ?? {},
    lastApplied: parsed.lastApplied ?? '',
  };
}

/** 读取单个配置档（不存在时后端报错）。 */
export async function getAgentProfile(name: string): Promise<AgentProfile> {
  const raw = await GetAgentProfile(name);
  return JSON.parse(raw) as AgentProfile;
}

/** 把当前 live 配置快照为命名配置档（存在同名则覆盖）。 */
export function captureAgentProfile(name: string): Promise<void> {
  return CaptureAgentProfile(name);
}

/** 显式内容保存配置档。 */
export function saveAgentProfile(name: string, piContent: string, ompContent: string): Promise<void> {
  return SaveAgentProfile(name, piContent, ompContent);
}

/** 应用配置档到 live 文件（后端自动做 .bak 备份）。 */
export function applyAgentProfile(name: string): Promise<void> {
  return ApplyAgentProfile(name);
}

/** 删除配置档。 */
export function deleteAgentProfile(name: string): Promise<void> {
  return DeleteAgentProfile(name);
}
