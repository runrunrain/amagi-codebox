/**
 * plugin store 归一化单测（契约 §7/§8）：
 *  - ccMcpPluginCount / resourceFilters MCP 计数诚实化（pluginType==='mcp' 为主、hasMcp 兜底）；
 *  - ccLoadError：loadCcAllData 的 track() 在子任务失败时写入（外层 catch 不可达），成功清空；
 *  - cxLoadError：loadCxPlugins 既有 catch 写入（rethrow 语义保持），成功清空；
 *  - refreshCc：失效缓存后全量三 load；
 *  - setExtSubPage：子页状态迁移契约（重构契约 §1.3）。
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

describe('setExtSubPage（子页状态迁移，重构契约 §1.3）', () => {
  it('引擎子页：同步 pluginEngine、清 activePluginId、重置 pluginView 列表态', () => {
    const store = usePluginStore();
    store.activePluginId = 'p1';
    store.pluginView = 'market';

    store.setExtSubPage('codex');
    expect(store.extSubPage).toBe('codex');
    // setExtSubPage 是唯一写入口：引擎子页时 pluginEngine 恒同步
    expect(store.pluginEngine).toBe('codex');
    // setPluginEngine 清 activePluginId + setPluginView('installed') 重置列表态
    expect(store.activePluginId).toBeNull();
    expect(store.pluginView).toBe('installed');
  });

  it('同值守卫：重击当前子页不动 activePluginId / pluginView', () => {
    const store = usePluginStore();
    store.setExtSubPage('pi');
    store.activePluginId = 'p2';
    store.pluginView = 'market';

    store.setExtSubPage('pi');
    expect(store.extSubPage).toBe('pi');
    expect(store.activePluginId).toBe('p2');
    expect(store.pluginView).toBe('market');
  });

  it('shared-skills 子页：不动 pluginEngine / activePluginId / pluginView', () => {
    const store = usePluginStore();
    store.activePluginId = 'p3';
    store.pluginView = 'market';

    store.setExtSubPage('shared-skills');
    expect(store.extSubPage).toBe('shared-skills');
    expect(store.pluginEngine).toBe('claude');
    expect(store.activePluginId).toBe('p3');
    expect(store.pluginView).toBe('market');

    // 从 shared-skills 切回引擎子页恢复引擎联动契约
    store.setExtSubPage('omp');
    expect(store.pluginEngine).toBe('omp');
    expect(store.activePluginId).toBeNull();
    expect(store.pluginView).toBe('installed');
  });

  it('shared-mcp 子页：不动 pluginEngine / activePluginId / pluginView（共用 MCP 契约 §4.1）', () => {
    const store = usePluginStore();
    store.activePluginId = 'p4';
    store.pluginView = 'market';

    store.setExtSubPage('shared-mcp');
    expect(store.extSubPage).toBe('shared-mcp');
    expect(store.pluginEngine).toBe('claude');
    expect(store.activePluginId).toBe('p4');
    expect(store.pluginView).toBe('market');

    // 从 shared-mcp 切回引擎子页恢复引擎联动契约
    store.setExtSubPage('opencode');
    expect(store.pluginEngine).toBe('opencode');
    expect(store.activePluginId).toBeNull();
    expect(store.pluginView).toBe('installed');
  });

  it('extMainTab 已删除（R5：plugin store 无主 tab 状态）', () => {
    const store = usePluginStore();
    expect('extMainTab' in store).toBe(false);
    expect('extSubPage' in store).toBe(true);
  });
});
