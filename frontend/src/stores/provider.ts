/**
 * Provider Store
 *
 * 管理服务提供商与预设的运行时状态。P3-A 扩展点：
 *  - loadProviders(): 通过 GetProviders() 拉取全部提供商（与 legacy ProviderCenter 一致），
 *    并行查询每个提供商的 API 密钥配置状态（HasAPIKey）。
 *  - filter: 前端按格式筛选（all / anthropic / openai），基于 provider.anthropic?.enabled 与
 *    provider.openai?.enabled 字段判断（与后端 IsAnthropicCompatible/IsOpenAICompatible 语义一致）。
 *  - activeProviderId: Provider Center 详情模式状态。
 *
 * 旧 API（setProviders / setTerminalPresets / setMergedPresets / loading*）保留以兼容 P3-B 预设实现。
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import { config } from '../../wailsjs/go/models';
import { GetProviders } from '../../wailsjs/go/config/ConfigService';
import { HasAPIKey } from '../../wailsjs/go/secrets/SecretsService';
import {
  getMergedTerminalPresets,
  getOpenCodeConfig,
  getOpenCodeConfigPath,
  deleteTerminalPreset,
} from '../api/provider';

type Provider = config.Provider;
type TerminalPreset = config.TerminalPreset;
type MergedTerminalPreset = config.MergedTerminalPreset;

export type ProviderFilter = 'all' | 'anthropic' | 'openai';

/** 预设引擎（二级 Tab） */
export type PresetEngine = 'claude' | 'codex' | 'opencode';

/** engine -> wailsjs terminalType 映射（opencode 走 config.json，不调 merged） */
const ENGINE_TO_TERMINAL_TYPE: Record<PresetEngine, string> = {
  claude: 'claude_code',
  codex: 'codex',
  opencode: 'opencode',
};

/** 带元数据的提供商视图模型（id + 密钥配置状态） */
export interface ProviderEntry {
  id: string;
  provider: Provider;
  /** 是否已配置 API 密钥 */
  keyConfigured: boolean;
}

function hasAnthropic(p: Provider): boolean {
  return !!(p && (p as any).anthropic && (p as any).anthropic.enabled);
}
function hasOpenAI(p: Provider): boolean {
  return !!(p && (p as any).openai && (p as any).openai.enabled);
}

export const useProviderStore = defineStore('provider', () => {
  // 原始 providers map（id -> Provider），与后端 GetProviders() 返回结构一致
  const providers = ref<Record<string, Provider>>({});
  // 密钥配置状态 map（id -> 是否已配置）
  const keyStatus = ref<Record<string, boolean>>({});

  // P3-A 新增：筛选 + 详情模式
  const filter = ref<ProviderFilter>('all');
  const activeProviderId = ref<string>('');
  const loading = ref(false);
  const loadError = ref<string>('');

  // 预设相关（P3-B 使用）
  const presets = ref<Record<string, Record<string, TerminalPreset>>>({
    claude: {},
    codex: {},
    opencode: {},
  });
  const mergedPresets = ref<Record<string, MergedTerminalPreset[]>>({
    claude: [],
    codex: [],
    opencode: [],
  });
  const loadingProviders = ref(false);
  const loadingPresets = ref(false);

  // P3-B 预设 tab 状态
  const presetEngine = ref<PresetEngine>('claude');
  const presetFilter = ref<string>('all'); // claude/codex 用 provider 名筛选
  const presetSearch = ref<string>(''); // opencode 卡片搜索
  /** 各引擎是否已加载过（避免重复请求；切换 tab 时按需加载） */
  const presetLoaded = ref<Record<PresetEngine, boolean>>({
    claude: false,
    codex: false,
    opencode: false,
  });
  const presetLoadError = ref<string>('');

  // OpenCode config.json 内容与路径（真实 config 文件，区别于受管 presets）
  const ocConfigContent = ref<string>('');
  const ocConfigPath = ref<string>('');

  // ---- Computed ----

  /** 全部提供商（带密钥状态），按 id 排序 */
  const providerEntries = computed<ProviderEntry[]>(() => {
    return Object.keys(providers.value)
      .sort((a, b) => a.localeCompare(b))
      .map((id) => ({
        id,
        provider: providers.value[id],
        keyConfigured: !!keyStatus.value[id],
      }));
  });

  /** 按当前 filter 过滤后的提供商列表 */
  const filteredProviders = computed<ProviderEntry[]>(() => {
    if (filter.value === 'all') return providerEntries.value;
    return providerEntries.value.filter((entry) => {
      if (filter.value === 'anthropic') return hasAnthropic(entry.provider);
      return hasOpenAI(entry.provider);
    });
  });

  /** 当前激活的详情提供商 */
  const activeProvider = computed<ProviderEntry | null>(() => {
    if (!activeProviderId.value) return null;
    const p = providers.value[activeProviderId.value];
    if (!p) return null;
    return {
      id: activeProviderId.value,
      provider: p,
      keyConfigured: !!keyStatus.value[activeProviderId.value],
    };
  });

  // 兼容旧 computed
  const providerList = computed(() => Object.values(providers.value));
  const claudePresets = computed(() => mergedPresets.value.claude || []);
  const codexPresets = computed(() => mergedPresets.value.codex || []);
  const opencodePresets = computed(() => mergedPresets.value.opencode || []);

  // ---- Actions ----

  /**
   * 拉取全部提供商并并行查询密钥状态。
   * 照搬 legacy Providers.vue loadProviders 逻辑。
   */
  async function loadProviders() {
    loading.value = true;
    loadingProviders.value = true;
    loadError.value = '';
    try {
      const records = await GetProviders();
      const ids = Object.keys(records);
      const statusEntries = await Promise.all(
        ids.map(async (id) => [id, await safeHasKey(id)] as const)
      );
      providers.value = records;
      keyStatus.value = Object.fromEntries(statusEntries);
    } catch (err) {
      console.error('[providerStore.loadProviders]', err);
      loadError.value = String(err);
      providers.value = {};
      keyStatus.value = {};
    } finally {
      loading.value = false;
      loadingProviders.value = false;
    }
  }

  /**
   * 安全检查密钥是否存在，包含 legacy 回退逻辑。
   * 与 ProviderDetailView loadKey 对称：主 key 失败时查 `{provider}:anthropic`/`{provider}:openai`。
   * 确保 ProviderGrid 列表密钥状态与 ProviderDetail 详情一致（P3 高问题修复）。
   */
  async function safeHasKey(id: string): Promise<boolean> {
    try {
      // 先查主 key
      const hasMain = await HasAPIKey(id);
      if (hasMain) return true;

      // Legacy 回退：查 `{provider}:anthropic` 和 `{provider}:openai`
      // 与 ProviderDetailView loadKey 逻辑对称
      const legacyKeys = [`${id}:anthropic`, `${id}:openai`];
      for (const key of legacyKeys) {
        try {
          const hasLegacy = await HasAPIKey(key);
          if (hasLegacy) return true;
        } catch {
          // 忽略单个 legacy key 查询失败
        }
      }
      return false;
    } catch {
      return false;
    }
  }

  function setFilter(next: ProviderFilter) {
    filter.value = next;
  }

  function openProvider(id: string) {
    activeProviderId.value = id;
  }

  function closeProvider() {
    activeProviderId.value = '';
  }

  // 兼容旧 actions
  function setProviders(newProviders: Record<string, Provider>) {
    providers.value = newProviders;
  }
  function setTerminalPresets(terminalType: string, newPresets: Record<string, TerminalPreset>) {
    presets.value[terminalType] = newPresets;
  }
  function setMergedPresets(terminalType: string, newPresets: MergedTerminalPreset[]) {
    mergedPresets.value[terminalType] = newPresets;
  }
  function setLoadingProviders(val: boolean) {
    loadingProviders.value = val;
  }
  function setLoadingPresets(val: boolean) {
    loadingPresets.value = val;
  }

  // P3-B 预设 actions

  /**
   * 加载指定引擎的预设数据。
   * - claude/codex: GetMergedTerminalPresets(terminalType)，结果存入 mergedPresets[engine]
   * - opencode: GetOpenCodeConfig + GetOpenCodeConfigPath（config.json 管理，不走 merged）
   * 已加载过的引擎会跳过，force=true 强制刷新。
   */
  async function loadPresets(engine: PresetEngine, force = false) {
    if (!force && presetLoaded.value[engine]) return;
    loadingPresets.value = true;
    presetLoadError.value = '';
    try {
      if (engine === 'opencode') {
        const [content, path] = await Promise.all([
          getOpenCodeConfig(),
          getOpenCodeConfigPath(),
        ]);
        ocConfigContent.value = content || '';
        ocConfigPath.value = path || '';
      } else {
        const terminalType = ENGINE_TO_TERMINAL_TYPE[engine];
        const list = await getMergedTerminalPresets(terminalType);
        mergedPresets.value = { ...mergedPresets.value, [engine]: list || [] };
      }
      presetLoaded.value = { ...presetLoaded.value, [engine]: true };
    } catch (err) {
      console.error('[providerStore.loadPresets]', engine, err);
      presetLoadError.value = String(err);
      if (engine !== 'opencode') {
        mergedPresets.value = { ...mergedPresets.value, [engine]: [] };
      }
    } finally {
      loadingPresets.value = false;
    }
  }

  /**
   * 删除指定引擎的预设（仅支持 claude/codex；opencode 走 config.json，不在此路径）。
   * engine → terminalType 映射与 loadPresets 一致；调用方传 MergedTerminalPreset.key。
   * 后端 DeleteTerminalPreset 仅对用户 map 生效：内置/managed 项 key 不在 map 里会 no-op，
   * 因此调用方必须先在前端按 source='user' 过滤，禁止对内置项调用此 action。
   */
  async function deletePreset(engine: PresetEngine, key: string) {
    if (engine === 'opencode') {
      throw new Error('opencode presets are managed via config.json, not deletable here');
    }
    const terminalType = ENGINE_TO_TERMINAL_TYPE[engine];
    await deleteTerminalPreset(terminalType, key);
    // 删除后强制刷新该引擎列表，确保 UI 与后端一致
    await loadPresets(engine, true);
  }

  /** 切换二级 Tab 引擎，并按需加载该引擎数据 */
  function setPresetEngine(engine: PresetEngine) {
    if (presetEngine.value === engine) return;
    presetEngine.value = engine;
    presetFilter.value = 'all';
    presetSearch.value = '';
    void loadPresets(engine);
  }

  function setPresetFilter(filter: string) {
    presetFilter.value = filter;
  }

  function setPresetSearch(q: string) {
    presetSearch.value = q;
  }

  return {
    // State
    providers,
    keyStatus,
    filter,
    activeProviderId,
    loading,
    loadError,
    presets,
    mergedPresets,
    loadingProviders,
    loadingPresets,

    // P3-B 预设状态
    presetEngine,
    presetFilter,
    presetSearch,
    presetLoaded,
    presetLoadError,
    ocConfigContent,
    ocConfigPath,

    // Computed (P3-A)
    providerEntries,
    filteredProviders,
    activeProvider,

    // Computed (legacy)
    providerList,
    claudePresets,
    codexPresets,
    opencodePresets,

    // Actions (P3-A)
    loadProviders,
    setFilter,
    openProvider,
    closeProvider,

    // Actions (P3-B)
    loadPresets,
    deletePreset,
    setPresetEngine,
    setPresetFilter,
    setPresetSearch,

    // Actions (legacy)
    setProviders,
    setTerminalPresets,
    setMergedPresets,
    setLoadingProviders,
    setLoadingPresets,
  };
});
