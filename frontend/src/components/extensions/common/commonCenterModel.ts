/**
 * 公共扩展中心（扩展管理「共用」tab）纯决策逻辑。
 *
 * 组件模板只做展示，行模型/勾选计算/摘要截断全部集中在此，
 * 以便 node 环境单测直接覆盖（仓库 vitest 无 DOM，见 vitest.config.ts）。
 * 消费方：CommonSkillsPanel / CommonMCPPanel / MCPImportDialog 与对应 composable。
 */

import { SHARED_EXT_ENGINES } from '../../../api/sharedExt';
import type { AggregatedSkill, SharedExtEngine, SharedMCPServer } from '../../../api/sharedExt';

/** 插件来源条目删除按钮的禁用提示（删除统一在插件管理中处理，P4 联动）。 */
export const PLUGIN_DELETE_TITLE = '来自插件，请在插件管理中卸载';

/** 插件市场来源条目删除按钮的禁用提示（P6 交汇点：卸载归共用市场子页管理）。 */
export const MARKET_DELETE_TITLE = '来自插件市场，请在「共用市场」子页管理';

/** 引擎展示名：targets 勾选 chips 与插件来源引擎标识共用。 */
export const ENGINE_LABELS: Record<SharedExtEngine, string> = {
  claude: 'Claude',
  codex: 'Codex',
  opencode: 'OpenCode',
  pi: 'Pi',
  omp: 'OMP',
};

/** 未知引擎原样返回；缺省返回空串（模板按需隐藏）。 */
export function engineLabelOf(engine?: string): string {
  if (engine && (SHARED_EXT_ENGINES as string[]).includes(engine)) {
    return ENGINE_LABELS[engine as SharedExtEngine];
  }
  return engine || '';
}

export interface SkillRowModel {
  /** 列表 key：plugin 条目带 pluginId 前缀避免与同名 manual 行撞 key。 */
  key: string;
  name: string;
  description: string;
  /** manual=true（仓库管理：启停/删除/目标可改）；plugin=false（只读聚合）。 */
  managed: boolean;
  enabled: boolean;
  canToggle: boolean;
  canDelete: boolean;
  /** 删除禁用时的 title 提示（仅 plugin 条目有值）。 */
  deleteTitle: string;
  canEditTargets: boolean;
  /** manual 条目来自 registry 的目标引擎（勾选 chips 的选中态）。 */
  targets: SharedExtEngine[];
  /** 来源徽章文案：「手动导入」或「插件·{PluginName}」。 */
  sourceText: string;
  /** 来源徽章颜色（Badge type=type 的 color）：skill=绿 / plugin=紫。 */
  sourceColor: string;
  pluginEngine?: string;
  pluginId?: string;
  pluginName?: string;
  /** 插件引擎标识徽章文案（manual 为空串，模板隐藏）。 */
  engineLabel: string;
}

/**
 * 聚合 skill 条目 → 列表行模型。
 * @param entry 聚合视图条目（manual+plugin 统一来源）
 * @param registryTargets manual 条目在 skill 仓库 registry 中的 targets
 *        （AggregatedSkill 不携带 targets，由面板按 name 从 skills 清单回填）
 */
export function skillRowModel(entry: AggregatedSkill, registryTargets?: string[]): SkillRowModel {
  const managed = entry.managed;
  const pluginEngine = managed ? undefined : entry.pluginEngine;
  const pluginId = managed ? undefined : entry.pluginId;
  const pluginName = managed ? undefined : entry.pluginName;
  // 插件市场来源（P6 交汇点）：徽章「插件市场·插件名」、禁删文案指向共用
  // 市场子页；无引擎概念（engineLabel 留空），viewPlugin 白名单不含 market
  // 天然不跳转（卸载归共用市场）。
  const isMarket = pluginEngine === 'market';
  return {
    key: managed ? `manual:${entry.name}` : `plugin:${pluginId}:${entry.name}`,
    name: entry.name,
    description: entry.description,
    managed,
    enabled: entry.enabled,
    canToggle: managed,
    canDelete: managed,
    deleteTitle: managed ? '' : isMarket ? MARKET_DELETE_TITLE : PLUGIN_DELETE_TITLE,
    canEditTargets: managed,
    // 勾选态统一按 SHARED_EXT_ENGINES 固定顺序呈现（registry 顺序不保证）
    targets: SHARED_EXT_ENGINES.filter((e) => (registryTargets ?? []).includes(e)),
    sourceText: managed ? '手动导入' : isMarket ? `插件市场·${pluginName || pluginId || entry.name}` : `插件·${pluginName || pluginId || entry.name}`,
    sourceColor: managed ? 'skill' : 'plugin',
    pluginEngine,
    pluginId,
    pluginName,
    engineLabel: isMarket ? '' : engineLabelOf(pluginEngine),
  };
}

/**
 * targets 勾选切换：返回新数组（勾选加入/取消移除，保持 SHARED_EXT_ENGINES 固定顺序）。
 * 不修改入参；空入参容忍（missing registry 行）。
 */
export function nextTargets(current: readonly string[], engine: SharedExtEngine): SharedExtEngine[] {
  const set = new Set<string>(current);
  if (set.has(engine)) {
    set.delete(engine);
  } else {
    set.add(engine);
  }
  return SHARED_EXT_ENGINES.filter((e) => set.has(e));
}

/**
 * MCP server config 摘要：command（stdio 型）或 url（HTTP 型）首行。
 * 复合值（args 数组等）不展开——列表行只放一行定位信息。
 */
export function mcpConfigSummary(config: Record<string, any> | null | undefined): string {
  if (!config || typeof config !== 'object') return '';
  const command = typeof config.command === 'string' ? config.command.trim() : '';
  if (command) return command;
  const url = typeof config.url === 'string' ? config.url.trim() : '';
  if (url) return url;
  return '';
}

/** MCP 列表行模型（清单条目全部 manual 管理，无只读分支）。 */
export interface McpRowModel {
  key: string;
  name: string;
  summary: string;
  enabled: boolean;
  targets: SharedExtEngine[];
}

export function mcpRowModel(server: SharedMCPServer): McpRowModel {
  return {
    key: `mcp:${server.name}`,
    name: server.name,
    summary: mcpConfigSummary(server.config),
    enabled: server.enabled,
    targets: SHARED_EXT_ENGINES.filter((e) => (server.targets ?? []).includes(e)),
  };
}
