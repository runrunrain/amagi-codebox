<!-- 契约 §6.6 市场视图（仅 Claude/Codex），两级分页（同技能页列表/详情范式）：
     一级＝市场源列表：面包屑（插件 › 插件市场，点击「插件」回已安装列表态）+「添加市场」
     主按钮 + ListToolbar（市场源 N + 刷新）+ 全宽市场源行（PluginRow：名称 + 类型徽章
     内置/本地/远程 Git + 可安装计数徽章 + 净化路径/Description 完整多行；行尾 Codex 删除；
     首行「全部市场」聚合入口）。
     二级＝市场内插件列表：← 返回市场列表 + 面包屑（插件市场 › 市场名）+ DetailHeader
     （N 个可安装 + 刷新）+ CountTabs（全部/未安装/已安装 + 行尾搜索）+ ListToolbar
     （可安装插件 + 排序 Segmented）+ 全宽行式条目（PluginRow + trailing 安装按钮，
     描述完整多行）。
     「添加市场」统一收敛为 emit('add_market', engine) 走 ExtensionsView 内联 Dialog（单一入口，
     面板内 AddMarketDialog 退役）；Codex 删除市场改走 store removeCxMarketplace（风险 10）。 -->
<template>
  <div class="plugin-market-panel">
    <!-- 首屏 Loading/Error 门控为 store 派生（契约 §8：SSR 测试不执行 onMounted） -->
    <LoadingState v-if="showLoading" message="加载市场中..." />

    <ErrorState v-else-if="showError" :message="loadError" :on-retry="handleRetry" />

    <template v-else>
      <!-- ═══ 一级子页：市场源列表（点击行进入二级插件列表） ═══ -->
      <template v-if="view === 'sources'">
        <!-- 面包屑（插件 → 回已安装列表态）+ 市场标题 + 添加市场入口（主按钮） -->
        <DetailHeader
          :breadcrumb="[{ label: '插件', clickable: true }, { label: '插件市场' }]"
          title="插件市场"
          :badges="[{ text: `${markets.length} 个市场源`, type: 'scope', variant: 'muted' }]"
          @back="goBack"
        >
          <template #actions>
            <AppButton variant="primary" size="small" @click="handleAddMarket">
              <span class="add-market-plus" aria-hidden="true">＋</span>添加市场
            </AppButton>
          </template>
        </DetailHeader>

        <template v-if="markets.length > 0">
          <ListToolbar title="市场源" :count="markets.length">
            <template #actions>
              <AppButton variant="ghost" size="small" :disabled="isLoading" @click="handleRetry">
                {{ isLoading ? '刷新中…' : '刷新' }}
              </AppButton>
            </template>
          </ListToolbar>

          <!-- 市场源行（与技能列表同构：名称+类型/计数徽章+描述完整多行；Codex 行尾删除）。
               计数文案用「N 个插件」：目录含已安装条目（本地市场目录直读），
               不再是只含未安装的「可安装」子集 -->
          <div class="market-source-list">
            <PluginRow
              name="全部市场"
              description="汇总所有市场源的插件目录，支持跨源搜索"
              :badges="[{ text: `${totalAvailableCount} 个插件`, type: 'scope', variant: 'muted' }]"
              @click="openAllMarkets"
            />
            <PluginRow
              v-for="market in markets"
              :key="getMarketName(market)"
              :name="getMarketName(market)"
              :description="getMarketDesc(market)"
              :badges="sourceRowBadges(market)"
              @click="selectMarket(market)"
            >
              <template v-if="engine === 'codex'" #trailing>
                <AppButton variant="danger" size="small" @click="handleRemoveMarket(market)">
                  删除
                </AppButton>
              </template>
            </PluginRow>
          </div>
        </template>

        <!-- 无市场源空态 -->
        <EmptyStateCard v-else title="暂无市场" description="添加市场源以浏览和安装插件">
          <template #primary>
            <AppButton variant="primary" size="small" @click="handleAddMarket">
              添加市场
            </AppButton>
          </template>
        </EmptyStateCard>
      </template>

      <!-- ═══ 二级子页：市场内插件列表（← 返回 + 面包屑 + 计数页签/搜索 + 工具栏 + 全宽行） ═══ -->
      <div v-else class="market-plugin-page">
        <button type="button" class="back-btn" @click="backToSources">← 返回市场列表</button>

        <DetailHeader
          :breadcrumb="[{ label: '插件市场', clickable: true }, { label: activeMarketLabel }]"
          :title="activeMarketLabel"
          :badges="[{ text: `${marketPluginCount} 个插件`, type: 'scope', variant: 'muted' }]"
          @back="backToSources"
        >
          <template #actions>
            <AppButton variant="ghost" size="small" :disabled="isLoading" @click="handleRetry">
              {{ isLoading ? '刷新中…' : '刷新' }}
            </AppButton>
          </template>
        </DetailHeader>

        <!-- 计数页签（全部/未安装/已安装，计数真实）+ 行尾搜索（与技能页 CountTabs 同构） -->
        <CountTabs
          :tabs="statusTabs"
          :model-value="statusTab"
          search-placeholder="搜索名称、描述或开发者"
          :search-value="searchQuery"
          @update:model-value="onTabChange"
          @update:search-value="onSearch"
        />

        <ListToolbar title="可安装插件" :count="filteredPlugins.length">
          <template #actions>
            <Segmented
              class="market-sort"
              :model-value="sortBy"
              :options="sortOptions"
              @update:model-value="onSortChange"
            />
          </template>
        </ListToolbar>

        <!-- 全宽列表（与已安装/技能列表同构；本地过滤，不发后端请求） -->
        <EmptyStateCard
          v-if="filteredPlugins.length === 0"
          title="未找到插件"
          :description="emptyPluginsDescription"
        />
        <div v-else class="market-list">
          <!-- 行式条目与已安装/技能列表同构（PluginRow + trailing 安装按钮），描述完整多行展示 -->
          <PluginRow
            v-for="plugin in filteredPlugins"
            :key="getPluginKey(plugin)"
            :name="getPluginName(plugin)"
            :description="getPluginDesc(plugin)"
            :badges="rowBadges(plugin)"
          >
            <template #trailing>
              <AppButton
                variant="primary"
                size="small"
                :disabled="isInstalled(plugin) || isInstalling(getPluginKey(plugin))"
                @click="handleInstall(plugin)"
              >
                {{ isInstalled(plugin) ? '已安装' : '安装' }}
              </AppButton>
            </template>
          </PluginRow>
        </div>
      </div>

      <!-- 删除市场确认（仅 Codex） -->
      <ConfirmDialog
        v-model:open="showRemoveMarketDialog"
        title="删除市场"
        :message="marketToRemove ? `确定要删除市场「${getMarketName(marketToRemove)}」吗？已安装插件不会自动卸载，但可安装列表将随之变化。` : ''"
        danger
        confirm-text="删除"
        @confirm="confirmRemoveMarket"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { storeToRefs } from 'pinia';
import { usePluginStore } from '../../stores/plugin';
import AppButton from '../ui/AppButton.vue';
import Segmented from '../ui/Segmented.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import CountTabs from './ui/CountTabs.vue';
import DetailHeader from './ui/DetailHeader.vue';
import EmptyStateCard from './ui/EmptyStateCard.vue';
import ListToolbar from './ui/ListToolbar.vue';
import PluginRow from './ui/PluginRow.vue';
import type {
  AvailablePluginView,
  CountTabItem,
  MarketplaceSourceView,
  PluginBadgeDescriptor,
} from './pluginViewTypes';
import { useToast } from '../../composables/useToast';

interface Props {
  engine?: 'claude' | 'codex';
}

const props = withDefaults(defineProps<Props>(), {
  engine: 'claude',
});

const emit = defineEmits<{
  (e: 'add_market', engine: 'claude' | 'codex'): void;
  (e: 'back'): void;
}>();

const pluginStore = usePluginStore();
const {
  ccInstalled,
  ccMarkets,
  ccAvailable,
  ccLoadError,
  loadingCC,
  cxInstalled,
  cxMarkets,
  cxAvailable,
  cxLoadError,
  loadingCX,
  activeMarketId,
} = storeToRefs(pluginStore);

const {
  setActiveMarketId,
  installCxPlugin,
  installCcPlugin,
  loadCcAllData,
  loadCxPlugins,
  removeCxMarketplace,
} = pluginStore;

// 注意：模板门控已有同名的 showError computed（ErrorState 显示条件），toast 走别名
const { showSuccess: toastSuccess, showError: toastError } = useToast();

// 本地搜索/排序状态（契约 §7：搜索不进 store，避免污染共享态）
const searchQuery = ref('');
const sortBy = ref<'installs' | 'name'>(props.engine === 'claude' ? 'installs' : 'name');

// 安装中的插件 key（行级禁用，避免全局 mutating）
const installingKeys = ref<Record<string, boolean>>({});

// 删除市场确认（仅 Codex）
const showRemoveMarketDialog = ref(false);
const marketToRemove = ref<MarketplaceSourceView | null>(null);

// --- store 派生门控（契约 §8：SSR 不执行 onMounted，门控不得依赖本地 ref 初值） ---
const showLoading = computed(() => {
  if (props.engine === 'claude') {
    return loadingCC.value && ccMarkets.value.length === 0 && ccAvailable.value.length === 0;
  }
  return loadingCX.value && cxMarkets.value.length === 0 && cxAvailable.value.length === 0;
});

const loadError = computed(() =>
  props.engine === 'claude' ? ccLoadError.value : cxLoadError.value
);

const showError = computed(() => {
  if (props.engine === 'claude') {
    return !!ccLoadError.value && ccMarkets.value.length === 0 && ccAvailable.value.length === 0;
  }
  return !!cxLoadError.value && cxMarkets.value.length === 0 && cxAvailable.value.length === 0;
});

async function handleRetry() {
  if (props.engine === 'claude') {
    await loadCcAllData();
  } else {
    await loadCxPlugins(true);
  }
}

// --- 数据视图 ---
const markets = computed<MarketplaceSourceView[]>(() => {
  return props.engine === 'claude' ? ccMarkets.value : cxMarkets.value;
});

// Claude available 在 store 中按 PluginDetail[] 消费（api 层事实契约），
// 运行时形状为后端归一化的 AvailablePlugin，视图层按 AvailablePluginView 读取
const allPlugins = computed<AvailablePluginView[]>(() => {
  return props.engine === 'claude'
    ? (ccAvailable.value as unknown as AvailablePluginView[])
    : cxAvailable.value;
});

const totalAvailableCount = computed(() => allPlugins.value.length);

// --- 两级分页状态（契约同技能页列表/详情分页）：sources=一级市场源列表，plugins=二级插件列表 ---
const view = ref<'sources' | 'plugins'>('sources');

// 二级安装状态页签：全部/未安装/已安装（计数真实；与技能页 CountTabs 同构）
type StatusTab = 'all' | 'uninstalled' | 'installed';
const statusTab = ref<StatusTab>('all');

// 二级标题：进入具体市场源显示市场名，「全部市场」聚合态显示全部市场
const activeMarketLabel = computed(() => activeMarketId.value || '全部市场');

// 当前市场内插件（仅按市场过滤；页签计数与列表共用，搜索/状态过滤不参与）
const marketPlugins = computed<AvailablePluginView[]>(() =>
  activeMarketId.value
    ? allPlugins.value.filter((p) => p.marketplaceName === activeMarketId.value)
    : [...allPlugins.value],
);

// 二级 DetailHeader 徽章计数：当前市场内可安装总数
const marketPluginCount = computed(() => marketPlugins.value.length);

const installedInMarketCount = computed(
  () => marketPlugins.value.filter((p) => isInstalled(p)).length,
);

const statusTabs = computed<CountTabItem[]>(() => [
  { key: 'all', label: '全部', count: marketPlugins.value.length },
  {
    key: 'uninstalled',
    label: '未安装',
    count: marketPlugins.value.length - installedInMarketCount.value,
  },
  { key: 'installed', label: '已安装', count: installedInMarketCount.value },
]);

// 状态页签切换（与技能页一致：切换时清空搜索，避免残留过滤词）
function onTabChange(key: string) {
  statusTab.value = key as StatusTab;
  searchQuery.value = '';
}

function onSearch(value: string) {
  searchQuery.value = value;
}

// 一级「全部市场」聚合行 → 二级（跨源浏览/搜索）
function openAllMarkets() {
  setActiveMarketId(null);
  enterPluginsView();
}

// 一级市场源行 → 二级（行进入语义，取代原页签的同源点击切换）
function selectMarket(market: MarketplaceSourceView) {
  setActiveMarketId(getMarketName(market));
  enterPluginsView();
}

function enterPluginsView() {
  statusTab.value = 'all';
  searchQuery.value = '';
  view.value = 'plugins';
}

// 二级 → 一级（← 返回市场列表 / 面包屑「插件市场」）
function backToSources() {
  view.value = 'sources';
}

const filteredPlugins = computed<AvailablePluginView[]>(() => {
  let plugins = [...marketPlugins.value];

  if (statusTab.value === 'installed') {
    plugins = plugins.filter((p) => isInstalled(p));
  } else if (statusTab.value === 'uninstalled') {
    plugins = plugins.filter((p) => !isInstalled(p));
  }

  if (searchQuery.value.trim()) {
    const query = searchQuery.value.toLowerCase();
    plugins = plugins.filter(
      (p) =>
        p.name?.toLowerCase().includes(query) ||
        p.description?.toLowerCase().includes(query) ||
        p.author?.toLowerCase().includes(query)
    );
  }

  if (sortBy.value === 'name') {
    plugins.sort((a, b) => (a.name || '').localeCompare(b.name || ''));
  } else if (props.engine === 'claude') {
    // CodexAvailablePlugin 无 installCount（models.ts），安装量排序仅 Claude 提供
    plugins.sort((a, b) => (b.installCount || 0) - (a.installCount || 0));
  }

  return plugins;
});

// 二级空态文案：搜索词优先提示，其次按状态页签/聚合态区分
const emptyPluginsDescription = computed(() => {
  if (searchQuery.value.trim()) return '尝试其他搜索词';
  if (statusTab.value === 'installed') return '该市场暂无已安装的插件';
  return activeMarketId.value ? '该市场暂无可安装插件' : '暂无可安装插件';
});

// 刷新中态（两级「刷新」按钮共用）
const isLoading = computed(() =>
  props.engine === 'claude' ? loadingCC.value : loadingCX.value
);

// 排序分段选项（Segmented 弱化切换）：Claude 提供安装量/名称，Codex 仅名称
const sortOptions = computed(() =>
  props.engine === 'claude'
    ? [
        { value: 'installs', label: '安装量' },
        { value: 'name', label: '名称 A-Z' },
      ]
    : [{ value: 'name', label: '名称 A-Z' }],
);

// --- 市场源 helpers ---
function getMarketName(market: MarketplaceSourceView): string {
  if (props.engine === 'claude') {
    return market.Name || market.name || 'Unknown';
  }
  return market.name || 'Unknown';
}

// 原始来源串（Codex：repo/url/source，可能为 \\?\ 前缀的 Windows 扩展长度路径）
function getMarketRawSource(market: MarketplaceSourceView): string {
  return market.repo || market.url || market.source || '';
}

// 来源类型徽章：远程 Git（URL）/ 内置（bundled 路径）/ 本地（其余路径）；无法判定时不渲染
function getMarketTypeLabel(market: MarketplaceSourceView): string {
  const raw = getMarketRawSource(market);
  if (!raw) return '';
  if (/^https?:\/\//i.test(raw)) return '远程 Git';
  if (/^(\\\\\?\\|\\\\\.\\|\\\\|[a-z]:[\\/])/i.test(raw)) {
    return /(^|[\\/])bundled/i.test(raw) ? '内置' : '本地';
  }
  return '';
}

// 剥离 Windows 扩展长度前缀（\\?\ / \\.\），\\?\ 绝不作为主文案裸露
function sanitizeSourcePath(raw: string): string {
  return raw.replace(/^(\\\\\?\\|\\\\\.\\)/, '');
}

// 显示用来源描述：Claude=Description；Codex=净化后的 repo/URL/路径（缺失回落「本地市场」）
function getMarketDesc(market: MarketplaceSourceView): string {
  if (props.engine === 'claude') {
    return market.Description || market.description || '';
  }
  const raw = getMarketRawSource(market);
  return raw ? sanitizeSourcePath(raw) : '本地市场';
}

function getPluginCountForMarket(market: MarketplaceSourceView): number {
  const marketName = getMarketName(market);
  return allPlugins.value.filter((p) => p.marketplaceName === marketName).length;
}

// 一级市场源行徽章：来源类型（内置/本地/远程 Git，可判定时）+ 可安装计数
function sourceRowBadges(market: MarketplaceSourceView): PluginBadgeDescriptor[] {
  const badges: PluginBadgeDescriptor[] = [];
  const typeLabel = getMarketTypeLabel(market);
  if (typeLabel) {
    badges.push({ text: typeLabel, type: 'type', color: 'plugin' });
  }
  badges.push({
    text: `${getPluginCountForMarket(market)} 个插件`,
    type: 'scope',
    variant: 'muted',
  });
  return badges;
}

// --- 插件条目 helpers ---
function getPluginKey(plugin: AvailablePluginView): string {
  if (props.engine === 'claude') {
    // 优先完整 pluginId（name@marketplace）：同名插件可能跨市场存在，仅按 name 会撞 key
    return plugin.pluginId || plugin.name || plugin.id || '';
  }
  return plugin.pluginId || plugin.id || '';
}

function getPluginName(plugin: AvailablePluginView): string {
  return plugin.name || 'Unknown';
}

function getPluginDesc(plugin: AvailablePluginView): string {
  return plugin.description || '';
}

// 来源徽章降级规则：author 缺失时显示市场源名称，保证每行都有来源标识
function getPluginSourceLabel(plugin: AvailablePluginView): string {
  return plugin.author || plugin.marketplaceName || '';
}

function formatInstallCount(plugin: AvailablePluginView): string {
  const count = plugin.installCount || 0;
  if (count >= 1000000) return `${(count / 1000000).toFixed(1)}M`;
  if (count >= 1000) return `${(count / 1000).toFixed(1)}K`;
  return count.toString();
}

// 行徽章：Claude=安装量；Codex=来源（走 getPluginSourceLabel 的降级规则）
function rowBadges(plugin: AvailablePluginView): PluginBadgeDescriptor[] {
  if (props.engine === 'claude') {
    return [{ text: `${formatInstallCount(plugin)} 次安装`, type: 'source', variant: 'muted' }];
  }
  const source = getPluginSourceLabel(plugin);
  return source ? [{ text: source, type: 'source', variant: 'muted' }] : [];
}

// 已安装判定（真实推导，无后端字段；契约 §6.6）
function isInstalled(plugin: AvailablePluginView): boolean {
  if (props.engine === 'claude') {
    // 优先按完整 id（name@marketplace）精确匹配：同一插件名可能同时存在于多个市场
    //（如 writing-skills 同时在 amagi 与 vexel），仅按名称会跨市场误判已安装
    if (plugin.pluginId) {
      return ccInstalled.value.some((p) => p.id === plugin.pluginId);
    }
    return ccInstalled.value.some((p) => p.name === plugin.name);
  }
  return cxInstalled.value.some((p) => p.id === plugin.pluginId || p.name === plugin.name);
}

function isInstalling(key: string): boolean {
  return !!installingKeys.value[key];
}

function setSortBy(sort: 'installs' | 'name') {
  sortBy.value = sort;
}

// Segmented 的 update 以 string 事件到达，在此收窄回排序联合类型
function onSortChange(value: string) {
  setSortBy(value === 'name' ? 'name' : 'installs');
}

// --- 动作 ---
// 统一收敛为 'add_market' 事件：ExtensionsView 监听 @add_market（编译为 prop onAdd_market，
// 仅 emit('add_market') 命中——契约 §0/§9 显式变更 ②）
function handleAddMarket() {
  emit('add_market', props.engine);
}

function goBack() {
  emit('back');
}

async function handleInstall(plugin: AvailablePluginView) {
  if (isInstalled(plugin)) return;
  const key = getPluginKey(plugin);
  if (isInstalling(key)) return;

  installingKeys.value = { ...installingKeys.value, [key]: true };
  try {
    if (props.engine === 'claude') {
      // 传完整 pluginId（name@marketplace）：同名插件跨市场时仅按名称安装有歧义
      await installCcPlugin(plugin.pluginId || plugin.name || plugin.id || '');
    } else {
      await installCxPlugin(plugin.pluginId || plugin.id || '', plugin.marketplaceName);
    }
    toastSuccess(`插件「${getPluginName(plugin)}」安装成功`);
  } catch (error) {
    // 走查修复：失败不能只 console.error 静默（按钮恢复可点、界面无变化），
    // 与 OpenCodePluginPanel.handleInstall 对齐补 toast 反馈
    console.error('[PluginMarketPanel] Install plugin failed:', error);
    toastError(`安装失败：${error instanceof Error ? error.message : String(error)}`);
  } finally {
    installingKeys.value = { ...installingKeys.value, [key]: false };
  }
}

function handleRemoveMarket(market: MarketplaceSourceView) {
  marketToRemove.value = market;
  showRemoveMarketDialog.value = true;
}

async function confirmRemoveMarket() {
  if (!marketToRemove.value) return;

  try {
    // 改走 store removeCxMarketplace（契约风险 10：不再绕过 store 直调 wailsjs）
    await removeCxMarketplace(getMarketName(marketToRemove.value));
  } catch (error) {
    console.error('[PluginMarketPanel] Remove market error:', error);
  } finally {
    showRemoveMarketDialog.value = false;
    marketToRemove.value = null;
  }
}

// 进入市场视图时装载市场数据（loadCcAllData/loadCxPlugins 均带 5min 缓存，命中时零开销）
onMounted(async () => {
  try {
    if (props.engine === 'claude') {
      await loadCcAllData();
    } else {
      await loadCxPlugins();
    }
  } catch (error) {
    // 失败信息已由 store 写入 ccLoadError/cxLoadError，由 ErrorState/DiagnosticBanner 呈现
    console.error('[PluginMarketPanel] Load failed:', error);
  }
});

// 测试契约（§8）：expose 触发器（两级导航 + 行为）
defineExpose({
  handleAddMarket,
  goBack,
  handleInstall,
  selectMarket,
  openAllMarkets,
  backToSources,
  onTabChange,
  onSearch,
  setSortBy,
});
</script>

<style scoped>
.plugin-market-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* 一级市场源行列表：与技能列表同构（2px 紧凑堆叠 + 描述完整多行） */
.market-source-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.market-source-list :deep(.plugin-row) {
  align-items: flex-start;
}

.market-source-list :deep(.plugin-row-desc) {
  white-space: normal;
  overflow: visible;
  word-break: break-word;
}

/* 二级插件列表页：与技能页二级同节奏（14px 分节） */
.market-plugin-page {
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

/* 排序分段：复用 ui/Segmented 胶囊，仅在工具栏内收窄为紧凑尺寸（选中=白底浮起，非实心主色块） */
.market-sort :deep(.seg) {
  flex: 0 0 auto;
  padding: 5px 12px;
  font-size: 12px;
}

/* 添加市场主按钮的＋图标 */
.add-market-plus {
  font-size: 13px;
  font-weight: 600;
  line-height: 1;
}

/* 行距与技能页一致（2px 紧凑堆叠） */
.market-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

/* 与 SharedSkillsPanel 同款深穿透：行首对齐 + 描述完整多行展示 */
.market-list :deep(.plugin-row) {
  align-items: flex-start;
}

.market-list :deep(.plugin-row-desc) {
  white-space: normal;
  overflow: visible;
  word-break: break-word;
}
</style>
