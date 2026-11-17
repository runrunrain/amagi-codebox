/**
 * PluginMarketPanel（市场视图，仅 Claude/Codex，两级分页）单测（契约 §6.6/§8）：
 *  - 一级＝市场源列表（PluginRow 行：名称/类型徽章/可安装计数/净化路径描述）；
 *    二级＝市场内插件列表（← 返回 + 面包屑市场名 + 状态页签 + 全宽行）；
 *  - 已安装判定真实推导：Claude 按名称命中 → 按钮「已安装」禁用态，未命中 → 「安装」；
 *  - 排序：Claude 提供安装量/名称，Codex 仅名称（CodexAvailablePlugin 无 installCount）；
 *    排序控件为 ui/Segmented 弱化分段（选中=白底浮起 seg active，非实心主色块）；
 *  - 安装量徽章 K/M 缩写；\\?\ 扩展长度前缀剥离为一级行描述、来源类型徽章
 *    （内置/本地/远程 Git）；
 *  - 两级联动：selectMarket 进入二级（标题/计数切换），backToSources 恢复一级；
 *  - 来源徽章降级：author 缺失时回落市场源名称，每行都有来源标识；
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
import { instanceOf, renderInstanceAgain, renderWithCapture } from '../ui/testUtils';

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
  it('Claude：一级市场源行 + 进入二级后已安装推导与安装量 K 缩写', async () => {
    const store = usePluginStore();
    store.ccMarkets = [{ Name: 'official', Description: '官方市场' }];
    store.ccInstalled = [ccInstalledFixture()];
    store.ccAvailable = [
      { name: 'Alpha', pluginId: 'alpha@official', marketplaceName: 'official', installCount: 1500, description: '已装的那个' },
      { name: 'NewOne', pluginId: 'new@official', marketplaceName: 'official', installCount: 2, description: '新的那个' },
    ] as unknown as typeof store.ccAvailable;

    const { html, instances } = await renderWithCapture(PluginMarketPanel, { engine: 'claude' });
    const panel = instanceOf(instances, PluginMarketPanel);

    // 一级市场源行：名称 + Description 完整展示 + 目录计数徽章（含已安装条目）
    expect(html).toMatch(/plugin-row-name[^>]*>official</);
    expect(html).toContain('官方市场');
    expect(html).toContain('2 个插件');

    // 二级插件列表：已安装推导（名称命中 →「已安装」；未命中 →「安装」两种状态并存）
    panel.exposed?.selectMarket({ Name: 'official' });
    const listHtml = await renderInstanceAgain(panel);
    expect(listHtml).toContain('已安装');
    expect(listHtml.replaceAll('已安装', '')).toContain('安装');
    expect(listHtml).toContain('NewOne');
    // 安装量徽章 K/M 缩写（1500 → 1.5K）
    expect(listHtml).toContain('1.5K 次安装');
  });

  it('Claude 提供安装量排序，Codex 仅名称排序（ui/Segmented 弱化分段选中态）', async () => {
    const claudeStore = usePluginStore();
    claudeStore.ccMarkets = [{ Name: 'official' }];
    const claude = await renderWithCapture(PluginMarketPanel, { engine: 'claude' });
    const claudePanel = instanceOf(claude.instances, PluginMarketPanel);
    claudePanel.exposed?.selectMarket({ Name: 'official' });
    const claudeHtml = await renderInstanceAgain(claudePanel);
    expect(claudeHtml).toContain('安装量');
    expect(claudeHtml).toContain('名称 A-Z');
    // 分段切换选中态=白底浮起胶囊（seg active），不再是实心主色块（sort-btn 不回归）
    expect(claudeHtml).toContain('seg active');
    expect(claudeHtml).not.toContain('sort-btn');

    setActivePinia(createPinia());
    const codexStore = usePluginStore();
    codexStore.cxMarkets = [{ name: 'mk1' }] as codexplugin.CodexMarketplace[];
    const codex = await renderWithCapture(PluginMarketPanel, { engine: 'codex' });
    const codexPanel = instanceOf(codex.instances, PluginMarketPanel);
    codexPanel.exposed?.selectMarket({ name: 'mk1' });
    const codexHtml = await renderInstanceAgain(codexPanel);
    expect(codexHtml).not.toContain('安装量');
    expect(codexHtml).toContain('名称 A-Z');
    expect(codexHtml).toContain('seg active');
  });

  it('Codex：按 pluginId/name 命中已安装 → 「已安装」', async () => {
    const store = usePluginStore();
    store.cxMarkets = [{ name: 'mk' }] as codexplugin.CodexMarketplace[];
    store.cxInstalled = [cxFixture({ id: 'px@mk', name: 'X' })];
    store.cxAvailable = [
      { pluginId: 'px@mk', name: 'X', marketplaceName: 'mk' },
      { pluginId: 'other@mk', name: 'Other', marketplaceName: 'mk' },
    ];

    const { instances } = await renderWithCapture(PluginMarketPanel, { engine: 'codex' });
    const panel = instanceOf(instances, PluginMarketPanel);
    panel.exposed?.selectMarket({ name: 'mk' });
    const html = await renderInstanceAgain(panel);
    expect(html).toContain('已安装');
    expect(html.replaceAll('已安装', '')).toContain('安装');
    expect(html).toContain('Other');
  });

  it('Claude：跨市场同名插件按完整 id 精确判定已安装，不互相污染', async () => {
    const store = usePluginStore();
    store.ccMarkets = [
      { Name: 'amagi-plugins-marketplace' },
      { Name: 'vexel-plugins-marketplace' },
    ];
    // writing-skills 同时存在于两个市场：仅 vexel 侧已安装
    store.ccInstalled = [
      ccInstalledFixture({ id: 'writing-skills@vexel-plugins-marketplace', name: 'writing-skills', marketplace: 'vexel-plugins-marketplace' }),
    ];
    store.ccAvailable = [
      { name: 'writing-skills', pluginId: 'writing-skills@amagi-plugins-marketplace', marketplaceName: 'amagi-plugins-marketplace' },
      { name: 'writing-skills', pluginId: 'writing-skills@vexel-plugins-marketplace', marketplaceName: 'vexel-plugins-marketplace' },
    ] as unknown as typeof store.ccAvailable;

    const { instances } = await renderWithCapture(PluginMarketPanel, { engine: 'claude' });
    const panel = instanceOf(instances, PluginMarketPanel);
    // 「已安装」出现次数：先剥离模板注释（SSR 会输出注释，字面计数会被注释污染），
    // 状态页签恒占 1 次，行按钮命中时 +1（页签文案与按钮文案同字，用计数区分）
    const installedOccurrences = (html: string) =>
      html.replace(/<!--[\s\S]*?-->/g, '').split('已安装').length - 1;

    // amagi 市场：同名但未安装 → 仅页签 1 次，行按钮为「安装」
    panel.exposed?.selectMarket({ Name: 'amagi-plugins-marketplace' });
    let html = await renderInstanceAgain(panel);
    expect(installedOccurrences(html)).toBe(1);
    expect(html).toContain('安装');

    // vexel 市场：完整 id 命中 → 页签 + 行按钮共 2 次
    panel.exposed?.selectMarket({ Name: 'vexel-plugins-marketplace' });
    html = await renderInstanceAgain(panel);
    expect(installedOccurrences(html)).toBe(2);
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

describe('市场源呈现与右栏联动（UI 走查修复）', () => {
  it('Codex 一级市场源行：\\\\?\\ 前缀剥离 + 来源类型徽章（内置/本地/远程 Git）', async () => {
    const store = usePluginStore();
    store.cxMarkets = [
      { name: 'openai-curated', source: '\\\\?\\C:\\Users\\dev\\.codex\\.tmp\\bundled-openai' },
      { name: 'community', repo: 'https://github.com/foo/market' },
      { name: 'localmk', source: 'C:\\markets\\local' },
    ] as unknown as codexplugin.CodexMarketplace[];

    const { html } = await renderWithCapture(PluginMarketPanel, { engine: 'codex' });

    // \\?\ 扩展长度前缀绝不作为主文案裸露，净化路径作为行描述完整展示
    expect(html).not.toContain('\\\\?\\');
    expect(html).toContain('C:\\Users\\dev\\.codex\\.tmp\\bundled-openai');
    // 来源类型徽章：bundled 路径 → 内置；URL → 远程 Git；本地盘符路径 → 本地
    expect(html).toContain('内置');
    expect(html).toContain('远程 Git');
    expect(html).toContain('本地');
  });

  it('两级联动：一级市场源列表 → 二级标题/计数切换，返回恢复一级', async () => {
    const store = usePluginStore();
    store.cxMarkets = [
      { name: 'openai-curated', source: 'https://example.com/curated' },
      { name: 'community', repo: 'https://github.com/foo/market' },
    ] as unknown as codexplugin.CodexMarketplace[];
    store.cxAvailable = [
      { pluginId: 'a@openai-curated', name: 'A', marketplaceName: 'openai-curated' },
      { pluginId: 'b@openai-curated', name: 'B', marketplaceName: 'openai-curated' },
      { pluginId: 'c@community', name: 'C', marketplaceName: 'community' },
    ];

    const { instances } = await renderWithCapture(PluginMarketPanel, { engine: 'codex' });
    const panel = instanceOf(instances, PluginMarketPanel);

    // 一级：市场源工具栏（计数=市场源数，「全部市场」聚合行不计入）
    let html = await renderInstanceAgain(panel);
    expect(html).toMatch(/class="lt-title"[^>]*>市场源</);
    expect(html).toMatch(/class="lt-count"[^>]*>2</);

    panel.exposed?.selectMarket({ name: 'openai-curated' });
    html = await renderInstanceAgain(panel);
    // 二级：面包屑尾段与标题=市场名；可安装插件计数=该市场内条数
    expect(html).toMatch(/dh-crumb-tail[^>]*>openai-curated</);
    expect(html).toMatch(/class="dh-title"[^>]*>openai-curated</);
    expect(html).toMatch(/class="lt-title"[^>]*>可安装插件</);
    expect(html).toMatch(/class="lt-count"[^>]*>2</);

    panel.exposed?.backToSources();
    html = await renderInstanceAgain(panel);
    expect(html).toMatch(/class="lt-title"[^>]*>市场源</);
  });

  it('来源徽章降级：author 缺失时回落市场源名称，保证每行都有来源标识', async () => {
    const store = usePluginStore();
    store.cxMarkets = [{ name: 'mk' }] as codexplugin.CodexMarketplace[];
    store.cxAvailable = [
      { pluginId: 'a@mk', name: 'WithAuthor', marketplaceName: 'mk', author: 'OpenAI' },
      { pluginId: 'b@mk', name: 'NoAuthor', marketplaceName: 'mk' },
    ];

    const { instances } = await renderWithCapture(PluginMarketPanel, { engine: 'codex' });
    const panel = instanceOf(instances, PluginMarketPanel);
    panel.exposed?.selectMarket({ name: 'mk' });
    const html = await renderInstanceAgain(panel);

    expect(html).toContain('OpenAI');
    // NoAuthor 行的来源徽章回落为市场源名称 mk（行结构与技能页同构：PluginRow）
    expect(html).toMatch(/plugin-row-name[^>]*>NoAuthor<\/span>[\s\S]{0,400}?source-badge[^>]*>mk</);
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

  it('handleInstall：Claude 按完整 pluginId 安装 / Codex 按 pluginId+marketplaceName 安装', async () => {
    const claudeStore = usePluginStore();
    claudeStore.ccMarkets = [{ Name: 'official' }];
    claudeStore.ccInstalled = [];
    claudeStore.ccAvailable = [
      { name: 'NewOne', pluginId: 'NewOne@official', marketplaceName: 'official' },
    ] as unknown as typeof claudeStore.ccAvailable;
    const claudeRender = await renderWithCapture(PluginMarketPanel, { engine: 'claude' });
    const avail: AvailablePluginView = { name: 'NewOne', pluginId: 'NewOne@official', marketplaceName: 'official' };
    await instanceOf(claudeRender.instances, PluginMarketPanel).exposed?.handleInstall(avail);
    // 同名插件跨市场时仅按名称安装有歧义，必须传 name@marketplace 完整 id
    expect(apiMocks.installPlugin).toHaveBeenCalledWith('NewOne@official');

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
