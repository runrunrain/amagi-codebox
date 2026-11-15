/**
 * SharedMcpPanel 单测（共用 MCP 契约 §4.4）：
 *  - 四段式骨架：DiagnosticBanner（有 error 才渲染）→ CountTabs（全部/stdio/远程/
 *    已分发/未分发，计数来自 store 真实数据）→ ListToolbar（「共用 MCP」+ 新增服务器/
 *    打开登记目录/刷新）→ 内容区 ex-split（左列 PluginRow / 右列详情+分发矩阵）；
 *  - 首屏门控（store 派生）：Loading / ErrorState；空态两形态（登记文件不存在/暂无条目）；
 *  - 详情 MetaGrid + env/headers 值经 MaskedValue（渲染 HTML 不含明文值）；
 *  - 分发矩阵：omp excluded 只读行（无 Switch）、conflict/unsupported 未分发格禁用、
 *    unsupported 仍留旧副本的格可点、drift 徽章 + 行内「重新分发」；
 *  - 开关 off / 删除确认弹窗（含『将移除已写入各引擎配置的托管条目』警示，经 teleports）；
 *  - ErrForeignModified（错误信息含「已被外部修改」）→ danger 确认后 force=true 重试；
 *  - 表单：stdio/远程双形态校验与提交（args 逐行解析、env/headers 键值对组装）。
 *
 * 范式：node 环境 renderToString + store 状态种子驱动门控分支（SSR 不执行 onMounted）；
 * 交互断言走 defineExpose 触发器；Dialog/ConfirmDialog 弹层内容查 __teleportBuffers
 * （renderInstanceAgain 追加段，同 sharedSkillsPanel.test.ts）。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { createSSRApp, defineComponent, getCurrentInstance, h } from 'vue';
import type { ComponentInternalInstance, Ref } from 'vue';
import { renderToString } from 'vue/server-renderer';

const apiMocks = vi.hoisted(() => ({
  listSharedMcp: vi.fn(async () => emptyOverview()),
  refreshSharedMcp: vi.fn(async () => emptyOverview()),
  createSharedMcpEntry: vi.fn(async () => emptyOverview()),
  updateSharedMcpEntry: vi.fn(async () => emptyOverview()),
  deleteSharedMcpEntry: vi.fn(async () => emptyOverview()),
  setSharedMcpEngineSelection: vi.fn(async () => emptyOverview()),
  openSharedMcpRegistryDir: vi.fn(async () => undefined),
}));

function emptyOverview(): import('../../../../api/sharedMcp').SharedMcpOverview {
  return { entries: [], registryExists: true, registryPath: '', enginesMeta: [] };
}

vi.mock('../../../../api/sharedMcp', () => ({
  listSharedMcp: apiMocks.listSharedMcp,
  refreshSharedMcp: apiMocks.refreshSharedMcp,
  createSharedMcpEntry: apiMocks.createSharedMcpEntry,
  updateSharedMcpEntry: apiMocks.updateSharedMcpEntry,
  deleteSharedMcpEntry: apiMocks.deleteSharedMcpEntry,
  setSharedMcpEngineSelection: apiMocks.setSharedMcpEngineSelection,
  openSharedMcpRegistryDir: apiMocks.openSharedMcpRegistryDir,
}));

import SharedMcpPanel from '../../../../components/extensions/SharedMcpPanel.vue';
import { useSharedMcpStore } from '../../../../stores/sharedMcp';
import { useToast } from '../../../../composables/useToast';
import type {
  SharedMcpEngineMeta,
  SharedMcpEntryOverview,
} from '../../../../api/sharedMcp';
import { renderInstanceAgain } from '../ui/testUtils';

// ---- 测试基建：渲染 + 实例捕获 + 保留 ssr 上下文（teleport buffer 断言用，同 skills 范式） ----

type PushItem = string | Promise<unknown> | PushItem[];

async function unroll(item: PushItem): Promise<string> {
  if (typeof item === 'string') return item;
  if (Array.isArray(item)) {
    let out = '';
    for (const nested of item) out += await unroll(nested);
    return out;
  }
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
  const Wrapper = defineComponent({ setup: () => () => h(SharedMcpPanel) });
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
  const found = result.instances.find((instance) => instance.type === SharedMcpPanel);
  if (!found) throw new Error('SharedMcpPanel 未被渲染');
  return found;
}

/** 状态变更后重渲并展开本趟新增的 body teleport 内容（Dialog/ConfirmDialog 弹层） */
async function rerenderTeleports(result: PanelRenderResult): Promise<string> {
  const bodyBuffer = result.context.__teleportBuffers?.body ?? [];
  const before = bodyBuffer.length;
  await renderInstanceAgain(panelInstance(result));
  const appended = bodyBuffer.slice(before);
  let out = '';
  for (const item of appended) out += await unroll(item);
  return out;
}

/** instance.exposed 为 defineExpose 的原始对象（函数直取、ref 即 ref 本体） */
interface PanelExposed {
  selectEntry: (item: SharedMcpEntryOverview) => void;
  clearActive: () => void;
  selectTab: (key: string) => void;
  onSearch: (value: string) => void;
  openCreateForm: () => void;
  openEditForm: (item: SharedMcpEntryOverview) => void;
  submitForm: () => Promise<void>;
  requestDelete: (item: SharedMcpEntryOverview) => void;
  confirmDelete: () => Promise<void>;
  toggleEngine: (
    item: SharedMcpEntryOverview,
    meta: SharedMcpEngineMeta,
    value: boolean,
  ) => void;
  confirmUndistribute: () => Promise<void>;
  resyncEngine: (item: SharedMcpEntryOverview, meta: SharedMcpEngineMeta) => void;
  confirmForeignRemove: () => Promise<void>;
  handleOpenRegistryDir: () => Promise<void>;
  handleRefresh: () => Promise<void>;
  formName: Ref<string>;
  formType: Ref<'stdio' | 'http'>;
  formCommand: Ref<string>;
  formArgsText: Ref<string>;
  formUrl: Ref<string>;
  formDescription: Ref<string>;
  formKvRows: Ref<{ key: string; value: string }[]>;
  formError: Ref<string>;
}

function panelExposed(result: PanelRenderResult): PanelExposed {
  const exposed = panelInstance(result).exposed as PanelExposed | null;
  if (!exposed) throw new Error('SharedMcpPanel 未 defineExpose');
  return exposed;
}

// ---- 数据种子 ----

const OMP_DETAIL = 'omp（config.yml）无 MCP 配置面——仓库与本机均无佐证，excluded 是裁定不是待办';

const ENGINE_METAS: SharedMcpEngineMeta[] = [
  { id: 'claude', name: 'Claude', support: 'ok', targetPath: '/home/u/.claude.json', managedCount: 1 },
  { id: 'codex', name: 'Codex', support: 'ok', targetPath: '/home/u/.codex/config.toml', managedCount: 1 },
  { id: 'opencode', name: 'OpenCode', support: 'ok', targetPath: '/home/u/.config/opencode/opencode.json', managedCount: 0 },
  { id: 'pi', name: 'Pi', support: 'ok', targetPath: '/home/u/.pi/agent/mcp.json', managedCount: 0 },
  { id: 'omp', name: 'omp', support: 'excluded', detail: OMP_DETAIL, managedCount: 0 },
];

const META_CLAUDE = ENGINE_METAS[0];
const META_CODEX = ENGINE_METAS[1];
const META_OPENCODE = ENGINE_METAS[2];

const STDIO_ITEM: SharedMcpEntryOverview = {
  entry: {
    name: 'zhipu-vision',
    type: 'stdio',
    command: 'npx',
    args: ['-y', 'zhipu-mcp'],
    env: { ZHIPU_API_KEY: 'sk-plaintext-abcdef' },
    description: '视觉理解工具',
  },
  nameDistributable: true,
  engines: {
    claude: { status: 'distributed', selected: true },
    codex: { status: 'drift', selected: true, reason: '登记内容已更新，可重新分发' },
    opencode: { status: 'not-distributed', selected: false },
    pi: {
      status: 'conflict',
      selected: false,
      reason: '目标配置已存在同名条目（非本应用写入），开启将被拒绝；请先手动处理目标文件',
    },
    omp: { status: 'excluded', selected: false, reason: OMP_DETAIL },
  },
};

const HTTP_ITEM: SharedMcpEntryOverview = {
  entry: {
    name: 'web-search-prime',
    type: 'http',
    url: 'https://mcp.example.com/sse',
    headers: { Authorization: 'Bearer tok-9876543210' },
    description: '联网搜索',
  },
  nameDistributable: true,
  engines: {
    claude: { status: 'distributed', selected: true },
    // 曾以 stdio 分发、登记更新为 http+headers 后：unsupported 仍留旧副本 → 可点（off 即移除）
    codex: {
      status: 'unsupported',
      selected: true,
      reason: 'Codex 远程 MCP 鉴权仅支持 OAuth/bearer_token_env_var，无法表达登记文件中的 Authorization 头；该引擎仍留有旧版分发副本，关闭开关可移除',
    },
    opencode: { status: 'not-distributed', selected: false },
    pi: { status: 'unsupported', selected: false, reason: 'Pi 无法表达此形态' },
    omp: { status: 'excluded', selected: false, reason: OMP_DETAIL },
  },
};

function seedStore(
  store: ReturnType<typeof useSharedMcpStore>,
  entries: SharedMcpEntryOverview[],
  extra: { loadError?: string; loading?: boolean; registryExists?: boolean } = {},
): void {
  store.entries = entries;
  store.enginesMeta = ENGINE_METAS;
  store.registryPath = '/home/u/.agents/.mcp.json';
  if (extra.loadError !== undefined) store.error = extra.loadError;
  if (extra.loading !== undefined) store.loading = extra.loading;
  if (extra.registryExists !== undefined) store.registryExists = extra.registryExists;
}

/** 统计 html 中 Switch 按钮个数（omp 行无 Switch 的断言基础） */
function countSwitches(html: string): number {
  return (html.match(/class="switch(?: off)?"/g) ?? []).length;
}

/** 统计禁用 Switch 个数（SSR 渲染 disabled 属性于 class 之后） */
function countDisabledSwitches(html: string): number {
  return (html.match(/class="switch(?: off)?" disabled/g) ?? []).length;
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  useToast().toasts.splice(0);
});

// ---- ①-④ 四段式骨架 ----

describe('SharedMcpPanel 四段式骨架', () => {
  it('计数标签计数来自 store 真实数据（全部/stdio/远程/已分发/未分发）', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM, HTTP_ITEM]);
    const { html } = await renderPanel();
    expect(html).toMatch(/class="ct-label"[^>]*>全部<\/span><span class="ct-count"[^>]*>2<\/span>/);
    expect(html).toMatch(/class="ct-label"[^>]*>stdio<\/span><span class="ct-count"[^>]*>1<\/span>/);
    expect(html).toMatch(/class="ct-label"[^>]*>远程<\/span><span class="ct-count"[^>]*>1<\/span>/);
    expect(html).toMatch(/class="ct-label"[^>]*>已分发<\/span><span class="ct-count"[^>]*>2<\/span>/);
    expect(html).toMatch(/class="ct-label"[^>]*>未分发<\/span><span class="ct-count"[^>]*>0<\/span>/);
  });

  it('工具栏标题/操作与行渲染（类型徽章+名称+描述单行+分发摘要；无行尾开关）', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM, HTTP_ITEM]);
    const { html } = await renderPanel();
    expect(html).toContain('共用 MCP');
    expect(html).toContain('新增服务器');
    expect(html).toContain('打开登记目录');
    expect(html).toContain('刷新');
    // 行：名称与描述摘要
    expect(html).toContain('zhipu-vision');
    expect(html).toContain('视觉理解工具 · 已分发到 2 个引擎');
    expect(html).toContain('联网搜索 · 已分发到 1 个引擎');
    // 分发在详情矩阵：左列行无开关（plugin-row-switch 不渲染）
    expect(html).not.toContain('plugin-row-switch');
    // 状态文案禁用「已连接」（R5：分发≠连通）
    expect(html).not.toContain('已连接');
  });

  it('无 error 时诊断横幅不渲染；有 error 且无数据时首屏 ErrorState', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    const clean = await renderPanel();
    expect(clean.html).not.toContain('diagnostic-banner');

    seedStore(store, [], { loadError: '加载共用 MCP 失败：boom' });
    const errored = await renderPanel();
    expect(errored.html).toContain('加载共用 MCP 失败：boom');
    expect(errored.html).toContain('重试');
  });

  it('首屏 Loading 门控（loading 且无数据）', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [], { loading: true });
    const { html } = await renderPanel();
    expect(html).toContain('加载共用 MCP 中');
    expect(html).not.toContain('ct-tabs');
  });
});

// ---- 空态 / 过滤 ----

describe('SharedMcpPanel 空态与过滤', () => {
  it('登记文件存在但无条目 → EmptyStateCard 暂无文案', async () => {
    const store = useSharedMcpStore();
    seedStore(store, []);
    const { html } = await renderPanel();
    expect(html).toContain('暂无共用 MCP 服务器');
    expect(html).toContain('~/.agents/.mcp.json 中 mcpServers 下的服务器条目会出现在这里');
  });

  it('登记文件不存在 → 空态承诺新增第一条后自动创建', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [], { registryExists: false });
    const { html } = await renderPanel();
    expect(html).toContain('登记文件不存在');
    expect(html).toContain('/home/u/.agents/.mcp.json');
    expect(html).toContain('新增第一条服务器后会自动创建');
  });

  it('计数标签切换与搜索过滤行', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM, HTTP_ITEM]);
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.selectTab('http');
    let html = await renderInstanceAgain(panelInstance(result));
    expect(html).toContain('web-search-prime');
    expect(html).not.toContain('zhipu-vision');

    panel.selectTab('all');
    panel.onSearch('zhipu');
    html = await renderInstanceAgain(panelInstance(result));
    expect(html).toContain('zhipu-vision');
    expect(html).not.toContain('web-search-prime');
  });
});

// ---- 详情态：MetaGrid + MaskedValue + 分发矩阵 ----

describe('SharedMcpPanel 详情态', () => {
  it('selectEntry → DetailHeader + MetaGrid（名称/类型/命令/参数/env 计数/已分发）', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM, HTTP_ITEM]);
    const result = await renderPanel();
    panelExposed(result).selectEntry(STDIO_ITEM);
    const html = await renderInstanceAgain(panelInstance(result));
    expect(html).toContain('dh-breadcrumb');
    expect(html).toContain('共用 MCP</button>');
    expect(html).toContain('meta-grid');
    expect(html).toContain('stdio（本地命令）');
    expect(html).toContain('npx');
    expect(html).toContain('-y zhipu-mcp');
    expect(html).toContain('1 项（值已脱敏，见下方）');
    expect(html).toContain('Claude、Codex（2 个引擎）');
    expect(html).toContain('编辑');
    expect(html).toContain('删除');
  });

  it('env/headers 值经 MaskedValue：渲染 HTML 不含明文值', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM, HTTP_ITEM]);
    const result = await renderPanel();
    panelExposed(result).selectEntry(STDIO_ITEM);
    const stdioHtml = await renderInstanceAgain(panelInstance(result));
    expect(stdioHtml).toContain('ZHIPU_API_KEY');
    expect(stdioHtml).toContain('值已脱敏');
    expect(stdioHtml).not.toContain('sk-plaintext-abcdef');

    panelExposed(result).selectEntry(HTTP_ITEM);
    const httpHtml = await renderInstanceAgain(panelInstance(result));
    expect(httpHtml).toContain('Authorization');
    expect(httpHtml).not.toContain('Bearer tok-9876543210');
    expect(httpHtml).toContain('https://mcp.example.com/sse');
  });

  it('分发矩阵：四引擎行 + 状态徽章 + drift 附行内「重新分发」', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    const result = await renderPanel();
    panelExposed(result).selectEntry(STDIO_ITEM);
    const html = await renderInstanceAgain(panelInstance(result));
    expect(html).toContain('引擎分发');
    expect(html).toContain('/home/u/.claude.json');
    expect(html).toContain('已分发');
    expect(html).toContain('有更新');
    expect(html).toContain('登记内容已更新，可重新分发');
    expect(html).toContain('重新分发');
    expect(html).toContain('同名冲突');
  });

  it('omp 排除态：excluded 徽章 + 裁定原因一句 + 无 Switch（OMT/omp 排除展示断言）', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    const result = await renderPanel();
    panelExposed(result).selectEntry(STDIO_ITEM);
    const html = await renderInstanceAgain(panelInstance(result));
    expect(html).toContain('裁定排除');
    expect(html).toContain('无 MCP 配置面');
    expect(html).toContain('excluded 是裁定不是待办');
    // 四引擎各一个 Switch，omp 行无
    expect(countSwitches(html)).toBe(4);
  });

  it('Switch 可用性：conflict 与 unsupported 未分发格禁用；unsupported 留旧副本的格可点', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM, HTTP_ITEM]);
    const result = await renderPanel();
    panelExposed(result).selectEntry(STDIO_ITEM);
    const stdioHtml = await renderInstanceAgain(panelInstance(result));
    // stdio 条目：pi=conflict 禁用（1 个），其余可点
    expect(countDisabledSwitches(stdioHtml)).toBe(1);

    panelExposed(result).selectEntry(HTTP_ITEM);
    const httpHtml = await renderInstanceAgain(panelInstance(result));
    // http 条目：codex=unsupported+selected（可点，off 即移除旧副本）、pi=unsupported 未分发（禁用）
    expect(countDisabledSwitches(httpHtml)).toBe(1);
    expect(httpHtml).toContain('该引擎不支持此形态');
    expect(httpHtml).toContain('关闭开关可移除');
  });
});

// ---- 矩阵操作：开关 / resync / 外改 force ----

describe('SharedMcpPanel 矩阵操作', () => {
  it('开启直接执行：toggleEngine(true) 调 setSharedMcpEngineSelection(name, engine, true, false)', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    const result = await renderPanel();
    panelExposed(result).toggleEngine(STDIO_ITEM, META_OPENCODE, true);
    await vi.waitFor(() => {
      expect(apiMocks.setSharedMcpEngineSelection).toHaveBeenCalledWith(
        'zhipu-vision',
        'opencode',
        true,
        false,
      );
    });
  });

  it('关闭（禁用分发）先确认：弹窗含『将移除已写入各引擎配置的托管条目』警示', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.toggleEngine(STDIO_ITEM, META_OPENCODE, false);
    const teleportsHtml = await rerenderTeleports(result);
    expect(teleportsHtml).toContain('停止分发');
    expect(teleportsHtml).toContain('停止向OpenCode分发「zhipu-vision」');
    expect(teleportsHtml).toContain('将移除已写入各引擎配置的托管条目');
    // 确认前未调 API
    expect(apiMocks.setSharedMcpEngineSelection).not.toHaveBeenCalled();

    await panel.confirmUndistribute();
    expect(apiMocks.setSharedMcpEngineSelection).toHaveBeenCalledWith(
      'zhipu-vision',
      'opencode',
      false,
      false,
    );
  });

  it('drift 格「重新分发」= 幂等 resync（selected=true, force=false）', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    const result = await renderPanel();
    panelExposed(result).resyncEngine(STDIO_ITEM, META_CODEX);
    await vi.waitFor(() => {
      expect(apiMocks.setSharedMcpEngineSelection).toHaveBeenCalledWith(
        'zhipu-vision',
        'codex',
        true,
        false,
      );
    });
  });

  it('off 收到外改错误 → danger 确认弹窗后以 force=true 重试', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    apiMocks.setSharedMcpEngineSelection.mockRejectedValueOnce(
      new Error('Claude 中的 zhipu-vision 已被外部修改（与上次分发的哈希不一致），直接移除将丢失这些修改；如确认请在弹窗中强制执行'),
    );
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.toggleEngine(STDIO_ITEM, META_CLAUDE, false);
    await panel.confirmUndistribute();
    expect(apiMocks.setSharedMcpEngineSelection).toHaveBeenCalledWith(
      'zhipu-vision',
      'claude',
      false,
      false,
    );

    const teleportsHtml = await rerenderTeleports(result);
    expect(teleportsHtml).toContain('已被外部修改');
    expect(teleportsHtml).toContain('继续将丢失这些修改，确认继续？');

    await panel.confirmForeignRemove();
    expect(apiMocks.setSharedMcpEngineSelection).toHaveBeenCalledWith(
      'zhipu-vision',
      'claude',
      false,
      true,
    );
  });

  it('非外改失败 → toast 错误信息，不弹 danger 确认', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    apiMocks.setSharedMcpEngineSelection.mockRejectedValueOnce(new Error('写目标文件失败'));
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.toggleEngine(STDIO_ITEM, META_CLAUDE, false);
    await panel.confirmUndistribute();
    const { toasts } = useToast();
    expect(toasts.some((t) => t.message.includes('写目标文件失败'))).toBe(true);
    const teleportsHtml = await rerenderTeleports(result);
    expect(teleportsHtml).not.toContain('已被外部修改');
  });
});

// ---- 删除：danger 确认（含托管条目移除警示）+ 外改二次确认 ----

describe('SharedMcpPanel 删除', () => {
  it('danger 确认弹窗含引擎数与托管条目警示，confirm 调 deleteSharedMcpEntry(name, false)', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.requestDelete(STDIO_ITEM);
    const teleportsHtml = await rerenderTeleports(result);
    expect(teleportsHtml).toContain('删除「zhipu-vision」');
    expect(teleportsHtml).toContain('从已分发的 2 个引擎配置中同步移除');
    expect(teleportsHtml).toContain('将移除已写入各引擎配置的托管条目');
    expect(teleportsHtml).toContain('此操作不可恢复');

    await panel.confirmDelete();
    expect(apiMocks.deleteSharedMcpEntry).toHaveBeenCalledWith('zhipu-vision', false);
  });

  it('删除遇外改 → 追加 danger 确认后以 force=true 重试', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    apiMocks.deleteSharedMcpEntry.mockRejectedValueOnce(
      new Error('Pi 中的 zhipu-vision 已被外部修改（与上次分发的哈希不一致），直接移除将丢失这些修改；如确认请在弹窗中强制执行'),
    );
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.requestDelete(STDIO_ITEM);
    await panel.confirmDelete();
    expect(apiMocks.deleteSharedMcpEntry).toHaveBeenCalledWith('zhipu-vision', false);

    const teleportsHtml = await rerenderTeleports(result);
    expect(teleportsHtml).toContain('有引擎中的条目已被外部修改');
    expect(teleportsHtml).toContain('将丢失这些修改');

    await panel.confirmForeignRemove();
    expect(apiMocks.deleteSharedMcpEntry).toHaveBeenCalledWith('zhipu-vision', true);
  });
});

// ---- 新增/编辑表单 ----

describe('SharedMcpPanel 表单', () => {
  it('openCreateForm 打开表单（teleports）；空名提交被前端校验拦下', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.openCreateForm();
    const teleportsHtml = await rerenderTeleports(result);
    expect(teleportsHtml).toContain('新增服务器');
    expect(teleportsHtml).toContain('~/.agents/.mcp.json');

    await panel.submitForm();
    const errorHtml = await rerenderTeleports(result);
    expect(errorHtml).toContain('名称仅支持字母、数字、- 与 _');
    expect(apiMocks.createSharedMcpEntry).not.toHaveBeenCalled();
  });

  it('stdio 新增提交：args 逐行解析、env 键值对组装', async () => {
    const store = useSharedMcpStore();
    seedStore(store, []);
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.openCreateForm();
    panel.formName.value = 'tavily-mcp';
    panel.formCommand.value = 'npx';
    panel.formArgsText.value = '-y\n  tavily-mcp \n';
    panel.formKvRows.value = [
      { key: 'TAVILY_KEY', value: 'tvly-secret' },
      { key: '', value: '' },
    ];
    panel.formDescription.value = '搜索';

    await panel.submitForm();
    expect(apiMocks.createSharedMcpEntry).toHaveBeenCalledWith({
      name: 'tavily-mcp',
      type: 'stdio',
      command: 'npx',
      args: ['-y', 'tavily-mcp'],
      env: { TAVILY_KEY: 'tvly-secret' },
      description: '搜索',
    });
  });

  it('远程形态：URL 校验 + headers 组装', async () => {
    const store = useSharedMcpStore();
    seedStore(store, []);
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.openCreateForm();
    panel.formName.value = 'web-reader';
    panel.formType.value = 'http';
    panel.formUrl.value = 'not-a-url';
    await panel.submitForm();
    expect(apiMocks.createSharedMcpEntry).not.toHaveBeenCalled();
    expect(panel.formError.value).toContain('URL');

    panel.formUrl.value = 'https://mcp.example.com/sse';
    panel.formKvRows.value = [{ key: 'Authorization', value: 'Bearer xyz' }];
    await panel.submitForm();
    expect(apiMocks.createSharedMcpEntry).toHaveBeenCalledWith({
      name: 'web-reader',
      type: 'http',
      url: 'https://mcp.example.com/sse',
      headers: { Authorization: 'Bearer xyz' },
      description: undefined,
    });
  });

  it('openEditForm 回填（名称禁改）+ 提交走 update', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    const result = await renderPanel();
    const panel = panelExposed(result);

    panel.openEditForm(STDIO_ITEM);
    const teleportsHtml = await rerenderTeleports(result);
    expect(teleportsHtml).toContain('编辑服务器');
    expect(teleportsHtml).toContain('名称不可改');
    expect(panel.formName.value).toBe('zhipu-vision');
    expect(panel.formType.value).toBe('stdio');
    expect(panel.formCommand.value).toBe('npx');
    expect(panel.formArgsText.value).toBe('-y\nzhipu-mcp');
    expect(panel.formKvRows.value).toEqual([{ key: 'ZHIPU_API_KEY', value: 'sk-plaintext-abcdef' }]);
    // 表单值输入框密码态（不回显明文）
    expect(teleportsHtml).toContain('type="password"');

    panel.formDescription.value = '视觉理解工具 v2';
    await panel.submitForm();
    expect(apiMocks.updateSharedMcpEntry).toHaveBeenCalledWith(
      'zhipu-vision',
      expect.objectContaining({ name: 'zhipu-vision', description: '视觉理解工具 v2' }),
    );
  });
});

// ---- 工具栏动作 ----

describe('SharedMcpPanel 工具栏动作', () => {
  it('刷新调 load（listSharedMcp）；打开登记目录调 openSharedMcpRegistryDir(registryPath)', async () => {
    const store = useSharedMcpStore();
    seedStore(store, [STDIO_ITEM]);
    const result = await renderPanel();
    const panel = panelExposed(result);

    // 先断言打开目录（load 的 mock 返回空 Overview 会覆盖种子 registryPath）
    await panel.handleOpenRegistryDir();
    expect(apiMocks.openSharedMcpRegistryDir).toHaveBeenCalledWith('/home/u/.agents/.mcp.json');

    await panel.handleRefresh();
    expect(apiMocks.listSharedMcp).toHaveBeenCalled();
  });
});
