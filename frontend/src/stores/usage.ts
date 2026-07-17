/**
 * Usage Store (Pinia setup style)
 * 使用统计 store：汇总/趋势/模型/供应商/价格表 + 同步状态。
 *
 * 仿 stores/session.ts。后台刷新失败时保留旧数据并 console.warn，绝不刷屏 toast（仿 LogsView:309-327）。
 * 四态契约：
 *   - loading 首次拉取进行时（UsageView 显示 LoadingState）
 *   - error  首次失败且 summary 为空（UsageView 显示 ErrorState + 重试）
 *   - empty  summary.totalRequests===0（UsageView 显示 EmptyState）
 *   - success 其余情况，渲染完整图表
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import * as usageApi from '../api/usage';
import type {
  SummaryFilter,
  TrendFilter,
  StatFilter,
  LogFilter,
  Summary,
  DailyTrendPoint,
  ModelStat,
  ProviderStat,
  UsageRecord,
  SyncResult,
  SyncState,
  ModelPricing,
  UnknownModel,
} from '../api/usage';

export const useUsageStore = defineStore('usage', () => {
  // === State: 业务数据 / Business data ===
  const summary = ref<Summary | null>(null);
  const trends = ref<DailyTrendPoint[]>([]);
  const modelStats = ref<ModelStat[]>([]);
  const providerStats = ref<ProviderStat[]>([]);
  const pricing = ref<ModelPricing[]>([]);
  const unknownModels = ref<UnknownModel[]>([]);
  const requestLogs = ref<UsageRecord[]>([]);
  const syncStates = ref<SyncState[]>([]);

  // === State: 交互状态 / Interaction state ===
  // loading 仅在"首次拉取"或"显式重试"时为 true；后台 30s 刷新不触发 loading，避免闪烁。
  const loading = ref(false);
  const error = ref('');
  // syncing 由「立即同步」按钮触发，与 loading 解耦：同步期间仍可查询现有数据。
  const syncing = ref(false);
  const syncError = ref('');
  const lastSyncedAt = ref<string>('');

  // === State: 筛选器 / Filter ===
  // 默认 source=session_log：避免与 proxy 实时拦截双计（设计 §7.2）。
  const filter = ref<SummaryFilter>(usageApi.createSummaryFilter());
  const trendDays = ref(30);

  // === Computed ===
  const hasData = computed(() => (summary.value?.totalRequests ?? 0) > 0);
  const hasError = computed(() => !!error.value && !summary.value);
  const knownProviders = computed(() => {
    const set = new Set<string>();
    providerStats.value.forEach(p => { if (p.provider) set.add(p.provider); });
    modelStats.value.forEach(m => { if (m.provider) set.add(m.provider); });
    return Array.from(set).sort();
  });

  // === Actions ===

  /**
   * 并发拉取汇总/趋势/模型/供应商四类聚合数据。
   * @param opts.silent 后台静默刷新：失败时保留旧数据 + console.warn，不设 error、不设 loading。
   *                    首次/重试传 false（默认）。
   *
   * Concurrently fetch summary / trends / model stats / provider stats.
   * Silent mode keeps existing data and warns on failure (LogsView pattern).
   */
  async function fetchAll(opts: { silent?: boolean } = {}): Promise<void> {
    const silent = opts.silent === true;
    if (!silent) {
      loading.value = true;
      error.value = '';
    }
    try {
      const trendFilter: TrendFilter = usageApi.createTrendFilter(filter.value, trendDays.value);
      const statFilter: StatFilter = usageApi.createStatFilter(filter.value);
      const [s, t, m, p] = await Promise.all([
        usageApi.getUsageSummary(filter.value),
        usageApi.getDailyTrends(trendFilter),
        usageApi.getModelStats(statFilter),
        usageApi.getProviderStats(statFilter),
      ]);
      summary.value = s;
      trends.value = Array.isArray(t) ? t : [];
      modelStats.value = Array.isArray(m) ? m : [];
      providerStats.value = Array.isArray(p) ? p : [];
      if (!silent) error.value = '';
    } catch (err) {
      const msg = errToString(err);
      if (!silent || !summary.value) {
        // 首次加载或重试失败：写入 error，让 ErrorState 显示 / Show error to the user.
        error.value = msg;
      } else {
        // 已有数据：保留旧数据 + 静默告警，不刷屏 / Keep stale data, warn silently.
        console.warn('[usage.fetchAll] background refresh failed:', err);
      }
    } finally {
      if (!silent) loading.value = false;
    }
  }

  /** 重试入口（ErrorState 的"重试"按钮调用）：等价于首次 fetchAll */
  async function retry(): Promise<void> {
    await fetchAll({ silent: false });
  }

  /**
   * 触发一次阻塞同步。「立即同步」按钮调用。
   * 成功后刷新主数据 + 同步状态；失败抛错供 UI toast。
   */
  async function syncNow(): Promise<SyncResult> {
    syncing.value = true;
    syncError.value = '';
    try {
      const result = await usageApi.syncSessionUsage();
      // 同步完成时间优先取后端返回，回退到当前时间 / Prefer backend time, fall back to now.
      lastSyncedAt.value = timeToString(result?.finishedAt) || new Date().toISOString();
      // 刷新主数据与同步状态（静默，不触发 loading）
      await Promise.all([
        fetchAll({ silent: true }),
        fetchSyncState({ silent: true }),
      ]);
      return result;
    } catch (err) {
      syncError.value = errToString(err);
      throw err;
    } finally {
      syncing.value = false;
    }
  }

  /** 拉取增量同步游标列表（状态展示用） */
  async function fetchSyncState(opts: { silent?: boolean } = {}): Promise<void> {
    try {
      const list = await usageApi.getSyncState();
      syncStates.value = Array.isArray(list) ? list : [];
      // 取最近一条同步完成时间作为 lastSyncedAt 兜底
      if (!lastSyncedAt.value) {
        const latest = syncStates.value
          .map(s => timeToString(s.lastSyncedAt))
          .filter(Boolean)
          .sort()
          .pop();
        if (latest) lastSyncedAt.value = latest;
      }
    } catch (err) {
      if (!opts.silent) {
        console.warn('[usage.fetchSyncState] failed:', err);
      }
    }
  }

  /** 拉取价格表（内置 + 自定义） */
  async function fetchPricing(): Promise<void> {
    try {
      const list = await usageApi.getModelPricing();
      pricing.value = Array.isArray(list) ? list : [];
    } catch (err) {
      console.warn('[usage.fetchPricing] failed:', err);
    }
  }

  /** 拉取未知模型列表 */
  async function fetchUnknownModels(): Promise<void> {
    try {
      const list = await usageApi.getUnknownModels();
      unknownModels.value = Array.isArray(list) ? list : [];
    } catch (err) {
      console.warn('[usage.fetchUnknownModels] failed:', err);
    }
  }

  /**
   * 新增/更新一条价格表条目。
   * 成功后刷新价格表与主数据（hasPrice 标记可能变化）。
   */
  async function savePricing(mp: ModelPricing): Promise<void> {
    await usageApi.upsertModelPricing(mp);
    await Promise.all([fetchPricing(), fetchAll({ silent: true }), fetchUnknownModels()]);
  }

  /** 删除一条自定义价格（内置模型后端会拒绝并抛错） */
  async function removePricing(id: string): Promise<void> {
    await usageApi.deleteModelPricing(id);
    await Promise.all([fetchPricing(), fetchAll({ silent: true })]);
  }

  /** 恢复内置价格 seed */
  async function resetPricing(): Promise<void> {
    await usageApi.resetModelPricing();
    await Promise.all([fetchPricing(), fetchAll({ silent: true })]);
  }

  /** 更新筛选器字段（合并式，便于 watch） */
  function patchFilter(patch: Partial<SummaryFilter>): void {
    filter.value = { ...filter.value, ...patch };
  }

  /** 重置筛选器为默认 */
  function resetFilter(): void {
    filter.value = usageApi.createSummaryFilter();
  }

  function $reset(): void {
    summary.value = null;
    trends.value = [];
    modelStats.value = [];
    providerStats.value = [];
    pricing.value = [];
    unknownModels.value = [];
    requestLogs.value = [];
    syncStates.value = [];
    loading.value = false;
    error.value = '';
    syncing.value = false;
    syncError.value = '';
    lastSyncedAt.value = '';
    filter.value = usageApi.createSummaryFilter();
  }

  return {
    // State
    summary,
    trends,
    modelStats,
    providerStats,
    pricing,
    unknownModels,
    requestLogs,
    syncStates,
    loading,
    error,
    syncing,
    syncError,
    lastSyncedAt,
    filter,
    trendDays,
    // Computed
    hasData,
    hasError,
    knownProviders,
    // Actions
    fetchAll,
    retry,
    syncNow,
    fetchSyncState,
    fetchPricing,
    fetchUnknownModels,
    savePricing,
    removePricing,
    resetPricing,
    patchFilter,
    resetFilter,
    $reset,
  };
});

// === 内部工具 / Internal helpers ===

function errToString(err: unknown): string {
  if (!err) return '未知错误 / Unknown error';
  if (typeof err === 'string') return err;
  if (err instanceof Error) return err.message;
  // Wails 错误数组形式：[message, ...] / Wails often returns arrays as error objects.
  if (Array.isArray(err)) return err.map(String).join('; ');
  try {
    return JSON.stringify(err);
  } catch {
    return String(err);
  }
}

function timeToString(v: unknown): string {
  if (!v) return '';
  if (typeof v === 'string') return v;
  if (typeof v === 'number') {
    let ms = v;
    if (v < 1e12) ms = v * 1000;
    else if (v > 1e15) ms = v / 1e6;
    try {
      return new Date(ms).toISOString();
    } catch {
      return '';
    }
  }
  return '';
}
