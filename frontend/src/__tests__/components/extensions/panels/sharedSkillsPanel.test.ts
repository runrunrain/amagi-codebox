/**
 * SharedSkillsPanel 单测（契约 §2.6/§7.2）：
 *  - 四段式骨架：DiagnosticBanner（有 error 才渲染）→ CountTabs（全部/已启用/已禁用，
 *    计数来自 store 真实数据）→ ListToolbar（「共用技能」+ 打开技能根目录/刷新）→ 内容区；
 *  - 首屏门控（store 派生）：Loading / ErrorState；
 *  - 空态卡：EmptyStateCard 文案；
 *  - 启停/删除走确认弹窗（目录移动/永久删除的警示文案断言，经 teleports）；
 *  - managed 项：点开关/删除不弹窗、不调 API，直接 toast 引导（§2.4 行为裁定）。
 *
 * 范式：node 环境 renderToString + store 状态种子驱动门控分支（SSR 不执行 onMounted）；
 * 交互断言走 defineExpose 触发器；ConfirmDialog 弹层内容查 ctx.teleports
 * （renderInstanceAgain 追加到同一 __teleportBuffers，同 ompPluginPanel.test.ts）。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { createSSRApp, defineComponent, getCurrentInstance, h } from 'vue';
import type { ComponentInternalInstance } from 'vue';
import { renderToString } from 'vue/server-renderer';

const apiMocks = vi.hoisted(() => ({
  listSharedSkills: vi.fn(async () => [] as Array<import('../../../../api/sharedSkills').SharedSkillView>),
  setSharedSkillEnabled: vi.fn(async () => [] as Array<import('../../../../api/sharedSkills').SharedSkillView>),
  deleteSharedSkill: vi.fn(async () => [] as Array<import('../../../../api/sharedSkills').SharedSkillView>),
  openSharedSkillFolder: vi.fn(async () => undefined),
  openSharedSkillsRoot: vi.fn(async () => undefined),
  refreshSharedSkills: vi.fn(async () => [] as Array<import('../../../../api/sharedSkills').SharedSkillView>),
}));

vi.mock('../../../../api/sharedSkills', () => ({
  listSharedSkills: apiMocks.listSharedSkills,
  setSharedSkillEnabled: apiMocks.setSharedSkillEnabled,
  deleteSharedSkill: apiMocks.deleteSharedSkill,
  openSharedSkillFolder: apiMocks.openSharedSkillFolder,
  openSharedSkillsRoot: apiMocks.openSharedSkillsRoot,
  refreshSharedSkills: apiMocks.refreshSharedSkills,
}));

import SharedSkillsPanel from '../../../../components/extensions/SharedSkillsPanel.vue';
import { useSharedSkillsStore } from '../../../../stores/sharedSkills';
import { useToast } from '../../../../composables/useToast';
import type { SharedSkillView } from '../../../../api/sharedSkills';
import { renderInstanceAgain } from '../ui/testUtils';

// ---- 测试基建：渲染 + 实例捕获 + 保留 ssr 上下文（teleport buffer 断言用，同 omp 范式） ----

type PushItem = string | Promise<unknown> | PushItem[];

async function unroll(item: PushItem): Promise<string> {
  if (typeof item === 'string') return item;
  if (Array.isArray(item)) {
    let out = '';
    for (const nested of item) out += await unroll(nested);
    return out;
  }
  // await 的结果经 Awaited 递归展开会塌缩为 unknown，对结果断言回 PushItem
  return unroll(await item as PushItem);
}

interface SsrRenderContext {
  teleports?: Record<string, string>;
  __teleportBuffers?: Record<string, PushItem[]>;
}

interface PanelRenderResult {
  html: string;
  teleports: Record<string, string>;
  context: SsrRenderContext;
  instances: ComponentInternalInstance[];
}

async function renderPanel(): Promise<PanelRenderResult> {
  const instances: ComponentInternalInstance[] = [];
  const Wrapper = defineComponent({ setup: () => () => h(SharedSkillsPanel) });
  const app = createSSRApp(Wrapper);
  app.mixin({
    beforeCreate() {
      const instance = getCurrentInstance();
      if (instance) instances.push(instance);
    },
  });
  const context: SsrRenderContext = {};
  const html = await renderToString(app, context);
  return { html, teleports: context.teleports ?? {}, context, instances };
}

function panelInstance(result: PanelRenderResult): ComponentInternalInstance {
  const found = result.instances.find((instance) => instance.type === SharedSkillsPanel);
  if (!found) throw new Error('SharedSkillsPanel 未被渲染');
  return found;
}

/** 同 omp 范式：状态变更后重渲并展开本趟新增的 body teleport 内容 */
async function rerenderTeleports(result: PanelRenderResult): Promise<string> {
  const bodyBuffer = result.context.__teleportBuffers?.body ?? [];
  const before = bodyBuffer.length;
  await renderInstanceAgain(panelInstance(result));
  const appended = bodyBuffer.slice(before);
  let out = '';
  for (const item of appended) out += await unroll(item);
  return out;
}

/** instance.exposed 为 defineExpose 的原始对象（函数直取） */
interface PanelExposed {
  selectSkill: (skill: SharedSkillView) => void;
  clearActive: () => void;
  selectTab: (key: string) => void;
  onSearch: (value: string) => void;
  handleToggle: (skill: SharedSkillView, value: boolean) => void;
  confirmToggle: () => Promise<void>;
  requestDelete: (skill: SharedSkillView) => void;
  confirmDelete: () => Promise<void>;
  handleOpenFolder: (skill: SharedSkillView) => Promise<void>;
  handleOpenRoot: () => Promise<void>;
  handleRefresh: () => Promise<void>;
}

function panelExposed(result: PanelRenderResult): PanelExposed {
  const exposed = panelInstance(result).exposed as PanelExposed | null;
  if (!exposed) throw new Error('SharedSkillsPanel 未 defineExpose');
  return exposed;
}

// ---- 数据种子 ----

function skill(overrides: Partial<SharedSkillView> = {}): SharedSkillView {
  return {
    name: 'skill-a',
    displayName: 'Skill A',
    description: '技能 A 的描述',
    version: '1.0.0',
    author: 'amagi',
    enabled: true,
    path: '/home/u/.agents/skills/skill-a',
    hasSkillMd: true,
    managed: false,
    ...overrides,
  };
}

const ENABLED_A = skill();
const DISABLED_B = skill({
  name: 'skill-b',
  displayName: 'Skill B',
  description: '技能 B 的描述',
  enabled: false,
  path: '/home/u/.agents/skills-disabled/skill-b',
  version: '',
});
const MANAGED_BOX = skill({
  name: 'amagi-codebox',
  displayName: 'amagi-codebox',
  description: '应用自带技能',
  managed: true,
  managedStatus: 'up-to-date',
});

function seedStore(
  store: ReturnType<typeof useSharedSkillsStore>,
  skills: SharedSkillView[],
  extra: { loadError?: string; loading?: boolean } = {},
): void {
  store.skills = skills;
  if (extra.loadError !== undefined) store.error = extra.loadError;
  if (extra.loading !== undefined) store.loading = extra.loading;
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  // useToast 全局单例：清掉上一用例残留
  useToast().toasts.splice(0);
});

// ---- ①-④ 四段式骨架 ----

describe('SharedSkillsPanel 四段式骨架', () => {
  it('计数标签计数来自 store 真实数据（全部/已启用/已禁用）', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [ENABLED_A, DISABLED_B, MANAGED_BOX]);
    const { html } = await renderPanel();
    // SSR 输出含 scoped data-v-* 属性，用正则容忍（标签-计数相邻成对断言）
    expect(html).toMatch(/class="ct-label"[^>]*>全部<\/span><span class="ct-count"[^>]*>3<\/span>/);
    expect(html).toMatch(/class="ct-label"[^>]*>已启用<\/span><span class="ct-count"[^>]*>2<\/span>/);
    expect(html).toMatch(/class="ct-label"[^>]*>已禁用<\/span><span class="ct-count"[^>]*>1<\/span>/);
  });

  it('工具栏标题/操作与行渲染（名称+描述+开关）', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [ENABLED_A, DISABLED_B]);
    const { html } = await renderPanel();
    // ③ ListToolbar：标题 + 两个操作
    expect(html).toContain('共用技能');
    expect(html).toContain('打开技能根目录');
    expect(html).toContain('刷新');
    // ④ 行：displayName 与描述
    expect(html).toContain('Skill A');
    expect(html).toContain('技能 B 的描述');
    // 行开关（PluginRow switchVisible）
    expect(html).toContain('plugin-row-switch');
  });

  it('无 error 时诊断横幅不渲染；有 error 且无数据时首屏 ErrorState', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [ENABLED_A]);
    const clean = await renderPanel();
    expect(clean.html).not.toContain('diagnostic-banner');

    seedStore(store, [], { loadError: '加载共用技能失败：boom' });
    const errored = await renderPanel();
    expect(errored.html).toContain('加载共用技能失败：boom');
    expect(errored.html).toContain('重试');
  });

  it('首屏 Loading 门控（loading 且无数据）', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [], { loading: true });
    const { html } = await renderPanel();
    expect(html).toContain('加载技能中');
    expect(html).not.toContain('ct-tabs');
  });
});

// ---- 空态 / 过滤 ----

describe('SharedSkillsPanel 空态与过滤', () => {
  it('空 store → EmptyStateCard 文案', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, []);
    const { html } = await renderPanel();
    expect(html).toContain('暂无共用技能');
    expect(html).toContain('~/.agents/skills 下的技能目录会自动出现在这里');
  });

  it('计数标签切换与搜索过滤行', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [ENABLED_A, DISABLED_B]);
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.selectTab('disabled');
    let html = await renderInstanceAgain(panelInstance(result));
    expect(html).toContain('Skill B');
    expect(html).not.toContain('Skill A');

    panel.selectTab('all');
    panel.onSearch('Skill A');
    html = await renderInstanceAgain(panelInstance(result));
    expect(html).toContain('Skill A');
    expect(html).not.toContain('Skill B');
  });
});

// ---- 详情态 ----

describe('SharedSkillsPanel 详情态', () => {
  it('selectSkill → DetailHeader + MetaGrid（名称/目录名/路径/状态）', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [ENABLED_A, DISABLED_B]);
    const result = await renderPanel();
    panelExposed(result).selectSkill(DISABLED_B);
    const html = await renderInstanceAgain(panelInstance(result));
    expect(html).toContain('dh-breadcrumb');
    expect(html).toContain('共用技能</button>');
    expect(html).toContain('meta-grid');
    expect(html).toContain('skill-b');
    expect(html).toContain('/home/u/.agents/skills-disabled/skill-b');
    expect(html).toContain('已禁用');
  });

  it('clearActive 回到选择空位', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [ENABLED_A]);
    const result = await renderPanel();
    const panel = panelExposed(result);
    panel.selectSkill(ENABLED_A);
    panel.clearActive();
    const html = await renderInstanceAgain(panelInstance(result));
    expect(html).toContain('选择技能');
    expect(html).not.toContain('meta-grid');
  });
});

// ---- 启停/删除：确认弹窗（目录移动真实生效的警示文案） ----

describe('SharedSkillsPanel 启停/删除确认', () => {
  it('停用：确认弹窗含目录移动警示文案，confirm 调 setSharedSkillEnabled(name, false)', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [ENABLED_A]);
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.handleToggle(ENABLED_A, false);
    const teleportsHtml = await rerenderTeleports(result);
    expect(teleportsHtml).toContain('停用「Skill A」');
    expect(teleportsHtml).toContain('将把目录移动到 ~/.agents/skills-disabled/');
    expect(teleportsHtml).toContain('所有读取 ~/.agents/skills 的 CLI 将立即不再加载该技能');
    expect(teleportsHtml).toContain('可随时重新启用恢复');

    await panel.confirmToggle();
    expect(apiMocks.setSharedSkillEnabled).toHaveBeenCalledWith('skill-a', false);
  });

  it('启用：确认弹窗含移回文案，confirm 调 setSharedSkillEnabled(name, true)', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [DISABLED_B]);
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.handleToggle(DISABLED_B, true);
    const teleportsHtml = await rerenderTeleports(result);
    expect(teleportsHtml).toContain('启用「Skill B」');
    expect(teleportsHtml).toContain('将把目录移回 ~/.agents/skills/');
    expect(teleportsHtml).toContain('各 CLI 即可重新发现并加载该技能');

    await panel.confirmToggle();
    expect(apiMocks.setSharedSkillEnabled).toHaveBeenCalledWith('skill-b', true);
  });

  it('删除：danger 确认弹窗含路径与不可恢复警示，confirm 调 deleteSharedSkill', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [DISABLED_B]);
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.requestDelete(DISABLED_B);
    const teleportsHtml = await rerenderTeleports(result);
    expect(teleportsHtml).toContain('删除「Skill B」');
    expect(teleportsHtml).toContain('/home/u/.agents/skills-disabled/skill-b');
    expect(teleportsHtml).toContain('此操作不可恢复');

    await panel.confirmDelete();
    expect(apiMocks.deleteSharedSkill).toHaveBeenCalledWith('skill-b');
  });
});

// ---- managed：不弹窗、不调 API，toast 引导（§2.4 行为裁定） ----

describe('SharedSkillsPanel managed 项', () => {
  it('托管徽章渲染；点开关/删除 → toast 引导且不弹窗、不调 API', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [MANAGED_BOX]);
    const result = await renderPanel();
    expect(result.html).toContain('应用托管');

    const panel = panelExposed(result);
    const { toasts } = useToast();

    panel.handleToggle(MANAGED_BOX, false);
    expect(toasts.some((t) => t.message.includes('amagi-codebox 为应用托管技能'))).toBe(true);
    panel.requestDelete(MANAGED_BOX);
    expect(toasts.filter((t) => t.message.includes('请在 设置 → 命令行访问 中管理'))).toHaveLength(2);

    // 未触发任何写操作 API，确认弹窗也未打开（teleport 无确认文案）
    expect(apiMocks.setSharedSkillEnabled).not.toHaveBeenCalled();
    expect(apiMocks.deleteSharedSkill).not.toHaveBeenCalled();
    const teleportsHtml = await rerenderTeleports(result);
    expect(teleportsHtml).not.toContain('停用「amagi-codebox」');
    expect(teleportsHtml).not.toContain('删除「amagi-codebox」');
  });
});

// ---- 工具栏动作 ----

describe('SharedSkillsPanel 工具栏动作', () => {
  it('刷新调 load（listSharedSkills）；打开根目录调 openSharedSkillsRoot', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [ENABLED_A]);
    const result = await renderPanel();
    const panel = panelExposed(result);

    await panel.handleRefresh();
    expect(apiMocks.listSharedSkills).toHaveBeenCalled();

    await panel.handleOpenRoot();
    expect(apiMocks.openSharedSkillsRoot).toHaveBeenCalled();
  });

  it('详情「打开目录」调 openSharedSkillFolder(name)', async () => {
    const store = useSharedSkillsStore();
    seedStore(store, [ENABLED_A]);
    const result = await renderPanel();
    await panelExposed(result).handleOpenFolder(ENABLED_A);
    expect(apiMocks.openSharedSkillFolder).toHaveBeenCalledWith('skill-a');
  });
});
