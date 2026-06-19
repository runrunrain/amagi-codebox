<template>
  <div class="plugin-installed-panel">
    <!-- Initial loading state -->
    <LoadingState v-if="initialLoading" message="加载插件中..." />

    <!-- Initial error state -->
    <ErrorState
      v-else-if="initialError"
      :message="initialError"
      :on-retry="handleRetry"
    />

    <!-- Main content -->
    <template v-else>
    <!-- Top segmented control: Installed | Market -->
    <div class="view-segmented">
      <button
        :class="['seg-btn', { active: localView === 'installed' }]"
        @click="setView('installed')"
      >
        已安装 <span class="res-count">{{ installedCount }}</span>
      </button>
      <button
        :class="['seg-btn', { active: localView === 'market' }]"
        @click="setView('market')"
      >
        市场 <span class="res-count">{{ marketCount }}</span>
      </button>
    </div>

    <!-- Installed view -->
    <div v-if="localView === 'installed'" class="installed-view">
      <!-- Codex-specific status banner -->
      <div v-if="engine === 'codex' && hasWarnings" class="status-banner warning">
        <span class="sb-text">{{ warningMessage }}</span>
        <button class="sb-btn" @click="handleRetry">重试</button>
      </div>

      <!-- Codex statistics -->
      <div v-if="engine === 'codex'" class="stat-bar">
        <div class="stat-cell">
          <div class="sv">{{ cxInstalledCount }}</div>
          <div class="sl">已安装</div>
        </div>
        <div class="stat-cell">
          <div class="sv">{{ cxEnabledCount }}</div>
          <div class="sl">已启用</div>
        </div>
        <div class="stat-cell">
          <div class="sv">{{ cxMarketCount }}</div>
          <div class="sl">市场源</div>
        </div>
      </div>

      <!-- Codex duplicate diagnostic -->
      <div v-if="engine === 'codex' && hasDuplicates" class="dup-card">
        <p>检测到 {{ duplicateCount }} 组重复插件：{{ duplicateNames }}。建议清理冗余市场源以避免子项级开关冲突。</p>
      </div>

      <!-- Header: title + resource filter -->
      <div class="panel-header">
        <div class="header-title">
          <h2>已安装</h2>
          <span class="count-badge">{{ filteredPlugins.length }}</span>
        </div>
        <div class="res-filter-bar">
          <span class="rf-label">筛选:</span>
          <button
            v-for="filter in resourceFilters"
            :key="filter.value"
            :class="['res-chip', { active: resFilter === filter.value }]"
            @click="setResFilter(filter.value)"
          >
            {{ filter.label }}
            <span v-if="filter.value !== 'all'" class="res-count">{{ filter.count }}</span>
          </button>
        </div>
      </div>

      <!-- Split view: list + detail -->
      <div v-if="filteredPlugins.length > 0" class="ex-split">
        <!-- Plugin list -->
        <div class="plugin-list">
          <div
            v-for="plugin in filteredPlugins"
            :key="(plugin as any).id || (plugin as any).pluginId"
            :class="['plg-item', { active: activePluginId === ((plugin as any).id || (plugin as any).pluginId) }]"
            @click="selectPlugin(plugin)"
          >
            <div class="plg-item-main">
              <div class="plg-name-row">
                <span class="plg-name">{{ plugin.name }}</span>
                <Switch
                  :model-value="plugin.enabled"
                  @update:model-value="(val) => handleToggle(plugin, val)"
                />
              </div>
              <div class="plg-meta-row">
                <Badge type="type" :text="pluginTypeLabel(plugin)" :color="pluginTypeColor(plugin)" />
                <Badge v-if="plugin.version" type="ver" :text="'v' + plugin.version" />
                <Badge v-if="engine === 'claude' && (plugin as any).scope" type="scope" :text="(plugin as any).scope === 'global' ? '全局' : '项目'" />
                <!-- Codex source badge -->
                <Badge v-if="engine === 'codex' && (plugin as any).source" type="source" :text="(plugin as any).source" variant="muted" />
                <!-- Codex duplicate warning badge -->
                <Badge v-if="engine === 'codex' && hasDuplicateWarning(plugin)" type="warning" text="重复诊断" color="warning" />
              </div>
              <p v-if="(plugin as any).description" class="plg-desc">{{ truncate((plugin as any).description, 80) }}</p>
            </div>
          </div>
        </div>

        <!-- Plugin detail -->
        <div v-if="activePlugin" class="plg-detail">
          <div class="plg-detail-head">
            <div>
              <h3>{{ activePlugin.name }}</h3>
              <div class="plg-row">
                <Badge type="type" :text="pluginTypeLabel(activePlugin)" :color="pluginTypeColor(activePlugin)" />
                <Badge v-if="activePlugin.version" type="ver" :text="'v' + activePlugin.version" />
                <Badge v-if="engine === 'claude' && (activePlugin as any).scope" type="scope" :text="(activePlugin as any).scope === 'global' ? '全局' : '项目'" />
                <!-- Codex source badge -->
                <Badge v-if="engine === 'codex' && (activePlugin as any).source" type="source" :text="(activePlugin as any).source" variant="muted" />
                <!-- Codex duplicate warning badge -->
                <Badge v-if="engine === 'codex' && hasDuplicateWarning(activePlugin)" type="warning" text="重复诊断" color="warning" />
                <!-- Codex pluginId badge -->
                <Badge v-if="engine === 'codex' && (activePlugin as any).pluginId" type="pid" :text="'PID: ' + shortId((activePlugin as any).pluginId)" variant="mono" />
              </div>
            </div>
            <div class="plg-detail-actions">
              <AppButton variant="ghost" size="small" @click="handleUpdate">
                更新
              </AppButton>
              <AppButton variant="danger" size="small" @click="handleUninstall">
                卸载
              </AppButton>
            </div>
          </div>

          <!-- Codex duplicate diagnostic detail -->
          <div v-if="engine === 'codex' && hasDuplicateWarning(activePlugin)" class="duplicate-diagnostic-card">
            <strong>重复诊断</strong>
            <p>{{ getDuplicateWarning(activePlugin) }}</p>
          </div>

          <div v-if="activePlugin.description" class="plg-detail-section">
            <p>{{ activePlugin.description }}</p>
          </div>

          <!-- Meta info -->
          <div class="plg-detail-meta">
            <div v-if="activePlugin.marketplace" class="meta-row">
              <span class="meta-label">来源:</span>
              <span class="meta-value">{{ activePlugin.marketplace }}</span>
            </div>
            <div class="meta-row">
              <span class="meta-label">安装路径:</span>
              <span class="meta-value mono">{{ (activePlugin as any).installPath || (activePlugin as any).manifestPath }}</span>
            </div>
            <div v-if="(activePlugin as any).installedAt" class="meta-row">
              <span class="meta-label">安装时间:</span>
              <span class="meta-value">{{ formatDate((activePlugin as any).installedAt) }}</span>
            </div>
            <div v-if="(activePlugin as any).lastUpdated" class="meta-row">
              <span class="meta-label">最后更新:</span>
              <span class="meta-value">{{ formatDate((activePlugin as any).lastUpdated) }}</span>
            </div>
            <!-- Codex pluginId -->
            <div v-if="engine === 'codex' && (activePlugin as any).pluginId" class="meta-row">
              <span class="meta-label">Plugin ID:</span>
              <span class="meta-value mono">{{ (activePlugin as any).pluginId }}</span>
            </div>
          </div>

          <!-- Resource counts / Capabilities (Codex) -->
          <div v-if="engine === 'codex'" class="plg-detail-resources">
            <h4>能力</h4>
            <div class="res-grid">
              <div v-if="hasSkills" class="res-item">
                <Badge type="type" text="Skill" color="skill" />
                <span class="res-count">{{ skillsCount }}</span>
              </div>
              <div v-if="hasAgents" class="res-item">
                <Badge type="type" text="Agent" color="agent" />
                <span class="res-count">{{ agentsCount }}</span>
              </div>
              <div v-if="hasCommands" class="res-item">
                <Badge type="type" text="Command" color="command" />
                <span class="res-count">{{ commandsCount }}</span>
              </div>
              <div v-if="hasHooks" class="res-item">
                <Badge type="type" text="Hook" color="hook" />
                <span class="res-count">{{ hooksCount }}</span>
              </div>
              <div v-if="hasMcp" class="res-item">
                <Badge type="type" text="MCP" color="mcp" />
                <span class="res-count">1</span>
              </div>
              <!-- Codex capabilities from interface -->
              <template v-if="activeCxDetail?.manifest?.interface?.capabilities">
                <div v-for="cap in activeCxDetail.manifest.interface.capabilities" :key="cap" class="res-item">
                  <Badge type="type" :text="cap" color="capability" />
                </div>
              </template>
            </div>
          </div>

          <!-- Claude resource counts -->
          <div v-else class="plg-detail-resources">
            <h4>资源</h4>
            <div class="res-grid">
              <div v-if="hasSkills" class="res-item">
                <Badge type="type" text="Skill" color="skill" />
                <span class="res-count">{{ skillsCount }}</span>
              </div>
              <div v-if="hasAgents" class="res-item">
                <Badge type="type" text="Agent" color="agent" />
                <span class="res-count">{{ agentsCount }}</span>
              </div>
              <div v-if="hasCommands" class="res-item">
                <Badge type="type" text="Command" color="command" />
                <span class="res-count">{{ commandsCount }}</span>
              </div>
              <div v-if="hasHooks" class="res-item">
                <Badge type="type" text="Hook" color="hook" />
                <span class="res-count">{{ hooksCount }}</span>
              </div>
              <div v-if="hasMcp" class="res-item">
                <Badge type="type" text="MCP" color="mcp" />
                <span class="res-count">1</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Empty detail when nothing selected -->
        <div v-else class="plg-detail-empty">
          <EmptyState icon="◇" title="选择插件" description="点击左侧插件查看详细信息" />
        </div>
      </div>

      <!-- Empty state when no plugins -->
      <EmptyState v-else icon="⊘" :title="emptyTitle" :description="emptyDesc" />
    </div>

    <!-- Market view (embedded PluginMarketPanel) -->
    <PluginMarketPanel
      v-else
      :engine="engine"
      @add-market="handleAddMarket"
    />

    <!-- Uninstall Confirmation Dialog -->
    <ConfirmDialog
      v-model:open="showUninstallDialog"
      title="确认卸载"
      :message="pluginToUninstall ? `确定要卸载插件「${pluginToUninstall.name}」吗？此操作不可恢复。` : ''"
      danger
      confirm-text="卸载"
      @confirm="confirmUninstall"
    />
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import { storeToRefs } from 'pinia';
import { usePluginStore } from '../../stores/plugin';
import Switch from '../ui/Switch.vue';
import Badge from '../ui/Badge.vue';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import PluginMarketPanel from './PluginMarketPanel.vue';
import { truncate } from '../../utils/format';

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
  filteredCcPlugins,
  activePluginId,
  activePlugin,
  activeCxPlugin,
  activeCxPluginDetail,
  resFilter,
  resourceFilters,
  ccInstalled,
  cxInstalled,
  cxWarnings,
  cxDuplicates,
  ccInstalledCount,
  cxInstalledCount,
  cxEnabledCount,
  ccMarketCount,
  cxMarketCount,
  pluginView,
} = storeToRefs(pluginStore);

const {
  setActivePlugin,
  setResFilter,
  togglePlugin,
  uninstallPlugin,
  updatePlugin,
  loadCcInstalled,
  loadCxPlugins,
  toggleCxPlugin,
  uninstallCxPlugin,
  loadCxPluginDetail,
  setPluginView,
} = pluginStore;

// Local view state (synced with store)
const localView = ref<'installed' | 'market'>(props.engine === 'claude' ? 'installed' : 'installed');

// Initial loading and error states
const initialLoading = ref(true);
const initialError = ref('');

// Uninstall confirmation
const showUninstallDialog = ref(false);
const pluginToUninstall = ref<any>(null);

// Retry function for ErrorState
const handleRetry = async () => {
  initialLoading.value = true
  initialError.value = ''
  try {
    if (props.engine === 'claude') {
      await loadCcInstalled()
    } else {
      await loadCxPlugins()
    }
  } catch (err) {
    initialError.value = String(err)
  } finally {
    initialLoading.value = false
  }
}

// Sync with store
watch(pluginView, (val) => {
  localView.value = val;
});

watch(localView, (val) => {
  setPluginView(val);
});

function setView(view: 'installed' | 'market') {
  localView.value = view;
  setPluginView(view);
}

// Computed counts
const installedCount = computed(() => {
  return props.engine === 'claude' ? ccInstalledCount.value : cxInstalledCount.value;
});

const marketCount = computed(() => {
  return props.engine === 'claude' ? ccMarketCount.value : cxMarketCount.value;
});

// Filtered plugins based on engine
const filteredPlugins = computed(() => {
  if (props.engine === 'claude') {
    return filteredCcPlugins.value;
  }
  // Codex: filter by resource type
  if (resFilter.value === 'all') return cxInstalled.value;

  return cxInstalled.value.filter((p: any) => {
    // Would need to check detail for resource types
    // For now, return all
    return true;
  });
});

// Current active plugin (engine-aware)
const currentActivePlugin = computed(() => {
  return props.engine === 'codex' ? activeCxPlugin.value : activePlugin.value;
});

const activeCxDetail = computed(() => {
  if (!activePluginId.value || props.engine !== 'codex') return null;
  return (activeCxPluginDetail.value as Record<string, any>)[activePluginId.value] || null;
});

// Codex warnings
const hasWarnings = computed(() => cxWarnings.value && cxWarnings.value.length > 0);
const warningMessage = computed(() => {
  if (!cxWarnings.value || cxWarnings.value.length === 0) return '';
  return cxWarnings.value.join('; ');
});

// Codex duplicates
const hasDuplicates = computed(() => cxDuplicates.value && cxDuplicates.value.length > 0);
const duplicateCount = computed(() => cxDuplicates.value?.length || 0);
const duplicateNames = computed(() => {
  if (!cxDuplicates.value) return '';
  return cxDuplicates.value.map(group => group[0]?.name).filter(Boolean).join('、');
});

// Resource counts for active plugin
const hasSkills = computed(() => {
  const p = currentActivePlugin.value;
  if (!p) return false;
  if (props.engine === 'codex') {
    const detail = activeCxDetail.value;
    return detail?.skills?.length > 0;
  }
  const type = (p as any).pluginType || '';
  return type === 'skill' || type === 'hybrid' || type === 'integration';
});

const hasAgents = computed(() => {
  const p = currentActivePlugin.value;
  if (!p) return false;
  if (props.engine === 'codex') {
    const detail = activeCxDetail.value;
    return detail?.agents?.length > 0;
  }
  const type = (p as any).pluginType || '';
  return type === 'agent' || type === 'hybrid' || type === 'integration';
});

const hasCommands = computed(() => {
  const p = currentActivePlugin.value;
  if (!p) return false;
  if (props.engine === 'codex') {
    const detail = activeCxDetail.value;
    return detail?.commands?.length > 0;
  }
  const type = (p as any).pluginType || '';
  return type === 'command' || type === 'hybrid' || type === 'integration';
});

const hasHooks = computed(() => {
  const p = currentActivePlugin.value;
  if (!p) return false;
  if (props.engine === 'codex') {
    const detail = activeCxDetail.value;
    return detail?.hooks?.length > 0;
  }
  const type = (p as any).pluginType || '';
  return type === 'hook' || type === 'hybrid' || type === 'integration';
});

const hasMcp = computed(() => {
  const p = currentActivePlugin.value;
  if (!p) return false;
  if (props.engine === 'codex') {
    const detail = activeCxDetail.value;
    return detail?.hasMcp === true;
  }
  return (p as any).hasMcp === true;
});

// Counts from detail (Codex)
const skillsCount = computed(() => {
  if (props.engine === 'codex') {
    return activeCxDetail.value?.skills?.length || 0;
  }
  return hasSkills.value ? 1 : 0;
});

const agentsCount = computed(() => {
  if (props.engine === 'codex') {
    return activeCxDetail.value?.agents?.length || 0;
  }
  return hasAgents.value ? 1 : 0;
});

const commandsCount = computed(() => {
  if (props.engine === 'codex') {
    return activeCxDetail.value?.commands?.length || 0;
  }
  return hasCommands.value ? 1 : 0;
});

const hooksCount = computed(() => {
  if (props.engine === 'codex') {
    return activeCxDetail.value?.hooks?.length || 0;
  }
  return hasHooks.value ? 1 : 0;
});

// Empty state texts
const emptyTitle = computed(() => {
  return props.engine === 'claude' ? '暂无已安装插件' : '暂无已安装 Codex 插件';
});

const emptyDesc = computed(() => {
  return props.engine === 'claude'
    ? '从插件市场安装 Claude 插件后将显示在此处'
    : '从插件市场安装 Codex 插件后将显示在此处';
});

// Plugin type helpers
function pluginTypeLabel(plugin: any): string {
  if (props.engine === 'codex') {
    // Codex plugins don't have pluginType in the base struct
    // Use capabilities or default to 'Plugin'
    return 'Plugin';
  }
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

function pluginTypeColor(plugin: any): string {
  if (props.engine === 'codex') return 'plugin';
  const type = plugin.pluginType || 'unknown';
  return type;
}

// Duplicate warning helpers
function hasDuplicateWarning(plugin: any): boolean {
  return !!(plugin as any).warning || !!(plugin as any).duplicateOf;
}

function getDuplicateWarning(plugin: any): string {
  const raw = (plugin as any).warning || (plugin as any).duplicateOf || '';
  if (!raw) return '';
  const mergedText = /归并|duplicate|重复/i.test(raw) ? raw : `检测到重复安装记录：${raw}`;
  return mergedText;
}

function shortId(id: string): string {
  if (!id) return '';
  if (id.length > 12) return id.slice(0, 12) + '...';
  return id;
}

// Format date
function formatDate(dateStr: string): string {
  if (!dateStr) return '';
  const date = new Date(dateStr);
  return date.toLocaleDateString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  });
}

// Actions
function selectPlugin(plugin: any) {
  const id = plugin.id || plugin.pluginId;
  setActivePlugin(id);
  // Load Codex detail if needed
  if (props.engine === 'codex' && plugin.marketplace) {
    loadCxPluginDetail(plugin.pluginId || id, plugin.marketplace);
  }
}

async function handleToggle(plugin: any, value: boolean) {
  if (props.engine === 'codex') {
    try {
      await toggleCxPlugin(plugin.pluginId || plugin.id, !value);
    } catch (error) {
      console.error('Toggle Codex plugin failed:', error);
    }
  } else {
    try {
      await togglePlugin(plugin.id, !value);
    } catch (error) {
      console.error('Toggle plugin failed:', error);
    }
  }
}

async function handleUninstall() {
  const plugin = currentActivePlugin.value;
  if (!plugin) return;
  pluginToUninstall.value = plugin;
  showUninstallDialog.value = true;
}

async function confirmUninstall() {
  const plugin = pluginToUninstall.value;
  if (!plugin) return;

  if (props.engine === 'codex') {
    try {
      await uninstallCxPlugin((plugin as any).pluginId || (plugin as any).id, (plugin as any).marketplace);
    } catch (error) {
      console.error('Uninstall Codex plugin failed:', error);
    }
  } else {
    try {
      await uninstallPlugin((plugin as any).id);
    } catch (error) {
      console.error('Uninstall plugin failed:', error);
    }
  }
  showUninstallDialog.value = false;
  pluginToUninstall.value = null;
}

async function handleUpdate() {
  const plugin = currentActivePlugin.value;
  if (!plugin) return;
  if (props.engine === 'codex') {
    // Codex update via marketplace upgrade
    console.log('Update Codex plugin:', plugin.name);
  } else {
    try {
      await updatePlugin(plugin.id);
    } catch (error) {
      console.error('Update plugin failed:', error);
    }
  }
}

function handleAddMarket(engine: 'claude' | 'codex') {
  emit('addMarket', engine);
}

// Load plugins on mount
onMounted(async () => {
  initialLoading.value = true;
  initialError.value = '';
  try {
    if (props.engine === 'claude') {
      await loadCcInstalled();
    } else {
      await loadCxPlugins();
    }
  } catch (err) {
    initialError.value = String(err);
  } finally {
    initialLoading.value = false;
  }
});
</script>

<style scoped>
.plugin-installed-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* Segmented control at top */
.view-segmented {
  display: flex;
  gap: 6px;
  background: var(--control);
  padding: 4px;
  border-radius: 10px;
  width: fit-content;
}

.seg-btn {
  font-size: 12px;
  padding: 5px 14px;
  border: none;
  background: transparent;
  color: var(--secondary);
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.15s;
  display: flex;
  align-items: center;
  gap: 4px;
}

.seg-btn:hover {
  color: var(--label);
}

.seg-btn.active {
  background: var(--label);
  color: #fff;
}

.res-count {
  font-size: 10px;
  opacity: 0.7;
}

/* Codex status banner */
.status-banner {
  display: flex;
  align-items: center;
  gap: 12px;
  border-radius: 10px;
  padding: 10px 14px;
  font-size: 12px;
  margin-bottom: 12px;
}

.status-banner.warning {
  background: rgba(255, 149, 0, 0.08);
  border: 1px solid rgba(255, 149, 0, 0.3);
  color: #9a6200;
}

.sb-text {
  flex: 1;
  line-height: 1.5;
}

.sb-btn {
  font-size: 11px;
  color: var(--accent);
  background: none;
  border: none;
  cursor: pointer;
  text-decoration: underline;
  font-family: inherit;
}

/* Codex stat bar */
.stat-bar {
  display: flex;
  gap: 20px;
  padding: 14px 16px;
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 10px;
  margin-bottom: 12px;
}

.stat-cell {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
}

.sv {
  font-size: 18px;
  font-weight: 600;
  color: var(--label);
}

.sl {
  font-size: 11px;
  color: var(--tertiary);
}

/* Codex duplicate diagnostic card */
.dup-card {
  background: rgba(255, 149, 0, 0.04);
  border: 1px solid rgba(255, 149, 0, 0.2);
  border-radius: 10px;
  padding: 12px 14px;
  margin-bottom: 12px;
}

.dup-card p {
  font-size: 12px;
  color: #9a6200;
  line-height: 1.5;
  margin: 0;
}

/* Duplicate diagnostic in detail */
.duplicate-diagnostic-card {
  background: rgba(255, 149, 0, 0.04);
  border: 1px solid rgba(255, 149, 0, 0.2);
  border-radius: 8px;
  padding: 10px 12px;
  margin-bottom: 12px;
}

.duplicate-diagnostic-card strong {
  display: block;
  font-size: 12px;
  color: #9a6200;
  margin-bottom: 4px;
}

.duplicate-diagnostic-card p {
  font-size: 11px;
  color: #9a6200;
  line-height: 1.5;
  margin: 0;
}

.installed-view {
  display: flex;
  flex-direction: column;
  gap: 12px;
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

.res-filter-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.rf-label {
  font-size: 11px;
  color: var(--tertiary);
  margin-right: 2px;
}

.res-chip {
  font-size: 11px;
  padding: 4px 8px;
  border-radius: 6px;
  background: var(--control);
  color: var(--secondary);
  border: none;
  cursor: pointer;
  transition: all 0.12s;
  display: flex;
  align-items: center;
  gap: 4px;
}

.res-chip:hover {
  background: var(--controlHover);
}

.res-chip.active {
  background: var(--label);
  color: #fff;
}

.res-count {
  font-size: 10px;
  opacity: 0.7;
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
  gap: 8px;
  max-height: calc(100vh - 320px);
  overflow-y: auto;
}

.plg-item {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 11px 13px;
  cursor: pointer;
  transition: border-color 0.15s, background 0.12s;
}

.plg-item:hover {
  border-color: #c5c5cc;
}

.plg-item.active {
  border-color: var(--accent);
  background: rgba(0, 122, 255, 0.04);
}

.plg-item-main {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.plg-name-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.plg-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.plg-meta-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.plg-desc {
  font-size: 12px;
  color: var(--secondary);
  line-height: 1.4;
  margin: 0;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.plg-detail {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 16px 18px;
  max-height: calc(100vh - 320px);
  overflow-y: auto;
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

.plg-detail-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--separator);
}

.plg-detail-head h3 {
  font-size: 16px;
  font-weight: 600;
  color: var(--label);
  margin: 0 0 6px 0;
}

.plg-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.plg-detail-actions {
  display: flex;
  gap: 8px;
}

.plg-detail-section {
  margin-bottom: 16px;
}

.plg-detail-section p {
  font-size: 13px;
  color: var(--secondary);
  line-height: 1.5;
  margin: 0;
}

.plg-detail-meta {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 16px;
  padding: 12px;
  background: var(--control);
  border-radius: 8px;
}

.meta-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}

.meta-label {
  color: var(--tertiary);
  min-width: 70px;
}

.meta-value {
  color: var(--secondary);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.meta-value.mono {
  font-family: var(--mono);
}

.plg-detail-resources {
  margin-top: 16px;
}

.plg-detail-resources h4 {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
  margin: 0 0 10px 0;
}

.res-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.res-item {
  display: flex;
  align-items: center;
  gap: 6px;
}

.res-item .res-count {
  font-size: 12px;
  color: var(--secondary);
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
