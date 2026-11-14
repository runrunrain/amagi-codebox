/**
 * Pi 插件管理面板测试（契约 §6.4/§8，R3）：
 *  - 资源标签 Σ 计数渲染（store 种子带 skills/extensions 数组）、零计数标签隐藏；
 *  - 空态 EmptyStateCard；warnings 诊断横幅真实计数；无数据 loadError → 整页 ErrorState；
 *    有数据 loadError → 诊断横幅且列表仍在；
 *  - 详情：面包屑 / MetaGrid / 切换源卡片（class 断言）/ 只读资源列表（无开关）；
 *  - 行为（expose handler，§8 面板契约）：handleUpdate → store.update(source)（Windows
 *    目标）；select → store.selectPackage；openResourceTab → 资源页分组渲染；
 *    openInstallDialog → 安装对话框经 Teleport 渲染（SSR 弹层断言查 teleport buffer）；
 *  - store（§7 R5a Pi 部分）：四个 Σ computed、loadError 失败写入/成功清空。
 *
 * 范式：node 环境 + renderToString（禁新增 DOM 测试库），store 状态种子驱动分支，
 * 交互断言直调 defineExpose 触发器（先例 ../ui/testUtils.ts）。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { ssrContextKey } from 'vue';
import type { ComponentInternalInstance } from 'vue';

// 在 api 边界打桩（真实类型保留；面板/WSL 区/store 均只经此层访问后端）。
const api = vi.hoisted(() => ({
  refreshPiPackages: vi.fn(),
  getPiPackageDetails: vi.fn(),
  installPiPackage: vi.fn(),
  updatePiPackage: vi.fn(),
  removePiPackage: vi.fn(),
  installPiPackageTargeted: vi.fn(),
  updatePiPackageTargeted: vi.fn(),
  removePiPackageTargeted: vi.fn(),
  getPiPluginWSLStatus: vi.fn(),
  checkPiPluginWSLStatus: vi.fn(),
  getWSLPluginTargetSupport: vi.fn(),
}));

vi.mock('../../../../api/piPlugin', () => ({ ...api }));

// 面板直接 import 生成物 SwitchPackageSource（wailsjs 禁改，测试打桩）。
vi.mock('../../../../wailsjs/go/piplugin/Service', () => ({
  SwitchPackageSource: vi.fn(),
}));

import PiPluginPanel from '../../../../components/extensions/PiPluginPanel.vue';
import { usePiPluginStore } from '../../../../stores/piPlugin';
import type { PiPackage, PiPackageDetail } from '../../../../api/piPlugin';
import { instanceOf, renderInstanceAgain, renderWithCapture } from '../ui/testUtils';

function pkg(overrides: Partial<PiPackage> = {}): PiPackage {
  return {
    id: 'pkg-a',
    source: 'npm:@scope/demo-pkg',
    sourceType: 'npm',
    name: 'demo-pkg',
    scope: 'user',
    enabled: true,
    ...overrides,
  };
}

function detail(overrides: Partial<PiPackageDetail> = {}): PiPackageDetail {
  return {
    ...pkg(),
    resources: [],
    manifestDeclared: true,
    ...overrides,
  };
}

interface SeedOptions {
  installed?: PiPackage[];
  warnings?: string[];
  loadError?: string;
  activeSource?: string | null;
  details?: Record<string, PiPackageDetail>;
}

function seedStore(seed: SeedOptions = {}) {
  const store = usePiPluginStore();
  store.installed = seed.installed ?? [];
  store.warnings = seed.warnings ?? [];
  store.loadError = seed.loadError ?? '';
  store.activeSource = seed.activeSource ?? null;
  store.details = seed.details ?? {};
  return store;
}

// ssrRenderTeleport 的 push 收集项（server-renderer Buffer 结构，同 testUtils）
type PushItem = string | Promise<unknown> | PushItem[];

async function unrollPush(item: PushItem): Promise<string> {
  if (typeof item === 'string') return item;
  if (Array.isArray(item)) {
    let out = '';
    for (const child of item) out += await unrollPush(child);
    return out;
  }
  // await 的结果经 Awaited 递归展开会塌缩为 unknown，对结果断言回 PushItem
  const resolved = await item;
  return unrollPush(resolved as PushItem);
}

/**
 * 同实例状态变更后重渲并收集本趟新增的 body teleport 内容。
 * 机制：ssrRenderTeleport 将弹层内容写入 appContext.provides[ssrContextKey]
 * 的 __teleportBuffers（server-renderer.esm-bundler.js:185-200），首趟
 * resolveTeleports 只消费不清空 → 记录趟前长度、只展开新增段。
 */
async function rerenderWithTeleports(
  panel: ComponentInternalInstance,
): Promise<{ html: string; newTeleports: string }> {
  const ssrCtx = panel.appContext.provides[ssrContextKey] as
    | { __teleportBuffers?: Record<string, PushItem[]> }
    | undefined;
  const before = ssrCtx?.__teleportBuffers?.body?.length ?? 0;
  const html = await renderInstanceAgain(panel);
  const appended = (ssrCtx?.__teleportBuffers?.body ?? []).slice(before);
  let newTeleports = '';
  for (const item of appended) newTeleports += await unrollPush(item);
  return { html, newTeleports };
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.resetAllMocks();
  api.refreshPiPackages.mockResolvedValue({ installed: [] });
});

describe('piPlugin store（契约 §7：Σ 计数与 loadError）', () => {
  it('四个 Σ computed 按列表级资源数组求和', () => {
    const store = seedStore({
      installed: [
        pkg({ skills: ['s1', 's2'], extensions: ['e1'], themes: ['t1'] }),
        pkg({ skills: ['s3'], prompts: ['p1', 'p2'] }),
      ],
    });
    expect(store.skillCount).toBe(3);
    expect(store.extensionCount).toBe(1);
    expect(store.promptCount).toBe(2);
    expect(store.themeCount).toBe(1);
  });

  it('列表级数组未填充时 Σ=0（标签隐藏口径）', () => {
    const store = seedStore({ installed: [pkg(), pkg()] });
    expect(store.skillCount).toBe(0);
    expect(store.extensionCount).toBe(0);
    expect(store.promptCount).toBe(0);
    expect(store.themeCount).toBe(0);
  });

  it('refresh 失败写入 loadError 并维持 rethrow；成功清空', async () => {
    const store = seedStore();
    api.refreshPiPackages.mockRejectedValueOnce(new Error('pi CLI 不可用'));
    await expect(store.refresh(true)).rejects.toThrow('pi CLI 不可用');
    expect(store.loadError).toBe('pi CLI 不可用');
    api.refreshPiPackages.mockResolvedValueOnce({ installed: [pkg()] });
    await store.refresh(true);
    expect(store.loadError).toBe('');
    expect(store.installed.length).toBe(1);
  });
});

describe('PiPluginPanel 渲染（store 种子驱动分支）', () => {
  it('资源标签 Σ 计数真实渲染、零计数标签隐藏（契约 §3.1/§8）', async () => {
    seedStore({
      installed: [
        pkg({ name: 'alpha', skills: ['a-s1', 'a-s2', 'a-s3'], extensions: ['a-e1'] }),
        pkg({ name: 'beta' }),
      ],
    });
    const { html } = await renderWithCapture(PiPluginPanel);
    // SSR 输出在 class 后插 data-v-* 作用域属性，配对断言用正则
    expect(html).toMatch(/class="ct-label"[^>]*>包</);
    expect(html).toMatch(/class="ct-label"[^>]*>技能</);
    expect(html).toMatch(/class="ct-label"[^>]*>扩展</);
    expect(html).toMatch(/class="ct-count"[^>]*>2</);
    expect(html).toMatch(/class="ct-count"[^>]*>3</);
    expect(html).toMatch(/class="ct-count"[^>]*>1</);
    // Σ=0 的标签不渲染（宁可少显示不显示假零，风险 5）；
    // 用 ct-label 配对断言，避免命中模板注释里的字样
    expect(html).not.toMatch(/class="ct-label"[^>]*>提示词</);
    expect(html).not.toMatch(/class="ct-label"[^>]*>主题</);
    // 工具栏标题与计数徽章（已安装 2）
    expect(html).toContain('已安装');
    expect(html).toMatch(/class="lt-count"[^>]*>2</);
  });

  it('包列表为行式条目、无开关（契约 §5.1 Pi：不显示开关）', async () => {
    seedStore({ installed: [pkg({ name: 'demo-pkg', version: '1.0.0', pinned: true })] });
    const { html } = await renderWithCapture(PiPluginPanel);
    expect(html).toContain('plugin-row');
    expect(html).toContain('demo-pkg');
    expect(html).toContain('v1.0.0');
    expect(html).toContain('pinned');
    expect(html).toContain('npm');
    expect(html).not.toContain('role="switch"');
  });

  it('空列表渲染 EmptyStateCard + 安装包 primary（契约 §6.4 空态）', async () => {
    seedStore();
    const { html } = await renderWithCapture(PiPluginPanel);
    expect(html).toContain('empty-state-card');
    expect(html).toContain('暂无 Pi 全局包');
    expect(html).toContain('输入 npm 包名、git 地址或本地路径');
    expect(html).toContain('安装包');
    // WSL 区整体保留（面板底部）
    expect(html).toContain('WSL 侧插件');
  });

  it('warnings 非空时诊断横幅出现且计数为真实长度', async () => {
    seedStore({ installed: [pkg()], warnings: ['JSONC 卸载需手工处理', 'w2'] });
    const { html } = await renderWithCapture(PiPluginPanel);
    expect(html).toContain('加载诊断：0 个错误，2 个警告');
  });

  it('首次加载失败（无数据）→ 整页 ErrorState 替换内容区', async () => {
    seedStore({ loadError: 'pi CLI 不可用' });
    const { html } = await renderWithCapture(PiPluginPanel);
    expect(html).toContain('pi CLI 不可用');
    expect(html).toContain('重试');
    expect(html).not.toContain('count-tabs');
  });

  it('有数据后的刷新失败 → loadError 进诊断横幅，列表仍渲染', async () => {
    seedStore({ installed: [pkg()], loadError: '刷新失败' });
    const { html } = await renderWithCapture(PiPluginPanel);
    expect(html).toContain('加载诊断：1 个错误，0 个警告');
    expect(html).toContain('package-rows');
  });

  it('选中包渲染详情：面包屑/MetaGrid/切换源卡片/只读资源列表', async () => {
    const d = detail({
      name: 'demo-pkg',
      description: '演示包',
      version: '1.2.0',
      author: 'runrunrain',
      installPath: '/home/.pi/agent/npm/demo-pkg',
      resources: [
        { name: 'res-skill', filePath: '/x/skill.md', type: 'skill' },
        { name: 'res-theme', filePath: '/y/theme.json', type: 'theme' },
      ],
    });
    seedStore({ installed: [d], activeSource: d.source, details: { [d.source]: d } });
    const { html } = await renderWithCapture(PiPluginPanel);
    // 面包屑：插件（可点）› 名称
    expect(html).toContain('dh-crumb-link');
    expect(html).toContain('插件');
    expect(html).toContain('demo-pkg');
    // MetaGrid 标签-值（契约 §5.2 Pi 列）
    expect(html).toContain('包源');
    expect(html).toContain('npm:@scope/demo-pkg');
    expect(html).toContain('来源类型');
    expect(html).toContain('安装路径');
    expect(html).toContain('开发者');
    expect(html).toContain('Manifest');
    expect(html).toContain('已声明');
    // 只读资源列表：typeBadge 中文（契约 §5.3 Pi）
    expect(html).toContain('res-skill');
    expect(html).toContain('res-theme');
    // 切换源卡片存在（class 断言，契约 §8）
    expect(html).toContain('switch-source-card');
    expect(html).toContain('切换源类型');
    // 只读：无开关
    expect(html).not.toContain('role="switch"');
  });

  it('本地源包的更新按钮禁用且文案为本地直载（契约 §5.4）', async () => {
    const d = detail({ name: 'local-pkg', source: '/abs/path/pkg', sourceType: 'local' });
    seedStore({ installed: [d], activeSource: d.source, details: { [d.source]: d } });
    const { html } = await renderWithCapture(PiPluginPanel);
    expect(html).toContain('本地直载');
    expect(html).toContain('disabled');
  });

  it('资源标签页按所属包分组渲染（expose openResourceTab 后重渲）', async () => {
    seedStore({
      installed: [
        pkg({ name: 'alpha', skills: ['alpha-skill-1', 'alpha-skill-2'] }),
        pkg({ name: 'beta', skills: ['beta-skill-1'] }),
      ],
    });
    const { html, instances } = await renderWithCapture(PiPluginPanel);
    // 默认包列表不渲染资源名
    expect(html).not.toContain('alpha-skill-1');
    const panel = instanceOf(instances, PiPluginPanel);
    panel.exposed?.openResourceTab('skills');
    const { html: rehtml } = await rerenderWithTeleports(panel);
    expect(rehtml).toContain('group-header');
    expect(rehtml).toContain('alpha');
    expect(rehtml).toContain('alpha-skill-1');
    expect(rehtml).toContain('alpha-skill-2');
    expect(rehtml).toContain('beta-skill-1');
    expect(rehtml).toMatch(/class="gh-count"[^>]*>2</);
    // 资源页工具栏计数 = 过滤后资源总数（3）
    expect(rehtml).toMatch(/class="lt-count"[^>]*>3</);
  });
});

describe('PiPluginPanel 交互（expose handler，§8 面板契约）', () => {
  it('expose select(source) → store.selectPackage 拉取详情', async () => {
    const d = detail({ source: 'npm:demo-pkg' });
    seedStore({ installed: [d] });
    api.getPiPackageDetails.mockResolvedValue(d);
    const { instances } = await renderWithCapture(PiPluginPanel);
    await instanceOf(instances, PiPluginPanel).exposed?.select('npm:demo-pkg');
    expect(api.getPiPackageDetails).toHaveBeenCalledWith('npm:demo-pkg');
  });

  it('expose handleUpdate → store.update(source)（Windows 目标走既有 store 路径）', async () => {
    const d = detail({ source: 'npm:demo-pkg', name: 'demo-pkg' });
    seedStore({ installed: [d], activeSource: d.source, details: { [d.source]: d } });
    api.updatePiPackage.mockResolvedValue({ success: true, output: 'updated' });
    api.refreshPiPackages.mockResolvedValue({ installed: [d] });
    api.getPiPackageDetails.mockResolvedValue(d);
    const { instances } = await renderWithCapture(PiPluginPanel);
    await instanceOf(instances, PiPluginPanel).exposed?.handleUpdate();
    expect(api.updatePiPackage).toHaveBeenCalledWith('npm:demo-pkg');
  });

  it('expose openInstallDialog → 安装对话框打开且经 Teleport 渲染', async () => {
    seedStore({ installed: [pkg()] });
    const { teleports, instances } = await renderWithCapture(PiPluginPanel);
    // 关闭态：弹层无对话框内容（契约 §8：Teleport 断言查 teleport 侧）
    expect(teleports.body ?? '').not.toContain('安装 Pi 包');
    const panel = instanceOf(instances, PiPluginPanel);
    panel.exposed?.openInstallDialog();
    // instance.exposed 为原始暴露对象：ref 未经代理解包，兼容两种形态读取
    const dialogState = panel.exposed?.showInstallDialog;
    const isOpen = typeof dialogState === 'boolean'
      ? dialogState
      : (dialogState as { value?: unknown } | undefined)?.value === true;
    expect(isOpen).toBe(true);
    const { newTeleports } = await rerenderWithTeleports(panel);
    expect(newTeleports).toContain('安装 Pi 包');
    expect(newTeleports).toContain('包源 Package source');
  });
});
