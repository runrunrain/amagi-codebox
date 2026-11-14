<!-- 契约 §6.6 市场视图（仅 Claude/Codex）：
     面包屑（插件 › 插件市场，点击「插件」回列表态）+ 顶部搜索/排序/「添加市场」+
     左窄栏市场源列表（名称+一行描述+「{n} 个可安装」+Codex 删除 ×）+ 右侧插件流行式条目。
     「添加市场」统一收敛为 emit('add_market', engine) 走 ExtensionsView 内联 Dialog（单一入口，
     面板内 AddMarketDialog 退役）；Codex 删除市场改走 store removeCxMarketplace（风险 10）。 -->
<template>
  <div class="plugin-market-panel">
    <!-- 首屏 Loading/Error 门控为 store 派生（契约 §8：SSR 测试不执行 onMounted） -->
    <LoadingState v-if="showLoading" message="加载市场中..." />

    <ErrorState v-else-if="showError" :message="loadError" :on-retry="handleRetry" />

    <template v-else>
      <!-- 面包屑 + 市场标题 + 添加市场入口 -->
      <DetailHeader
        :breadcrumb="[{ label: '插件', clickable: true }, { label: '插件市场' }]"
        title="插件市场"
        :badges="[{ text: `${totalAvailableCount} 个可安装`, type: 'scope', variant: 'muted' }]"
        @back="goBack"
      >
        <template #actions>
          <AppButton variant="ghost" size="small" @click="handleAddMarket">
            添加市场
          </AppButton>
        </template>
      </DetailHeader>

      <!-- 搜索与排序（本地过滤，不发后端请求） -->
      <div class="market-toolbar">
        <div class="market-search">
          <TextInput
            v-model="searchQuery"
            placeholder="搜索名称、描述或开发者"
          >
            <template #prefix><span class="search-icon">⌕</span></template>
          </TextInput>
        </div>
        <div class="sort-options">
          <AppButton
            v-if="engine === 'claude'"
            variant="outline"
            :class="['sort-btn', { active: sortBy === 'installs' }]"
            size="small"
            @click="setSortBy('installs')"
          >
            安装量
          </AppButton>
          <AppButton
            variant="outline"
            :class="['sort-btn', { active: sortBy === 'name' }]"
            size="small"
            @click="setSortBy('name')"
          >
            名称 A-Z
          </AppButton>
        </div>
      </div>

      <!-- 市场源 + 插件列表 -->
      <div v-if="markets.length > 0" class="market-layout">
        <!-- 左窄栏：市场源列表 -->
        <aside class="market-source-pane">
          <div
            v-for="market in markets"
            :key="getSourceKey(market)"
            :class="['market-card', { active: isSourceActive(market) }]"
          >
            <div class="mk-main" @click="selectMarket(market)">
              <div class="mk-name">{{ getMarketName(market) }}</div>
              <div class="mk-desc">{{ getMarketDesc(market) }}</div>
              <span class="mk-meta">{{ getPluginCountForMarket(market) }} 个可安装</span>
            </div>
            <button
              v-if="engine === 'codex'"
              type="button"
              class="mk-delete-btn"
              title="删除市场"
              @click.stop="handleRemoveMarket(market)"
            >
              ×
            </button>
          </div>
          <div v-if="activeMarketId === null" class="market-card active">
            <div class="mk-name">全部市场</div>
            <div class="mk-desc">显示所有市场的插件</div>
            <span class="mk-meta">{{ totalAvailableCount }} 个可安装</span>
          </div>
        </aside>

        <!-- 右侧：可安装插件行式条目 -->
        <section class="market-plugin-pane">
          <EmptyStateCard
            v-if="filteredPlugins.length === 0"
            title="未找到插件"
            :description="searchQuery.trim() ? '尝试其他搜索词' : '该市场暂无可安装插件'"
          />
          <div v-else class="market-list">
            <div v-for="plugin in filteredPlugins" :key="getPluginKey(plugin)" class="market-row">
              <PluginIcon :name="getPluginName(plugin)" size="sm" />
              <div class="mr-main">
                <div class="mr-head">
                  <span class="mr-name">{{ getPluginName(plugin) }}</span>
                  <Badge
                    v-if="engine === 'claude'"
                    type="source"
                    variant="muted"
                    :text="`${formatInstallCount(plugin)} 次安装`"
                  />
                  <Badge
                    v-else-if="getPluginAuthor(plugin)"
                    type="source"
                    variant="muted"
                    :text="getPluginAuthor(plugin)"
                  />
                </div>
                <p v-if="getPluginDesc(plugin)" class="mr-desc">{{ getPluginDesc(plugin) }}</p>
              </div>
              <AppButton
                variant="primary"
                size="small"
                :disabled="isInstalled(plugin) || isInstalling(getPluginKey(plugin))"
                @click="handleInstall(plugin)"
              >
                {{ isInstalled(plugin) ? '已安装' : '安装' }}
              </AppButton>
            </div>
          </div>
        </section>
      </div>

      <!-- 无市场源空态 -->
      <EmptyStateCard
        v-else
        title="暂无市场"
        description="添加市场源以浏览和安装插件"
      >
        <template #primary>
          <AppButton variant="primary" size="small" @click="handleAddMarket">
            添加市场
          </AppButton>
        </template>
      </EmptyStateCard>

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
import Badge from '../ui/Badge.vue';
import TextInput from '../ui/TextInput.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import DetailHeader from './ui/DetailHeader.vue';
import EmptyStateCard from './ui/EmptyStateCard.vue';
import PluginIcon from './ui/PluginIcon.vue';
import type {
  AvailablePluginView,
  MarketplaceSourceView,
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

const filteredPlugins = computed<AvailablePluginView[]>(() => {
  let plugins = [...allPlugins.value];

  if (activeMarketId.value) {
    plugins = plugins.filter((p) => p.marketplaceName === activeMarketId.value);
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

// --- 市场源 helpers ---
function getSourceKey(market: MarketplaceSourceView): string {
  return getMarketName(market);
}

function getMarketName(market: MarketplaceSourceView): string {
  if (props.engine === 'claude') {
    return market.Name || market.name || 'Unknown';
  }
  return market.name || 'Unknown';
}

function getMarketDesc(market: MarketplaceSourceView): string {
  if (props.engine === 'claude') {
    return market.Description || market.description || '';
  }
  return market.repo || market.url || market.source || '本地市场';
}

function getPluginCountForMarket(market: MarketplaceSourceView): number {
  const marketName = getMarketName(market);
  return allPlugins.value.filter((p) => p.marketplaceName === marketName).length;
}

function isSourceActive(market: MarketplaceSourceView): boolean {
  return activeMarketId.value === getMarketName(market);
}

function selectMarket(market: MarketplaceSourceView) {
  const name = getMarketName(market);
  setActiveMarketId(activeMarketId.value === name ? null : name);
}

// --- 插件条目 helpers ---
function getPluginKey(plugin: AvailablePluginView): string {
  if (props.engine === 'claude') {
    return plugin.name || plugin.id || '';
  }
  return plugin.pluginId || plugin.id || '';
}

function getPluginName(plugin: AvailablePluginView): string {
  return plugin.name || 'Unknown';
}

function getPluginDesc(plugin: AvailablePluginView): string {
  return plugin.description || '';
}

function getPluginAuthor(plugin: AvailablePluginView): string {
  return plugin.author || '';
}

function formatInstallCount(plugin: AvailablePluginView): string {
  const count = plugin.installCount || 0;
  if (count >= 1000000) return `${(count / 1000000).toFixed(1)}M`;
  if (count >= 1000) return `${(count / 1000).toFixed(1)}K`;
  return count.toString();
}

// 已安装判定（真实推导，无后端字段；契约 §6.6）
function isInstalled(plugin: AvailablePluginView): boolean {
  if (props.engine === 'claude') {
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
      await installCcPlugin(plugin.name || plugin.id || '');
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

// 测试契约（§8）：expose 触发器
defineExpose({
  handleAddMarket,
  goBack,
  handleInstall,
  selectMarket,
  setSortBy,
});
</script>

<style scoped>
.plugin-market-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.market-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.market-search {
  flex: 1;
  min-width: 220px;
  max-width: 320px;
}

.market-search :deep(.text-input) {
  width: 100%;
}

.search-icon {
  color: var(--tertiary);
  font-size: 13px;
}

.sort-options {
  display: flex;
  gap: 6px;
}

/* 排序按钮：非选中态走 AppButton 的 outline variant；仅 active 态在此覆盖为强调色 */
.sort-btn.active {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}

.market-layout {
  display: grid;
  grid-template-columns: 240px 1fr;
  gap: 14px;
  align-items: start;
}

.market-source-pane {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: calc(100vh - 300px);
  overflow-y: auto;
}

.market-card {
  position: relative;
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 10px 12px;
  transition: border-color 0.15s, background 0.12s;
}

.market-card:hover {
  border-color: var(--controlHover);
}

.market-card.active {
  border-color: var(--accent);
  background: rgba(0, 122, 255, 0.04);
}

.mk-main {
  cursor: pointer;
}

.mk-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
  margin-bottom: 3px;
}

.mk-desc {
  font-size: 11px;
  color: var(--secondary);
  line-height: 1.4;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.mk-meta {
  display: inline-block;
  margin-top: 5px;
  font-size: 10px;
  font-weight: 600;
  color: var(--secondary);
  background: var(--control);
  border-radius: 999px;
  padding: 1px 6px;
  line-height: 1.4;
}

.mk-delete-btn {
  position: absolute;
  top: 6px;
  right: 6px;
  width: 20px;
  height: 20px;
  border: none;
  background: transparent;
  color: var(--tertiary);
  font-size: 16px;
  cursor: pointer;
  border-radius: 4px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.12s;
}

.mk-delete-btn:hover {
  color: var(--danger);
  background: rgba(255, 59, 48, 0.1);
}

.market-plugin-pane {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 6px;
  max-height: calc(100vh - 300px);
  overflow-y: auto;
}

.market-list {
  display: flex;
  flex-direction: column;
}

.market-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 10px;
  border-radius: var(--radius-md);
}

.market-row:hover {
  background: var(--control);
}

.mr-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.mr-head {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.mr-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.mr-desc {
  margin: 0;
  font-size: 12px;
  color: var(--secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.market-row :deep(button.btn) {
  flex-shrink: 0;
}

/* Scrollbar styling */
.market-source-pane::-webkit-scrollbar,
.market-plugin-pane::-webkit-scrollbar {
  width: 8px;
}

.market-source-pane::-webkit-scrollbar-track,
.market-plugin-pane::-webkit-scrollbar-track {
  background: transparent;
}

.market-source-pane::-webkit-scrollbar-thumb,
.market-plugin-pane::-webkit-scrollbar-thumb {
  background: var(--separator);
  border-radius: 4px;
}

.market-source-pane::-webkit-scrollbar-thumb:hover,
.market-plugin-pane::-webkit-scrollbar-thumb:hover {
  background: var(--tertiary);
}
</style>
