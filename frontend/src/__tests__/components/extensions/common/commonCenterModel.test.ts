import { describe, expect, it } from 'vitest';
import {
  ENGINE_LABELS,
  PLUGIN_DELETE_TITLE,
  engineLabelOf,
  mcpConfigSummary,
  mcpRowModel,
  nextTargets,
  skillRowModel,
} from '../../../../components/extensions/common/commonCenterModel';
import type { AggregatedSkill } from '../../../../api/sharedExt';

// 夹具与 wailsjs createFrom 同构（字段名/形态一致即可，纯函数不依赖类方法）
function manualAgg(name: string, overrides: Record<string, unknown> = {}): AggregatedSkill {
  return {
    name,
    description: `${name} 的描述`,
    origin: 'manual',
    managed: true,
    enabled: true,
    ...overrides,
  } as AggregatedSkill;
}

function pluginAgg(name: string, overrides: Record<string, unknown> = {}): AggregatedSkill {
  return {
    name,
    description: `${name} 来自插件`,
    origin: 'plugin',
    pluginEngine: 'claude',
    pluginId: 'demo@marketplace',
    pluginName: 'Demo Plugin',
    managed: false,
    enabled: true,
    ...overrides,
  } as AggregatedSkill;
}

describe('skillRowModel：聚合条目 → 列表行决策', () => {
  it('manual 条目全权管理：可启停/删除/勾选目标，徽章「手动导入」', () => {
    const row = skillRowModel(manualAgg('my-skill'), ['claude', 'pi']);
    expect(row.managed).toBe(true);
    expect(row.canToggle).toBe(true);
    expect(row.canDelete).toBe(true);
    expect(row.canEditTargets).toBe(true);
    expect(row.deleteTitle).toBe('');
    expect(row.sourceText).toBe('手动导入');
    expect(row.sourceColor).toBe('skill');
    expect(row.targets).toEqual(['claude', 'pi']);
    expect(row.engineLabel).toBe('');
    expect(row.key).toBe('manual:my-skill');
  });

  it('plugin 条目只读：删除禁用 + title 提示，无启停/目标勾选，来源徽章带插件名与引擎标识', () => {
    const row = skillRowModel(pluginAgg('p-skill'));
    expect(row.managed).toBe(false);
    expect(row.canToggle).toBe(false);
    expect(row.canDelete).toBe(false);
    expect(row.deleteTitle).toBe(PLUGIN_DELETE_TITLE);
    expect(PLUGIN_DELETE_TITLE).toContain('来自插件');
    expect(row.canEditTargets).toBe(false);
    expect(row.targets).toEqual([]);
    expect(row.sourceText).toBe('插件·Demo Plugin');
    expect(row.sourceColor).toBe('plugin');
    expect(row.engineLabel).toBe(ENGINE_LABELS.claude);
    // key 带 pluginId 前缀：与同名 manual 行不撞
    expect(row.key).toBe('plugin:demo@marketplace:p-skill');
  });

  it('plugin 条目缺 PluginName 时回退 pluginId；registry targets 未提供容忍为空', () => {
    const row = skillRowModel(pluginAgg('x', { pluginName: undefined }));
    expect(row.sourceText).toBe('插件·demo@marketplace');
    const bare = skillRowModel(manualAgg('y'));
    expect(bare.targets).toEqual([]);
  });

  it('registry targets 中的非法引擎值被过滤（服务层校验前的不变式）', () => {
    const row = skillRowModel(manualAgg('z'), ['claude', 'wsl', 'omp']);
    expect(row.targets).toEqual(['claude', 'omp']);
  });
});

describe('nextTargets：勾选切换计算（固定顺序，不改入参）', () => {
  it('勾选加入并保持 SHARED_EXT_ENGINES 顺序', () => {
    const next = nextTargets(['omp'], 'claude');
    expect(next).toEqual(['claude', 'omp']);
    const next2 = nextTargets(['omp', 'codex'], 'pi');
    expect(next2).toEqual(['codex', 'pi', 'omp']);
  });

  it('取消移除已勾选引擎', () => {
    expect(nextTargets(['claude', 'codex'], 'claude')).toEqual(['codex']);
    expect(nextTargets(['pi'], 'pi')).toEqual([]);
  });

  it('不修改入参数组', () => {
    const current = ['codex'];
    nextTargets(current, 'pi');
    expect(current).toEqual(['codex']);
  });
});

describe('mcpConfigSummary / mcpRowModel：MCP 行摘要', () => {
  it('stdio 型取 command，HTTP 型取 url，均无则空串', () => {
    expect(mcpConfigSummary({ command: 'npx -y @context7/mcp', args: [] })).toBe('npx -y @context7/mcp');
    expect(mcpConfigSummary({ command: '  uvx  ' })).toBe('uvx');
    expect(mcpConfigSummary({ url: 'https://mcp.example.com/sse' })).toBe('https://mcp.example.com/sse');
    expect(mcpConfigSummary({ headers: {} })).toBe('');
    expect(mcpConfigSummary(null)).toBe('');
    expect(mcpConfigSummary(undefined)).toBe('');
  });

  it('command 优先于 url；非字符串形态忽略', () => {
    expect(mcpConfigSummary({ command: 'x', url: 'https://a' })).toBe('x');
    expect(mcpConfigSummary({ command: ['a', 'b'] })).toBe('');
  });

  it('mcpRowModel 汇总名称/摘要/启停/目标', () => {
    const row = mcpRowModel({
      name: 'context7',
      config: { command: 'npx' },
      origin: 'manual',
      enabled: false,
      targets: ['claude', 'bogus'],
      deployed: [],
      createdAt: '',
    } as any);
    expect(row.key).toBe('mcp:context7');
    expect(row.summary).toBe('npx');
    expect(row.enabled).toBe(false);
    expect(row.targets).toEqual(['claude']);
  });
});

describe('engineLabelOf：引擎标识展示', () => {
  it('已知引擎用展示名，未知原样，缺省空串', () => {
    expect(engineLabelOf('opencode')).toBe('OpenCode');
    expect(engineLabelOf('omp')).toBe('OMP');
    expect(engineLabelOf('wsl')).toBe('wsl');
    expect(engineLabelOf(undefined)).toBe('');
  });
});
