/**
 * plugin store 归一化单测（契约 §7/§8）：
 *  - ccMcpPluginCount / resourceFilters MCP 计数诚实化（pluginType==='mcp' 为主、hasMcp 兜底）；
 *  - ccLoadError：loadCcAllData 的 track() 在子任务失败时写入（外层 catch 不可达），成功清空；
 *  - cxLoadError：loadCxPlugins 既有 catch 写入（rethrow 语义保持），成功清空；
 *  - refreshCc：失效缓存后全量三 load。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const { getInstalledPlugins, analyzePluginType, getMarketplaces, getAvailablePlugins, refreshCodexPlugins } = vi.hoisted(() => ({
  getInstalledPlugins: vi.fn(),
  analyzePluginType: vi.fn(),
  getMarketplaces: vi.fn(),
  getAvailablePlugins: vi.fn(),
  refreshCodexPlugins: vi.fn(),
}));

vi.mock('../../../../api/plugin', () => ({
  getInstalledPlugins,
  analyzePluginType,
  getMarketplaces,
  getAvailablePlugins,
}));

vi.mock('../../../../api/codexPlugin', () => ({
  refreshCodexPlugins,
}));

import { usePluginStore } from '../../../../stores/plugin';
import { plugin, codexplugin } from '../../../../../wailsjs/go/models';

/** Claude 已安装插件夹具（InstalledPlugin 必填字段全给） */
function ccFixture(overrides: Partial<plugin.InstalledPlugin> & { pluginType?: string; hasMcp?: boolean } = {}) {
  return {
    id: 'p1',
    name: 'Plugin One',
    marketplace: 'official',
    version: '1.0.0',
    scope: 'user',
    enabled: true,
    installPath: '/path',
    installedAt: '2026-01-01T00:00:00Z',
    lastUpdated: '2026-01-02T00:00:00Z',
    ...overrides,
  };
}

function cxFixture(overrides: Partial<codexplugin.CodexPlugin> = {}): codexplugin.CodexPlugin {
  return {
    id: 'c1',
    name: 'Cx One',
    marketplace: 'mk1',
    enabled: true,
    ...overrides,
  } as codexplugin.CodexPlugin;
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  analyzePluginType.mockResolvedValue('skill');
  getMarketplaces.mockResolvedValue([]);
  getAvailablePlugins.mockResolvedValue([]);
});

describe('ccMcpPluginCount / resourceFilters MCP 计数（§3.1/§7 计数诚实化）', () => {
  it('pluginType=mcp 与 hasMcp 兜底各计一次，均无者不计', () => {
    const store = usePluginStore();
    store.ccInstalled = [
      ccFixture({ id: 'a', pluginType: 'mcp' }),
      ccFixture({ id: 'b', pluginType: 'integration', hasMcp: true }),
      ccFixture({ id: 'c', pluginType: 'skill' }),
    ];
    expect(store.ccMcpPluginCount).toBe(2);
    expect(store.resourceFilters.find((f) => f.value === 'MCP')?.count).toBe(2);
  });
});

describe('ccLoadError（写入点在 loadCcAllData 的 track，§7）', () => {
  it('子任务失败 → track 写入且不更新缓存标志；动作本身 resolve（吞掉子任务错误）', async () => {
    getInstalledPlugins.mockRejectedValue(new Error('boom'));
    const store = usePluginStore();

    await expect(store.loadCcAllData()).resolves.toBeUndefined();
    expect(store.ccLoadError).toContain('boom');
    expect(store.ccDataLoaded).toBe(false);
  });

  it('后续成功 → ccLoadError 清空', async () => {
    getInstalledPlugins.mockRejectedValueOnce(new Error('boom'));
    const store = usePluginStore();
    await store.loadCcAllData();
    expect(store.ccLoadError).not.toBe('');

    getInstalledPlugins.mockResolvedValue([]);
    await store.loadCcAllData();
    expect(store.ccLoadError).toBe('');
    expect(store.ccDataLoaded).toBe(true);
  });
});

describe('cxLoadError（loadCxPlugins 既有 catch 写入，rethrow 保持，§7）', () => {
  it('失败 → 写入并 rethrow；成功 → 清空', async () => {
    const store = usePluginStore();

    refreshCodexPlugins.mockRejectedValueOnce(new Error('cli down'));
    await expect(store.loadCxPlugins(true)).rejects.toThrow('cli down');
    expect(store.cxLoadError).toContain('cli down');

    refreshCodexPlugins.mockResolvedValueOnce({
      installed: [],
      marketplaces: [],
      available: [],
      warnings: [],
    });
    await store.loadCxPlugins(true);
    expect(store.cxLoadError).toBe('');
  });
});

describe('refreshCc（失效缓存 + 三 load，§6.1/§7）', () => {
  it('5min 缓存内 loadCcAllData 跳过重载，refreshCc 强制重载', async () => {
    const store = usePluginStore();
    getInstalledPlugins.mockResolvedValue([ccFixture({ id: 'p1' })]);

    await store.loadCcAllData();
    expect(store.ccInstalled).toHaveLength(1);

    getInstalledPlugins.mockResolvedValue([ccFixture({ id: 'p1' }), ccFixture({ id: 'p2' })]);
    await store.loadCcAllData();
    expect(store.ccInstalled).toHaveLength(1);

    await store.refreshCc();
    expect(store.ccInstalled).toHaveLength(2);
  });
});

describe('cxDuplicates（重复诊断摘要数据源，§6.2）', () => {
  it('同名不同市场的插件归为一组', () => {
    const store = usePluginStore();
    store.cxInstalled = [
      cxFixture({ id: 'a@mk1', name: 'dup', marketplace: 'mk1' }),
      cxFixture({ id: 'a@mk2', name: 'dup', marketplace: 'mk2' }),
      cxFixture({ id: 'b@mk1', name: 'uniq', marketplace: 'mk1' }),
    ];
    expect(store.cxDuplicates).toHaveLength(1);
    expect(store.cxDuplicates[0]).toHaveLength(2);
  });
});
