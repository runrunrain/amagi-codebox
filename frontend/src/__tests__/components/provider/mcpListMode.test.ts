// mcpListMode 纯函数：mcp.leader / mcp.default 三态（all 未设置 = 全部 / list 白名单 / none 空数组 = 不加载）。
// 语义锚定 amagi-pi loadMcpRouting / resolveLeaderMcpServers：未设置键与显式空数组行为不同，
// UI 必须可区分表达；写入侧 all 删键、mcp 删空连键清理不留空壳。
import { describe, expect, it } from 'vitest';
import { applyMcpListMode, mcpListModeOf, type McpConfigLike } from '../../../components/provider/mcpListMode';

describe('mcpListModeOf（读取侧：配置值 → 三态）', () => {
  it('未设置 / null → all（加载全部）', () => {
    expect(mcpListModeOf(undefined)).toBe('all');
    expect(mcpListModeOf(null)).toBe('all');
  });

  it('显式空数组 → none（不加载任何）', () => {
    expect(mcpListModeOf([])).toBe('none');
  });

  it('非空列表 → list（含 ["*"]——显式通配也是列表数据，不归一改写）', () => {
    expect(mcpListModeOf(['web-search-prime'])).toBe('list');
    expect(mcpListModeOf(['*'])).toBe('list');
  });

  it('非法形态 → list 降级展示（不破坏原数据）', () => {
    expect(mcpListModeOf('oops')).toBe('list');
    expect(mcpListModeOf({ 0: 'a' })).toBe('list');
  });
});

describe('applyMcpListMode（写入侧：模式 → 数据 + 空壳清理）', () => {
  it('all → 删除键（语义 = 加载全部，与显式 [] 区分）', () => {
    const cfg: McpConfigLike = { mcp: { leader: ['a', 'b'] } };
    applyMcpListMode(cfg, 'leader', 'all');
    expect(cfg.mcp).toBeUndefined();
    expect(JSON.stringify(cfg)).toBe('{}');
  });

  it('none → 写显式空数组 []（不加载任何）', () => {
    const cfg: McpConfigLike = {};
    applyMcpListMode(cfg, 'leader', 'none');
    expect(cfg.mcp?.leader).toEqual([]);
  });

  it('list → 写入列表副本（后续改动不影响已写入数据）', () => {
    const cfg: McpConfigLike = {};
    const src = ['web-search-prime'];
    applyMcpListMode(cfg, 'default', 'list', src);
    src.push('tavily-mcp');
    expect(cfg.mcp?.default).toEqual(['web-search-prime']);
  });

  it('all 删键后 mcp 仍有其他字段（如 agents / default）→ 保留 mcp 对象', () => {
    const cfg: McpConfigLike = { mcp: { leader: ['a'], default: ['b'], agents: { baize: ['c'] } } };
    applyMcpListMode(cfg, 'leader', 'all');
    const mcp = cfg.mcp as Record<string, unknown>;
    expect(mcp).toBeDefined();
    expect(mcp.default).toEqual(['b']);
    expect((mcp.agents as Record<string, string[]>).baize).toEqual(['c']);
    expect('leader' in mcp).toBe(false);
  });

  it('none → all → mcp 删空：两步往返后配置回到未设置态（无空壳）', () => {
    const cfg: McpConfigLike = {};
    applyMcpListMode(cfg, 'leader', 'none');
    expect(cfg.mcp).toBeDefined();
    applyMcpListMode(cfg, 'leader', 'all');
    expect(cfg.mcp).toBeUndefined();
  });
});
