/**
 * OmpPluginPanel 面板单测（契约 §6.5/§8）。
 * 范式：node 环境 renderToString + store 状态种子驱动门控分支（SSR 不执行 onMounted，
 * 门控已改为 store 派生故主内容分支可达）；交互断言走面板/共享组件 defineExpose 的
 * 触发器；Dialog 弹层内容查 ctx.teleports（renderToString 主输出只含 teleport 锚点）。
 *
 * teleports 重渲机制：ssrRenderTeleport 把内容写入 render 上下文的 __teleportBuffers
 * （server-renderer dist 实证）；renderInstanceAgain 复用同一 app 的 ssrContext，
 * 新弹层内容会追加到同一 buffer，用本地 unroll 展开断言。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { createSSRApp, defineComponent, getCurrentInstance, h } from 'vue';
import type { ComponentInternalInstance, Ref } from 'vue';
import { renderToString } from 'vue/server-renderer';

import OmpPluginPanel from '../../../components/extensions/OmpPluginPanel.vue';
import CountTabs from '../../../components/extensions/ui/CountTabs.vue';
import DiagnosticBanner from '../../../components/extensions/ui/DiagnosticBanner.vue';
import { renderInstanceAgain } from './ui/testUtils';
import { useOmpPluginStore } from '../../../stores/ompPlugin';
import type { OmpPlugin } from '../../../api/ompPlugin';

// ---- 测试基建：渲染 + 实例捕获 + 保留 ssr 上下文（teleport buffer 断言用） ----

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
  const Wrapper = defineComponent({ setup: () => () => h(OmpPluginPanel) });
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

function instanceOf(result: PanelRenderResult, component: object): ComponentInternalInstance {
  const found = result.instances.find((instance) => instance.type === component);
  if (!found) throw new Error('目标组件未被渲染');
  return found;
}

/** instance.exposed 为原始对象（runtime-core expose 直写，refs 不解包） */
interface PanelExposed {
  view: Ref<'list' | 'detail'>;
  searchQuery: Ref<string>;
  showInstallDialog: Ref<boolean>;
  installSpec: Ref<string>;
  handleRefresh: () => Promise<void>;
  openInstallDialog: () => void;
  handleInstall: () => Promise<void>;
  handleRowToggle: (plugin: OmpPlugin, value: boolean) => Promise<void>;
  openDetail: (plugin: OmpPlugin) => void;
  closeDetail: () => void;
  handleEnable: (plugin: OmpPlugin) => Promise<void>;
  handleUpgrade: (plugin: OmpPlugin) => Promise<void>;
  openUninstallDialog: (plugin: OmpPlugin) => void;
  handleUninstall: () => Promise<void>;
}

function panelExposed(result: PanelRenderResult): PanelExposed {
  const exposed = instanceOf(result, OmpPluginPanel).exposed as PanelExposed | null;
  if (!exposed) throw new Error('OmpPluginPanel 未 defineExpose');
  return exposed;
}

// ---- 数据种子 ----

function plugin(overrides: Partial<OmpPlugin> = {}): OmpPlugin {
  return {
    id: 'omp-npm',
    name: 'omp.nvim',
    kind: 'npm',
    enabled: true,
    ...overrides,
  };
}

const NPM_PLUGIN = plugin({
  id: 'omp-npm',
  name: 'omp.nvim',
  version: '1.2.0',
  description: '官方 omp 增强插件',
  scope: 'user',
  installPath: '/home/u/.omp/plugins/omp.nvim',
  enabledFeatures: ['feature-a', 'feature-b'],
});

const MARKET_PLUGIN = plugin({
  id: 'omp-market',
  name: 'market-one',
  kind: 'marketplace',
  enabled: false,
  description: '来自 marketplace 的插件',
});

function seedStore(
  store: ReturnType<typeof useOmpPluginStore>,
  installed: OmpPlugin[],
  extra: { loadError?: string; warnings?: string[]; loading?: boolean } = {},
): void {
  store.installed = installed;
  store.warnings = extra.warnings ?? [];
  store.loadError = extra.loadError ?? '';
  store.loading = extra.loading ?? false;
}

const cmdResult = (output = '') => ({ success: true, output });

beforeEach(() => {
  setActivePinia(createPinia());
});

// ---- 渲染：门控双形态（契约 §6.5 / §8） ----

describe('OmpPluginPanel 首屏门控（store 派生）', () => {
  it('loadError 非空且 installed=[] → 整页 ErrorState 替换内容区', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [], { loadError: '获取 OMP 插件列表失败：omp 未安装' });
    const { html } = await renderPanel();
    expect(html).toContain('error-state');
    expect(html).toContain('获取 OMP 插件列表失败：omp 未安装');
    expect(html).toContain('重试');
    expect(html).not.toContain('list-toolbar');
    expect(html).not.toContain('plugin-row');
  });

  it('installed 非空 + loadError 非空 → 诊断横幅承载错误而非整页 ErrorState', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [NPM_PLUGIN], { loadError: '刷新失败' });
    const { html } = await renderPanel();
    expect(html).toContain('diagnostic-banner');
    expect(html).toContain('加载诊断：1 个错误，0 个警告');
    expect(html).not.toContain('error-state');
    expect(html).toContain('list-toolbar'); // 有数据时主内容仍在
  });

  it('诊断横幅可展开逐条列出错误与警告', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [NPM_PLUGIN], {
      loadError: '刷新失败',
      warnings: ['omp CLI 未安装，列表可能不完整'],
    });
    const result = await renderPanel();
    expect(result.html).toContain('加载诊断：1 个错误，1 个警告');
    instanceOf(result, DiagnosticBanner).exposed?.toggleExpand();
    const expanded = await renderInstanceAgain(instanceOf(result, DiagnosticBanner));
    expect(expanded).toContain('刷新失败');
    expect(expanded).toContain('omp CLI 未安装，列表可能不完整');
  });

  it('warnings 恒走诊断横幅（无错误时仅警告形态）', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [NPM_PLUGIN], { warnings: ['omp CLI 未安装'] });
    const { html } = await renderPanel();
    expect(html).toContain('diagnostic-banner');
    expect(html).toContain('加载诊断：0 个错误，1 个警告');
  });

  it('loading 且无数据无错误 → LoadingState 门控', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [], { loading: true });
    const { html } = await renderPanel();
    expect(html).toContain('加载 OMP 插件中');
    expect(html).not.toContain('plugin-row');
  });
});

// ---- 渲染：列表态 ----

describe('OmpPluginPanel 列表态', () => {
  it('CountTabs 插件计数与工具栏已安装计数来自 store 数据', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [NPM_PLUGIN, MARKET_PLUGIN]);
    const { html } = await renderPanel();
    expect(html).toContain('插件');
    expect(html).toContain('>2</span>'); // CountTabs 计数徽章
    expect(html).toContain('list-toolbar');
    expect(html).toContain('已安装');
    expect(html).toMatch(/lt-count[^>]*>2</);
  });

  it('行式列表渲染：kind 徽章（npm/marketplace）、版本徽章、描述、行尾开关', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [NPM_PLUGIN, MARKET_PLUGIN]);
    const { html } = await renderPanel();
    expect(html).toContain('plugin-row');
    expect(html).toContain('omp.nvim');
    expect(html).toContain('market-one');
    expect(html).toContain('npm');
    expect(html).toContain('marketplace');
    expect(html).toContain('v1.2.0');
    expect(html).toContain('官方 omp 增强插件');
    expect(html).toContain('plugin-icon-sm');
    // 唯一新增开关位：行尾启用开关（契约 §6.5）
    expect(html).toContain('role="switch"');
    // 无版本数据的插件不渲染版本徽章（缺数据不显示，不造假值）
    expect(html.match(/v1\.2\.0/g)).toHaveLength(1);
  });

  it('空列表渲染 EmptyStateCard 与安装入口', async () => {
    const store = useOmpPluginStore();
    seedStore(store, []);
    const { html } = await renderPanel();
    expect(html).toContain('empty-state-card');
    expect(html).toContain('暂无 OMP 插件');
    expect(html).toContain('点击『安装插件』，输入 npm 包名、GitHub 源、marketplace 引用或本地路径');
    expect(html).toContain('安装插件');
    expect(html).not.toContain('plugin-row');
  });

  it('搜索为面板本地过滤：经 CountTabs expose search() 更新后仅保留命中行', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [NPM_PLUGIN, MARKET_PLUGIN]);
    const result = await renderPanel();
    expect(result.html).toContain('market-one');
    instanceOf(result, CountTabs).exposed?.search('omp.nvim');
    const filtered = await renderInstanceAgain(instanceOf(result, OmpPluginPanel));
    expect(filtered).toContain('omp.nvim');
    expect(filtered).not.toContain('market-one');
    expect(filtered).toMatch(/lt-count[^>]*>1</); // 计数随过滤结果收敛
  });
});

// ---- 渲染：详情态 ----

describe('OmpPluginPanel 详情态', () => {
  it('点行进入详情：面包屑「OMP › 名称」+ 返回入口、MetaGrid 字段、启用特性拼接', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [NPM_PLUGIN, MARKET_PLUGIN]);
    const result = await renderPanel();
    panelExposed(result).openDetail(NPM_PLUGIN);
    const html = await renderInstanceAgain(instanceOf(result, OmpPluginPanel));
    expect(html).toContain('dh-breadcrumb');
    expect(html).toContain('OMP</button>');
    expect(html).toContain('← 返回列表');
    expect(html).toContain('omp.nvim');
    expect(html).toContain('meta-grid');
    expect(html).toContain('引用 ID');
    expect(html).toContain('omp-npm');
    expect(html).toContain('类型');
    expect(html).toContain('版本');
    expect(html).toContain('作用域');
    expect(html).toContain('user');
    expect(html).toContain('安装路径');
    expect(html).toContain('启用特性');
    expect(html).toContain('feature-a、feature-b');
    // enabled=true → 主操作为「升级」
    expect(html).toContain('升级');
    expect(html).toContain('卸载');
    expect(html).not.toContain('plugin-row');
  });

  it('enabled=false 插件主操作为「启用」且无升级入口', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [MARKET_PLUGIN]);
    const result = await renderPanel();
    panelExposed(result).openDetail(MARKET_PLUGIN);
    const html = await renderInstanceAgain(instanceOf(result, OmpPluginPanel));
    expect(html).toContain('启用');
    expect(html).not.toContain('升级');
  });

  it('DetailHeader goBack 事件回到列表态', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [NPM_PLUGIN]);
    const result = await renderPanel();
    const panel = panelExposed(result);
    panel.openDetail(NPM_PLUGIN);
    panel.closeDetail();
    const html = await renderInstanceAgain(instanceOf(result, OmpPluginPanel));
    expect(html).toContain('plugin-row');
    expect(html).not.toContain('meta-grid');
    expect(html).not.toContain('← 返回列表');
  });
});

// ---- 交互：expose handler → store 动作（契约 §8 行为断言） ----

describe('OmpPluginPanel 交互', () => {
  it('行开关 handleRowToggle 以目标态调用 setEnabled(id, 目标态)', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [NPM_PLUGIN, MARKET_PLUGIN]);
    const result = await renderPanel();
    const setEnabled = vi.spyOn(store, 'setEnabled').mockResolvedValue(cmdResult());
    const panel = panelExposed(result);
    await panel.handleRowToggle(MARKET_PLUGIN, true); // 禁用态插件打开开关
    expect(setEnabled).toHaveBeenCalledWith('omp-market', true);
    await panel.handleRowToggle(NPM_PLUGIN, false); // 启用态插件关闭开关
    expect(setEnabled).toHaveBeenCalledWith('omp-npm', false);
  });

  it('详情操作：启用走 setEnabled(id, true)，升级走 upgrade(id)', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [MARKET_PLUGIN, NPM_PLUGIN]);
    const result = await renderPanel();
    const setEnabled = vi.spyOn(store, 'setEnabled').mockResolvedValue(cmdResult());
    const upgrade = vi.spyOn(store, 'upgrade').mockResolvedValue(cmdResult());
    const panel = panelExposed(result);
    await panel.handleEnable(MARKET_PLUGIN);
    expect(setEnabled).toHaveBeenCalledWith('omp-market', true);
    await panel.handleUpgrade(NPM_PLUGIN);
    expect(upgrade).toHaveBeenCalledWith('omp-npm');
  });

  it('卸载：openUninstallDialog 后经 ConfirmDialog confirm 调 uninstall(id)', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [NPM_PLUGIN]);
    const result = await renderPanel();
    const uninstall = vi.spyOn(store, 'uninstall').mockResolvedValue(cmdResult());
    const panel = panelExposed(result);
    panel.openUninstallDialog(NPM_PLUGIN);
    // ConfirmDialog 弹层内容进入 teleports.body（卸载确认文案出现）
    const bodyBuffer = result.context.__teleportBuffers?.body ?? [];
    const before = bodyBuffer.length;
    await renderInstanceAgain(instanceOf(result, OmpPluginPanel));
    const teleportsHtml = await unroll(bodyBuffer.slice(before));
    expect(teleportsHtml).toContain('卸载 OMP 插件');
    expect(teleportsHtml).toContain('此操作不可恢复');
    await panel.handleUninstall(); // ConfirmDialog @confirm 的处理路径
    expect(uninstall).toHaveBeenCalledWith('omp-npm');
  });

  it('刷新以 force=true 调 store.refresh', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [NPM_PLUGIN]);
    const result = await renderPanel();
    const refresh = vi.spyOn(store, 'refresh').mockResolvedValue(undefined);
    await panelExposed(result).handleRefresh();
    expect(refresh).toHaveBeenCalledWith(true);
  });

  it('安装对话框保留：openInstallDialog 后内容进入 teleports.body，提交调 install(spec)', async () => {
    const store = useOmpPluginStore();
    seedStore(store, [NPM_PLUGIN]);
    const result = await renderPanel();
    // 初始关闭：Dialog 内容不在 teleport 弹层
    expect(result.teleports.body ?? '').not.toContain('安装 OMP 插件');
    const install = vi.spyOn(store, 'install').mockResolvedValue(cmdResult('done'));
    const panel = panelExposed(result);
    panel.openInstallDialog();
    const bodyBuffer = result.context.__teleportBuffers?.body ?? [];
    const before = bodyBuffer.length;
    await renderInstanceAgain(instanceOf(result, OmpPluginPanel));
    const teleportsHtml = await unroll(bodyBuffer.slice(before));
    expect(teleportsHtml).toContain('安装 OMP 插件');
    expect(teleportsHtml).toContain('omp-plugin-spec');
    // 提交：spec 透传 store.install，成功后关闭对话框
    panel.installSpec.value = 'omp.nvim';
    await panel.handleInstall();
    expect(install).toHaveBeenCalledWith('omp.nvim');
    expect(panel.showInstallDialog.value).toBe(false);
  });
});
