/**
 * PluginInstalledPanel（Claude/Codex 双引擎）单测（契约 §6.1/§6.2/§8）：
 *  - 分组渲染（多市场多行、分组头计数徽章真实数字）；
 *  - Codex：CountTabs 无 MCP 标签、行无描述行、子项区无 switch（只读）；
 *  - cxWarnings + 重复诊断 → DiagnosticBanner 计数正确；
 *  - 空列表 → EmptyStateCard 双按钮文案；
 *  - 行为（expose 触发器）：handleToggle 的两引擎开关语义（§5.4 警示：当前态 vs 目标态）、
 *    handleAddMarket 事件名恒为 'add_market'（§0 断链回归）、市场视图转发链（§9 变更 ②④）。
 * 范式：node + renderToString（不执行 onMounted），store 状态种子驱动；门控为 store 派生。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const apiMocks = vi.hoisted(() => ({
  enablePlugin: vi.fn(async () => ({ success: true })),
  disablePlugin: vi.fn(async () => ({ success: true })),
  getInstalledPlugins: vi.fn(async () => []),
  analyzePluginType: vi.fn(async () => 'skill'),
  getPluginDetail: vi.fn(async () => null),
  installPlugin: vi.fn(async () => ({ success: true })),
  refreshCodexPlugins: vi.fn(async () => ({ installed: [], marketplaces: [], available: [], warnings: [] })),
  setCodexPluginEnabled: vi.fn(async () => ({ success: true })),
  installCodexPlugin: vi.fn(async () => ({ success: true })),
}));

vi.mock('../../../../api/plugin', () => ({
  enablePlugin: apiMocks.enablePlugin,
  disablePlugin: apiMocks.disablePlugin,
  getInstalledPlugins: apiMocks.getInstalledPlugins,
  analyzePluginType: apiMocks.analyzePluginType,
  getPluginDetail: apiMocks.getPluginDetail,
  installPlugin: apiMocks.installPlugin,
}));

vi.mock('../../../../api/codexPlugin', () => ({
  refreshCodexPlugins: apiMocks.refreshCodexPlugins,
  setCodexPluginEnabled: apiMocks.setCodexPluginEnabled,
  installCodexPlugin: apiMocks.installCodexPlugin,
}));

import PluginInstalledPanel from '../../../../components/extensions/PluginInstalledPanel.vue';
import PluginMarketPanel from '../../../../components/extensions/PluginMarketPanel.vue';
import { usePluginStore } from '../../../../stores/plugin';
import { plugin, codexplugin } from '../../../../../wailsjs/go/models';
import { instanceOf, renderInstanceAgain, renderWithCapture } from '../ui/testUtils';

/** Claude 已安装条目（补 store 的 ExtendedInstalledPlugin 运行时字段） */
type CcItem = plugin.InstalledPlugin & { pluginType?: string; hasMcp?: boolean };

function ccFixture(overrides: Partial<CcItem> = {}): CcItem {
  return {
    id: 'p1',
    name: 'Plugin One',
    marketplace: 'official',
    version: '1.0.0',
    description: 'Claude 插件描述',
    scope: 'user',
    enabled: true,
    installPath: '/path/p1',
    installedAt: '2026-01-01T00:00:00Z',
    lastUpdated: '2026-01-02T00:00:00Z',
    gitCommitSha: 'abcdef1234567890',
    pluginType: 'skill',
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
});

describe('Claude 列表态渲染', () => {
  it('按市场分组渲染多行，分组头计数徽章为真实数字', async () => {
    const store = usePluginStore();
    store.ccInstalled = [
      ccFixture({ id: 'p1', name: 'Alpha', marketplace: 'official', description: 'desc-alpha' }),
      ccFixture({ id: 'p2', name: 'Beta', marketplace: 'official', hasMcp: true }),
      ccFixture({ id: 'p3', name: 'Gamma', marketplace: 'community' }),
    ];
    const { html } = await renderWithCapture(PluginInstalledPanel, { engine: 'claude' });

    expect(html).toContain('Alpha');
    expect(html).toContain('Beta');
    expect(html).toContain('Gamma');
    expect(html).toContain('desc-alpha');
    // 分组头（GroupHeader）与计数徽章（SSR 输出 class 后带 data-v 属性，用正则匹配计数）
    expect(html).toContain('group-header');
    expect(html).toContain('official');
    expect(html).toContain('community');
    expect(html).toMatch(/class="gh-count"[^>]*>2</);
    expect(html).toMatch(/class="gh-count"[^>]*>1</);
    // 行式条目 + 工具栏计数
    expect(html).toContain('plugin-row');
    expect(html).toMatch(/class="lt-count"[^>]*>3</);
    // Claude 计数标签含 MCP；MCP 计数 = pluginType=mcp 或 hasMcp 兜底（p2 → 1）
    expect(html).toContain('MCP');
    expect(html).toMatch(/class="ct-count"[^>]*>1</);
  });

  it('空列表渲染 EmptyStateCard 双按钮文案', async () => {
    const store = usePluginStore();
    store.ccInstalled = [];
    const { html } = await renderWithCapture(PluginInstalledPanel, { engine: 'claude' });

    expect(html).toContain('暂无已安装插件');
    expect(html).toContain('从插件市场安装 Claude 插件后将显示在此处');
    expect(html).toContain('浏览插件市场');
    expect(html).toContain('添加市场');
  });

  it('工具栏含刷新 / 浏览插件市场 / 添加▾触发器文案（菜单项在收起态不渲染）', async () => {
    const store = usePluginStore();
    store.ccInstalled = [ccFixture()];
    const { html } = await renderWithCapture(PluginInstalledPanel, { engine: 'claude' });

    expect(html).toContain('刷新');
    expect(html).toContain('浏览插件市场');
    expect(html).toContain('添加');
    // ActionMenu 收起态：菜单项（Teleport + v-if）不在输出中
    expect(html).not.toContain('am-item');
    expect(html).not.toContain('menuitem');
  });
});

describe('Codex 列表态渲染（§6.2 差异点）', () => {
  it('CountTabs 无 MCP 标签；行无描述行', async () => {
    const store = usePluginStore();
    store.cxInstalled = [cxFixture({ id: 'c1@mk1', name: 'CxOne', marketplace: 'mk1' })];
    const { html } = await renderWithCapture(PluginInstalledPanel, { engine: 'codex' });

    expect(html).not.toContain('>MCP<');
    // CodexPlugin 无 description 字段 → 行内不渲染描述行（§5.1）
    expect(html).not.toContain('plugin-row-desc');
    expect(html).toContain('CxOne');
  });

  it('cxWarnings 非空 + 重复插件 → 横幅出现且计数为真实长度（2 警告 + 1 组重复）', async () => {
    const store = usePluginStore();
    store.cxInstalled = [
      cxFixture({ id: 'dup@mk1', name: 'dup', marketplace: 'mk1' }),
      cxFixture({ id: 'dup@mk2', name: 'dup', marketplace: 'mk2' }),
    ];
    store.cxWarnings = ['警告一', '警告二'];
    const { html } = await renderWithCapture(PluginInstalledPanel, { engine: 'codex' });

    expect(html).toContain('diagnostic-banner');
    expect(html).toContain('加载诊断：0 个错误，3 个警告');
  });

  it('详情子项区只读：仅行开关，子项行无 switch（§5.3 Codex no-op）', async () => {
    const store = usePluginStore();
    store.cxInstalled = [cxFixture({ id: 'c1@mk1', name: 'CxOne', marketplace: 'mk1', enabled: true })];
    store.activePluginId = 'c1@mk1';
    store.cxActivePluginDetail = {
      'c1@mk1': {
        id: 'c1@mk1',
        name: 'CxOne',
        marketplace: 'mk1',
        skills: [{ name: 'skill-x', description: '一个技能', filePath: '/f' }],
        hasMcp: false,
      },
    } as unknown as typeof store.cxActivePluginDetail;
    const { html } = await renderWithCapture(PluginInstalledPanel, { engine: 'codex' });

    expect(html).toContain('skill-x');
    // 唯一的 switch 是列表行开关；子项行（SubItemRow）toggleable=false 不渲染开关
    const switchCount = (html.match(/role="switch"/g) ?? []).length;
    expect(switchCount).toBe(1);
  });

  it('Codex 空态文案', async () => {
    const store = usePluginStore();
    store.cxInstalled = [];
    const { html } = await renderWithCapture(PluginInstalledPanel, { engine: 'codex' });
    expect(html).toContain('暂无已安装 Codex 插件');
    expect(html).toContain('从插件市场安装 Codex 插件后将显示在此处');
  });
});

describe('Claude 详情态（§5.2 字段映射）', () => {
  it('面包屑 + MetaGrid 六项 + 子项行', async () => {
    const store = usePluginStore();
    store.ccInstalled = [ccFixture({ id: 'p1', enabled: false })];
    store.activePluginId = 'p1';
    store.ccActivePluginDetail = {
      p1: {
        id: 'p1',
        name: 'Plugin One',
        marketplace: 'official',
        scope: 'user',
        version: '1.0.0',
        gitCommitSha: 'abcdef1234567890',
        installedAt: '2026-01-01T00:00:00Z',
        lastUpdated: '2026-01-02T00:00:00Z',
        subItems: [{ type: 'skill', name: 'sub-skill', enabled: true, selectable: true }],
      },
    } as unknown as typeof store.ccActivePluginDetail;
    const { html } = await renderWithCapture(PluginInstalledPanel, { engine: 'claude' });

    // 面包屑（DetailHeader）
    expect(html).toContain('插件');
    expect(html).toContain('Plugin One');
    // 禁用态主操作为「启用」（§5.4）
    expect(html).toContain('启用');
    // MetaGrid：来源市场/版本/作用域/安装时间/最后更新/提交（short 8 位 mono）
    expect(html).toContain('来源市场');
    expect(html).toContain('official');
    expect(html).toContain('作用域');
    expect(html).toContain('安装时间');
    expect(html).toContain('最后更新');
    expect(html).toContain('提交');
    expect(html).toContain('abcdef12');
    expect(html).not.toContain('abcdef1234567890</dd>');
    // 子项（SubItemRow）
    expect(html).toContain('sub-skill');
  });
});

describe('Claude MCP 资源页（§6.1.1，面板本地标签页）', () => {
  it('selectTab("mcp") 后按插件分组渲染服务器与状态点', async () => {
    const store = usePluginStore();
    store.ccInstalled = [ccFixture({ id: 'p1', enabled: true, pluginType: 'mcp' })];
    store.ccActivePluginDetail = {
      p1: { mcpServers: { 'server-a': {}, 'server-b': {} } },
    } as unknown as typeof store.ccActivePluginDetail;

    const { instances } = await renderWithCapture(PluginInstalledPanel, { engine: 'claude' });
    const panel = instanceOf(instances, PluginInstalledPanel);
    panel.exposed?.selectTab('mcp');
    const html = await renderInstanceAgain(panel);

    expect(html).toContain('server-a');
    expect(html).toContain('server-b');
    expect(html).toContain('Model Context Protocol 服务器');
    // 所属插件 enabled → 绿点（class 断言；颜色由 token 保证）
    expect(html).toContain('mcp-dot on');
  });
});

describe('市场态（§6.6）', () => {
  it('pluginView=market 渲染 PluginMarketPanel（面包屑返回入口）', async () => {
    const store = usePluginStore();
    store.pluginView = 'market';
    const { html } = await renderWithCapture(PluginInstalledPanel, { engine: 'claude' });

    expect(html).toContain('插件市场');
    expect(html).toContain('插件');
  });

  it('市场视图「添加市场」经 @add_market 转发链到达外层（§9 显式变更 ②④）', async () => {
    const store = usePluginStore();
    store.pluginView = 'market';
    const onAddMarket = vi.fn();
    const { instances } = await renderWithCapture(PluginInstalledPanel, {
      engine: 'claude',
      onAdd_market: onAddMarket,
    });
    const market = instanceOf(instances, PluginMarketPanel);
    market.exposed?.handleAddMarket();
    expect(onAddMarket).toHaveBeenCalledWith('claude');
  });
});

describe('行为断言（expose 触发器，§8）', () => {
  it('handleToggle：Claude 第二参为「当前态」语义 → 启用即 togglePlugin(id,false) → enablePlugin', async () => {
    const store = usePluginStore();
    store.ccInstalled = [ccFixture({ id: 'p1', enabled: false })];
    const { instances } = await renderWithCapture(PluginInstalledPanel, { engine: 'claude' });
    await instanceOf(instances, PluginInstalledPanel).exposed?.handleToggle(
      ccFixture({ id: 'p1', enabled: false }),
      true,
    );
    // togglePlugin(id, currentState=false) → enablePlugin(id)（传 true 则会走 disablePlugin）
    expect(apiMocks.enablePlugin).toHaveBeenCalledWith('p1');
    expect(apiMocks.disablePlugin).not.toHaveBeenCalled();
  });

  it('handleToggle：Codex 第二参为「目标态」语义 → 启用即 toggleCxPlugin(id,true)', async () => {
    const store = usePluginStore();
    store.cxInstalled = [cxFixture({ id: 'c1@mk1', enabled: false })];
    const { instances } = await renderWithCapture(PluginInstalledPanel, { engine: 'codex' });
    await instanceOf(instances, PluginInstalledPanel).exposed?.handleToggle(
      cxFixture({ id: 'c1@mk1', enabled: false }),
      true,
    );
    expect(apiMocks.setCodexPluginEnabled).toHaveBeenCalledWith({ pluginId: 'c1@mk1' }, true);
  });

  it('handleAddMarket 触发的事件名恒为 add_market（camelCase 断链回归，§0）', async () => {
    const store = usePluginStore();
    store.ccInstalled = [ccFixture()];
    const onAddMarketSnake = vi.fn();
    const onAddMarketCamel = vi.fn();
    const { instances } = await renderWithCapture(PluginInstalledPanel, {
      engine: 'claude',
      onAdd_market: onAddMarketSnake,
      onAddMarket: onAddMarketCamel,
    });
    instanceOf(instances, PluginInstalledPanel).exposed?.handleAddMarket();
    expect(onAddMarketSnake).toHaveBeenCalledWith('claude');
    expect(onAddMarketCamel).not.toHaveBeenCalled();
  });
});
