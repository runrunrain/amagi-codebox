<!-- 契约 §6.1（Claude）/§6.2（Codex）双引擎插件管理面板，四段式布局：
     ① DiagnosticBanner（有错误/警告才出现）→ ② CountTabs（资源计数标签 + 搜索）→
     ③ ListToolbar（「已安装 N」+ 刷新/浏览插件市场/添加▾）→ ④ 内容区
     （列表态：按市场分组 PluginRow + 右侧详情列；MCP 态：Claude 独有资源页；
      市场态：PluginMarketPanel；空态：EmptyStateCard）。
     事件契约（§9 显式变更 ①④）：emit 恒为 'add_market'（ExtensionsView 监听 @add_market
     编译为 prop onAdd_market，camelCase 不命中）；市场视图转发监听同为 @add_market。
     首屏 Loading/Error 门控为 store 派生（契约 §8：SSR 测试不执行 onMounted）。 -->
<template>
  <div class="plugin-installed-panel">
    <LoadingState v-if="showInitialLoading" message="加载插件中..." />

    <ErrorState
      v-else-if="showInitialError"
      :message="loadError"
      :on-retry="handleRetry"
    />

    <template v-else>
      <!-- ① 诊断横幅：errors=引擎加载错误（有数据后的降级形态）；warnings=Codex 诊断/Claude MCP 惰性加载失败 -->
      <DiagnosticBanner :errors="diagnosticErrors" :warnings="diagnosticWarnings" />

      <!-- ② 资源计数标签（Claude：插件|MCP|市场；Codex：插件|市场）+ 当前标签搜索框 -->
      <CountTabs
        :tabs="countTabs"
        :model-value="activeTabKey"
        :search-placeholder="searchPlaceholder"
        :search-value="searchText"
        @update:model-value="selectTab"
        @update:search-value="onSearch"
      />

      <!-- 市场态：PluginMarketPanel（添加市场经 @add_market 转发，§9 显式变更 ④） -->
      <PluginMarketPanel
        v-if="isMarketView"
        :engine="engine"
        @add_market="handleAddMarket"
        @back="backToInstalled"
      />

      <template v-else>
        <!-- ③ 工具栏 -->
        <ListToolbar :title="toolbarTitle" :count="toolbarCount" :loading="isRefreshing">
          <template #actions>
            <AppButton
              variant="ghost"
              size="small"
              :disabled="isRefreshing"
              @click="handleRefresh"
            >
              {{ isRefreshing ? '刷新中…' : '刷新' }}
            </AppButton>
            <AppButton
              variant="outline"
              size="small"
              @click="browseMarket"
            >
              浏览插件市场
            </AppButton>
            <ActionMenu
              trigger-text="添加"
              :items="addActionItems"
              @select="handleAddSelect"
            />
          </template>
        </ListToolbar>

        <!-- ④ MCP 资源页（Claude 独有，§6.1.1） -->
        <div v-if="isMcpView" class="mcp-view">
          <!-- 与「公共 → 共用 MCP」子页的区分说明（跨面板一致性）：本页只读展示插件自带 MCP，用户级登记/分发走共用 MCP -->
          <p class="mcp-view-hint">
            只读展示已启用 Claude 插件自带的 MCP 服务器；用户级登记与跨引擎分发请在「公共 → 共用 MCP」子页管理。
          </p>
          <EmptyStateCard
            v-if="mcpGroups.length === 0"
            title="未发现 MCP 服务器"
            description="已启用插件中没有可展示的 MCP 服务器"
          />
          <template v-else>
            <div v-for="group in mcpGroups" :key="group.pluginId" class="mcp-group">
              <div class="mcp-group-click" @click="openPluginDetail(group.pluginId)">
                <GroupHeader :title="group.name" :count="group.servers.length" />
              </div>
              <div v-for="server in group.servers" :key="`${group.pluginId}:${server}`" class="mcp-row">
                <PluginIcon :name="server" size="sm" />
                <div class="mcp-main">
                  <span class="mcp-name">{{ server }}</span>
                  <span class="mcp-desc">Model Context Protocol 服务器</span>
                </div>
                <span
                  :class="['mcp-dot', group.enabled ? 'on' : 'off']"
                  :title="group.enabled ? '所属插件已启用' : '所属插件已禁用'"
                />
              </div>
            </div>
          </template>
        </div>

        <!-- ④ 列表态 -->
        <template v-else>
          <EmptyStateCard v-if="listedPlugins.length === 0" :title="emptyTitle" :description="emptyDesc">
            <template #primary>
              <AppButton variant="primary" size="small" @click="browseMarket">
                浏览插件市场
              </AppButton>
            </template>
            <template #secondary>
              <AppButton variant="ghost" size="small" @click="handleAddMarket">
                添加市场
              </AppButton>
            </template>
          </EmptyStateCard>

          <div v-else class="ex-split">
            <!-- 左列：按市场分组的行式列表 -->
            <div class="plugin-list">
              <template v-for="group in groupedPlugins" :key="group.marketplace">
                <GroupHeader :title="group.marketplace" :count="group.plugins.length">
                  <template #actions>
                    <AppButton
                      v-if="group.marketplace !== 'Unknown'"
                      variant="ghost"
                      size="small"
                      :disabled="isMarketUpdating(group.marketplace)"
                      @click.stop="handleUpdateMarket(group.marketplace)"
                    >
                      {{ isMarketUpdating(group.marketplace) ? '更新中…' : '更新市场' }}
                    </AppButton>
                  </template>
                </GroupHeader>
                <PluginRow
                  v-for="plugin in group.plugins"
                  :key="plugin.id"
                  :name="plugin.name"
                  :description="plugin.description"
                  :badges="rowBadges(plugin)"
                  :enabled="plugin.enabled"
                  switch-visible
                  :active="activePluginId === plugin.id"
                  @click="selectPlugin(plugin)"
                  @toggle="(value) => handleToggle(plugin, value)"
                />
              </template>
            </div>

            <!-- 右列：详情 -->
            <div v-if="currentActivePlugin" class="plg-detail">
              <DetailHeader
                :breadcrumb="[{ label: '插件', clickable: true }, { label: currentActivePlugin.name }]"
                :title="detailTitle"
                :badges="detailBadges"
                @back="clearActive"
              >
                <template #actions>
                  <!-- 主操作语义按数据实情（§5.4）：禁用→「启用」；启用→「更新」 -->
                  <AppButton
                    v-if="!currentActivePlugin.enabled"
                    variant="primary"
                    size="small"
                    :disabled="updating"
                    @click="handleToggle(currentActivePlugin, true)"
                  >
                    启用
                  </AppButton>
                  <AppButton
                    v-else
                    variant="primary"
                    size="small"
                    :disabled="updating || updateDisabled"
                    :title="updateDisabled ? '该插件缺少市场来源信息，无法更新' : undefined"
                    @click="handleUpdate"
                  >
                    {{ updating ? '更新中…' : '更新' }}
                  </AppButton>
                  <AppButton variant="danger" size="small" :disabled="updating" @click="handleUninstall">
                    卸载
                  </AppButton>
                </template>
              </DetailHeader>

              <p v-if="detailDescription" class="detail-desc">{{ detailDescription }}</p>

              <!-- Codex 重复诊断详情（§6.2） -->
              <div
                v-if="engine === 'codex' && hasDuplicateWarning(currentActivePlugin)"
                class="duplicate-diagnostic-card"
              >
                <strong>重复诊断</strong>
                <p>{{ getDuplicateWarning(currentActivePlugin) }}</p>
              </div>

              <!-- 子项列表（Claude 可切换 / Codex 只读，§5.3） -->
              <div v-if="hasManageableSubItems" class="detail-subitems">
                <PluginSubItemsPanel
                  :engine="engine"
                  :plugin-detail="subItemsDetail"
                  @toggle-sub-item="handleToggleSubItem"
                />
              </div>

              <!-- 元信息（§5.2） -->
              <MetaGrid v-if="metaItems.length > 0" :items="metaItems" class="detail-meta" />
            </div>

            <div v-else class="plg-detail-empty">
              <EmptyState icon="◇" title="选择插件" description="点击左侧插件查看详细信息" />
            </div>
          </div>
        </template>
      </template>

      <!-- 卸载确认 -->
      <ConfirmDialog
        v-model:open="showUninstallDialog"
        title="确认卸载"
        :message="pluginToUninstall ? `确定要卸载插件「${pluginToUninstall.name}」吗？此操作不可恢复。` : ''"
        danger
        confirm-text="卸载"
        @confirm="confirmUninstall"
      />

      <!-- 手动安装（按名称/ID） -->
      <Dialog v-model:open="showManualInstall" :title="manualInstallTitle" :description="manualInstallDesc">
        <div class="manual-install-form">
          <div class="form-field">
            <label>{{ engine === 'claude' ? '插件名称' : 'Plugin ID' }}</label>
            <TextInput
              v-model="manualPluginKey"
              :placeholder="engine === 'claude' ? '例：superplugin' : '例：name@marketplace'"
              mono
            />
          </div>
          <div v-if="engine === 'codex'" class="form-field">
            <label>市场名（可选）</label>
            <TextInput v-model="manualMarketName" placeholder="缺省时由后端解析" />
          </div>
        </div>
        <template #footer>
          <AppButton variant="ghost" @click="showManualInstall = false">取消</AppButton>
          <AppButton
            variant="primary"
            :disabled="!manualPluginKey.trim() || manualInstalling"
            @click="submitManualInstall"
          >
            {{ manualInstalling ? '安装中…' : '安装' }}
          </AppButton>
        </template>
      </Dialog>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import { storeToRefs } from 'pinia';
import { usePluginStore } from '../../stores/plugin';
import AppButton from '../ui/AppButton.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import Dialog from '../ui/Dialog.vue';
import EmptyState from '../ui/EmptyState.vue';
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import TextInput from '../ui/TextInput.vue';
import ActionMenu from './ui/ActionMenu.vue';
import CountTabs from './ui/CountTabs.vue';
import DetailHeader from './ui/DetailHeader.vue';
import DiagnosticBanner from './ui/DiagnosticBanner.vue';
import EmptyStateCard from './ui/EmptyStateCard.vue';
import GroupHeader from './ui/GroupHeader.vue';
import ListToolbar from './ui/ListToolbar.vue';
import MetaGrid from './ui/MetaGrid.vue';
import PluginIcon from './ui/PluginIcon.vue';
import PluginRow from './ui/PluginRow.vue';
import PluginMarketPanel from './PluginMarketPanel.vue';
import PluginSubItemsPanel from './PluginSubItemsPanel.vue';
import { useToast } from '../../composables/useToast';
import type {
  ActionMenuItem,
  CountTabItem,
  InstalledPluginView,
  MetaItemView,
  PluginBadgeDescriptor,
  PluginDetailView,
} from './pluginViewTypes';

interface Props {
  engine?: 'claude' | 'codex';
}

const props = withDefaults(defineProps<Props>(), {
  engine: 'claude',
});

// 事件名必须带下划线（契约 §0/§9 显式变更 ①）：ExtensionsView 监听 @add_market
// 编译为 prop onAdd_market，仅 emit('add_market') 命中，camelCase 为断链死事件
const emit = defineEmits<{
  (e: 'add_market', engine: 'claude' | 'codex'): void;
}>();

const pluginStore = usePluginStore();
const {
  ccInstalled,
  ccMarkets,
  ccInstalledCount,
  ccMcpPluginCount,
  ccLoadError,
  loadingCC,
  ccActivePluginDetail,
  activePluginId,
  activePlugin: storeActivePlugin,
  cxInstalled,
  cxMarkets,
  cxInstalledCount,
  cxWarnings,
  cxDuplicates,
  cxLoadError,
  loadingCX,
  activeCxPlugin,
  activeCxPluginDetail,
  pluginView,
} = storeToRefs(pluginStore);

const {
  setActivePlugin,
  togglePlugin,
  uninstallPlugin,
  updatePlugin,
  updateCcMarketplace,
  refreshCc,
  loadCcAllData,
  loadPluginDetail,
  loadCxPlugins,
  loadCxPluginDetail,
  toggleCxPlugin,
  uninstallCxPlugin,
  upgradeCxMarketplace,
  installCcPlugin,
  installCxPlugin,
  setPluginSubItemEnabled,
  setPluginView,
} = pluginStore;

const { showSuccess, showError, showInfo } = useToast();

// --- 本地面板状态（视图机/对话框/mutating，契约 §6.1：门控类状态一律 store 派生） ---
// MCP 资源页为面板本地态（契约 §2：pluginView 值域保持 'installed'|'market' 不变）
const resourceTab = ref<'plugins' | 'mcp'>('plugins');
const searchText = ref('');
const showUninstallDialog = ref(false);
const pluginToUninstall = ref<InstalledPluginView | null>(null);
const updating = ref(false);
const manualInstalling = ref(false);
const showManualInstall = ref(false);
const manualPluginKey = ref('');
const manualMarketName = ref('');
// MCP 惰性加载失败的单项警告（契约 §6.1.1/风险 3：静默跳过并计入诊断 warnings）
const mcpLoadWarnings = ref<string[]>([]);
// 市场分组头部「更新市场」的 per-market loading
const updatingMarkets = ref<Record<string, boolean>>({});

// 引擎切换时重置面板本地资源页（契约 §2）
watch(
  () => props.engine,
  () => {
    resourceTab.value = 'plugins';
    searchText.value = '';
  },
);

// --- 首屏门控（store 派生，契约 §6.1：SSR 测试不执行 onMounted） ---
const installedList = computed<InstalledPluginView[]>(() =>
  props.engine === 'claude' ? ccInstalled.value : cxInstalled.value
);

const loadError = computed(() =>
  props.engine === 'claude' ? ccLoadError.value : cxLoadError.value
);

const isLoading = computed(() =>
  props.engine === 'claude' ? loadingCC.value : loadingCX.value
);

const showInitialLoading = computed(
  () => isLoading.value && installedList.value.length === 0 && !loadError.value
);

const showInitialError = computed(() => !!loadError.value && installedList.value.length === 0);

// --- ① 诊断横幅数据源 ---
const diagnosticErrors = computed(() => (loadError.value ? [loadError.value] : []));

// Codex：cxWarnings 逐条 + 重复诊断摘要（§6.2）；Claude：恒空 + MCP 惰性加载失败
const diagnosticWarnings = computed(() => {
  if (props.engine === 'claude') {
    return mcpLoadWarnings.value;
  }
  const warnings = [...cxWarnings.value];
  if (cxDuplicates.value.length > 0) {
    const names = cxDuplicates.value
      .map((group) => group[0]?.name)
      .filter(Boolean)
      .join('、');
    warnings.push(`检测到 ${cxDuplicates.value.length} 组重复插件：${names}`);
  }
  return warnings;
});

// --- ② 资源计数标签 ---
const countTabs = computed<CountTabItem[]>(() => {
  if (props.engine === 'claude') {
    return [
      { key: 'plugins', label: '插件', count: ccInstalledCount.value },
      { key: 'mcp', label: 'MCP', count: ccMcpPluginCount.value },
      { key: 'market', label: '市场', count: ccMarkets.value.length },
    ];
  }
  return [
    { key: 'plugins', label: '插件', count: cxInstalledCount.value },
    { key: 'market', label: '市场', count: cxMarkets.value.length },
  ];
});

const isMarketView = computed(() => pluginView.value === 'market');
const isMcpView = computed(() => props.engine === 'claude' && resourceTab.value === 'mcp');

const activeTabKey = computed(() =>
  isMarketView.value ? 'market' : isMcpView.value ? 'mcp' : 'plugins'
);

// 搜索框（市场态由 PluginMarketPanel 自带搜索，CountTabs 搜索框隐藏）
const searchPlaceholder = computed(() => {
  if (isMarketView.value) return '';
  return isMcpView.value ? '搜索服务器或所属插件' : '搜索插件名称或描述';
});

function selectTab(key: string) {
  if (key === 'market') {
    // 面板切到市场标签时同步 setPluginView('market')（契约 §2：保持既有 store 兼容）
    setPluginView('market');
    searchText.value = '';
    return;
  }
  setPluginView('installed');
  if (props.engine === 'claude') {
    resourceTab.value = key === 'mcp' ? 'mcp' : 'plugins';
    if (resourceTab.value === 'mcp') {
      void loadMcpDetails();
    }
  }
  searchText.value = '';
}

function onSearch(value: string) {
  searchText.value = value;
}

function browseMarket() {
  setPluginView('market');
}

function backToInstalled() {
  setPluginView('installed');
}

// --- ③ 工具栏 ---
const addActionItems = computed<ActionMenuItem[]>(() => [
  { key: 'add-market', label: '添加市场' },
  { key: 'manual-install', label: '手动安装' },
]);

function handleAddSelect(key: string) {
  if (key === 'add-market') {
    handleAddMarket();
  } else if (key === 'manual-install') {
    openManualInstall();
  }
}

const isRefreshing = computed(() => isLoading.value);

async function handleRefresh() {
  if (isRefreshing.value) return;
  try {
    if (props.engine === 'claude') {
      await refreshCc();
    } else {
      await loadCxPlugins(true);
    }
  } catch (error) {
    // 失败信息已由 store 写入 ccLoadError/cxLoadError，由横幅/ErrorState 呈现
    console.error('[PluginInstalledPanel] Refresh failed:', error);
  }
}

async function handleRetry() {
  if (props.engine === 'claude') {
    await loadCcAllData();
  } else {
    await loadCxPlugins(true);
  }
}

// --- ④ 列表态：搜索过滤 + 按市场分组 ---
const listedPlugins = computed<InstalledPluginView[]>(() => {
  const query = searchText.value.trim().toLowerCase();
  if (!query) return installedList.value;
  return installedList.value.filter(
    (p) =>
      p.name.toLowerCase().includes(query) ||
      // CodexPlugin 无 description（§5.1），可选链兜底即可覆盖两引擎
      (p.description?.toLowerCase().includes(query) ?? false)
  );
});

const groupedPlugins = computed(() => {
  const groups = new Map<string, InstalledPluginView[]>();
  listedPlugins.value.forEach((plugin) => {
    const marketplace = plugin.marketplace || 'Unknown';
    if (!groups.has(marketplace)) {
      groups.set(marketplace, []);
    }
    groups.get(marketplace)!.push(plugin);
  });
  return Array.from(groups.entries())
    .map(([marketplace, plugins]) => ({ marketplace, plugins }))
    .sort((a, b) => a.marketplace.localeCompare(b.marketplace));
});

const toolbarTitle = computed(() => (isMcpView.value ? 'MCP 服务器' : '已安装'));
const toolbarCount = computed(() =>
  isMcpView.value ? mcpGroups.value.reduce((sum, g) => sum + g.servers.length, 0) : listedPlugins.value.length
);

// --- ④ MCP 资源页（§6.1.1） ---
interface McpGroup {
  pluginId: string;
  name: string;
  enabled: boolean;
  servers: string[];
}

const mcpCandidates = computed(() =>
  ccInstalled.value.filter((p) => p.enabled && (p.pluginType === 'mcp' || p.hasMcp))
);

const mcpGroups = computed<McpGroup[]>(() => {
  const query = searchText.value.trim().toLowerCase();
  return mcpCandidates.value
    .map((p) => {
      const detail = ccActivePluginDetail.value[p.id];
      const servers = Object.keys(detail?.mcpServers || {});
      return { pluginId: p.id, name: p.name, enabled: p.enabled, servers };
    })
    .filter((group) => group.servers.length > 0)
    .map((group) => {
      if (!query) return group;
      const servers = group.servers.filter(
        (s) => s.toLowerCase().includes(query) || group.name.toLowerCase().includes(query)
      );
      return { ...group, servers };
    })
    .filter((group) => group.servers.length > 0);
});

// 进入 MCP 页时对无缓存详情的候选并发加载（纯本地读；失败单项静默跳过并计入 warnings）
async function loadMcpDetails() {
  const missing = mcpCandidates.value.filter((p) => !ccActivePluginDetail.value[p.id]);
  if (missing.length === 0) return;
  const results = await Promise.allSettled(
    missing.map((p) => loadPluginDetail(p.id))
  );
  const failures: string[] = [];
  results.forEach((result, index) => {
    if (result.status === 'rejected') {
      const err = result.reason;
      failures.push(
        `MCP 详情加载失败（${missing[index].name}）：${err instanceof Error ? err.message : String(err)}`
      );
    }
  });
  if (failures.length > 0) {
    mcpLoadWarnings.value = [...mcpLoadWarnings.value, ...failures];
  }
}

function openPluginDetail(pluginId: string) {
  resourceTab.value = 'plugins';
  setActivePlugin(pluginId);
  void loadPluginDetail(pluginId);
}

// --- 空态文案（§6.1/§6.2） ---
const emptyTitle = computed(() =>
  props.engine === 'claude' ? '暂无已安装插件' : '暂无已安装 Codex 插件'
);

const emptyDesc = computed(() =>
  props.engine === 'claude'
    ? '从插件市场安装 Claude 插件后将显示在此处'
    : '从插件市场安装 Codex 插件后将显示在此处'
);

// --- 行字段映射（§5.1） ---
function pluginTypeLabel(plugin: InstalledPluginView): string {
  const type = plugin.pluginType || 'unknown';
  const labels: Record<string, string> = {
    integration: '集成',
    hybrid: '混合',
    skill: 'Skill',
    hook: 'Hook',
    command: 'Command',
    agent: 'Agent',
    unknown: '未知',
  };
  return labels[type] || type;
}

function rowBadges(plugin: InstalledPluginView): PluginBadgeDescriptor[] {
  const badges: PluginBadgeDescriptor[] = [];
  if (props.engine === 'claude') {
    badges.push({ text: pluginTypeLabel(plugin), type: 'type', color: plugin.pluginType || 'unknown' });
    if (plugin.marketplace) {
      badges.push({ text: plugin.marketplace, type: 'source', variant: 'muted' });
    }
  } else {
    if (plugin.source) {
      badges.push({ text: plugin.source, type: 'source', variant: 'muted' });
    }
    if (hasDuplicateWarning(plugin)) {
      badges.push({ text: '重复诊断', type: 'warning', color: 'warning' });
    }
  }
  if (plugin.version) {
    badges.push({ text: `v${plugin.version}`, type: 'ver' });
  }
  return badges;
}

// --- 详情态（§5.2） ---
const currentActivePlugin = computed<InstalledPluginView | null>(() =>
  props.engine === 'codex' ? activeCxPlugin.value : storeActivePlugin.value
);

const activeCxDetail = computed<PluginDetailView | null>(() =>
  props.engine === 'codex' ? (activeCxPluginDetail.value as unknown as PluginDetailView | null) : null
);

const detailTitle = computed(() => {
  if (props.engine === 'codex') {
    return activeCxDetail.value?.displayName || currentActivePlugin.value?.name || '';
  }
  return currentActivePlugin.value?.name || '';
});

const detailDescription = computed(() => {
  if (props.engine === 'codex') {
    const detail = activeCxDetail.value;
    return (
      detail?.longDescription ||
      detail?.shortDescription ||
      detail?.manifest?.description ||
      ''
    );
  }
  return currentActivePlugin.value?.description || '';
});

const detailBadges = computed<PluginBadgeDescriptor[]>(() => {
  const plugin = currentActivePlugin.value;
  if (!plugin) return [];
  const badges: PluginBadgeDescriptor[] = [];
  if (props.engine === 'claude') {
    badges.push({ text: pluginTypeLabel(plugin), type: 'type', color: plugin.pluginType || 'unknown' });
  } else {
    if (plugin.source) {
      badges.push({ text: plugin.source, type: 'source', variant: 'muted' });
    }
    if (hasDuplicateWarning(plugin)) {
      badges.push({ text: '重复诊断', type: 'warning', color: 'warning' });
    }
  }
  if (plugin.version) {
    badges.push({ text: `v${plugin.version}`, type: 'ver' });
  }
  if (props.engine === 'claude' && plugin.scope) {
    badges.push({
      text: plugin.scope === 'global' ? '全局' : plugin.scope === 'project' ? '项目' : plugin.scope,
      type: 'scope',
    });
  }
  // Codex capabilities 徽章区并入 DetailHeader badges（§6.2）
  if (props.engine === 'codex') {
    (activeCxDetail.value?.manifest?.interface?.capabilities || []).forEach((cap) => {
      badges.push({ text: cap, type: 'type', color: 'capability' });
    });
  }
  return badges;
});

// --- 子项数据源（§5.3） ---
const subItemsDetail = computed<PluginDetailView | null>(() =>
  props.engine === 'codex' ? activeCxDetail.value : (storeActivePlugin.value as unknown as PluginDetailView | null)
);

const hasManageableSubItems = computed(() => {
  const detail = subItemsDetail.value;
  if (!detail) return false;
  return (
    (detail.subItems?.length || 0) > 0 ||
    (detail.skills?.length || 0) > 0 ||
    (detail.agents?.length || 0) > 0 ||
    (detail.commands?.length || 0) > 0 ||
    (detail.hooks?.length || 0) > 0 ||
    detail.hasMcp === true
  );
});

// --- MetaGrid（§5.2） ---
function formatDate(dateStr?: string): string {
  if (!dateStr) return '';
  const date = new Date(dateStr);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleDateString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  });
}

const metaItems = computed<MetaItemView[]>(() => {
  const plugin = currentActivePlugin.value;
  if (!plugin) return [];
  const items: MetaItemView[] = [];
  if (props.engine === 'codex') {
    items.push({ label: '状态', value: plugin.enabled ? '已启用' : '已禁用' });
  }
  if (plugin.marketplace) {
    items.push({ label: '来源市场', value: plugin.marketplace });
  }
  if (plugin.version) {
    items.push({ label: '版本', value: plugin.version });
  }
  if (props.engine === 'claude' && plugin.scope) {
    items.push({ label: '作用域', value: plugin.scope });
  }
  const installedAt = formatDate(plugin.installedAt);
  if (installedAt) {
    items.push({ label: '安装时间', value: installedAt });
  }
  const lastUpdated = formatDate(plugin.lastUpdated);
  if (lastUpdated) {
    items.push({ label: '最后更新', value: lastUpdated });
  }
  if (props.engine === 'claude' && plugin.gitCommitSha) {
    items.push({ label: '提交', value: plugin.gitCommitSha.slice(0, 8), mono: true });
  }
  if (props.engine === 'codex' && plugin.id) {
    items.push({ label: 'Plugin ID', value: plugin.id, mono: true });
  }
  return items;
});

// --- 重复诊断 helpers（§5.1/§6.2） ---
function hasDuplicateWarning(plugin: InstalledPluginView): boolean {
  return !!plugin.warning || !!plugin.duplicateOf;
}

function getDuplicateWarning(plugin: InstalledPluginView): string {
  const raw = plugin.warning || plugin.duplicateOf || '';
  if (!raw) return '';
  return /归并|duplicate|重复/i.test(raw) ? raw : `检测到重复安装记录：${raw}`;
}

// --- 动作 ---
async function selectPlugin(plugin: InstalledPluginView) {
  const id = plugin.id || '';
  setActivePlugin(id);
  if (props.engine === 'codex') {
    if (plugin.marketplace) {
      await loadCxPluginDetail(id, plugin.marketplace);
    }
  } else {
    await loadPluginDetail(id);
  }
}

function clearActive() {
  setActivePlugin(null);
}

// 开关语义警示（§5.4）：togglePlugin 第二参=「当前态」（true→disable），
// toggleCxPlugin 第二参=「目标态」——value 为 Switch 发出的目标态
async function handleToggle(plugin: InstalledPluginView, value: boolean) {
  try {
    if (props.engine === 'codex') {
      await toggleCxPlugin(plugin.id, value);
    } else {
      await togglePlugin(plugin.id, !value);
    }
  } catch (error) {
    console.error('[PluginInstalledPanel] Toggle plugin failed:', error);
    showError(`状态切换失败：${error instanceof Error ? error.message : String(error)}`);
  }
}

const updateDisabled = computed(() => props.engine === 'codex' && !currentActivePlugin.value?.marketplace);

async function handleUpdate() {
  const plugin = currentActivePlugin.value;
  if (!plugin || updating.value) return;

  updating.value = true;
  try {
    if (props.engine === 'codex') {
      // Codex 无单插件更新接口，更新机制是整个市场源 upgrade（§6.2）
      const marketplace = plugin.marketplace;
      if (!marketplace) {
        showError('该插件缺少市场来源信息，无法更新');
        return;
      }
      showInfo(`正在更新市场源「${marketplace}」…`);
      await upgradeCxMarketplace(marketplace);
      await loadCxPluginDetail(plugin.id, marketplace);
      showSuccess(`市场「${marketplace}」已更新，插件「${plugin.name}」内容已刷新`);
    } else {
      showInfo(`正在更新插件「${plugin.name}」…`);
      await updatePlugin(plugin.id);
      await loadPluginDetail(plugin.id);
      showSuccess(`插件「${plugin.name}」更新成功`);
    }
  } catch (error) {
    console.error('[PluginInstalledPanel] Update plugin failed:', error);
    showError(`更新失败: ${error instanceof Error ? error.message : String(error)}`);
  } finally {
    updating.value = false;
  }
}

function handleUninstall() {
  const plugin = currentActivePlugin.value;
  if (!plugin) return;
  pluginToUninstall.value = plugin;
  showUninstallDialog.value = true;
}

async function confirmUninstall() {
  const plugin = pluginToUninstall.value;
  if (!plugin) return;

  try {
    if (props.engine === 'codex') {
      await uninstallCxPlugin(plugin.id, plugin.marketplace);
    } else {
      await uninstallPlugin(plugin.id);
    }
  } catch (error) {
    console.error('[PluginInstalledPanel] Uninstall plugin failed:', error);
    showError(`卸载失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    showUninstallDialog.value = false;
    pluginToUninstall.value = null;
  }
}

async function handleUpdateMarket(market: string) {
  if (!market || market === 'Unknown') return;
  if (isMarketUpdating(market)) return;

  updatingMarkets.value = { ...updatingMarkets.value, [market]: true };
  try {
    showInfo(`正在更新市场「${market}」…`);
    if (props.engine === 'codex') {
      await upgradeCxMarketplace(market);
    } else {
      await updateCcMarketplace(market);
    }
    showSuccess(`市场「${market}」更新成功`);
  } catch (error) {
    console.error('[PluginInstalledPanel] Update marketplace failed:', error);
    showError(`市场更新失败: ${error instanceof Error ? error.message : String(error)}`);
  } finally {
    updatingMarkets.value = { ...updatingMarkets.value, [market]: false };
  }
}

function isMarketUpdating(market: string): boolean {
  return !!updatingMarkets.value[market];
}

// 子项开关（仅 Claude 真正落盘，§5.3）：统一走 store action 后回刷详情
async function handleToggleSubItem(itemType: string, itemId: string, enabled: boolean) {
  const plugin = currentActivePlugin.value;
  if (!plugin) return;

  try {
    await setPluginSubItemEnabled(plugin.id, itemType, itemId, enabled);
    if (props.engine === 'codex' && plugin.marketplace) {
      await loadCxPluginDetail(plugin.id, plugin.marketplace);
    } else if (props.engine === 'claude') {
      await loadPluginDetail(plugin.id);
    }
  } catch (error) {
    console.error('[PluginInstalledPanel] Toggle sub-item failed:', error);
    showError(`子项状态切换失败：${error instanceof Error ? error.message : String(error)}`);
  }
}

// 添加市场：单一入口，经 emit('add_market') 走 ExtensionsView 内联 Dialog（§6.6）
function handleAddMarket() {
  emit('add_market', props.engine);
}

// --- 手动安装对话框 ---
const manualInstallTitle = computed(() =>
  props.engine === 'claude' ? '安装 Claude 插件' : '安装 Codex 插件'
);

const manualInstallDesc = computed(() =>
  props.engine === 'claude'
    ? '输入插件名称，从默认市场安装'
    : '输入 Plugin ID（name@marketplace），可选指定市场名'
);

function openManualInstall() {
  manualPluginKey.value = '';
  manualMarketName.value = '';
  showManualInstall.value = true;
}

async function submitManualInstall() {
  const key = manualPluginKey.value.trim();
  if (!key || manualInstalling.value) return;

  manualInstalling.value = true;
  try {
    if (props.engine === 'claude') {
      await installCcPlugin(key);
    } else {
      await installCxPlugin(key, manualMarketName.value.trim() || undefined);
    }
    showSuccess(`插件「${key}」安装成功`);
    showManualInstall.value = false;
  } catch (error) {
    console.error('[PluginInstalledPanel] Manual install failed:', error);
    showError(`安装失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    manualInstalling.value = false;
  }
}

// 装载（onMounted 在 SSR 测试中不执行；门控已 store 派生）。
// ExtensionsView 的 Claude/Codex 挂载点带 key（claude/codex）：Claude⇄Codex 切换
// 经 key 强制重建面板实例，本 onMounted 随新实例执行 → 按引擎装载并重置到列表态（契约 §2）
onMounted(async () => {
  setPluginView('installed');
  try {
    if (props.engine === 'claude') {
      await loadCcAllData();
    } else {
      await loadCxPlugins();
    }
  } catch (error) {
    // 失败信息已由 store 写入 ccLoadError/cxLoadError，由门控/横幅呈现
    console.error('[PluginInstalledPanel] Load failed:', error);
  }
});

// 测试契约（§8）：expose 触发器
defineExpose({
  handleToggle,
  handleAddMarket,
  handleUpdate,
  handleUninstall,
  confirmUninstall,
  handleUpdateMarket,
  selectPlugin,
  selectTab,
  handleRefresh,
  openManualInstall,
  submitManualInstall,
  openPluginDetail,
  clearActive,
});
</script>

<style scoped>
.plugin-installed-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.ex-split {
  display: grid;
  grid-template-columns: 320px 1fr;
  gap: 14px;
  align-items: start;
}

.plugin-list {
  display: flex;
  flex-direction: column;
  max-height: calc(100vh - 320px);
  overflow-y: auto;
}

/* 详情列 */
.plg-detail {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 16px 18px;
  max-height: calc(100vh - 320px);
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.plg-detail-empty {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  min-height: 200px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.detail-desc {
  margin: 0;
  font-size: 13px;
  color: var(--secondary);
  line-height: 1.6;
}

.detail-subitems {
  padding-top: 4px;
  border-top: 1px solid var(--separator);
}

.detail-meta {
  padding-top: 12px;
  border-top: 1px solid var(--separator);
}

/* Codex 重复诊断详情卡 */
.duplicate-diagnostic-card {
  background: rgba(255, 149, 0, 0.04);
  border: 1px solid rgba(255, 149, 0, 0.2);
  border-radius: 8px;
  padding: 10px 12px;
}

.duplicate-diagnostic-card strong {
  display: block;
  font-size: 12px;
  color: var(--warning-strong);
  margin-bottom: 4px;
}

.duplicate-diagnostic-card p {
  font-size: 11px;
  color: var(--warning-strong);
  line-height: 1.5;
  margin: 0;
}

/* MCP 资源页 */
.mcp-view {
  display: flex;
  flex-direction: column;
}

/* 与共用 MCP 子页的区分副标题（跨面板一致性） */
.mcp-view-hint {
  margin: 0 0 10px;
  font-size: 12px;
  color: var(--secondary);
  line-height: 1.6;
}

.mcp-group + .mcp-group {
  margin-top: 4px;
}

.mcp-group-click {
  cursor: pointer;
}

.mcp-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  border-radius: var(--radius-md);
}

.mcp-row:hover {
  background: var(--control);
}

.mcp-main {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.mcp-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.mcp-desc {
  font-size: 12px;
  color: var(--tertiary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 状态点：如实映射所属插件启用态（非连接状态，§6.1.1） */
.mcp-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.mcp-dot.on {
  background: var(--success);
}

.mcp-dot.off {
  background: var(--separator);
}

/* 手动安装对话框 */
.manual-install-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.form-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-field label {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

/* Scrollbar styling */
.plugin-list::-webkit-scrollbar,
.plg-detail::-webkit-scrollbar {
  width: 8px;
}

.plugin-list::-webkit-scrollbar-track,
.plg-detail::-webkit-scrollbar-track {
  background: transparent;
}

.plugin-list::-webkit-scrollbar-thumb,
.plg-detail::-webkit-scrollbar-thumb {
  background: var(--separator);
  border-radius: 4px;
}

.plugin-list::-webkit-scrollbar-thumb:hover,
.plg-detail::-webkit-scrollbar-thumb:hover {
  background: var(--tertiary);
}
</style>
