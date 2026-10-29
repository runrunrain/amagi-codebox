import { parseModelSpec } from './useModelCatalog';
export type AdvisorMode = 'unset' | 'off' | 'pi' | 'codex-chat';
export function advisorMode(value: any): AdvisorMode {
  if (value === undefined) return 'unset';
  if (value?.backend === 'codex-chat') return 'codex-chat';
  return value?.model === null ? 'off' : 'pi';
}
export function validateAdvisorConfig(config: Record<string, any>): string {
  if (config.advisor === undefined) return '';
  const c = config.advisor;
  if (!c || typeof c !== 'object' || Array.isArray(c)) return 'advisor 必须是对象';
  if (Object.keys(c).some(k => !['backend', 'model', 'codex', 'leader', 'roles', 'maxCalls', 'maxTokens', 'timeoutMs'].includes(k))) return 'advisor 含未知字段，请在 JSON 模式修复';
  if (c.backend !== undefined && !['pi', 'codex-chat'].includes(c.backend)) return 'advisor.backend 只允许 pi/codex-chat';
  if (c.backend === 'codex-chat') {
    const t = c.codex === undefined ? { mode: 'managed' } : c.codex;
    if (!t || typeof t !== 'object' || Array.isArray(t) || Object.keys(t).some(k => !['mode', 'socketPath', 'threadId', 'model', 'reasoningEffort'].includes(k))) return 'Codex 配置字段无效';
    const mode = t.mode ?? (t.threadId ? 'existing' : 'managed');
    if (mode === 'managed') {
      if (t.threadId !== undefined || t.socketPath !== undefined) return '自有咨询会话不接受其他聊天 ID 或共享连接';
      if (t.model !== undefined && (typeof t.model !== 'string' || !/^\S+$/.test(t.model))) return 'Codex 模型 ID 无效';
      if (t.reasoningEffort !== undefined && !['low','medium','high','xhigh','max'].includes(t.reasoningEffort)) return 'Codex 思考档位无效';
    } else if (mode === 'existing') {
      if (typeof t.socketPath !== 'string' || (t.socketPath !== 'auto' && !/^(?:\/|[A-Za-z]:[\\/]|\\\\)/.test(t.socketPath))) return '请填写 Codex 桌面共享 socket 的本机绝对路径或 auto';
      if (typeof t.threadId !== 'string' || !/^[a-zA-Z0-9-]{8,128}$/.test(t.threadId)) return '请选择或填写 Codex 目标聊天 ID';
      if (t.model !== undefined || t.reasoningEffort !== undefined) return '既有聊天不接受模型或思考档位覆盖';
    } else return 'Codex 会话方式无效';

  } else {
    if (c.model !== null && (typeof c.model !== 'string' || !parseModelSpec(c.model))) return '请选择完整的 Advisor 模型 provider/model[:thinking]';
    if (c.codex !== undefined) return 'pi 后端不接受 codex 字段';
  }
  if (c.leader !== undefined && typeof c.leader !== 'boolean') return 'advisor.leader 必须是 boolean';
  if (c.roles !== undefined && (!Array.isArray(c.roles) || c.roles.some((r: unknown) => typeof r !== 'string' || !/^[a-z][a-z0-9-]*$/.test(r)))) return 'advisor.roles 必须是 canonical 角色名数组';
  for (const [k, max] of [['maxCalls', 50], ['maxTokens', 16384], ['timeoutMs', 300000]] as const) if (c[k] !== undefined && (!Number.isSafeInteger(c[k]) || c[k] < 1 || c[k] > max)) return `advisor.${k} 必须是 1–${max} 的整数`;
  return '';
}
