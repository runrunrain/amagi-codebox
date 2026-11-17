/**
 * SharedMarketPanel（共用插件市场）单测（范式对齐 pluginMarketPanel.test.ts）：
 *  - 一级＝市场列表（PluginRow：名称/插件计数徽章/Missing 警示/描述多行；
 *    行尾 更新/删除）；
 *  - 二级＝市场内插件列表（selectMarket 进入，技能数徽章 + 行尾「部署技能」；
 *    外部引用/无技能按钮禁用）；
 *  - 行为断言（expose 触发器）：handleUpdate/handleDeploy/requestRemove→
 *    confirmRemove/openAddDialog→submitAdd/openImportDialog→toggleImport→
 *    confirmImport 均打到 api/sharedMarket 对应桩；
 *  - 空态：无市场 → EmptyStateCard。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import type { SharedMarketplace, ClaudeMarketplaceForImport, SharedMarketDeployReport } from '../../../../api/sharedMarket';

// 显式返回类型标注：避免 vi.fn(async () => []) 推断为 Promise<never[]>，
// 导致 mockResolvedValueOnce 传真实对象时 vue-tsc 报 never 不兼容
const apiMocks = vi.hoisted(() => ({
  listSharedMarkets: vi.fn(async (): Promise<SharedMarketplace[]> => []),
  refreshSharedMarkets: vi.fn(async (): Promise<SharedMarketplace[]> => []),
  addSharedMarketplace: vi.fn(async (): Promise<SharedMarketplace[]> => []),
  listClaudeMarketplacesForImport: vi.fn(async (): Promise<ClaudeMarketplaceForImport[]> => []),
  importSharedMarketplacesFromClaude: vi.fn(async (): Promise<SharedMarketplace[]> => []),
  updateSharedMarketplace: vi.fn(async (): Promise<SharedMarketplace[]> => []),
  removeSharedMarketplace: vi.fn(async (): Promise<SharedMarketplace[]> => []),
  deploySharedMarketplacePlugin: vi.fn(async (): Promise<SharedMarketDeployReport> => ({
    marketplace: 'mk',
    plugin: 'alpha',
    deployed: ['skill-one', 'skill-two'],
    skipped: [],
    targetRoot: '/home/x/.agents/skills',
  })),
  openSharedMarketplaceFolder: vi.fn(async (): Promise<void> => {}),
}));

vi.mock('../../../../api/sharedMarket', () => apiMocks);

// F06：部署成功后刷新共用技能 store——mock 其 api 层断言联动
const sharedSkillsApiMocks = vi.hoisted(() => ({
  listSharedSkills: vi.fn(async () => []),
}));
vi.mock('../../../../api/sharedSkills', () => ({
  listSharedSkills: sharedSkillsApiMocks.listSharedSkills,
  setSharedSkillEnabled: vi.fn(async () => []),
  deleteSharedSkill: vi.fn(async () => []),
  openSharedSkillFolder: vi.fn(async () => {}),
  openSharedSkillsRoot: vi.fn(async () => {}),
}));

import SharedMarketPanel from '../../../../components/extensions/SharedMarketPanel.vue';
import { useSharedMarketStore } from '../../../../stores/sharedMarket';

import { instanceOf, renderInstanceAgain, renderWithCapture } from '../ui/testUtils';

function marketFixture(overrides: Partial<SharedMarketplace> = {}): SharedMarketplace {
  return {
    name: 'vexel-plugins-marketplace',
    source: 'https://github.com/runrunrain/vexel-plugins-marketplace.git',
    clonePath: '/home/x/.agents/marketplaces/vexel-plugins-marketplace',
    description: 'Vexel 官方市场',
    addedAt: '2026-11-17T00:00:00Z',
    lastUpdated: '2026-11-17T00:00:00Z',
    pluginCount: 2,
    plugins: [
      {
        name: 'writing-skills',
        version: '1.2.0',
        description: '写作方法论技能集',
        author: 'Vexel',
        skills: ['planner-doc-style', 'resume-writing'],
      },
      { name: 'external-one', description: '外部引用', skills: [], externalSource: true },
      { name: 'no-skill-one', description: '无技能插件', skills: [] },
    ],
    ...overrides,
  };
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
});

describe('一级：市场列表渲染', () => {
  it('市场行：名称 + 插件计数徽章 + 描述 + 更新/删除按钮', async () => {
    const store = useSharedMarketStore();
    store.marketplaces = [marketFixture()];

    const { html } = await renderWithCapture(SharedMarketPanel);
    expect(html).toMatch(/plugin-row-name[^>]*>vexel-plugins-marketplace</);
    expect(html).toContain('3 个插件');
    expect(html).toContain('Vexel 官方市场');
    expect(html).toContain('更新');
    expect(html).toContain('删除');
  });

  it('Missing 市场：警示徽章 + 描述引导恢复', async () => {
    const store = useSharedMarketStore();
    store.marketplaces = [marketFixture({ missing: true, plugins: [], pluginCount: 0 })];

    const { html } = await renderWithCapture(SharedMarketPanel);
    expect(html).toContain('目录缺失');
    expect(html).toContain('点击「更新」可恢复');
  });

  it('无市场 → EmptyStateCard 引导添加', async () => {
    const store = useSharedMarketStore();
    store.marketplaces = [];
    const { html } = await renderWithCapture(SharedMarketPanel);
    expect(html).toContain('暂无共用市场');
    expect(html).toContain('~/.agents/marketplaces');
  });
});

describe('二级：市场内插件列表', () => {
  it('selectMarket 进入：插件行 + 技能数徽章；返回恢复一级', async () => {
    const store = useSharedMarketStore();
    store.marketplaces = [marketFixture()];

    const { instances } = await renderWithCapture(SharedMarketPanel);
    const panel = instanceOf(instances, SharedMarketPanel);
    panel.exposed?.selectMarket(store.marketplaces[0]);
    let html = await renderInstanceAgain(panel);

    expect(html).toMatch(/dh-crumb-tail[^>]*>vexel-plugins-marketplace</);
    expect(html).toMatch(/plugin-row-name[^>]*>writing-skills</);
    expect(html).toContain('2 个技能');
    // 外部引用徽章如实呈现
    expect(html).toContain('外部引用');

    panel.exposed?.backToMarkets();
    html = await renderInstanceAgain(panel);
    expect(html).toMatch(/class="lt-title"[^>]*>共用市场</);
  });

  it('部署按钮态：外部引用/无技能禁用，可部署可点（二级内校验）', async () => {
    const store = useSharedMarketStore();
    store.marketplaces = [marketFixture()];
    const { instances } = await renderWithCapture(SharedMarketPanel);
    const panel = instanceOf(instances, SharedMarketPanel);
    panel.exposed?.selectMarket(store.marketplaces[0]);
    const html = await renderInstanceAgain(panel);
    expect(html).toContain('不可部署');
    expect(html).toContain('无技能');
    expect(html).toContain('部署技能');
  });

  it('handleDeploy → deploySharedMarketplacePlugin 桩命中（市场名+插件名），并刷新共用技能缓存（F06）', async () => {
    const store = useSharedMarketStore();
    store.marketplaces = [marketFixture()];
    const { instances } = await renderWithCapture(SharedMarketPanel);
    const panel = instanceOf(instances, SharedMarketPanel);
    // 进入二级市场后部署（handleDeploy 依赖 activeMarket）
    panel.exposed?.selectMarket(store.marketplaces[0]);
    await panel.exposed?.handleDeploy(store.marketplaces[0].plugins[0]);
    expect(apiMocks.deploySharedMarketplacePlugin).toHaveBeenCalledWith(
      'vexel-plugins-marketplace',
      'writing-skills',
    );
    // 部署写入 ~/.agents/skills 后必须刷新共用技能缓存，返回技能页立即可见
    expect(sharedSkillsApiMocks.listSharedSkills).toHaveBeenCalled();
  });
});

describe('行为断言（expose 触发器）', () => {
  it('handleUpdate → updateSharedMarketplace', async () => {
    const store = useSharedMarketStore();
    store.marketplaces = [marketFixture()];
    const { instances } = await renderWithCapture(SharedMarketPanel);
    await instanceOf(instances, SharedMarketPanel).exposed?.handleUpdate(store.marketplaces[0]);
    expect(apiMocks.updateSharedMarketplace).toHaveBeenCalledWith('vexel-plugins-marketplace');
  });

  it('requestRemove → confirmRemove → removeSharedMarketplace', async () => {
    const store = useSharedMarketStore();
    store.marketplaces = [marketFixture()];
    const { instances } = await renderWithCapture(SharedMarketPanel);
    const panel = instanceOf(instances, SharedMarketPanel);
    panel.exposed?.requestRemove(store.marketplaces[0]);
    await panel.exposed?.confirmRemove();
    expect(apiMocks.removeSharedMarketplace).toHaveBeenCalledWith('vexel-plugins-marketplace');
  });

  it('openAddDialog → 注入源 → submitAdd → addSharedMarketplace（源透传；空输入不发请求）', async () => {
    const store = useSharedMarketStore();
    store.marketplaces = [];
    const { instances } = await renderWithCapture(SharedMarketPanel);
    const panel = instanceOf(instances, SharedMarketPanel);
    panel.exposed?.openAddDialog();
    await panel.exposed?.submitAdd();
    // 空输入不发请求
    expect(apiMocks.addSharedMarketplace).not.toHaveBeenCalled();

    // 注入源后提交：源透传 + 成功后刷新列表
    apiMocks.addSharedMarketplace.mockResolvedValueOnce([marketFixture()]);
    (panel.exposed as unknown as { addSource: { value: string } }).addSource.value =
      'https://github.com/user/market.git';
    await panel.exposed?.submitAdd();
    expect(apiMocks.addSharedMarketplace).toHaveBeenCalledWith(
      'https://github.com/user/market.git',
    );
  });

  it('openImportDialog → 读取 Claude 市场列表；toggleImport 选择 + 已导入不可选；confirmImport 派发名单', async () => {
    const store = useSharedMarketStore();
    store.marketplaces = [marketFixture()];
    apiMocks.listClaudeMarketplacesForImport.mockResolvedValueOnce([
      { name: 'vexel-plugins-marketplace', source: 'https://example.com/a.git', imported: true },
      { name: 'godot-claude-skills', source: 'https://example.com/b.git', imported: false },
    ]);

    const { instances } = await renderWithCapture(SharedMarketPanel);
    const panel = instanceOf(instances, SharedMarketPanel);
    await panel.exposed?.openImportDialog();
    expect(apiMocks.listClaudeMarketplacesForImport).toHaveBeenCalled();

    // 已导入的不可选；未导入的可选
    panel.exposed?.toggleImport({ name: 'vexel-plugins-marketplace', source: '', imported: true });
    panel.exposed?.toggleImport({ name: 'godot-claude-skills', source: '', imported: false });

    apiMocks.importSharedMarketplacesFromClaude.mockResolvedValueOnce([marketFixture()]);
    apiMocks.listSharedMarkets.mockResolvedValueOnce([marketFixture()]);
    await panel.exposed?.confirmImport();
    expect(apiMocks.importSharedMarketplacesFromClaude).toHaveBeenCalledWith([
      'godot-claude-skills',
    ]);
  });
});
