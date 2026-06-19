/**
 * Plugin Store
 * Manages Claude and Codex plugin cache
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import * as pluginApi from '../api/plugin';
import * as codexPluginApi from '../api/codexPlugin';
import { plugin, codexplugin } from '../../wailsjs/go/models';

// Use types from wailsjs models
type InstalledPlugin = plugin.InstalledPlugin;
type PluginDetail = plugin.PluginDetail;

// Claude types
interface ClaudeMarketplace {
  Name: string;
  Description?: string;
  URL?: string;
  installCount?: number;
}

// Codex types
type CodexPlugin = codexplugin.CodexPlugin;
type CodexPluginDetail = codexplugin.CodexPluginDetail;
type CodexMarketplace = codexplugin.CodexMarketplace;
type CodexAvailablePlugin = codexplugin.CodexAvailablePlugin;
type CodexPluginsData = codexplugin.CodexPluginsData;

// Extended installed plugin with runtime computed fields
interface ExtendedInstalledPlugin extends InstalledPlugin {
  pluginType?: string;
  hasMcp?: boolean;
}

interface ResourceFilter {
  value: 'all' | 'Skills' | 'Agents' | 'Commands' | 'Hooks' | 'MCP';
  label: string;
  count: number;
}

export const usePluginStore = defineStore('plugin', () => {
  // Extension main tab state
  const extMainTab = ref<'plugins' | 'workspaces' | 'env'>('plugins');

  // Plugin engine selection
  const pluginEngine = ref<'claude' | 'codex'>('claude');

  // Plugin view state: installed | market
  const pluginView = ref<'installed' | 'market'>('installed');

  // Claude plugins
  const ccInstalled = ref<ExtendedInstalledPlugin[]>([]);
  const ccMarkets = ref<ClaudeMarketplace[]>([]);
  const ccAvailable = ref<PluginDetail[]>([]);

  // Codex plugins
  const cxInstalled = ref<CodexPlugin[]>([]);
  const cxMarkets = ref<CodexMarketplace[]>([]);
  const cxAvailable = ref<CodexAvailablePlugin[]>([]);
  const cxWarnings = ref<string[]>([]);

  // Codex active marketplace filter
  const activeMarketId = ref<string | null>(null);

  // Codex search query
  const marketSearchQuery = ref('');

  // Codex sort by: 'installs' | 'name'
  const marketSortBy = ref<'installs' | 'name'>('installs');

  // Active plugin for detail view
  const activePluginId = ref<string | null>(null);

  // Codex active plugin detail
  const cxActivePluginDetail = ref<Record<string, CodexPluginDetail>>({});

  // Claude active plugin detail
  const ccActivePluginDetail = ref<Record<string, PluginDetail>>({});

  // Resource filter
  const resFilter = ref<ResourceFilter['value']>('all');

  // Loading states
  const loadingCC = ref(false);
  const loadingCX = ref(false);
  const loadingDetail = ref(false);
  const loadingMarket = ref(false);

  // Cache state to prevent redundant loads
  const ccDataLoaded = ref(false);
  const cxDataLoaded = ref(false);
  const ccDataLoadedAt = ref<number | null>(null);
  const cxDataLoadedAt = ref<number | null>(null);

  // Computed
  const ccInstalledCount = computed(() => ccInstalled.value.length);
  const cxInstalledCount = computed(() => cxInstalled.value.length);
  const ccMarketCount = computed(() => ccMarkets.value.length);
  const cxMarketCount = computed(() => cxMarkets.value.length);
  const ccAvailableCount = computed(() => ccAvailable.value.length);
  const cxCxAailableCount = computed(() => cxAvailable.value.length);
  const cxEnabledCount = computed(() => cxInstalled.value.filter(p => p.enabled).length);

  // Active plugin detail (Claude)
  const activePlugin = computed(() => {
    if (!activePluginId.value) return null;
    // Prioritize cached detail (contains subItems)
    const cached = ccActivePluginDetail.value[activePluginId.value];
    if (cached) return cached;
    // Fallback to lightweight list item
    return ccInstalled.value.find(p => p.id === activePluginId.value) || null;
  });

  // Active Codex plugin
  const activeCxPlugin = computed(() => {
    if (!activePluginId.value) return null;
    return cxInstalled.value.find(p => p.id === activePluginId.value) || null;
  });

  // Active Codex plugin detail
  const activeCxPluginDetail = computed(() => {
    if (!activePluginId.value) return null;
    return cxActivePluginDetail.value[activePluginId.value] || null;
  });

  // Codex filtered available plugins (by market, search, sort)
  const filteredCxAvailable = computed(() => {
    let plugins = [...cxAvailable.value];

    // Filter by active marketplace
    if (activeMarketId.value) {
      plugins = plugins.filter(p => p.marketplaceName === activeMarketId.value);
    }

    // Filter by search query
    if (marketSearchQuery.value.trim()) {
      const query = marketSearchQuery.value.toLowerCase();
      plugins = plugins.filter(p =>
        (p.name?.toLowerCase().includes(query)) ||
        (p.description?.toLowerCase().includes(query)) ||
        (p.author?.toLowerCase().includes(query))
      );
    }

    // Sort
    if (marketSortBy.value === 'name') {
      plugins.sort((a, b) => (a.name || '').localeCompare(b.name || ''));
    }
    // 'installs' is default, but we don't have install count in CodexAvailablePlugin
    // Keep original order for now

    return plugins;
  });

  // Codex duplicate diagnostic
  const cxDuplicates = computed(() => {
    const seen = new Map<string, CodexPlugin[]>();
    cxInstalled.value.forEach(p => {
      const name = p.name.toLowerCase();
      if (!seen.has(name)) {
        seen.set(name, []);
      }
      seen.get(name)!.push(p);
    });
    return Array.from(seen.values()).filter(group => group.length > 1);
  });

  // Filter options with counts
  const resourceFilters = computed<ResourceFilter[]>(() => {
    const filters: ResourceFilter[] = [
      { value: 'all', label: '全部', count: ccInstalled.value.length },
    ];

    // Count plugins by resource type
    const counts = {
      Skills: 0,
      Agents: 0,
      Commands: 0,
      Hooks: 0,
      MCP: 0,
    };

    ccInstalled.value.forEach(p => {
      // Check by plugin type or sub items
      const type = (p as any).pluginType || '';
      if (type === 'skill' || type === 'hybrid' || type === 'integration') counts.Skills++;
      if (type === 'agent' || type === 'hybrid' || type === 'integration') counts.Agents++;
      if (type === 'command' || type === 'hybrid' || type === 'integration') counts.Commands++;
      if (type === 'hook' || type === 'hybrid' || type === 'integration') counts.Hooks++;
      if ((p as any).hasMcp) counts.MCP++;
    });

    filters.push(
      { value: 'Skills', label: 'Skills', count: counts.Skills },
      { value: 'Agents', label: 'Agents', count: counts.Agents },
      { value: 'Commands', label: 'Commands', count: counts.Commands },
      { value: 'Hooks', label: 'Hooks', count: counts.Hooks },
      { value: 'MCP', label: 'MCP', count: counts.MCP }
    );

    return filters;
  });

  // Filtered Claude plugins based on resource filter
  const filteredCcPlugins = computed(() => {
    if (resFilter.value === 'all') return ccInstalled.value;

    return ccInstalled.value.filter(p => {
      const type = (p as any).pluginType || '';
      switch (resFilter.value) {
        case 'Skills':
          return type === 'skill' || type === 'hybrid' || type === 'integration';
        case 'Agents':
          return type === 'agent' || type === 'hybrid' || type === 'integration';
        case 'Commands':
          return type === 'command' || type === 'hybrid' || type === 'integration';
        case 'Hooks':
          return type === 'hook' || type === 'hybrid' || type === 'integration';
        case 'MCP':
          return (p as any).hasMcp === true;
        default:
          return true;
      }
    });
  });

  // Actions
  function setExtMainTab(tab: 'plugins' | 'workspaces' | 'env') {
    extMainTab.value = tab;
  }

  function setPluginEngine(engine: 'claude' | 'codex') {
    pluginEngine.value = engine;
    // Reset active plugin when switching engines
    activePluginId.value = null;
  }

  function setPluginView(view: 'installed' | 'market') {
    pluginView.value = view;
  }

  function setCCInstalled(plugins: InstalledPlugin[]) {
    ccInstalled.value = plugins as ExtendedInstalledPlugin[];
  }

  function setCCMarkets(markets: ClaudeMarketplace[]) {
    ccMarkets.value = markets;
  }

  function setCCAvailable(plugins: PluginDetail[]) {
    ccAvailable.value = plugins;
  }

  function setCXInstalled(plugins: CodexPlugin[]) {
    cxInstalled.value = plugins;
  }

  function setCXMarkets(markets: CodexMarketplace[]) {
    cxMarkets.value = markets;
  }

  function setCXAvailable(plugins: CodexAvailablePlugin[]) {
    cxAvailable.value = plugins;
  }

  function setCXWarnings(warnings: string[] | undefined) {
    cxWarnings.value = warnings || [];
  }

  function setActiveMarketId(id: string | null) {
    activeMarketId.value = id;
  }

  function setMarketSearchQuery(query: string) {
    marketSearchQuery.value = query;
  }

  function setMarketSortBy(sort: 'installs' | 'name') {
    marketSortBy.value = sort;
  }

  function setLoadingCC(loading: boolean) {
    loadingCC.value = loading;
  }

  function setLoadingCX(loading: boolean) {
    loadingCX.value = loading;
  }

  function setLoadingDetail(loading: boolean) {
    loadingDetail.value = loading;
  }

  function setActivePlugin(id: string | null) {
    activePluginId.value = id;
  }

  function setResFilter(filter: ResourceFilter['value']) {
    resFilter.value = filter;
  }

  // Load Claude installed plugins
  async function loadCcInstalled() {
    setLoadingCC(true);
    try {
      const plugins = await pluginApi.getInstalledPlugins();
      // Analyze plugin type for each plugin
      const extendedPlugins = await Promise.all(
        plugins.map(async (p) => {
          try {
            const pluginType = await pluginApi.analyzePluginType(p.id);
            return { ...p, pluginType };
          } catch {
            return { ...p, pluginType: 'unknown' };
          }
        })
      );
      setCCInstalled(extendedPlugins);
    } catch (error) {
      console.error('[plugin.store.loadCcInstalled]', error);
      throw error;
    } finally {
      setLoadingCC(false);
    }
  }

  // Load Claude marketplaces
  async function loadCcMarkets() {
    try {
      const markets = await pluginApi.getMarketplaces();
      setCCMarkets(markets as unknown as ClaudeMarketplace[]);
    } catch (error) {
      console.error('[plugin.store.loadCcMarkets]', error);
      throw error;
    }
  }

  // Load Claude available plugins
  async function loadCcAvailable() {
    try {
      const plugins = await pluginApi.getAvailablePlugins();
      setCCAvailable(plugins);
    } catch (error) {
      console.error('[plugin.store.loadCcAvailable]', error);
      throw error;
    }
  }

  // Load all Claude plugin data in parallel (installed + markets + available)
  async function loadCcAllData() {
    // Check cache: if loaded within last 5 minutes, skip
    const CACHE_TTL = 5 * 60 * 1000; // 5 minutes
    const now = Date.now();
    if (ccDataLoaded.value && ccDataLoadedAt.value && (now - ccDataLoadedAt.value) < CACHE_TTL) {
      console.log('[plugin.store] Using cached Claude data');
      return;
    }

    setLoadingCC(true);
    try {
      // 子任务状态标记：失败时置 false，最终仅当全部成功才更新缓存标志，
      // 避免失败后误用"已加载"缓存而跳过下次重试
      let allSuccess = true;
      const track = (label: string) => (err: unknown) => {
        allSuccess = false;
        console.error(`[plugin.store] Failed to load ${label}:`, err);
      };
      await Promise.all([
        loadCcInstalled().catch(track('installed')),
        loadCcMarkets().catch(track('markets')),
        loadCcAvailable().catch(track('available')),
      ]);
      if (allSuccess) {
        ccDataLoaded.value = true;
        ccDataLoadedAt.value = Date.now();
      } else {
        // 加载失败时确保不留下陈旧"已加载"缓存
        ccDataLoaded.value = false;
      }
    } catch (error) {
      // 非预期异常同样不更新"成功"缓存
      ccDataLoaded.value = false;
      console.error('[plugin.store.loadCcAllData]', error);
      throw error;
    } finally {
      setLoadingCC(false);
    }
  }

  // --- Codex plugin operations ---

  // Load all Codex plugin data (with cache check)
  async function loadCxPlugins(forceLoad = false) {
    // Check cache: if loaded within last 5 minutes, skip (unless forceLoad)
    const CACHE_TTL = 5 * 60 * 1000; // 5 minutes
    const now = Date.now();
    if (!forceLoad && cxDataLoaded.value && cxDataLoadedAt.value && (now - cxDataLoadedAt.value) < CACHE_TTL) {
      console.log('[plugin.store] Using cached Codex data');
      return;
    }

    setLoadingCX(true);
    try {
      const data = await codexPluginApi.refreshCodexPlugins();
      setCXInstalled(data.installed || []);
      setCXMarkets(data.marketplaces || []);
      setCXAvailable(data.available || []);
      setCXWarnings(data.warnings);
      cxDataLoaded.value = true;
      cxDataLoadedAt.value = Date.now();
    } catch (error) {
      console.error('[plugin.store.loadCxPlugins]', error);
      throw error;
    } finally {
      setLoadingCX(false);
    }
  }

  // Toggle plugin enabled state
  async function togglePlugin(pluginId: string, currentState: boolean) {
    try {
      const result = currentState
        ? await pluginApi.disablePlugin(pluginId)
        : await pluginApi.enablePlugin(pluginId);

      // Refresh the list after toggle
      await loadCcInstalled();

      return result;
    } catch (error) {
      console.error('[plugin.store.togglePlugin]', error);
      throw error;
    }
  }

  // Load plugin detail
  async function loadPluginDetail(pluginId: string): Promise<PluginDetail | null> {
    setLoadingDetail(true);
    try {
      const detail = await pluginApi.getPluginDetail(pluginId);
      // Cache detail for subItems access
      if (detail) {
        ccActivePluginDetail.value[pluginId] = detail;
      }
      return detail;
    } catch (error) {
      console.error('[plugin.store.loadPluginDetail]', error);
      return null;
    } finally {
      setLoadingDetail(false);
    }
  }

  // Get plugin sub items for resource counting
  async function loadPluginSubItems(pluginId: string) {
    try {
      return await pluginApi.getPluginSubItems(pluginId);
    } catch (error) {
      console.error('[plugin.store.loadPluginSubItems]', error);
      return [];
    }
  }

  // Uninstall plugin
  async function uninstallPlugin(pluginId: string) {
    try {
      const result = await pluginApi.uninstallPlugin(pluginId);
      // Refresh after uninstall
      await loadCcInstalled();
      // Clear active if it was the uninstalled plugin
      if (activePluginId.value === pluginId) {
        activePluginId.value = null;
      }
      return result;
    } catch (error) {
      console.error('[plugin.store.uninstallPlugin]', error);
      throw error;
    }
  }

  // Update plugin
  async function updatePlugin(pluginId: string) {
    try {
      const result = await pluginApi.updatePlugin(pluginId);
      // Refresh after update
      await loadCcInstalled();
      return result;
    } catch (error) {
      console.error('[plugin.store.updatePlugin]', error);
      throw error;
    }
  }

  // Add Claude marketplace
  async function addCcMarketplace(source: string) {
    try {
      const result = await pluginApi.addMarketplace(source);
      // Refresh after add
      await loadCcMarkets();
      return result;
    } catch (error) {
      console.error('[plugin.store.addCcMarketplace]', error);
      throw error;
    }
  }

  // Install Claude plugin
  async function installCcPlugin(pluginName: string) {
    try {
      const result = await pluginApi.installPlugin(pluginName);
      // Refresh after install - invalidate cache and reload
      ccDataLoaded.value = false;
      await Promise.all([loadCcInstalled(), loadCcAvailable()]);
      ccDataLoaded.value = true;
      ccDataLoadedAt.value = Date.now();
      return result;
    } catch (error) {
      console.error('[plugin.store.installCcPlugin]', error);
      throw error;
    }
  }

  // Toggle Codex plugin enabled state
  async function toggleCxPlugin(pluginId: string, enabled: boolean) {
    try {
      const selector = { pluginId };
      await codexPluginApi.setCodexPluginEnabled(selector, enabled);
      // Refresh after toggle (force bypass cache)
      await loadCxPlugins(true);
    } catch (error) {
      console.error('[plugin.store.toggleCxPlugin]', error);
      throw error;
    }
  }

  // Install Codex plugin
  async function installCxPlugin(pluginId: string, marketplaceName?: string) {
    try {
      const selector = { pluginId, marketplace: marketplaceName };
      const result = await codexPluginApi.installCodexPlugin(selector);
      // Refresh after install (force bypass cache)
      await loadCxPlugins(true);
      return result;
    } catch (error) {
      console.error('[plugin.store.installCxPlugin]', error);
      throw error;
    }
  }

  // Uninstall Codex plugin
  async function uninstallCxPlugin(pluginId: string, marketplace?: string) {
    try {
      const selector = { pluginId, marketplace };
      const result = await codexPluginApi.uninstallCodexPlugin(selector);
      // Refresh after uninstall (force bypass cache)
      await loadCxPlugins(true);
      // Clear active if it was the uninstalled plugin
      if (activePluginId.value === pluginId) {
        activePluginId.value = null;
      }
      return result;
    } catch (error) {
      console.error('[plugin.store.uninstallCxPlugin]', error);
      throw error;
    }
  }

  // Add Codex marketplace
  async function addCxMarketplace(source: string) {
    try {
      const req = { source };
      const result = await codexPluginApi.addCodexMarketplace(req);
      // Refresh after add (force bypass cache)
      await loadCxPlugins(true);
      return result;
    } catch (error) {
      console.error('[plugin.store.addCxMarketplace]', error);
      throw error;
    }
  }

  // Remove Codex marketplace
  async function removeCxMarketplace(name: string) {
    try {
      const result = await codexPluginApi.removeCodexMarketplace(name);
      // Refresh after remove (force bypass cache)
      await loadCxPlugins(true);
      // Clear active market if it was removed
      if (activeMarketId.value === name) {
        activeMarketId.value = null;
      }
      return result;
    } catch (error) {
      console.error('[plugin.store.removeCxMarketplace]', error);
      throw error;
    }
  }

  // Upgrade Codex marketplace
  async function upgradeCxMarketplace(name: string) {
    try {
      const result = await codexPluginApi.upgradeCodexMarketplace(name);
      // Refresh after upgrade (force bypass cache)
      await loadCxPlugins(true);
      return result;
    } catch (error) {
      console.error('[plugin.store.upgradeCxMarketplace]', error);
      throw error;
    }
  }

  // Load Codex plugin details
  async function loadCxPluginDetail(pluginId: string, marketplace: string): Promise<CodexPluginDetail | null> {
    setLoadingDetail(true);
    try {
      const selector = { pluginId, marketplace };
      const detail = await codexPluginApi.getCodexPluginDetails(selector);
      // Cache detail
      if (detail) {
        cxActivePluginDetail.value[pluginId] = detail;
      }
      return detail;
    } catch (error) {
      console.error('[plugin.store.loadCxPluginDetail]', error);
      return null;
    } finally {
      setLoadingDetail(false);
    }
  }

  return {
    // State
    extMainTab,
    pluginEngine,
    pluginView,
    ccInstalled,
    ccMarkets,
    ccAvailable,
    cxInstalled,
    cxMarkets,
    cxAvailable,
    cxWarnings,
    activeMarketId,
    marketSearchQuery,
    marketSortBy,
    activePluginId,
    resFilter,
    loadingCC,
    loadingCX,
    loadingDetail,
    loadingMarket,
    ccDataLoaded,
    cxDataLoaded,
    ccDataLoadedAt,
    cxDataLoadedAt,

    // Computed
    ccInstalledCount,
    cxInstalledCount,
    cxEnabledCount,
    ccMarketCount,
    cxMarketCount,
    ccAvailableCount,
    cxCxAailableCount,
    activePlugin,
    activeCxPlugin,
    activeCxPluginDetail,
    ccActivePluginDetail,
    resourceFilters,
    filteredCcPlugins,
    filteredCxAvailable,
    cxDuplicates,

    // Actions
    setExtMainTab,
    setPluginEngine,
    setPluginView,
    setCCInstalled,
    setCCMarkets,
    setCCAvailable,
    setCXInstalled,
    setCXMarkets,
    setCXAvailable,
    setCXWarnings,
    setActiveMarketId,
    setMarketSearchQuery,
    setMarketSortBy,
    setLoadingCC,
    setLoadingCX,
    setLoadingDetail,
    setActivePlugin,
    setResFilter,
    loadCcInstalled,
    loadCcMarkets,
    loadCcAvailable,
    loadCcAllData,
    addCcMarketplace,
    installCcPlugin,
    togglePlugin,
    loadPluginDetail,
    loadPluginSubItems,
    uninstallPlugin,
    updatePlugin,
    loadCxPlugins,
    toggleCxPlugin,
    installCxPlugin,
    uninstallCxPlugin,
    addCxMarketplace,
    removeCxMarketplace,
    upgradeCxMarketplace,
    loadCxPluginDetail,
  };
});
