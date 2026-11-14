/**
 * PluginMarketPanel（市场视图，仅 Claude/Codex）单测（契约 §6.6/§8）：
 *  - 已安装判定真实推导：Claude 按名称命中 → 按钮「已安装」禁用态，未命中 → 「安装」；
 *  - 排序：Claude 提供安装量/名称，Codex 仅名称（CodexAvailablePlugin 无 installCount）；
 *  - 安装量徽章 K/M 缩写；市场源列表「{n} 个可安装」计数；
 *  - expose：handleAddMarket → emit('add_market', engine)、goBack → emit('back')、
 *    handleInstall → installCcPlugin(name) / installCxPlugin(id, marketplace)。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const apiMocks = vi.hoisted(() => ({
  installPlugin: vi.fn(async () => ({ success: true })),
  getInstalledPlugins: vi.fn(async () => []),
  analyzePluginType: vi.fn(async () => 'skill'),
  getAvailablePlugins: vi.fn(async () => []),
  installCodexPlugin: vi.fn(async () => ({ success: true })),
  refreshCodexPlugins: vi.fn(async () => ({ installed: [], marketplaces: [], available: [], warnings: [] })),
}));

vi.mock('../../../../api/plugin', () => ({
  installPlugin: apiMocks.installPlugin,
  getInstalledPlugins: apiMocks.getInstalledPlugins,
  analyzePluginType: apiMocks.analyzePluginType,
  getAvailablePlugins: apiMocks.getAvailablePlugins,
}));

vi.mock('../../../../api/codexPlugin', () => ({
  installCodexPlugin: apiMocks.installCodexPlugin,
  refreshCodexPlugins: apiMocks.refreshCodexPlugins,
}));

import PluginMarketPanel from '../../../../components/extensions/PluginMarketPanel.vue';
import { usePluginStore } from '../../../../stores/plugin';
import { plugin, codexplugin } from '../../../../../wailsjs/go/models';
import type { AvailablePluginView } from '../../../../components/extensions/pluginViewTypes';
import { instanceOf, renderWithCapture } from '../ui/testUtils';

function ccInstalledFixture(overrides: Partial<plugin.InstalledPlugin> = {}): plugin.InstalledPlugin {
  return {
    id: 'alpha@official',
    name: 'Alpha',
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
    id: 'px@mk',
    name: 'X',
    marketplace: 'mk',
    enabled: true,
    ...overrides,
  } as codexplugin.CodexPlugin;
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
});

describe('市场视图渲染', () => {
  it('Claude：已安装推导（名称命中 →「已安装」；未命中 →「安装」）+ 安装量 K 缩写', async () => {
    const store = usePluginStore();
    store.ccMarkets = [{ Name: 'official', Description: '官方市场' }];
    store.ccInstalled = [ccInstalledFixture()];
    store.ccAvailable = [
      { name: 'Alpha', pluginId: 'alpha', marketplaceName: 'official', installCount: 1500, description: '已装的那个' },
      { name: 'NewOne', pluginId: 'new', marketplaceName: 'official', installCount: 2, description: '新的那个' },
    ] as unknown as typeof store.ccAvailable;

    const { html } = await renderWithCapture(PluginMarketPanel, { engine: 'claude' });

    expect(html).toContain('已安装');
    // 去掉「已安装」后仍应有可安装条目的「安装」按钮（两种状态并存）
    expect(html.replaceAll('已安装', '')).toContain('安装');
    expect(html).toContain('NewOne');
    // 安装量徽章 K/M 缩写（1500 → 1.5K）
    expect(html).toContain('1.5K 次安装');
    // 市场源列表与「{n} 个可安装」计数
    expect(html).toContain('official');
    expect(html).toContain('官方市场');
    expect(html).toContain('2 个可安装');
  });

  it('Claude 提供安装量排序，Codex 仅名称排序', async () => {
    const claudeStore = usePluginStore();
    claudeStore.ccMarkets = [{ Name: 'official' }];
    const claude = await renderWithCapture(PluginMarketPanel, { engine: 'claude' });
    expect(claude.html).toContain('安装量');
    expect(claude.html).toContain('名称 A-Z');

    setActivePinia(createPinia());
    const codexStore = usePluginStore();
    codexStore.cxMarkets = [{ name: 'mk1' }] as codexplugin.CodexMarketplace[];
    const codex = await renderWithCapture(PluginMarketPanel, { engine: 'codex' });
    expect(codex.html).not.toContain('安装量');
    expect(codex.html).toContain('名称 A-Z');
  });

  it('Codex：按 pluginId/name 命中已安装 → 「已安装」', async () => {
    const store = usePluginStore();
    store.cxMarkets = [{ name: 'mk' }] as codexplugin.CodexMarketplace[];
    store.cxInstalled = [cxFixture({ id: 'px@mk', name: 'X' })];
    store.cxAvailable = [
      { pluginId: 'px@mk', name: 'X', marketplaceName: 'mk' },
      { pluginId: 'other@mk', name: 'Other', marketplaceName: 'mk' },
    ];

    const { html } = await renderWithCapture(PluginMarketPanel, { engine: 'codex' });
    expect(html).toContain('已安装');
    expect(html.replaceAll('已安装', '')).toContain('安装');
    expect(html).toContain('Other');
  });

  it('无市场源 → EmptyStateCard + 添加市场入口', async () => {
    const store = usePluginStore();
    store.ccMarkets = [];
    store.ccAvailable = [];
    const { html } = await renderWithCapture(PluginMarketPanel, { engine: 'claude' });
    expect(html).toContain('暂无市场');
    expect(html).toContain('添加市场源以浏览和安装插件');
  });
});

describe('行为断言（expose 触发器）', () => {
  it('handleAddMarket → emit("add_market", engine)（§0 断链回归：snake_case 命中）', async () => {
    const store = usePluginStore();
    store.ccMarkets = [{ Name: 'official' }];
    const onAddMarket = vi.fn();
    const { instances } = await renderWithCapture(PluginMarketPanel, {
      engine: 'claude',
      onAdd_market: onAddMarket,
    });
    instanceOf(instances, PluginMarketPanel).exposed?.handleAddMarket();
    expect(onAddMarket).toHaveBeenCalledWith('claude');
  });

  it('goBack → emit("back")（面包屑返回列表态）', async () => {
    const store = usePluginStore();
    store.ccMarkets = [{ Name: 'official' }];
    const onBack = vi.fn();
    const { instances } = await renderWithCapture(PluginMarketPanel, {
      engine: 'claude',
      onBack,
    });
    instanceOf(instances, PluginMarketPanel).exposed?.goBack();
    expect(onBack).toHaveBeenCalled();
  });

  it('handleInstall：Claude 按名称安装 / Codex 按 pluginId+marketplaceName 安装', async () => {
    const claudeStore = usePluginStore();
    claudeStore.ccMarkets = [{ Name: 'official' }];
    claudeStore.ccInstalled = [];
    claudeStore.ccAvailable = [
      { name: 'NewOne', pluginId: 'new', marketplaceName: 'official' },
    ] as unknown as typeof claudeStore.ccAvailable;
    const claudeRender = await renderWithCapture(PluginMarketPanel, { engine: 'claude' });
    const avail: AvailablePluginView = { name: 'NewOne', pluginId: 'new', marketplaceName: 'official' };
    await instanceOf(claudeRender.instances, PluginMarketPanel).exposed?.handleInstall(avail);
    expect(apiMocks.installPlugin).toHaveBeenCalledWith('NewOne');

    setActivePinia(createPinia());
    const codexStore = usePluginStore();
    codexStore.cxMarkets = [{ name: 'mk' }] as codexplugin.CodexMarketplace[];
    codexStore.cxInstalled = [];
    codexStore.cxAvailable = [{ pluginId: 'other@mk', name: 'Other', marketplaceName: 'mk' }];
    const codexRender = await renderWithCapture(PluginMarketPanel, { engine: 'codex' });
    await instanceOf(codexRender.instances, PluginMarketPanel).exposed?.handleInstall({
      pluginId: 'other@mk',
      name: 'Other',
      marketplaceName: 'mk',
    });
    expect(apiMocks.installCodexPlugin).toHaveBeenCalledWith({
      pluginId: 'other@mk',
      marketplace: 'mk',
    });
  });
});
