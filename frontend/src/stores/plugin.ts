/**
 * Plugin Store
 * Manages Claude and Codex plugin cache. OpenCode uses its dedicated store.
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

/**
 * 扩展中心子页 key（契约 §1.2 + P6 融合）：'shared-skills' = 公共「共用技能」
 * 子页、'shared-mcp' = 公共「共用 MCP」子页、'shared-market' = 公共「共用插件
 * 市场」子页、'common-center' = 共用中心（P3：CodeBox 公共 skill/MCP 仓库与
 * 五引擎分发/聚合视图）；其余五个值与 pluginEngine 引擎名同字面（引擎子页）。
 */
export type ExtSubPage =
  | 'shared-skills'
  | 'shared-mcp'
  | 'shared-market'
  | 'common-center'
  | 'claude'
  | 'opencode'
  | 'codex'
  | 'pi'
  | 'omp';

export const usePluginStore = defineStore('plugin', () => {
  // 子页选中状态（契约 §1.3）：默认 'claude'（沿用原 pluginEngine 默认值）。
  // 仅内存态不落盘（与原 extMainTab/pluginEngine 一致），刷新回默认。
  // P6 融合：原主 tab 体系（plugins/common/env/tools）随远端扩展中心重构退役，
  // 本分支共用中心改以 'common-center' 子页形式融入。
  const extSubPage = ref<ExtSubPage>('claude');

  // Plugin engine selection
  const pluginEngine = ref<'claude' | 'opencode' | 'codex' | 'pi' | 'omp'>('claude');

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

  // Active plugin for detail view
  const activePluginId = ref<string | null>(null);

  // Codex active plugin detail
  const cxActivePluginDetail = ref<Record<string, CodexPluginDetail>>({});

  // Claude active plugin detail
  const ccActivePluginDetail = ref<Record<string, PluginDetail>>({});

  // Resource filter（契约 §10.6：旧五类资源筛选 chips 已被「MCP 标签 + 类型徽章 + 搜索」
  // 替代，state 保留在 store 不删以避免破坏序列化；面板不再消费，无写入口）
  const resFilter = ref<ResourceFilter['value']>('all');

  // 加载诊断（契约 §7）：面板 DiagnosticBanner 的 errors 数据源。
  // - ccLoadError 由 loadCcAllData 的 track() 在子任务失败时写入（外层 catch 仅非预期异常可达）
  // - cxLoadError 由 loadCxPlugins 既有 catch 写入（该动作 rethrow）
  // 两者成功时清空；动作签名不变。
  const ccLoadError = ref('');
  const cxLoadError = ref('');

  // Loading states
  const loadingCC = ref(false);
  const loadingCX = ref(false);
  const loadingDetail = ref(false);

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
  const cxAvailableCount = computed(() => cxAvailable.value.length);
  const cxEnabledCount = computed(() => cxInstalled.value.filter(p => p.enabled).length);

  // Claude「MCP」资源标签计数（契约 §3.1/§7）：列表级 hasMcp 常缺失，
  // 以 pluginType==='mcp' 为主、hasMcp 兜底
  const ccMcpPluginCount = computed(
    () => ccInstalled.value.filter(p => p.hasMcp || p.pluginType === 'mcp').length
  );

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
      const type = p.pluginType || '';
      if (type === 'skill' || type === 'hybrid' || type === 'integration') counts.Skills++;
      if (type === 'agent' || type === 'hybrid' || type === 'integration') counts.Agents++;
      if (type === 'command' || type === 'hybrid' || type === 'integration') counts.Commands++;
      if (type === 'hook' || type === 'hybrid' || type === 'integration') counts.Hooks++;
      // hasMcp 列表级常缺失，以 pluginType==='mcp' 兜底（契约 §7 计数诚实化）
      if (p.hasMcp || type === 'mcp') counts.MCP++;
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

  // Actions
  function setPluginEngine(engine: 'claude' | 'opencode' | 'codex' | 'pi' | 'omp') {
    pluginEngine.value = engine;
    // Reset active plugin when switching engines
    activePluginId.value = null;
  }

  function setPluginView(view: 'installed' | 'market') {
    pluginView.value = view;
  }

  // 引擎子页判定（共用 MCP 契约 §4.1）：ExtSubPage 中与 pluginEngine 同字面的
  // 五个值；公共子页 'shared-skills' / 'shared-mcp' / 'shared-market' /
  // 'common-center' 不联动引擎态。类型收窄使 setPluginEngine(page) 通过编译
  //（公共子页不在引擎联合内）。
  function isEngineSubPage(
    page: ExtSubPage,
  ): page is 'claude' | 'opencode' | 'codex' | 'pi' | 'omp' {
    return (
      page !== 'shared-skills' &&
      page !== 'shared-mcp' &&
      page !== 'shared-market' &&
      page !== 'common-center'
    );
  }

  // 子页切换（契约 §1.3）：setExtSubPage 是 extSubPage 的唯一写入口。
  // - 同值守卫：顶部页签重击当前子页不得清 activePluginId / 重置 pluginView（防详情态被踢回列表）；
  // - 引擎子页：联动既有引擎切换契约——setPluginEngine（清 activePluginId）+
  //   setPluginView('installed') 重置回列表态（原 ExtensionsView handleEngineChange 契约平移）；
  // - shared-skills / shared-mcp / shared-market 公共子页：不动引擎态（pluginEngine/pluginView/activePluginId 均保持）。
  function setExtSubPage(page: ExtSubPage) {
    if (page === extSubPage.value) return;
    extSubPage.value = page;
    if (isEngineSubPage(page)) {
      setPluginEngine(page);
      setPluginView('installed');
    }
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

  // Load Claude installed plugins
  async function loadCcInstalled() {
    setLoadingCC(true);
    try {
      const plugins = await pluginApi.getInstalledPlugins();
      // Analyze plugin type for each plugin
      const extendedPlugins = await Promise.all(
        (plugins || []).map(async (p) => {
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
      setCCMarkets((markets || []) as unknown as ClaudeMarketplace[]);
    } catch (error) {
      console.error('[plugin.store.loadCcMarkets]', error);
      throw error;
    }
  }

  // Load Claude available plugins
  async function loadCcAvailable() {
    try {
      const plugins = await pluginApi.getAvailablePlugins();
      setCCAvailable(plugins || []);
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
        // 契约 §7：写入点必须落在 track（子任务被 .catch(track) 吞掉、外层 catch 不可达），
        // 供面板 DiagnosticBanner 消费
        ccLoadError.value = `加载${label}失败：${err instanceof Error ? err.message : String(err)}`;
        console.error(`[plugin.store] Failed to load ${label}:`, err);
      };
      await Promise.all([
        loadCcInstalled().catch(track('已安装列表')),
        loadCcMarkets().catch(track('市场源')),
        loadCcAvailable().catch(track('可安装列表')),
      ]);
      if (allSuccess) {
        ccDataLoaded.value = true;
        ccDataLoadedAt.value = Date.now();
        ccLoadError.value = '';
      } else {
        // 加载失败时确保不留下陈旧"已加载"缓存
        ccDataLoaded.value = false;
      }
    } catch (error) {
      // 非预期异常同样不更新"成功"缓存
      ccDataLoaded.value = false;
      ccLoadError.value = `加载失败：${error instanceof Error ? error.message : String(error)}`;
      console.error('[plugin.store.loadCcAllData]', error);
      throw error;
    } finally {
      setLoadingCC(false);
    }
  }

  // 工具栏「刷新」组合动作（契约 §6.1/§7）：失效缓存后全量三 load，不改 loadCcAllData 签名
  async function refreshCc() {
    ccDataLoaded.value = false;
    await loadCcAllData();
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
      cxLoadError.value = '';
    } catch (error) {
      // 契约 §7：写入 cxLoadError 供面板 DiagnosticBanner 消费；保持 rethrow 语义不变
      cxLoadError.value = `Codex 插件加载失败：${error instanceof Error ? error.message : String(error)}`;
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

  // 设置插件子项启用/禁用状态（统一入口，Codex/Claude 都走此 action）
  // - 内部调 main.App.SetPluginSubItemEnabled，按 pluginId 自动分派
  // - subItemType 取后端 SubItemType 单数：skill / agent / command / hook / mcp
  // - 不在此处自动 reload 详情，由调用方根据 engine 决定调 loadPluginDetail / loadCxPluginDetail
  async function setPluginSubItemEnabled(
    pluginId: string,
    subItemType: string,
    subItemId: string,
    enabled: boolean
  ): Promise<void> {
    try {
      await pluginApi.setPluginSubItemEnabled(pluginId, subItemType, subItemId, enabled);
    } catch (error) {
      console.error('[plugin.store.setPluginSubItemEnabled]', error);
      throw error;
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
      // 新市场已 clone 到本地，立即失效缓存并全量刷新（市场源+目录+已装）：
      // 只刷新市场列表的话 ccAvailable 的 5min 缓存不失效，新市场会显示 0 个插件
      ccDataLoaded.value = false;
      await Promise.all([loadCcMarkets(), loadCcInstalled(), loadCcAvailable()]);
      ccDataLoaded.value = true;
      ccDataLoadedAt.value = Date.now();
      return result;
    } catch (error) {
      // 失败时确保不留下陈旧“已加载”缓存
      ccDataLoaded.value = false;
      console.error('[plugin.store.addCcMarketplace]', error);
      throw error;
    }
  }

  // Update Claude marketplace（拉取指定市场源的最新内容）
  // 后端 plugin.Service.UpdateMarketplace 已存在，此处补齐 store 层入口，
  // 供「已安装插件」视图的市场分组头部「更新市场」按钮调用。
  async function updateCcMarketplace(name: string) {
    try {
      const result = await pluginApi.updateMarketplace(name);
      // 更新市场源可能影响 available 与 installed（版本/内容变化），失效缓存后全量 reload
      ccDataLoaded.value = false;
      await Promise.all([loadCcMarkets(), loadCcInstalled(), loadCcAvailable()]);
      ccDataLoaded.value = true;
      ccDataLoadedAt.value = Date.now();
      return result;
    } catch (error) {
      // 失败时确保不留下陈旧"已加载"缓存
      ccDataLoaded.value = false;
      console.error('[plugin.store.updateCcMarketplace]', error);
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

  // Update a single Codex plugin（P4：市场刷新 + 卸载重装 + 恢复启用状态，
  // 后端 codexplugin.UpdatePlugin）；成功后强制刷新列表。
  async function updateCxPlugin(pluginId: string, marketplace?: string) {
    try {
      const selector = { pluginId, marketplace };
      const result = await codexPluginApi.updateCodexPlugin(selector);
      // Refresh after update (force bypass cache)
      await loadCxPlugins(true);
      return result;
    } catch (error) {
      console.error('[plugin.store.updateCxPlugin]', error);
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
    extSubPage,
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
    activePluginId,
    resFilter,
    // Codex 详情缓存（与下方 ccActivePluginDetail 对称；此前漏导出导致实例外赋值
    // 只写穿到普通属性、内部 computed 仍读旧 ref，契约 §7 面板测试需种子化此缓存）
    cxActivePluginDetail,
    ccLoadError,
    cxLoadError,
    loadingCC,
    loadingCX,
    loadingDetail,
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
    cxAvailableCount,
    ccMcpPluginCount,
    activePlugin,
    activeCxPlugin,
    activeCxPluginDetail,
    ccActivePluginDetail,
    resourceFilters,
    cxDuplicates,

    // Actions
    setPluginEngine,
    setPluginView,
    setExtSubPage,
    setCCInstalled,
    setCCMarkets,
    setCCAvailable,
    setCXInstalled,
    setCXMarkets,
    setCXAvailable,
    setCXWarnings,
    setActiveMarketId,
    setLoadingCC,
    setLoadingCX,
    setLoadingDetail,
    setActivePlugin,
    loadCcInstalled,
    loadCcMarkets,
    loadCcAvailable,
    loadCcAllData,
    refreshCc,
    addCcMarketplace,
    updateCcMarketplace,
    installCcPlugin,
    togglePlugin,
    loadPluginDetail,
    setPluginSubItemEnabled,
    uninstallPlugin,
    updatePlugin,
    loadCxPlugins,
    toggleCxPlugin,
    installCxPlugin,
    uninstallCxPlugin,
    updateCxPlugin,
    addCxMarketplace,
    removeCxMarketplace,
    upgradeCxMarketplace,
    loadCxPluginDetail,
  };
});
