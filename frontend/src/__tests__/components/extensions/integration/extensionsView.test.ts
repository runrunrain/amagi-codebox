/**
 * ExtensionsView 集成测试（契约 §7.2 / R7 顶部页签布局）：
 *  - 顶部页签行渲染（七子页项：公共两页 + 五引擎，无分组/图标）与选中胶囊高亮；
 *    左侧子页导航栏已移除（ex-subnav 类不回归）；
 *  - 子页切换走 setExtSubPage：引擎子页联动 pluginEngine 同步、pluginView
 *    重置回列表态、activePluginId 清空（契约 §2 引擎切换契约平移）；同值守卫；
 *  - shared-skills 子页挂载 SharedSkillsPanel 且不动引擎态；
 *  - 五引擎子页面板挂载不回归（engine prop / WSL 占位 / 空态文案）；
 *  - add_market 单一入口：expose 的 handleAddMarket(engine) 打开内联 Dialog
 *    （经 ctx.teleports.body 断言，标题按引擎）；
 *  - submitAddMarket 按引擎分派 addCcMarketplace / addCxMarketplace；
 *  - 面板链路回归：市场视图 PluginMarketPanel「添加市场」→（@add_market 转发）→
 *    ExtensionsView 内联 Dialog 打开（§9 显式变更 ②④ 的端到端验证）。
 *
 * 范式：node 环境 + renderToString（禁新增 DOM 测试库），store 状态种子驱动，
 * 交互断言直调 defineExpose 触发器（先例 ../ui/testUtils.ts）。
 * 主 tab（extMainTab）/EnvVarsPanel/OtherToolsPanel 分支已随 R5/R6 移除，
 * 已下线功能的相关 mock 行已删除（契约 §4.8）。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { ssrContextKey } from 'vue';
import type { ComponentInternalInstance, Ref } from 'vue';

// 在 api / wails 边界打桩（ExtensionsView 依赖图覆盖五引擎面板、共用技能面板与 WSL 占位组件）
const api = vi.hoisted(() => ({
  // stores/plugin（Claude / Codex）
  getInstalledPlugins: vi.fn(async () => []),
  analyzePluginType: vi.fn(async () => 'skill'),
  getMarketplaces: vi.fn(async () => []),
  getAvailablePlugins: vi.fn(async () => []),
  addMarketplace: vi.fn(async () => ({ success: true })),
  refreshCodexPlugins: vi.fn(async () => ({ installed: [], marketplaces: [], available: [], warnings: [] })),
  addCodexMarketplace: vi.fn(async () => ({ success: true })),
  // stores/piPlugin + WSLPluginTargetPlaceholder / PiPluginWSLSection
  refreshPiPackages: vi.fn(async () => ({ installed: [] })),
  getPiPackageDetails: vi.fn(async () => null),
  installPiPackage: vi.fn(),
  updatePiPackage: vi.fn(),
  removePiPackage: vi.fn(),
  installPiPackageTargeted: vi.fn(),
  updatePiPackageTargeted: vi.fn(),
  removePiPackageTargeted: vi.fn(),
  getPiPluginWSLStatus: vi.fn(async () => ({ available: false })),
  checkPiPluginWSLStatus: vi.fn(async () => ({ available: false })),
  getWSLPluginTargetSupport: vi.fn(async () => ({ entries: [] })),
  // stores/opencodePlugin / stores/ompPlugin（模块装载用，SSR 不触发调用）
  refreshOpenCodePlugins: vi.fn(async () => ({ installed: [] })),
  refreshOmpPlugins: vi.fn(async () => ({ installed: [], warnings: [] })),
  // stores/sharedSkills（SharedSkillsPanel，SSR 不执行 onMounted）
  listSharedSkills: vi.fn(async () => []),
  setSharedSkillEnabled: vi.fn(async () => []),
  deleteSharedSkill: vi.fn(async () => []),
  refreshSharedSkills: vi.fn(async () => []),
  // stores/sharedMcp（SharedMcpPanel，SSR 不执行 onMounted）
  listSharedMcp: vi.fn(async () => ({
    entries: [],
    registryExists: true,
    registryPath: '/home/u/.agents/.mcp.json',
    enginesMeta: [],
  })),
  refreshSharedMcp: vi.fn(async () => ({
    entries: [],
    registryExists: true,
    registryPath: '/home/u/.agents/.mcp.json',
    enginesMeta: [],
  })),
  createSharedMcpEntry: vi.fn(),
  updateSharedMcpEntry: vi.fn(),
  deleteSharedMcpEntry: vi.fn(),
  setSharedMcpEngineSelection: vi.fn(),
  openSharedMcpRegistryDir: vi.fn(async () => undefined),
}));

vi.mock('../../../../api/plugin', () => ({
  getInstalledPlugins: api.getInstalledPlugins,
  analyzePluginType: api.analyzePluginType,
  getMarketplaces: api.getMarketplaces,
  getAvailablePlugins: api.getAvailablePlugins,
  addMarketplace: api.addMarketplace,
}));

vi.mock('../../../../api/codexPlugin', () => ({
  refreshCodexPlugins: api.refreshCodexPlugins,
  addCodexMarketplace: api.addCodexMarketplace,
}));

vi.mock('../../../../api/piPlugin', () => ({
  refreshPiPackages: api.refreshPiPackages,
  getPiPackageDetails: api.getPiPackageDetails,
  installPiPackage: api.installPiPackage,
  updatePiPackage: api.updatePiPackage,
  removePiPackage: api.removePiPackage,
  installPiPackageTargeted: api.installPiPackageTargeted,
  updatePiPackageTargeted: api.updatePiPackageTargeted,
  removePiPackageTargeted: api.removePiPackageTargeted,
  getPiPluginWSLStatus: api.getPiPluginWSLStatus,
  checkPiPluginWSLStatus: api.checkPiPluginWSLStatus,
  getWSLPluginTargetSupport: api.getWSLPluginTargetSupport,
}));

// PiPluginPanel 直接 import 生成物 SwitchPackageSource（wailsjs 禁改，测试打桩）
vi.mock('../../../../../wailsjs/go/piplugin/Service', () => ({
  SwitchPackageSource: vi.fn(),
}));

vi.mock('../../../../api/opencodePlugin', () => ({
  refreshOpenCodePlugins: api.refreshOpenCodePlugins,
  getOpenCodePluginDetails: vi.fn(async () => null),
  installOpenCodePlugin: vi.fn(),
  updateOpenCodePlugin: vi.fn(),
  uninstallOpenCodePlugin: vi.fn(),
}));

vi.mock('../../../../api/ompPlugin', () => ({
  refreshOmpPlugins: api.refreshOmpPlugins,
  installOmpPlugin: vi.fn(),
  uninstallOmpPlugin: vi.fn(),
  setOmpPluginEnabled: vi.fn(),
  upgradeOmpPlugin: vi.fn(),
}));

vi.mock('../../../../api/sharedSkills', () => ({
  listSharedSkills: api.listSharedSkills,
  setSharedSkillEnabled: api.setSharedSkillEnabled,
  deleteSharedSkill: api.deleteSharedSkill,
  openSharedSkillFolder: vi.fn(async () => undefined),
  openSharedSkillsRoot: vi.fn(async () => undefined),
  refreshSharedSkills: api.refreshSharedSkills,
}));

vi.mock('../../../../api/sharedMcp', () => ({
  listSharedMcp: api.listSharedMcp,
  refreshSharedMcp: api.refreshSharedMcp,
  createSharedMcpEntry: api.createSharedMcpEntry,
  updateSharedMcpEntry: api.updateSharedMcpEntry,
  deleteSharedMcpEntry: api.deleteSharedMcpEntry,
  setSharedMcpEngineSelection: api.setSharedMcpEngineSelection,
  openSharedMcpRegistryDir: api.openSharedMcpRegistryDir,
}));

// stores/remoteClient（RemoteScopeBanner 依赖）装载期引用 wails runtime，打桩
vi.mock('../../../../../wailsjs/runtime/runtime', () => ({
  EventsOn: vi.fn(() => () => {}),
}));
vi.mock('../../../../api/remoteClient', () => ({
  remoteErrorText: (error: unknown) => String(error),
}));

import ExtensionsView from '../../../../views/ExtensionsView.vue';
import PluginInstalledPanel from '../../../../components/extensions/PluginInstalledPanel.vue';
import PluginMarketPanel from '../../../../components/extensions/PluginMarketPanel.vue';
import SharedSkillsPanel from '../../../../components/extensions/SharedSkillsPanel.vue';
import SharedMcpPanel from '../../../../components/extensions/SharedMcpPanel.vue';
import { usePluginStore } from '../../../../stores/plugin';
import type { ExtSubPage } from '../../../../stores/plugin';
import { instanceOf, renderInstanceAgain, renderWithCapture } from '../ui/testUtils';

/** ssrRenderTeleport 的 push 收集项（与 piPluginPanel.test.ts 同型） */
type PushItem = string | Promise<unknown> | PushItem[];

async function unrollPush(item: PushItem): Promise<string> {
  if (typeof item === 'string') return item;
  if (Array.isArray(item)) {
    let out = '';
    for (const nested of item) out += await unrollPush(nested);
    return out;
  }
  // await 的结果经 Awaited 递归展开会塌缩为 unknown，对结果断言回 PushItem
  return unrollPush(await item as PushItem);
}

/**
 * 同实例状态变更后重渲并收集本趟新增的 body teleport 内容：
 * ssrRenderTeleport 把弹层写入 appContext.provides[ssrContextKey] 的
 * __teleportBuffers，首趟 resolveTeleports 只消费不清空 → 记录趟前长度、只展开新增段。
 */
async function rerenderWithTeleports(
  view: ComponentInternalInstance,
): Promise<{ html: string; newTeleports: string }> {
  const ssrCtx = view.appContext.provides[ssrContextKey] as
    | { __teleportBuffers?: Record<string, PushItem[]> }
    | undefined;
  const before = ssrCtx?.__teleportBuffers?.body?.length ?? 0;
  const html = await renderInstanceAgain(view);
  const appended = (ssrCtx?.__teleportBuffers?.body ?? []).slice(before);
  let newTeleports = '';
  for (const item of appended) newTeleports += await unrollPush(item);
  return { html, newTeleports };
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
});

describe('ExtensionsView 集成（契约 §7.2 / R7 顶部页签布局）', () => {
  it('默认渲染：共用技能、MCP 和市场只有一个入口，并保留五个引擎', async () => {
    const { html } = await renderWithCapture(ExtensionsView);
    for (const label of ['共用技能', '共用 MCP', '共用市场', 'Claude Code', 'OpenCode', 'Codex', 'Pi', 'OMP']) {
      expect(html).toContain(label);
    }
    // 默认 Claude 子页选中胶囊高亮（active 类随 extSubPage）
    expect(html).toContain('ex-tab active');
    // 左侧子页导航栏已移除（ex-subnav 类不回归）
    expect(html).not.toContain('ex-subnav');
    // 页签行为纯文字（nav 区块内无图标）
    const navMatch = html.match(/<nav class="ex-tabs"[^>]*>[\s\S]*?<\/nav>/);
    expect(navMatch).not.toBeNull();
    expect(navMatch?.[0]).not.toContain('<svg');
    expect(navMatch?.[0]).not.toContain('共用中心');
    expect(navMatch?.[0].match(/<button/g)).toHaveLength(8);
    // 主 tab Segmented 已移除（R5/R6：插件管理/环境变量/其他工具）
    expect(html).not.toContain('插件管理');
    expect(html).not.toContain('其他工具');
    // 默认 Claude pane：PluginInstalledPanel 空态 + WSLPluginTargetPlaceholder(cli=claude)
    expect(html).toContain('暂无已安装插件');
    expect(html).toContain('Claude Code：');
    // 其他引擎 pane / 共用技能 pane 未挂载
    expect(html).not.toContain('暂无 OpenCode 全局插件');
    expect(html).not.toContain('暂无 Pi 全局包');
    expect(html).not.toContain('暂无 OMP 插件');
    expect(html).not.toContain('暂无共用技能');
  });

  it('子页切换：setExtSubPage 驱动各引擎 pane 挂载且 pluginEngine 同步', async () => {
    const store = usePluginStore();
    const markers: Record<string, string> = {
      codex: '暂无已安装 Codex 插件',
      opencode: '暂无 OpenCode 全局插件',
      pi: '暂无 Pi 全局包',
      omp: '暂无 OMP 插件',
    };
    for (const [subPage, marker] of Object.entries(markers)) {
      store.setExtSubPage(subPage as ExtSubPage);
      // 引擎子页：pluginEngine 恒同步（setExtSubPage 是唯一写入口，契约 §1.3）
      expect(store.pluginEngine).toBe(subPage);
      const { html } = await renderWithCapture(ExtensionsView);
      expect(html).toContain(marker);
      expect(html).not.toContain('暂无已安装插件');
    }
  });

  it('shared-skills 子页：挂载 SharedSkillsPanel，且不动引擎态', async () => {
    const store = usePluginStore();
    store.setExtSubPage('codex');
    store.setExtSubPage('shared-skills');
    // shared-skills 不联动引擎状态（契约 §1.3）
    expect(store.pluginEngine).toBe('codex');

    const { html, instances } = await renderWithCapture(ExtensionsView);
    expect(html).toContain('暂无共用技能');
    expect(html).toContain('导入压缩包');
    expect(html).toContain('导入文件夹');
    expect(html).not.toContain('暂无已安装 Codex 插件');
    expect(() => instanceOf(instances, SharedSkillsPanel)).not.toThrow();
  });

  it('shared-mcp 子页：挂载 SharedMcpPanel，且不动引擎态（共用 MCP 契约 §4.1）', async () => {
    const store = usePluginStore();
    store.setExtSubPage('codex');
    store.setExtSubPage('shared-mcp');
    // shared-mcp 不联动引擎状态（共用 MCP 契约 §4.1：与 shared-skills 同语义）
    expect(store.pluginEngine).toBe('codex');

    const { html, instances } = await renderWithCapture(ExtensionsView);
    // 空态面板挂载（store 未加载，entries=[]）
    expect(html).toContain('暂无共用 MCP 服务器');
    expect(html).toContain('导入 JSON');
    expect(html).not.toContain('暂无已安装 Codex 插件');
    expect(html).not.toContain('暂无共用技能');
    expect(() => instanceOf(instances, SharedMcpPanel)).not.toThrow();
  });

  it('handleSubPageChange：走 setExtSubPage 并重置到列表态（契约 §2 平移）；同值守卫', async () => {
    const store = usePluginStore();
    store.setPluginView('market');
    store.activePluginId = 'p1';

    const { instances } = await renderWithCapture(ExtensionsView);
    const view = instanceOf(instances, ExtensionsView);
    view.exposed?.handleSubPageChange('codex');

    expect(store.extSubPage).toBe('codex');
    // setPluginEngine 清 activePluginId；setPluginView 拉回列表态
    expect(store.pluginEngine).toBe('codex');
    expect(store.activePluginId).toBeNull();
    expect(store.pluginView).toBe('installed');

    // 同值守卫：重击当前子页不清详情态/不重置视图态
    store.activePluginId = 'p2';
    store.setPluginView('market');
    view.exposed?.handleSubPageChange('codex');
    expect(store.extSubPage).toBe('codex');
    expect(store.activePluginId).toBe('p2');
    expect(store.pluginView).toBe('market');
  });

  it('handleAddMarket(engine)：内联 Dialog 经 teleports 打开，标题按引擎', async () => {
    const { teleports, instances } = await renderWithCapture(ExtensionsView);
    expect(teleports.body ?? '').not.toContain('Codex 市场');

    const view = instanceOf(instances, ExtensionsView);
    view.exposed?.handleAddMarket('codex');
    const { newTeleports } = await rerenderWithTeleports(view);
    expect(newTeleports).toContain('Codex 市场');
    expect(newTeleports).toContain('市场源');
    expect(newTeleports).toContain('添加');
  });

  it('submitAddMarket：按引擎分派 addCcMarketplace / addCxMarketplace', async () => {
    const { instances } = await renderWithCapture(ExtensionsView);
    const view = instanceOf(instances, ExtensionsView);
    // SSR 捕获的 exposed 为 defineExpose 的原始对象：ref 成员即 ref 本体，经 .value 写入
    const setMarketSource = (source: string) => {
      (view.exposed as unknown as { marketSource: Ref<string> }).marketSource.value = source;
    };

    // Claude：addCcMarketplace → api.addMarketplace(source)
    view.exposed?.handleAddMarket('claude');
    setMarketSource('owner/marketplace');
    await view.exposed?.submitAddMarket();
    expect(api.addMarketplace).toHaveBeenCalledWith('owner/marketplace');

    // Codex：addCxMarketplace → api.addCodexMarketplace({ source })
    view.exposed?.handleAddMarket('codex');
    setMarketSource('user/marketplace');
    await view.exposed?.submitAddMarket();
    expect(api.addCodexMarketplace).toHaveBeenCalledWith({ source: 'user/marketplace' });

    // 提交成功后 Dialog 关闭（新增 teleport 段不再含市场对话框标题）
    const { newTeleports } = await rerenderWithTeleports(view);
    expect(newTeleports).not.toContain('Codex 市场');
  });

  it('面板链路回归：市场视图「添加市场」→ @add_market 转发 → 内联 Dialog 打开（§9 变更 ②④）', async () => {
    const store = usePluginStore();
    store.setPluginView('market');
    store.ccMarkets = [{ Name: 'official', Description: '官方市场' }];

    const { html, instances } = await renderWithCapture(ExtensionsView);
    // 市场视图渲染（PluginInstalledPanel 内嵌 PluginMarketPanel）
    expect(html).toContain('插件市场');
    expect(html).toContain('official');

    // PluginMarketPanel 的「添加市场」emit 'add_market' → PluginInstalledPanel
    // 转发（@add_market）→ ExtensionsView handleAddMarket → 内联 Dialog 打开
    const market = instanceOf(instances, PluginMarketPanel);
    market.exposed?.handleAddMarket();
    const view = instanceOf(instances, ExtensionsView);
    expect(view.exposed).toBeDefined();
    const { newTeleports } = await rerenderWithTeleports(view);
    expect(newTeleports).toContain('Claude 市场');
  });

  it('PluginInstalledPanel 双引擎挂载：engine prop 传递（claude/codex 子页各一挂载点）', async () => {
    const store = usePluginStore();
    store.setExtSubPage('codex');
    const { instances } = await renderWithCapture(ExtensionsView);
    const panel = instanceOf(instances, PluginInstalledPanel);
    // 挂载的是 codex 分支的 PluginInstalledPanel（engine prop 生效）
    expect(panel.props.engine).toBe('codex');
  });
});
