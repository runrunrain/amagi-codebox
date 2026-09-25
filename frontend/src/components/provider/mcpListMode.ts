/**
 * amagi.json mcp 列表字段（mcp.leader / mcp.default）的三态语义纯函数。
 *
 * amagi-pi 权威语义（loadMcpRouting / resolveLeaderMcpServers）：
 * - 键未设置（undefined）= 缺省 ["*"] → 加载全部
 * - 显式空数组 [] → 不加载任何
 * - 非空列表 → 白名单（列表含 "*" 等价全部）
 *
 * UI 三态与数据映射：all = 未设置键 / list = 白名单列表 / none = 显式空数组。
 * 读取侧非法形态（非数组非 null）按 list 降级展示，不破坏原数据；
 * 写入侧 all 删除键（语义 = 全部），mcp 对象删空则连 mcp 键一并清理，不留空壳。
 */

/** UI 三态：全部（未设置键）/ 白名单（列表）/ 不加载（显式空数组）。 */
export type McpListMode = 'all' | 'list' | 'none';

/** 读取侧：配置值 → 三态模式。undefined/null → all；[] → none；其余（含非法形态）→ list。 */
export function mcpListModeOf(value: unknown): McpListMode {
  if (value === undefined || value === null) return 'all';
  if (Array.isArray(value)) return value.length === 0 ? 'none' : 'list';
  return 'list';
}

export interface McpConfigLike {
  mcp?: {
    leader?: unknown;
    default?: unknown;
    agents?: unknown;
    [key: string]: unknown;
  } | Record<string, unknown>;
  [key: string]: unknown;
}

/**
 * 写入侧：按模式写入 leader/default 字段。
 * - all → 删除键（未设置 = 加载全部）
 * - none → 写 []（显式空数组 = 不加载任何）
 * - list → 写入列表副本（空列表保存后读取侧将呈现 none——数据事实，编辑中间态可接受）
 * mcp 对象删空（无任何键）→ 连 mcp 键删除，避免残留 "mcp": {} 空壳。
 */
export function applyMcpListMode<T extends McpConfigLike>(
  config: T,
  field: 'leader' | 'default',
  mode: McpListMode,
  list: readonly string[] = []
): void {
  if (!config.mcp || typeof config.mcp !== 'object') {
    config.mcp = {};
  }
  const mcp = config.mcp as Record<string, unknown>;
  if (mode === 'all') {
    delete mcp[field];
  } else if (mode === 'none') {
    mcp[field] = [];
  } else {
    mcp[field] = [...list];
  }
  if (Object.keys(mcp).length === 0) {
    delete config.mcp;
  }
}
