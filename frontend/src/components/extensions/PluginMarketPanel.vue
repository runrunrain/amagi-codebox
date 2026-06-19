<template>
  <div class="plugin-market-panel">
    <!-- Header with title and actions -->
    <div class="panel-header">
      <div class="header-title">
        <h2>市场</h2>
        <span class="count-badge">{{ totalAvailableCount }}</span>
      </div>
      <div class="market-actions">
        <AppButton
          v-if="engine === 'codex'"
          variant="ghost"
          size="small"
          @click="handleAddMarket"
        >
          添加市场
        </AppButton>
        <AppButton
          v-if="engine === 'claude'"
          variant="ghost"
          size="small"
          @click="handleAddMarket"
        >
          添加市场
        </AppButton>
      </div>
    </div>

    <!-- Search and sort toolbar -->
    <div class="market-toolbar">
      <div class="search-box">
        <input
          v-model="searchQuery"
          type="text"
          placeholder="搜索插件名称、描述或作者"
          class="search-input"
        />
        <span class="search-icon">🔍</span>
      </div>
      <div class="sort-options">
        <button
          :class="['sort-btn', { active: sortBy === 'name' }]"
          @click="setSortBy('name')"
        >
          名称 A-Z
        </button>
        <button
          v-if="engine === 'claude'"
          :class="['sort-btn', { active: sortBy === 'installs' }]"
          @click="setSortBy('installs')"
        >
          安装量 ↓
        </button>
      </div>
    </div>

    <!-- Market layout: source list + plugin list -->
    <div v-if="!loading && markets.length > 0" class="market-layout">
      <!-- Market source list (left sidebar) -->
      <aside class="market-source-pane">
        <div
          v-for="(market, idx) in markets"
          :key="getSourceKey(market)"
          :class="['market-card', { active: isSourceActive(market) }]"
          @click="selectMarket(market)"
        >
          <div class="mk-name">{{ getMarketName(market) }}</div>
          <div class="mk-desc">
            {{ getMarketDesc(market) }}
            <br />
            <span class="mk-meta">{{ getPluginCountForMarket(market) }} 个可安装</span>
          </div>
        </div>
        <div v-if="activeMarketId === null" class="market-card active">
          <div class="mk-name">全部市场</div>
          <div class="mk-desc">
            显示所有市场的插件
            <br />
            <span class="mk-meta">{{ totalAvailableCount }} 个可安装</span>
          </div>
        </div>
      </aside>

      <!-- Plugin list (right pane) -->
      <section class="market-plugin-pane">
        <div v-if="filteredPlugins.length === 0" class="empty-state-sm">
          <EmptyState
            icon="⊘"
            title="未找到插件"
            :description="searchQuery ? '尝试其他搜索词' : '该市场暂无可安装插件'"
          />
        </div>
        <div v-else class="market-list">
          <div
            v-for="plugin in filteredPlugins"
            :key="getPluginKey(plugin)"
            class="market-item"
          >
            <div class="mki-top">
              <div class="mki-name">{{ getPluginName(plugin) }}</div>
              <span v-if="engine === 'claude'" class="installs-badge">
                {{ formatInstallCount(plugin) }} installs
              </span>
              <span v-if="engine === 'codex' && getPluginAuthor(plugin)" class="author-badge">
                {{ getPluginAuthor(plugin) }}
              </span>
            </div>
            <div class="mki-desc">{{ getPluginDesc(plugin) }}</div>
            <AppButton
              variant="primary"
              size="small"
              @click="handleInstall(plugin)"
            >
              安装
            </AppButton>
          </div>
        </div>
      </section>
    </div>

    <!-- Empty state -->
    <div v-else-if="!loading && markets.length === 0" class="empty-state-lg">
      <EmptyState
        icon="⊙"
        title="暂无市场"
        description="添加市场源以浏览和安装插件"
      />
    </div>

    <!-- Loading state -->
    <div v-if="loading" class="loading-state">
      <div class="spinner"></div>
      <p>加载市场数据...</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import { storeToRefs } from 'pinia';
import { usePluginStore } from '../../stores/plugin';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';

interface Props {
  engine?: 'claude' | 'codex';
}

const props = withDefaults(defineProps<Props>(), {
  engine: 'claude',
});

const emit = defineEmits<{
  (e: 'addMarket', engine: 'claude' | 'codex'): void;
}>();

const pluginStore = usePluginStore();
const {
  ccMarkets,
  ccAvailable,
  cxMarkets,
  cxAvailable,
  activeMarketId,
  marketSearchQuery,
  marketSortBy,
  loadingMarket,
} = storeToRefs(pluginStore);

const {
  setActiveMarketId,
  setMarketSearchQuery,
  setMarketSortBy,
  installCxPlugin,
  installCcPlugin,
  loadCcMarkets,
  loadCcAvailable,
} = pluginStore;

// Local search state (synced with store)
const searchQuery = ref('');
const sortBy = ref<'installs' | 'name'>('installs');
const loading = ref(false);

// Sync local search with store
watch(searchQuery, (val) => {
  setMarketSearchQuery(val);
});

watch(sortBy, (val) => {
  setMarketSortBy(val);
});

// Markets based on engine
const markets = computed(() => {
  return props.engine === 'claude' ? ccMarkets.value : cxMarkets.value;
});

// All available plugins
const allPlugins = computed(() => {
  return props.engine === 'claude' ? ccAvailable.value : cxAvailable.value;
});

// Total available count
const totalAvailableCount = computed(() => allPlugins.value.length);

// Filtered plugins
const filteredPlugins = computed(() => {
  let plugins = [...allPlugins.value];

  // Filter by active marketplace
  if (activeMarketId.value) {
    if (props.engine === 'claude') {
      plugins = plugins.filter((p: any) => p.marketplace === activeMarketId.value);
    } else {
      plugins = plugins.filter((p: any) => p.marketplaceName === activeMarketId.value);
    }
  }

  // Filter by search query
  if (searchQuery.value.trim()) {
    const query = searchQuery.value.toLowerCase();
    plugins = plugins.filter((p: any) =>
      (p.name?.toLowerCase().includes(query)) ||
      (p.description?.toLowerCase().includes(query)) ||
      (p.author?.toLowerCase().includes(query))
    );
  }

  // Sort
  if (sortBy.value === 'name') {
    plugins.sort((a: any, b: any) => (a.name || '').localeCompare(b.name || ''));
  } else if (sortBy.value === 'installs' && props.engine === 'claude') {
    plugins.sort((a: any, b: any) => (b.installCount || 0) - (a.installCount || 0));
  }

  return plugins;
});

// Helper functions
function getSourceKey(market: any): string {
  if (props.engine === 'claude') {
    return market.Name || market.name || '';
  }
  return market.name || '';
}

function getMarketName(market: any): string {
  if (props.engine === 'claude') {
    return market.Name || market.name || 'Unknown';
  }
  return market.name || 'Unknown';
}

function getMarketDesc(market: any): string {
  if (props.engine === 'claude') {
    return market.Description || market.description || '';
  }
  return market.repo || market.url || market.source || '本地市场';
}

function getPluginCountForMarket(market: any): number {
  const marketName = getMarketName(market);
  if (props.engine === 'claude') {
    return (allPlugins.value as any[]).filter((p: any) => p.marketplace === marketName).length;
  }
  return (allPlugins.value as any[]).filter((p: any) => p.marketplaceName === marketName).length;
}

function isSourceActive(market: any): boolean {
  return activeMarketId.value === getMarketName(market);
}

function selectMarket(market: any) {
  const name = getMarketName(market);
  setActiveMarketId(activeMarketId.value === name ? null : name);
}

function getPluginKey(plugin: any): string {
  if (props.engine === 'claude') {
    return plugin.name || plugin.id || '';
  }
  return plugin.pluginId || plugin.id || '';
}

function getPluginName(plugin: any): string {
  return plugin.name || 'Unknown';
}

function getPluginDesc(plugin: any): string {
  return plugin.description || '';
}

function getPluginAuthor(plugin: any): string {
  return plugin.author || '';
}

function formatInstallCount(plugin: any): string {
  const count = plugin.installCount || 0;
  if (count >= 1000000) return (count / 1000000).toFixed(1) + 'M';
  if (count >= 1000) return (count / 1000).toFixed(1) + 'K';
  return count.toString();
}

function setSortBy(sort: 'installs' | 'name') {
  sortBy.value = sort;
}

// Actions
function handleAddMarket() {
  emit('addMarket', props.engine);
}

async function handleInstall(plugin: any) {
  if (props.engine === 'claude') {
    // Install Claude plugin using plugin name
    try {
      const pluginName = plugin.name || plugin.id;
      await installCcPlugin(pluginName);
    } catch (error) {
      console.error('[PluginMarketPanel] Install Claude plugin failed:', error);
    }
  } else {
    try {
      await installCxPlugin(plugin.pluginId, plugin.marketplaceName);
    } catch (error) {
      console.error('[PluginMarketPanel] Install Codex plugin failed:', error);
    }
  }
}

// Load markets on mount
onMounted(async () => {
  loading.value = true;
  try {
    if (props.engine === 'claude') {
      await Promise.all([loadCcMarkets(), loadCcAvailable()]);
    }
  } catch (error) {
    console.error('[PluginMarketPanel] Load failed:', error);
  } finally {
    loading.value = false;
  }
});
</script>

<style scoped>
.plugin-market-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.header-title {
  display: flex;
  align-items: center;
  gap: 8px;
}

.header-title h2 {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
}

.count-badge {
  font-size: 12px;
  color: var(--secondary);
  background: var(--control);
  padding: 2px 8px;
  border-radius: 10px;
}

.market-actions {
  display: flex;
  gap: 8px;
}

.market-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.search-box {
  position: relative;
  flex: 1;
  min-width: 200px;
}

.search-input {
  width: 100%;
  padding: 7px 12px 7px 32px;
  font-size: 12px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  transition: border-color 0.15s;
}

.search-input:focus {
  outline: none;
  border-color: var(--accent);
}

.search-icon {
  position: absolute;
  left: 10px;
  top: 50%;
  transform: translateY(-50%);
  font-size: 12px;
  opacity: 0.5;
  pointer-events: none;
}

.sort-options {
  display: flex;
  gap: 6px;
}

.sort-btn {
  font-size: 11px;
  padding: 5px 10px;
  border: 1px solid var(--separator);
  border-radius: 6px;
  background: var(--card);
  color: var(--secondary);
  cursor: pointer;
  transition: all 0.12s;
}

.sort-btn:hover {
  background: var(--control);
}

.sort-btn.active {
  background: var(--accent);
  color: #fff;
  border-color: var(--accent);
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
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 12px 14px;
  cursor: pointer;
  transition: border-color 0.15s, background 0.12s;
}

.market-card:hover {
  border-color: #c5c5cc;
}

.market-card.active {
  border-color: var(--accent);
  background: rgba(0, 122, 255, 0.04);
}

.mk-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
  margin-bottom: 4px;
}

.mk-desc {
  font-size: 11px;
  color: var(--secondary);
  line-height: 1.4;
}

.mk-meta {
  color: var(--tertiary);
}

.market-plugin-pane {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 14px 16px;
  max-height: calc(100vh - 300px);
  overflow-y: auto;
}

.market-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.market-item {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 12px 14px;
  transition: border-color 0.15s;
}

.market-item:hover {
  border-color: #c5c5cc;
}

.mki-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 4px;
}

.mki-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
}

.installs-badge,
.author-badge {
  font-size: 10px;
  color: var(--tertiary);
  background: var(--control);
  padding: 2px 6px;
  border-radius: 4px;
}

.mki-desc {
  font-size: 11px;
  color: var(--secondary);
  line-height: 1.5;
  margin-bottom: 9px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.empty-state-sm,
.empty-state-lg {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 200px;
}

.empty-state-lg {
  grid-column: 1 / -1;
}

.loading-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 40px;
  color: var(--secondary);
}

.spinner {
  width: 32px;
  height: 32px;
  border: 3px solid var(--separator);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
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
