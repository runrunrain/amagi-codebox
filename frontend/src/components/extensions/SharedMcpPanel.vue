<!-- 共用 MCP 面板（共用 MCP 契约 §4.2，列表/详情分页化）：~/.agents/.mcp.json 登记 +
     四引擎分发，复用 ui/* 与 extensions/ui/* 共享件（SharedSkillsPanel 同范式）。
     一级列表态四段式：① DiagnosticBanner（errors=store.error）→ ② CountTabs（全部/stdio/
     远程/已分发/未分发 + 搜索）→ ③ ListToolbar（「共用 MCP N」+ 新增服务器/打开登记目录/
     刷新）→ ④ 全宽列表（PluginRow：类型徽章+名称+描述完整多行，行尾「已分发 N」轻量徽章；
     空态：EmptyStateCard 两形态）。
     二级详情态（点击行进入，替换子页内容区）：「← 返回列表」+ 面包屑「共用 MCP › <名>」+
     居中窄单列（max-width 720px）：DetailHeader（大图标/名称/主操作：编辑/删除）→ 描述全文
     → 配置 MetaGrid + env/headers 脱敏值（MaskedValue）→ 条目×引擎分发矩阵（启停分发/
     重新分发/接管 force 流程全部在此）。新增/编辑表单仍从工具栏进入（弹窗形态不变）。
     分发语义（契约 §1.5）：状态只用「已分发到 N 个引擎/未分发/有更新」，绝不用连接类措辞
     （分发≠连通，本功能不做握手探测）。开关 off（禁用分发）与删除均带确认弹窗，含
     『将移除已写入各引擎配置的托管条目』警示；任一路径收到 ErrForeignModified（错误
     信息含「已被外部修改」）→ 追加 danger 确认后以 force=true 重试。env/headers 值
     在列表/详情只经 MaskedValue 展示、表单编辑态为密码框不回显明文。 -->
<template>
  <div class="shared-mcp-panel">
    <LoadingState v-if="showInitialLoading" message="加载共用 MCP 中..." />

    <ErrorState v-else-if="showInitialError" :message="error" :on-retry="load" />

    <template v-else>
      <!-- 一级页面：纯列表态（activeName 为空；四段式①-④） -->
      <template v-if="!activeItem">
        <!-- ① 诊断横幅：errors=store 加载错误（有数据后的降级形态） -->
        <DiagnosticBanner :errors="error ? [error] : []" />

        <!-- ② 计数标签（计数来自 store 真实数据）+ 搜索框 -->
        <CountTabs
          :tabs="countTabs"
          :model-value="activeTab"
          search-placeholder="搜索名称或描述…"
          :search-value="searchText"
          @update:model-value="selectTab"
          @update:search-value="onSearch"
        />

        <!-- ③ 工具栏（新增/编辑表单从这里进入，弹窗形态不变） -->
        <ListToolbar title="共用 MCP" :count="visibleEntries.length" :loading="isLoading">
          <template #actions>
            <AppButton variant="primary" size="small" @click="openCreateForm">
              新增服务器
            </AppButton>
            <AppButton
              variant="ghost"
              size="small"
              :disabled="!registryPath"
              title="在系统文件管理器中打开登记文件所在目录"
              @click="handleOpenRegistryDir"
            >
              打开登记目录
            </AppButton>
            <AppButton variant="ghost" size="small" :disabled="isLoading" @click="handleRefresh">
              {{ isLoading ? '刷新中…' : '刷新' }}
            </AppButton>
          </template>
        </ListToolbar>

        <!-- 与各 Agent 子页插件自带 MCP（只读）的区分说明（跨面板一致性，一句话副标题） -->
        <p class="panel-hint">
          用户级登记（~/.agents/.mcp.json）并按引擎分发；各 Agent 子页展示的是其插件自带的 MCP 服务器（只读）。
        </p>

        <!-- ④ 内容区：空态两形态（登记文件不存在 / 暂无条目）/ 全宽列表 -->
        <EmptyStateCard
          v-if="entries.length === 0"
          :title="registryExists ? '暂无共用 MCP 服务器' : '登记文件不存在'"
          :description="
            registryExists
              ? '~/.agents/.mcp.json 中 mcpServers 下的服务器条目会出现在这里'
              : `登记文件 ${registryPath || '~/.agents/.mcp.json'} 不存在——新增第一条服务器后会自动创建`
          "
        />

        <div v-else class="mcp-list">
          <!-- 行：图标+类型徽章+名称+描述完整多行，行尾「已分发 N」轻量徽章；
               分发开关在详情页矩阵，行内不放 Switch -->
          <div
            v-for="item in visibleEntries"
            :key="item.entry.name"
            class="mcp-row"
            @click="selectEntry(item)"
          >
            <PluginRow
              :name="item.entry.name"
              :description="item.entry.description ?? ''"
              :badges="rowBadges(item)"
            />
            <span class="mcp-row-dist">
              <Badge
                v-if="distributedCount(item) > 0"
                type="type"
                color="capability"
                :text="`已分发 ${distributedCount(item)}`"
              />
              <Badge v-else type="type" color="capability" text="未分发" />
            </span>
          </div>
          <EmptyState
            v-if="visibleEntries.length === 0"
            icon="◇"
            title="无匹配服务器"
            description="换个关键词或切换计数标签试试"
          />
        </div>
      </template>

      <!-- 二级页面：详情态（点击列表行进入，替换子页内容区；居中窄单列） -->
      <div v-else class="mcp-detail-page">
        <button type="button" class="back-btn" @click="clearActive">← 返回列表</button>

        <DetailHeader
          :breadcrumb="[{ label: '共用 MCP', clickable: true }, { label: activeItem.entry.name }]"
          :title="activeItem.entry.name"
          :badges="rowBadges(activeItem)"
          @back="clearActive"
        >
          <template #actions>
            <AppButton variant="primary" size="small" @click="openEditForm(activeItem)">
              编辑
            </AppButton>
            <AppButton variant="danger" size="small" @click="requestDelete(activeItem)">
              删除
            </AppButton>
          </template>
        </DetailHeader>

        <p v-if="activeItem.entry.description" class="detail-desc">
          {{ activeItem.entry.description }}
        </p>
        <p v-else class="detail-desc">该服务器暂无描述。</p>

        <MetaGrid :items="metaItems" class="detail-meta" />

        <!-- env/headers 敏感值：键明文 + 值经 MaskedValue（渲染 HTML 不含明文值） -->
        <div v-if="secretGroups.length" class="secret-groups">
          <div v-for="group in secretGroups" :key="group.label" class="secret-group">
            <div class="secret-group-head">{{ group.label }}（{{ group.pairs.length }} 项，值已脱敏）</div>
            <div v-for="pair in group.pairs" :key="pair.key" class="secret-row">
              <code class="secret-key">{{ pair.key }}</code>
              <MaskedValue :value="pair.value" />
            </div>
          </div>
        </div>

        <!-- 分发矩阵（条目 × 引擎，契约 §1.3/§1.5）：每格状态 = §1.5 算法结果 -->
        <div class="matrix">
          <div class="matrix-head">引擎分发</div>
          <div v-for="row in matrixRows" :key="row.meta.id" class="matrix-row">
            <div class="matrix-engine">
              <span class="matrix-engine-name">{{ row.meta.name }}</span>
              <span v-if="row.meta.targetPath" class="matrix-engine-path">{{ row.meta.targetPath }}</span>
            </div>
            <div class="matrix-cell">
              <!-- omp（excluded）：只读行，无开关 -->
              <Switch
                v-if="row.meta.support !== 'excluded'"
                :model-value="row.cell.selected"
                :disabled="switchDisabled(row.cell)"
                @update:model-value="(value: boolean) => activeItem && toggleEngine(activeItem, row.meta, value)"
              />
              <Badge
                :type="statusBadge(row.cell).type"
                :color="statusBadge(row.cell).color"
                :text="statusBadge(row.cell).text"
              />
              <button
                v-if="row.cell.status === 'drift'"
                class="link-btn"
                @click="resyncEngine(activeItem, row.meta)"
              >
                重新分发
              </button>
            </div>
            <p v-if="cellReason(row)" class="matrix-reason">{{ cellReason(row) }}</p>
          </div>
        </div>
      </div>
    </template>

    <!-- 新增/编辑表单（stdio 与远程双形态切换；值输入框密码态不回显明文） -->
    <Dialog
      v-model:open="showFormDialog"
      :title="editingName ? '编辑服务器' : '新增服务器'"
      :description="editingName ? '修改登记条目（名称不可改；已分发引擎将显示「有更新」，需在详情矩阵逐引擎重新分发）' : '登记到 ~/.agents/.mcp.json（文件不存在时自动创建）'"
    >
      <div class="mcp-form">
        <div class="form-group">
          <label>名称</label>
          <input
            v-model="formName"
            type="text"
            class="form-input"
            :disabled="!!editingName"
            placeholder="例: web-search（字母、数字、- 与 _）"
          />
        </div>

        <div class="form-group">
          <label>类型</label>
          <div class="type-toggle">
            <button
              type="button"
              :class="['type-btn', { active: formType === 'stdio' }]"
              @click="formType = 'stdio'"
            >
              stdio（本地命令）
            </button>
            <button
              type="button"
              :class="['type-btn', { active: formType === 'http' }]"
              @click="formType = 'http'"
            >
              远程（HTTP）
            </button>
          </div>
        </div>

        <template v-if="formType === 'stdio'">
          <div class="form-group">
            <label>命令（command）</label>
            <input
              v-model="formCommand"
              type="text"
              class="form-input mono"
              placeholder="例: npx"
            />
          </div>
          <div class="form-group">
            <label>参数（args，每行一个）</label>
            <textarea v-model="formArgsText" class="form-input mono" rows="3" placeholder="-y&#10;@example/mcp-server" />
          </div>
        </template>

        <template v-else>
          <div class="form-group">
            <label>URL</label>
            <input
              v-model="formUrl"
              type="text"
              class="form-input mono"
              placeholder="https://mcp.example.com/sse"
            />
          </div>
        </template>

        <div class="form-group">
          <label>{{ formType === 'stdio' ? '环境变量（env）' : '请求头（headers）' }}</label>
          <div class="kv-editor">
            <div v-for="(row, index) in formKvRows" :key="index" class="kv-row">
              <input v-model="row.key" type="text" class="form-input mono" placeholder="键" />
              <input
                v-model="row.value"
                type="password"
                autocomplete="new-password"
                class="form-input mono"
                placeholder="值（脱敏输入）"
              />
              <button type="button" class="kv-remove" @click="removeKvRow(index)">×</button>
            </div>
            <button type="button" class="kv-add" @click="addKvRow">+ 添加一项</button>
          </div>
        </div>

        <div class="form-group">
          <label>描述</label>
          <textarea v-model="formDescription" class="form-input" rows="2" placeholder="这个服务器做什么（可选）" />
        </div>

        <p v-if="formError" class="form-error">{{ formError }}</p>
      </div>
      <template #footer>
        <AppButton variant="ghost" @click="showFormDialog = false">取消</AppButton>
        <AppButton variant="primary" :disabled="formSubmitting" @click="submitForm">
          {{ formSubmitting ? '保存中…' : '保存' }}
        </AppButton>
      </template>
    </Dialog>

    <!-- 禁用分发确认：移除已写入该引擎配置的托管条目（警示文案契约） -->
    <ConfirmDialog
      v-model:open="showUndistributeDialog"
      title="停止分发"
      :message="undistributeConfirmMessage"
      confirm-text="停止分发"
      @confirm="confirmUndistribute"
    />

    <!-- 删除确认（danger）：登记永久移除 + 已分发引擎同步还原 -->
    <ConfirmDialog
      v-model:open="showDeleteDialog"
      title="删除服务器"
      :message="deleteConfirmMessage"
      danger
      confirm-text="删除"
      @confirm="confirmDelete"
    />

    <!-- 外改 danger 确认（契约 §4.2 唯一例外）：ErrForeignModified 后以 force=true 重试 -->
    <ConfirmDialog
      v-model:open="showForeignDialog"
      title="目标条目已被外部修改"
      :message="foreignConfirmMessage"
      danger
      confirm-text="强制继续"
      @confirm="confirmForeignRemove"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { storeToRefs } from 'pinia';
import { useSharedMcpStore, isForeignModifiedError } from '../../stores/sharedMcp';
import * as sharedMcpApi from '../../api/sharedMcp';
import type {
  SharedMcpEntry,
  SharedMcpEngineCell,
  SharedMcpEngineMeta,
  SharedMcpEntryOverview,
} from '../../api/sharedMcp';
import AppButton from '../ui/AppButton.vue';
import Badge from '../ui/Badge.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import Dialog from '../ui/Dialog.vue';
import EmptyState from '../ui/EmptyState.vue';
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import MaskedValue from '../ui/MaskedValue.vue';
import Switch from '../ui/Switch.vue';
import CountTabs from './ui/CountTabs.vue';
import DetailHeader from './ui/DetailHeader.vue';
import DiagnosticBanner from './ui/DiagnosticBanner.vue';
import EmptyStateCard from './ui/EmptyStateCard.vue';
import ListToolbar from './ui/ListToolbar.vue';
import MetaGrid from './ui/MetaGrid.vue';
import PluginRow from './ui/PluginRow.vue';
import type {
  CountTabItem,
  MetaItemView,
  PluginBadgeDescriptor,
} from './pluginViewTypes';
import { useToast } from '../../composables/useToast';

const store = useSharedMcpStore();
const {
  entries,
  enginesMeta,
  registryExists,
  registryPath,
  loading: isLoading,
  error,
  loaded,
} = storeToRefs(store);
const { load, create, update, remove, setEngineSelection, engineCell } = store;
const { showSuccess, showError } = useToast();

// --- 本地视图状态（门控类状态一律 store 派生） ---
type McpTab = 'all' | 'stdio' | 'http' | 'distributed' | 'undistributed';
const activeTab = ref<McpTab>('all');
const searchText = ref('');
const activeName = ref<string | null>(null);

const showDeleteDialog = ref(false);
const showUndistributeDialog = ref(false);
const showForeignDialog = ref(false);
const pendingDelete = ref<SharedMcpEntryOverview | null>(null);
const pendingUndistribute = ref<{ item: SharedMcpEntryOverview; meta: SharedMcpEngineMeta } | null>(null);
// 外改重试上下文：引擎开关（selected 方向）或删除（force 重试）
const pendingForeign = ref<{
  name: string;
  engine: string;
  engineName: string;
  selected: boolean;
} | null>(null);
const pendingForeignDelete = ref<string | null>(null);

// --- 首屏门控（store 派生，SSR 测试不执行 onMounted） ---
const showInitialLoading = computed(
  () => isLoading.value && entries.value.length === 0 && !error.value,
);
const showInitialError = computed(() => !!error.value && entries.value.length === 0);

// --- 引擎格语义（契约 §1.5）：applied = 目标当前留有我方托管条目 ---
const APPLIED_STATUSES = new Set(['distributed', 'drift']);

function fallbackCell(engine: string): SharedMcpEngineCell {
  return {
    status: engine === 'omp' ? 'excluded' : 'not-distributed',
    selected: false,
  };
}

/** 引擎行取格（后端未返回该引擎时按未分发呈现；omp 恒 excluded） */
function cellOf(item: SharedMcpEntryOverview, meta: SharedMcpEngineMeta): SharedMcpEngineCell {
  return engineCell(item, meta.id) ?? fallbackCell(meta.id);
}

/** 已分发引擎名列表（distributed | drift：目标存在我方托管条目，实测非缓存） */
function distributedEngineNames(item: SharedMcpEntryOverview): string[] {
  return enginesMeta.value
    .filter((meta) => APPLIED_STATUSES.has(cellOf(item, meta).status))
    .map((meta) => meta.name);
}

function distributedCount(item: SharedMcpEntryOverview): number {
  return distributedEngineNames(item).length;
}

// --- ② 计数标签：计数来自 store 真实数据 ---
const countTabs = computed<CountTabItem[]>(() => [
  { key: 'all', label: '全部', count: entries.value.length },
  { key: 'stdio', label: 'stdio', count: entries.value.filter((i) => i.entry.type === 'stdio').length },
  { key: 'http', label: '远程', count: entries.value.filter((i) => i.entry.type === 'http').length },
  { key: 'distributed', label: '已分发', count: entries.value.filter((i) => distributedCount(i) > 0).length },
  { key: 'undistributed', label: '未分发', count: entries.value.filter((i) => distributedCount(i) === 0).length },
]);

function selectTab(key: string) {
  activeTab.value = key as McpTab;
  searchText.value = '';
}

function onSearch(value: string) {
  searchText.value = value;
}

// --- ④ 列表态：标签过滤 + 搜索过滤（名称/描述） ---
const visibleEntries = computed<SharedMcpEntryOverview[]>(() => {
  const query = searchText.value.trim().toLowerCase();
  return entries.value
    .filter((item) => {
      switch (activeTab.value) {
        case 'stdio':
          return item.entry.type === 'stdio';
        case 'http':
          return item.entry.type === 'http';
        case 'distributed':
          return distributedCount(item) > 0;
        case 'undistributed':
          return distributedCount(item) === 0;
        default:
          return true;
      }
    })
    .filter(
      (item) =>
        !query ||
        item.entry.name.toLowerCase().includes(query) ||
        (item.entry.description ?? '').toLowerCase().includes(query),
    );
});

// --- ④ 详情态 ---
const activeItem = computed<SharedMcpEntryOverview | null>(
  () => entries.value.find((i) => i.entry.name === activeName.value) || null,
);

function selectEntry(item: SharedMcpEntryOverview) {
  activeName.value = item.entry.name;
}

function clearActive() {
  activeName.value = null;
}

// 类型徽章：stdio=mono 徽章、远程=蓝色 type 徽章、未知 type=warning 如实展示
function typeBadge(item: SharedMcpEntryOverview): PluginBadgeDescriptor {
  if (item.entry.type === 'stdio') return { text: 'stdio', type: 'ver' };
  if (item.entry.type === 'http') return { text: '远程', type: 'type', color: 'mcp' };
  return { text: item.entry.type, type: 'warning' };
}

function rowBadges(item: SharedMcpEntryOverview): PluginBadgeDescriptor[] {
  const badges: PluginBadgeDescriptor[] = [typeBadge(item)];
  if (!item.nameDistributable) {
    badges.push({ text: '名字不兼容分发', type: 'warning' });
  }
  return badges;
}

const metaItems = computed<MetaItemView[]>(() => {
  const item = activeItem.value;
  if (!item) return [];
  const entry = item.entry;
  const items: MetaItemView[] = [
    { label: '名称', value: entry.name, mono: true },
    {
      label: '类型',
      value: entry.type === 'stdio' ? 'stdio（本地命令）' : entry.type === 'http' ? '远程（HTTP）' : entry.type,
    },
  ];
  if (entry.command) items.push({ label: '命令', value: entry.command, mono: true });
  if (entry.url) items.push({ label: 'URL', value: entry.url, mono: true });
  if (entry.args?.length) items.push({ label: '参数', value: entry.args.join(' '), mono: true });
  const envKeys = Object.keys(entry.env ?? {});
  if (envKeys.length) items.push({ label: 'env', value: `${envKeys.length} 项（值已脱敏，见下方）` });
  const headerKeys = Object.keys(entry.headers ?? {});
  if (headerKeys.length) items.push({ label: 'headers', value: `${headerKeys.length} 项（值已脱敏，见下方）` });
  items.push({
    label: '已分发',
    value:
      distributedCount(item) > 0
        ? `${distributedEngineNames(item).join('、')}（${distributedCount(item)} 个引擎）`
        : '未分发',
  });
  if (entry.unknownKeys?.length) {
    items.push({ label: '未知键', value: `${entry.unknownKeys.length} 个（应用未识别，写入时原样保留）` });
  }
  return items;
});

// --- env/headers 敏感值（MaskedValue；键明文、值打码） ---
const secretGroups = computed<{ label: string; pairs: { key: string; value: string }[] }[]>(() => {
  const item = activeItem.value;
  if (!item) return [];
  const groups: { label: string; pairs: { key: string; value: string }[] }[] = [];
  const env = Object.entries(item.entry.env ?? {});
  if (env.length) groups.push({ label: '环境变量（env）', pairs: env.map(([key, value]) => ({ key, value })) });
  const headers = Object.entries(item.entry.headers ?? {});
  if (headers.length) {
    groups.push({ label: '请求头（headers）', pairs: headers.map(([key, value]) => ({ key, value })) });
  }
  return groups;
});

// --- 分发矩阵（条目 × 引擎；行序 = 后端 enginesMeta：四引擎 + omp） ---
const matrixRows = computed(() => {
  const item = activeItem.value;
  if (!item) return [];
  return enginesMeta.value.map((meta) => ({ meta, cell: cellOf(item, meta) }));
});

function statusBadge(cell: SharedMcpEngineCell): PluginBadgeDescriptor {
  switch (cell.status) {
    case 'distributed':
      return { text: '已分发', type: 'type', color: 'command' };
    case 'drift':
      return { text: '有更新', type: 'type', color: 'warning' };
    case 'conflict':
      return { text: '同名冲突', type: 'warning' };
    case 'unsupported':
      return { text: '该引擎不支持此形态', type: 'type', color: 'unknown' };
    case 'excluded':
      return { text: '裁定排除', type: 'type', color: 'plugin' };
    default:
      return { text: '未分发', type: 'type', color: 'capability' };
  }
}

/** Switch 可用性（契约 §4.2 无死角裁定）：conflict / unsupported 且未分发 → 禁用；其余可点 */
function switchDisabled(cell: SharedMcpEngineCell): boolean {
  if (cell.status === 'conflict') return true;
  return cell.status === 'unsupported' && !cell.selected;
}

function cellReason(row: { meta: SharedMcpEngineMeta; cell: SharedMcpEngineCell }): string {
  if (row.cell.reason) return row.cell.reason;
  if (row.meta.support === 'excluded') return row.meta.detail ?? '';
  return '';
}

// --- 矩阵操作：开启=直接分发（显式动作）；关闭=确认后移除；外改 → danger force 重试 ---
function toggleEngine(item: SharedMcpEntryOverview, meta: SharedMcpEngineMeta, value: boolean) {
  if (value) {
    void runEngineSelection(item, meta, true);
    return;
  }
  pendingUndistribute.value = { item, meta };
  showUndistributeDialog.value = true;
}

const undistributeConfirmMessage = computed(() => {
  const pending = pendingUndistribute.value;
  if (!pending) return '';
  return `停止向${pending.meta.name}分发「${pending.item.entry.name}」？将移除已写入各引擎配置的托管条目（${pending.meta.name} 的 MCP 配置中该条目会被删除）。可重新打开开关再次分发。`;
});

async function confirmUndistribute() {
  const pending = pendingUndistribute.value;
  if (!pending) return;
  showUndistributeDialog.value = false;
  pendingUndistribute.value = null;
  await runEngineSelection(pending.item, pending.meta, false);
}

/** drift 格的行内「重新分发」= 幂等 resync（契约 §2.3） */
function resyncEngine(item: SharedMcpEntryOverview, meta: SharedMcpEngineMeta) {
  void runEngineSelection(item, meta, true);
}

async function runEngineSelection(
  item: SharedMcpEntryOverview,
  meta: SharedMcpEngineMeta,
  selected: boolean,
) {
  const name = item.entry.name;
  try {
    await setEngineSelection(name, meta.id, selected, false);
    showSuccess(
      selected
        ? `已分发到 ${meta.name}（分发≠连通，请以各 CLI 实际加载为准）`
        : `已停止向 ${meta.name} 分发`,
    );
  } catch (err) {
    console.error('[SharedMcpPanel] Engine selection failed:', err);
    if (isForeignModifiedError(err)) {
      pendingForeign.value = { name, engine: meta.id, engineName: meta.name, selected };
      showForeignDialog.value = true;
      return;
    }
    showError(`引擎分发操作失败：${err instanceof Error ? err.message : String(err)}`);
  }
}

const foreignConfirmMessage = computed(() => {
  if (pendingForeign.value) {
    const pending = pendingForeign.value;
    return `${pending.engineName} 中的 ${pending.name} 已被外部修改，继续将丢失这些修改，确认继续？`;
  }
  if (pendingForeignDelete.value) {
    return `有引擎中的条目已被外部修改，继续删除「${pendingForeignDelete.value}」将丢失这些修改，确认继续？`;
  }
  return '';
});

/** 外改 danger 确认：以 force=true 重试原操作（引擎开关或删除） */
async function confirmForeignRemove() {
  const foreign = pendingForeign.value;
  const deleteName = pendingForeignDelete.value;
  pendingForeign.value = null;
  pendingForeignDelete.value = null;
  showForeignDialog.value = false;
  try {
    if (foreign) {
      await setEngineSelection(foreign.name, foreign.engine, foreign.selected, true);
      showSuccess(foreign.selected ? `已强制重新分发到 ${foreign.engineName}` : `已强制停止向 ${foreign.engineName} 分发`);
      return;
    }
    if (deleteName) {
      await remove(deleteName, true);
      showSuccess(`已删除「${deleteName}」`);
      if (activeName.value === deleteName) activeName.value = null;
    }
  } catch (err) {
    console.error('[SharedMcpPanel] Force retry failed:', err);
    showError(`强制执行失败：${err instanceof Error ? err.message : String(err)}`);
  }
}

// --- 删除：登记永久移除 + 已分发引擎同步还原（danger 确认，文案契约） ---
function requestDelete(item: SharedMcpEntryOverview) {
  pendingDelete.value = item;
  showDeleteDialog.value = true;
}

const deleteConfirmMessage = computed(() => {
  const item = pendingDelete.value;
  if (!item) return '';
  const count = distributedCount(item);
  const suffix =
    count > 0
      ? `，并从已分发的 ${count} 个引擎配置中同步移除——将移除已写入各引擎配置的托管条目`
      : '';
  return `删除「${item.entry.name}」？将从登记文件永久移除该条目${suffix}。此操作不可恢复。`;
});

async function confirmDelete() {
  const item = pendingDelete.value;
  if (!item) return;
  showDeleteDialog.value = false;
  pendingDelete.value = null;
  const name = item.entry.name;
  try {
    await remove(name, false);
    showSuccess(`已删除「${name}」`);
    if (activeName.value === name) activeName.value = null;
  } catch (err) {
    console.error('[SharedMcpPanel] Delete failed:', err);
    if (isForeignModifiedError(err)) {
      // 契约 §4.2：某引擎返回外改 → 追加二次 danger 确认后以 force=true 重试
      pendingForeignDelete.value = name;
      showForeignDialog.value = true;
      return;
    }
    showError(`删除失败：${err instanceof Error ? err.message : String(err)}`);
  }
}

// --- 新增/编辑表单（stdio 与远程双形态；值输入框密码态不回显明文） ---
interface KvRow {
  key: string;
  value: string;
}

const showFormDialog = ref(false);
const formSubmitting = ref(false);
const formError = ref('');
const editingName = ref<string | null>(null);
const formName = ref('');
const formType = ref<'stdio' | 'http'>('stdio');
const formCommand = ref('');
const formArgsText = ref('');
const formUrl = ref('');
const formDescription = ref('');
const formKvRows = ref<KvRow[]>([]);

function addKvRow() {
  formKvRows.value.push({ key: '', value: '' });
}

function removeKvRow(index: number) {
  formKvRows.value.splice(index, 1);
}

function openCreateForm() {
  editingName.value = null;
  formName.value = '';
  formType.value = 'stdio';
  formCommand.value = '';
  formArgsText.value = '';
  formUrl.value = '';
  formDescription.value = '';
  formKvRows.value = [{ key: '', value: '' }];
  formError.value = '';
  showFormDialog.value = true;
}

function openEditForm(item: SharedMcpEntryOverview) {
  const entry = item.entry;
  editingName.value = entry.name;
  formName.value = entry.name;
  formType.value = entry.type === 'http' ? 'http' : 'stdio';
  formCommand.value = entry.command ?? '';
  formArgsText.value = (entry.args ?? []).join('\n');
  formUrl.value = entry.url ?? '';
  formDescription.value = entry.description ?? '';
  const pairs = Object.entries(entry.type === 'http' ? entry.headers ?? {} : entry.env ?? {});
  formKvRows.value = pairs.length ? pairs.map(([key, value]) => ({ key, value })) : [{ key: '', value: '' }];
  formError.value = '';
  showFormDialog.value = true;
}

// 提交前基础校验（名称字符集 / 必填项 / 键值对完整性；后端仍兜底校验）
function validateForm(): string {
  if (!/^[A-Za-z0-9_-]+$/.test(formName.value.trim())) {
    return '名称仅支持字母、数字、- 与 _（不含点与路径分隔符，否则无法分发到各引擎）';
  }
  if (formType.value === 'stdio' && !formCommand.value.trim()) {
    return 'stdio 形态需填写命令（command）';
  }
  if (formType.value === 'http') {
    const url = formUrl.value.trim();
    if (!url) return '远程形态需填写 URL';
    try {
      const parsed = new URL(url);
      if (!parsed.host) return '远程形态的 URL 需包含主机名';
    } catch {
      return '远程形态的 URL 不是合法地址';
    }
  }
  const keys = new Set<string>();
  for (const row of formKvRows.value) {
    const key = row.key.trim();
    const value = row.value;
    if (!key && !value) continue; // 全空行静默跳过
    if (!key) return '键值对存在键名为空的行';
    if (keys.has(key)) return `键值对存在重复的键：${key}`;
    keys.add(key);
  }
  return '';
}

function kvRowsToRecord(): Record<string, string> {
  const record: Record<string, string> = {};
  for (const row of formKvRows.value) {
    const key = row.key.trim();
    if (!key) continue;
    record[key] = row.value;
  }
  return record;
}

async function submitForm() {
  if (formSubmitting.value) return;
  formError.value = validateForm();
  if (formError.value) return;

  const entry: SharedMcpEntry = {
    name: formName.value.trim(),
    type: formType.value,
    description: formDescription.value.trim() || undefined,
  };
  if (formType.value === 'stdio') {
    entry.command = formCommand.value.trim();
    const args = formArgsText.value
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line.length > 0);
    if (args.length) entry.args = args;
    const env = kvRowsToRecord();
    if (Object.keys(env).length) entry.env = env;
  } else {
    entry.url = formUrl.value.trim();
    const headers = kvRowsToRecord();
    if (Object.keys(headers).length) entry.headers = headers;
  }

  formSubmitting.value = true;
  try {
    if (editingName.value) {
      await update(editingName.value, entry);
      showSuccess(`已更新「${entry.name}」（已分发引擎如有变化将显示「有更新」）`);
    } else {
      await create(entry);
      showSuccess(`已新增「${entry.name}」`);
      activeName.value = entry.name;
    }
    showFormDialog.value = false;
  } catch (err) {
    console.error('[SharedMcpPanel] Submit form failed:', err);
    formError.value = err instanceof Error ? err.message : String(err);
  } finally {
    formSubmitting.value = false;
  }
}

// --- 打开登记目录（复用 OpenFileInEditor 通用门面：目录经系统默认程序=文件管理器打开） ---
async function handleOpenRegistryDir() {
  if (!registryPath.value) return;
  try {
    await sharedMcpApi.openSharedMcpRegistryDir(registryPath.value);
  } catch (err) {
    console.error('[SharedMcpPanel] Open registry dir failed:', err);
    showError(`打开登记目录失败：${err instanceof Error ? err.message : String(err)}`);
  }
}

async function handleRefresh() {
  await load();
}

// 首载：store 已加载过则不重复（切走再切回不重复加载，靠 store 状态）
onMounted(() => {
  if (!loaded.value) {
    void load();
  }
});

// 测试契约（面板范式，契约 §4.2）：expose 触发器；表单字段 ref 一并暴露供
// 测试注入输入（ExtensionsView marketSource 同先例）
defineExpose({
  selectEntry,
  clearActive,
  selectTab,
  onSearch,
  openCreateForm,
  openEditForm,
  submitForm,
  requestDelete,
  confirmDelete,
  toggleEngine,
  confirmUndistribute,
  resyncEngine,
  confirmForeignRemove,
  handleOpenRegistryDir,
  handleRefresh,
  formName,
  formType,
  formCommand,
  formArgsText,
  formUrl,
  formDescription,
  formKvRows,
  formError,
});
</script>

<style scoped>
.shared-mcp-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* 工具栏下区分副标题：负 margin 贴住工具栏（14px 面板间距不放大） */
.panel-hint {
  margin: -6px 0 0;
  font-size: 12px;
  color: var(--tertiary);
  line-height: 1.5;
}

/* 一级列表态：全宽列表。描述完整多行显示（覆盖 PluginRow 单行省略号，
   ui/ 共享件本期不可改，:deep 覆写在消费侧落地）；整行可点入详情 */
.mcp-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.mcp-row {
  display: flex;
  align-items: center;
  gap: 10px;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background 0.12s;
}

.mcp-row:hover {
  background: var(--control);
}

.mcp-row :deep(.plugin-row) {
  flex: 1;
  min-width: 0;
  align-items: flex-start;
}

.mcp-row :deep(.plugin-row-desc) {
  white-space: normal;
  overflow: visible;
  word-break: break-word;
}

/* 行尾「已分发 N」轻量徽章（灰底轻量，分发≠连通，绝不用连接类措辞） */
.mcp-row-dist {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
}

/* 二级详情态：居中窄单列（max-width 720px，左右留白），页面自然滚动 */
.mcp-detail-page {
  width: 100%;
  max-width: 720px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.back-btn {
  align-self: flex-start;
  border: none;
  background: none;
  padding: 2px 0;
  font-size: 13px;
  color: var(--accent);
  cursor: pointer;
  font-family: inherit;
}

.back-btn:hover {
  text-decoration: underline;
}

.detail-desc {
  margin: 0;
  font-size: 13px;
  color: var(--secondary);
  line-height: 1.6;
}

.detail-meta {
  padding-top: 12px;
  border-top: 1px solid var(--separator);
}

/* env/headers 脱敏值区 */
.secret-groups {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.secret-group {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.secret-group-head {
  font-size: 11px;
  color: var(--tertiary);
}

.secret-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 3px 0;
}

.secret-key {
  font-family: var(--mono);
  font-size: 12px;
  color: var(--label);
  background: var(--control);
  border-radius: 4px;
  padding: 2px 6px;
}

/* 分发矩阵 */
.matrix {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-top: 12px;
  border-top: 1px solid var(--separator);
}

.matrix-head {
  font-size: 11px;
  font-weight: 600;
  color: var(--tertiary);
  letter-spacing: 0.4px;
}

.matrix-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 10px;
  border: 1px solid var(--separator);
  border-radius: 10px;
}

.matrix-engine {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
}

.matrix-engine-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
}

.matrix-engine-path {
  font-family: var(--mono);
  font-size: 11px;
  color: var(--tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.matrix-cell {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.matrix-reason {
  margin: 0;
  font-size: 12px;
  color: var(--secondary);
  line-height: 1.5;
}

.link-btn {
  background: none;
  border: none;
  padding: 0;
  font-size: 12px;
  color: var(--accent);
  cursor: pointer;
  font-family: inherit;
}

.link-btn:hover {
  text-decoration: underline;
}

/* 表单 */
.mcp-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-group label {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

.form-input {
  padding: 8px 12px;
  font-size: 13px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  transition: border-color 0.15s;
  font-family: inherit;
}

.form-input.mono {
  font-family: var(--mono);
}

.form-input:focus {
  outline: none;
  border-color: var(--accent);
}

.form-input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

textarea.form-input {
  resize: vertical;
}

.type-toggle {
  display: inline-flex;
  gap: 6px;
}

.type-btn {
  padding: 7px 14px;
  font-size: 13px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--secondary);
  cursor: pointer;
  font-family: inherit;
  transition: all 0.12s;
}

.type-btn.active {
  border-color: var(--accent);
  color: var(--accent);
  background: rgba(0, 122, 255, 0.08);
}

.kv-editor {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.kv-row {
  display: grid;
  grid-template-columns: 1fr 1fr auto;
  gap: 6px;
  align-items: center;
}

.kv-remove {
  width: 26px;
  height: 26px;
  border: none;
  border-radius: 6px;
  background: var(--control);
  color: var(--secondary);
  cursor: pointer;
  font-size: 14px;
  line-height: 1;
}

.kv-remove:hover {
  color: var(--warning);
}

.kv-add {
  align-self: flex-start;
  background: none;
  border: none;
  padding: 2px 0;
  font-size: 12px;
  color: var(--accent);
  cursor: pointer;
  font-family: inherit;
}

.kv-add:hover {
  text-decoration: underline;
}

.form-error {
  margin: 0;
  font-size: 12px;
  color: var(--danger, #ff3b30);
  line-height: 1.5;
}
</style>
