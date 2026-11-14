/**
 * OpenCodePluginPanel 重写测试（契约 §6.3/§8，R2 交付）。
 *
 * 范式：node 环境 + renderToString（ui/testUtils 基建），store 状态种子驱动
 * 分支（门控为 store 派生），交互断言直调面板/共享组件 defineExpose 的触发器。
 * Dialog 断言查 ctx.teleports（主输出只有 teleport 锚点注释）：
 *  - 首渲 teleports 由 renderToString 的 resolveTeleports 产出；
 *  - expose 触发器改状态后经 renderInstanceAgain 重渲，Teleport 内容写入
 *    ssrContext.__teleportBuffers（resolveTeleports 已随首渲结束，不再消费），
 *    故由 teleportsAfterRerender 手动 unroll（与 resolveTeleports 同构）。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import type { ComponentInternalInstance } from 'vue';

// 在 api 边界打桩（真实类型保留；store 动作真实执行）。
const api = vi.hoisted(() => ({
  refreshOpenCodePlugins: vi.fn(),
  getOpenCodePluginDetails: vi.fn(),
  installOpenCodePlugin: vi.fn(),
  updateOpenCodePlugin: vi.fn(),
  uninstallOpenCodePlugin: vi.fn(),
}));

vi.mock('../../../api/opencodePlugin', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../api/opencodePlugin')>();
  return { ...actual, ...api };
});

import type { OpenCodePlugin, OpenCodePluginDetail } from '../../../api/opencodePlugin';
import OpenCodePluginPanel from '../../../components/extensions/OpenCodePluginPanel.vue';
import CountTabs from '../../../components/extensions/ui/CountTabs.vue';
import { useOpenCodePluginStore } from '../../../stores/opencodePlugin';
import { instanceOf, renderInstanceAgain, renderWithCapture } from './ui/testUtils';

function plugin(overrides: Partial<OpenCodePlugin> = {}): OpenCodePlugin {
  return {
    id: 'p1',
    spec: 'npm:demo-plugin',
    name: 'demo-plugin',
    version: '1.2.3',
    description: '演示插件描述',
    author: 'alice',
    repository: 'https://github.com/alice/demo-plugin',
    source: 'npm',
    scope: 'global',
    enabled: true,
    installPath: 'C:\\cache\\demo-plugin',
    lastUpdated: '2026-11-01T08:00:00Z',
    targets: ['tui'],
    ...overrides,
  };
}

function detail(overrides: Partial<OpenCodePluginDetail> = {}): OpenCodePluginDetail {
  return {
    ...plugin(),
    skills: [{ name: 'demo-skill', filePath: '/skills/demo' }],
    agents: [{ name: 'demo-agent', filePath: '/agents/demo' }],
    commands: [],
    hooks: [{ name: 'demo-hook', filePath: '/hooks/demo' }],
    hasMcp: true,
    ...overrides,
  };
}

function seedStore(seed: {
  installed?: OpenCodePlugin[];
  warnings?: string[];
  loading?: boolean;
  loadError?: string;
  activeSpec?: string | null;
  details?: Record<string, OpenCodePluginDetail>;
} = {}) {
  const store = useOpenCodePluginStore();
  store.installed = seed.installed ?? [];
  store.warnings = seed.warnings ?? [];
  store.loading = seed.loading ?? false;
  store.loadError = seed.loadError ?? '';
  store.details = seed.details ?? {};
  if (seed.activeSpec !== undefined) {
    store.activeSpec = seed.activeSpec;
  }
  return store;
}

function resetMocks() {
  api.refreshOpenCodePlugins.mockReset().mockResolvedValue({ installed: [], warnings: [] });
  api.getOpenCodePluginDetails.mockReset().mockResolvedValue(detail());
  api.installOpenCodePlugin.mockReset().mockResolvedValue({ success: true, output: '' });
  api.updateOpenCodePlugin.mockReset().mockResolvedValue({ success: true, output: 'updated' });
  api.uninstallOpenCodePlugin.mockReset().mockResolvedValue({ success: true, output: '' });
}

// —— Teleport buffer 手动解析（重渲后 ssrContext.__teleportBuffers 无人消费）——
type TeleportPush = string | Promise<unknown> | TeleportPush[];

async function unrollBuffer(item: TeleportPush): Promise<string> {
  if (typeof item === 'string') {
    return item;
  }
  if (Array.isArray(item)) {
    let out = '';
    for (const nested of item) {
      out += await unrollBuffer(nested);
    }
    return out;
  }
  // await 的结果经 Awaited 递归展开会塌缩为 unknown，对结果断言回 TeleportPush
  return unrollBuffer(await item as TeleportPush);
}

/** 重渲后从 app 级 ssrContext（appContext.provides 上的 symbol key）解析 teleport buffers */
async function teleportsAfterRerender(
  instance: ComponentInternalInstance,
): Promise<Record<string, string>> {
  const provides = instance.appContext.provides as Record<symbol, unknown>;
  let buffers: Record<string, TeleportPush[]> | undefined;
  for (const symbol of Object.getOwnPropertySymbols(provides)) {
    const value = provides[symbol];
    if (value && typeof value === 'object' && '__teleportBuffers' in value) {
      buffers = (value as { __teleportBuffers?: Record<string, TeleportPush[]> }).__teleportBuffers;
      break;
    }
  }
  const result: Record<string, string> = {};
  if (buffers) {
    for (const target of Object.keys(buffers)) {
      result[target] = await unrollBuffer(buffers[target]);
    }
  }
  return result;
}

beforeEach(() => {
  setActivePinia(createPinia());
  resetMocks();
});

describe('store loadError（契约 §7 R5a opencode 部分）', () => {
  it('refresh 失败写入 loadError 并照旧 rethrow', async () => {
    api.refreshOpenCodePlugins.mockRejectedValueOnce(new Error('opencode.json 读取失败'));
    const store = useOpenCodePluginStore();
    await expect(store.refresh(true)).rejects.toThrow('opencode.json 读取失败');
    expect(store.loadError).toBe('opencode.json 读取失败');
  });

  it('refresh 成功清空 loadError', async () => {
    api.refreshOpenCodePlugins.mockRejectedValueOnce(new Error('临时失败'));
    const store = useOpenCodePluginStore();
    await store.refresh(true).catch(() => {});
    expect(store.loadError).toBe('临时失败');

    api.refreshOpenCodePlugins.mockResolvedValueOnce({ installed: [plugin()], warnings: [] });
    await store.refresh(true);
    expect(store.loadError).toBe('');
    expect(store.installed).toHaveLength(1);
  });
});

describe('首屏门控（store 派生，契约 §6.3）', () => {
  it('loading 且无数据无错误 → LoadingState，主内容分支不可达', async () => {
    seedStore({ loading: true });
    const { html } = await renderWithCapture(OpenCodePluginPanel);
    expect(html).toContain('加载 OpenCode 插件中');
    expect(html).not.toContain('搜索插件名称或描述');
    expect(html).not.toContain('已安装');
  });

  it('loadError 且列表为空 → 整页 ErrorState（含重试）', async () => {
    seedStore({ loadError: 'opencode.json 读取失败' });
    const { html } = await renderWithCapture(OpenCodePluginPanel);
    expect(html).toContain('opencode.json 读取失败');
    expect(html).toContain('重试');
    expect(html).not.toContain('安装插件');
  });

  it('已有数据后刷新失败 → 诊断横幅计数错误、列表仍在（双形态）', async () => {
    seedStore({ installed: [plugin()], loadError: 'CLI 超时' });
    const { html } = await renderWithCapture(OpenCodePluginPanel);
    expect(html).toContain('加载诊断：1 个错误，0 个警告');
    expect(html).toContain('demo-plugin');
  });
});

describe('列表态渲染', () => {
  it('空列表 → EmptyStateCard 契约文案与 primary 按钮', async () => {
    seedStore();
    const { html } = await renderWithCapture(OpenCodePluginPanel);
    expect(html).toContain('暂无 OpenCode 全局插件');
    expect(html).toContain('点击『安装插件』，输入 npm、GitHub 或本地模块地址');
    expect(html).toContain('安装插件');
    expect(html).toContain('esc-primary');
  });

  it('行渲染：名称/描述/来源/版本/targets 徽章，计数真实，行内无开关', async () => {
    seedStore({
      installed: [
        plugin(),
        plugin({
          id: 'p2',
          spec: 'github:owner/repo',
          name: 'repo-plugin',
          version: '',
          description: '',
          source: 'github',
          targets: ['tui', 'api'],
        }),
      ],
    });
    const { html } = await renderWithCapture(OpenCodePluginPanel);
    expect(html).toContain('demo-plugin');
    expect(html).toContain('演示插件描述');
    expect(html).toContain('GitHub');
    expect(html).toContain('v1.2.3');
    expect(html).toContain('tui');
    // CountTabs 标签计数与工具栏计数徽章均为真实计数（2 个插件）
    expect(html).toContain('插件');
    expect(html).toContain('>2</span>');
    expect(html).toContain('已安装');
    // 开关诚实：OpenCode 无启停 API，行内不渲染 Switch
    expect(html).not.toContain('class="switch');
  });

  it('warnings 非空 → 诊断横幅出现且计数正确', async () => {
    seedStore({ warnings: ['检测到 JSONC 格式配置，卸载需手工处理'] });
    const { html } = await renderWithCapture(OpenCodePluginPanel);
    expect(html).toContain('加载诊断：0 个错误，1 个警告');
  });

  it('搜索无命中 → 列表切换为空态（本地过滤，不发后端请求）', async () => {
    seedStore({ installed: [plugin()] });
    const { instances } = await renderWithCapture(OpenCodePluginPanel);
    instanceOf(instances, CountTabs).exposed?.search('不存在的关键词');
    const html = await renderInstanceAgain(instanceOf(instances, OpenCodePluginPanel));
    expect(html).toContain('暂无 OpenCode 全局插件');
    expect(html).not.toContain('demo-plugin');
    expect(api.refreshOpenCodePlugins).not.toHaveBeenCalled();
  });
});

describe('详情态（list ⇄ detail，面板本地 ref）', () => {
  it('openDetail 后重渲：面包屑/MetaGrid/资源汇总/只读子项，子项无开关', async () => {
    const base = plugin();
    seedStore({ installed: [base] });
    const { instances } = await renderWithCapture(OpenCodePluginPanel);
    const panel = instanceOf(instances, OpenCodePluginPanel);
    await panel.exposed?.openDetail(base.spec);
    const html = await renderInstanceAgain(panel);

    expect(api.getOpenCodePluginDetails).toHaveBeenCalledWith(base.spec);
    // 面包屑「插件 › 名称」：插件段可点
    expect(html).toContain('dh-crumb-link');
    expect(html).toContain('插件</button>');
    expect(html).toContain('demo-plugin');
    // 主操作恒为「检查更新」（无启停概念）
    expect(html).toContain('检查更新');
    // MetaGrid 字段（§5.2 OpenCode 映射）
    expect(html).toContain('模块 Spec');
    expect(html).toContain('npm:demo-plugin');
    expect(html).toContain('安装路径');
    expect(html).toContain('开发者');
    expect(html).toContain('仓库');
    expect(html).toContain('缓存更新时间');
    // 资源汇总徽章行（真实计数）与只读子项列表
    expect(html).toContain('技能 1');
    expect(html).toContain('代理 1');
    expect(html).toContain('钩子 1');
    expect(html).toContain('含 MCP');
    expect(html).toContain('demo-skill');
    expect(html).toContain('MCP 服务器');
    // 子项只读：SubItemRow 不渲染 Switch
    expect(html).not.toContain('class="switch');
  });

  it('file 源插件：检查更新禁用且文案为「本地直载」', async () => {
    const local = plugin({ spec: 'file:///C:/plugins/local', source: 'file', version: '' });
    seedStore({ installed: [local] });
    api.getOpenCodePluginDetails.mockResolvedValue(
      detail({ spec: local.spec, source: 'file', version: '' }),
    );
    const { instances } = await renderWithCapture(OpenCodePluginPanel);
    const panel = instanceOf(instances, OpenCodePluginPanel);
    await panel.exposed?.openDetail(local.spec);
    const html = await renderInstanceAgain(panel);
    expect(html).toContain('本地直载');
    expect(html).not.toContain('检查更新');
    const buttonSegment = html.slice(html.indexOf('本地直载') - 200, html.indexOf('本地直载'));
    expect(buttonSegment).toContain('disabled');
  });

  it('backToList 返回列表态', async () => {
    const base = plugin();
    seedStore({ installed: [base] });
    const { instances } = await renderWithCapture(OpenCodePluginPanel);
    const panel = instanceOf(instances, OpenCodePluginPanel);
    await panel.exposed?.openDetail(base.spec);
    panel.exposed?.backToList();
    const html = await renderInstanceAgain(panel);
    expect(html).toContain('已安装');
    expect(html).not.toContain('dh-crumb-link');
  });
});

describe('安装对话框（ctx.teleports.body 断言，契约 §8）', () => {
  it('折叠态 teleports 无内容；expose openInstallDialog 后重渲内容出现', async () => {
    seedStore({ installed: [plugin()] });
    const { teleports, instances } = await renderWithCapture(OpenCodePluginPanel);
    expect(teleports.body ?? '').not.toContain('安装 OpenCode 插件');

    const panel = instanceOf(instances, OpenCodePluginPanel);
    panel.exposed?.openInstallDialog();
    await renderInstanceAgain(panel);
    const body = (await teleportsAfterRerender(panel)).body ?? '';
    expect(body).toContain('安装 OpenCode 插件');
    expect(body).toContain('github:owner/repo#v1.2.3 或 package-name@1.2.3');
    expect(body).toContain('取消');
  });
});

describe('行为断言（面板 expose handler → store 动作 → api）', () => {
  it('handleRefresh 强制 reload（refresh(true)）', async () => {
    seedStore({ installed: [plugin()] });
    const { instances } = await renderWithCapture(OpenCodePluginPanel);
    await instanceOf(instances, OpenCodePluginPanel).exposed?.handleRefresh();
    expect(api.refreshOpenCodePlugins).toHaveBeenCalledTimes(1);
  });

  it('handleInstall 以输入 spec 调 install', async () => {
    seedStore();
    const { instances } = await renderWithCapture(OpenCodePluginPanel);
    const panel = instanceOf(instances, OpenCodePluginPanel);
    panel.exposed?.openInstallDialog();
    // exposed 为原始对象（非 proxyRefs），ref 以 .value 读写；此处已触发过
    // openInstallDialog，exposed 必然存在，非空断言后读写 ref 成员
    const exposed = panel.exposed!;
    exposed.installSpec.value = 'npm:new-plugin';
    await exposed.handleInstall();
    expect(api.installOpenCodePlugin).toHaveBeenCalledWith('npm:new-plugin');
    expect(exposed.showInstallDialog.value).toBe(false);
  });

  it('handleUpdate 以当前插件 spec 调 update', async () => {
    const base = plugin();
    seedStore({ installed: [base] });
    const { instances } = await renderWithCapture(OpenCodePluginPanel);
    const panel = instanceOf(instances, OpenCodePluginPanel);
    await panel.exposed?.openDetail(base.spec);
    await panel.exposed?.handleUpdate();
    expect(api.updateOpenCodePlugin).toHaveBeenCalledWith(base.spec);
  });

  it('handleUninstall 以当前插件 spec 调 uninstall', async () => {
    const base = plugin();
    seedStore({ installed: [base] });
    const { instances } = await renderWithCapture(OpenCodePluginPanel);
    const panel = instanceOf(instances, OpenCodePluginPanel);
    await panel.exposed?.openDetail(base.spec);
    await panel.exposed?.handleUninstall();
    expect(api.uninstallOpenCodePlugin).toHaveBeenCalledWith(base.spec);
  });
});
