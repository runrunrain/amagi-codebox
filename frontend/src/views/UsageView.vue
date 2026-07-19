<template>
  <section class="view-usage">
    <LoadingState v-if="loading && !summary" message="加载使用统计中..." />

    <ErrorState
      v-else-if="error && !summary"
      :message="error"
      :on-retry="handleRetry"
    />

    <template v-else>
      <PageHead title="使用统计" description="模型用量、缓存经济性与成本趋势；默认只统计会话日志">
        <template #actions>
          <div class="head-actions">
            <span v-if="lastSyncedAt" class="last-synced" :title="`上次同步：${lastSyncedAt}`">
              已同步 · {{ formatRelative(lastSyncedAt) }}
            </span>
            <AppButton variant="ghost" size="small" :disabled="syncing" @click="handleSync">
              <span class="sync-icon" :class="{ spinning: syncing }" />
              {{ syncing ? '同步中...' : '立即同步' }}
            </AppButton>
          </div>
        </template>
      </PageHead>

      <EmptyState
        v-if="summary && summary.totalRequests === 0"
        title="暂无使用数据"
        description="可从 ~/.claude / ~/.codex / ~/.local/share/opencode 拉取历史会话用量"
      >
        <template #action>
          <AppButton variant="primary" :disabled="syncing" @click="handleSync">
            {{ syncing ? '同步中...' : '立即同步' }}
          </AppButton>
        </template>
      </EmptyState>

      <template v-else>
        <ConfigCard class="summary-card">
          <div class="summary-head">
            <div class="summary-title">
              <h2>使用概览</h2>
              <span class="summary-sub">{{ dateRangeLabel }} · {{ sourceLabel }}</span>
            </div>
            <span v-if="!loading && summary" class="summary-live"><span class="live-dot" />数据已就绪</span>
          </div>
          <div class="summary-grid">
            <div class="metric metric-primary">
              <span class="metric-label" title="Total Cost">累计成本</span>
              <span class="metric-value mono accent">{{ formatCost(summary?.totalCostUSD ?? 0, 'USD') }}</span>
              <span v-if="multiCurrencyText" class="metric-sub mono">{{ multiCurrencyText }}</span>
            </div>
            <div class="metric">
              <span class="metric-label" title="Requests">累计请求</span>
              <span class="metric-value mono">{{ formatCount(summary?.totalRequests ?? 0) }}</span>
            </div>
            <div class="metric">
              <span class="metric-label" title="Total Tokens">总用量</span>
              <span class="metric-value mono">{{ formatTokens(summary?.totalTokens ?? 0) }}</span>
              <span class="metric-sub">新输入、输出及缓存读写去重合并</span>
            </div>
            <div class="metric">
              <span class="metric-label" title="Input Tokens">新输入用量</span>
              <span class="metric-value mono">{{ formatTokens(summary?.totalBillableInput ?? 0) }}</span>
            </div>
            <div class="metric">
              <span class="metric-label" title="Output Tokens">输出用量</span>
              <span class="metric-value mono">{{ formatTokens(summary?.totalOutputTokens ?? 0) }}</span>
            </div>
            <div class="metric">
              <span class="metric-label" title="Cache Read Tokens">缓存读取用量</span>
              <span class="metric-value mono">{{ formatTokens(summary?.totalCacheRead ?? 0) }}</span>
            </div>
            <div class="metric">
              <span class="metric-label" title="Cache Write Tokens">缓存写入用量</span>
              <span class="metric-value mono">{{ formatTokens(summary?.totalCacheCreation ?? 0) }}</span>
            </div>
            <div class="metric">
              <span class="metric-label" title="Cache Hit Rate">缓存命中率</span>
              <span class="metric-value mono">{{ formatPercent(summaryCacheHitRate) }}</span>
            </div>
            <div class="metric">
              <span class="metric-label" title="Cache-adjusted Tokens">缓存折算用量</span>
              <span class="metric-value mono">{{ formatTokens(cacheAdjustedTotal) }}</span>
              <span class="metric-sub">按各模型缓存单价折算</span>
            </div>
          </div>
        </ConfigCard>

        <ConfigCard>
          <div class="range-row">
            <span class="range-label" title="Date Range">统计区间</span>
            <div class="range-presets" role="group" aria-label="统计区间快捷选择">
              <button
                v-for="preset in rangePresets"
                :key="preset.id"
                class="range-btn"
                :class="{ active: activeRange === preset.id }"
                @click="selectRange(preset.id)"
              >
                {{ preset.label }}
              </button>
              <button class="range-btn" :class="{ active: activeRange === 'custom' }" @click="selectRange('custom')">自定义</button>
            </div>
            <span class="range-detail mono">{{ dateRangeLabel }}</span>
          </div>
          <div v-if="activeRange === 'custom'" class="custom-range">
            <label class="filter-group">
              <span>开始日期</span>
              <input v-model="customStartDate" type="date" class="filter-input" />
            </label>
            <label class="filter-group">
              <span>结束日期</span>
              <input v-model="customEndDate" type="date" class="filter-input" />
            </label>
            <AppButton variant="primary" size="small" @click="applyCustomRange">应用区间</AppButton>
          </div>
          <div class="filter-row">
            <label class="filter-group">
              <span title="Client">客户端</span>
              <select v-model="filterAppType" class="filter-select">
                <option value="">全部</option>
                <option value="claudecode">Claude Code</option>
                <option value="opencode">OpenCode</option>
                <option value="codex">Codex</option>
              </select>
            </label>
            <label class="filter-group">
              <span title="Data Source">数据源</span>
              <select v-model="filterSource" class="filter-select">
                <option value="">全部（含实时代理）</option>
                <option value="session_log">仅会话日志（推荐）</option>
                <option value="proxy">仅实时代理</option>
              </select>
            </label>
            <label class="filter-group">
              <span title="Provider">供应商</span>
              <select v-model="filterProvider" class="filter-select">
                <option value="">全部</option>
                <option v-for="provider in knownProviders" :key="provider" :value="provider">{{ provider }}</option>
              </select>
            </label>
            <div class="filter-actions">
              <AppButton variant="ghost" size="small" @click="handleResetFilter">重置筛选</AppButton>
            </div>
          </div>
          <div v-if="filterSource === ''" class="filter-warn">
            提示：同时统计会话日志和实时代理，可能会包含同一请求的两个来源；默认只看会话日志更准确。
          </div>
        </ConfigCard>

        <ConfigCard>
          <div class="card-head trend-head">
            <div>
              <h2>{{ trendMode === 'cost' ? '模型成本趋势' : '模型用量趋势' }}</h2>
              <span class="card-sub">每条曲线对应一个模型，不再混合为总数据</span>
            </div>
            <div class="trend-tabs" role="tablist" aria-label="趋势指标">
              <button class="trend-tab" :class="{ active: trendMode === 'cost' }" @click="trendMode = 'cost'">成本</button>
              <button class="trend-tab" :class="{ active: trendMode === 'tokens' }" @click="trendMode = 'tokens'">Token</button>
            </div>
          </div>
          <div class="trend-toolbar">
            <label class="trend-select-label">
              <span title="Model Curves">模型曲线</span>
              <select v-model="selectedTrendSeries" class="filter-select">
                <option value="all">全部模型（分别绘制，最多 8 条）</option>
                <option v-for="series in trendSeries" :key="series.key" :value="series.key">{{ series.label }}</option>
              </select>
            </label>
            <span class="trend-hint">{{ trendMode === 'cost' ? '按 USD 折算比较' : '以 m 显示（百万 Token）' }}</span>
          </div>
          <div v-if="visibleTrendSeries.length === 0" class="chart-empty">当前筛选下暂无趋势数据</div>
          <div v-else class="chart-box"><Line :data="trendChartData" :options="trendChartOptions" /></div>
        </ConfigCard>

        <div class="grid-2">
          <ConfigCard>
            <div class="card-head">
              <h2 title="Model Cost Share">模型成本占比</h2>
              <span class="card-sub">USD 折算</span>
            </div>
            <div v-if="modelStats.length === 0" class="chart-empty">暂无模型数据</div>
            <div v-else class="chart-box chart-box-short"><Doughnut :data="modelPieData" :options="doughnutOptions" /></div>
          </ConfigCard>
          <ConfigCard>
            <div class="card-head">
              <h2 title="Provider Comparison">供应商对比</h2>
              <span class="card-sub">USD 折算</span>
            </div>
            <div v-if="providerStats.length === 0" class="chart-empty">暂无供应商数据</div>
            <div v-else class="chart-box chart-box-short"><Bar :data="providerBarData" :options="barOptions" /></div>
          </ConfigCard>
        </div>

        <ConfigCard>
          <div class="card-head">
            <h2 title="Token Distribution">用量分布（按模型）</h2>
            <span class="card-sub">以 m 显示的四维拆分</span>
          </div>
          <div v-if="modelStats.length === 0" class="chart-empty">暂无模型数据</div>
          <div v-else class="chart-box"><Bar :data="tokenStackData" :options="stackedBarOptions" /></div>
        </ConfigCard>

        <ConfigCard>
          <div class="card-head">
            <h2>模型缓存与成本明细</h2>
            <span class="card-sub">{{ modelStats.length }} 个模型 / 提供商组合</span>
          </div>
          <div v-if="modelStats.length === 0" class="chart-empty">暂无模型数据</div>
          <div v-else class="table-wrap">
            <table class="usage-table">
              <thead>
                <tr>
                  <th title="Model">模型</th>
                  <th title="Provider">供应商</th>
                  <th class="num" title="Requests">请求</th>
                  <th class="num" title="Total Tokens">总用量（m）</th>
                  <th class="num" title="Input Tokens">新输入（m）</th>
                  <th class="num" title="Output Tokens">输出（m）</th>
                  <th class="num" title="Cache Read Tokens">缓存读取（m）</th>
                  <th class="num" title="Cache Hit Rate">命中率</th>
                  <th class="num" title="Cache-adjusted Tokens">缓存折算（m）</th>
                  <th class="num" title="Cache Hit Cost">命中成本</th>
                  <th class="num" title="Cache Savings">缓存节省</th>
                  <th class="num" title="Total Cost">成本（原生币种）</th>
                  <th title="Pricing Status">状态</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="model in modelStatsSorted" :key="modelRowKey(model)">
                  <td class="mono" :title="model.normalizedModel">{{ model.displayName || model.normalizedModel }}</td>
                  <td>{{ model.provider || '未知' }}</td>
                  <td class="num mono">{{ formatCount(model.requests) }}</td>
                  <td class="num mono">{{ formatTokens(model.totalTokens) }}</td>
                  <td class="num mono">{{ formatTokens(model.billableInput) }}</td>
                  <td class="num mono">{{ formatTokens(model.outputTokens) }}</td>
                  <td class="num mono">{{ formatTokens(model.cacheRead) }}</td>
                  <td class="num mono">{{ formatPercent(model.cacheHitRate) }}</td>
                  <td class="num mono">{{ model.hasPrice ? formatTokens(model.cacheAdjustedTokens) : '—' }}</td>
                  <td class="num mono">{{ model.hasPrice ? formatCost(model.cacheReadEstimatedCost, model.currencyCode) : '—' }}</td>
                  <td class="num mono">{{ model.hasPrice ? formatCost(model.cacheHitSavings, model.currencyCode) : '—' }}</td>
                  <td class="num mono">{{ formatCost(model.totalCost, model.currencyCode) }}</td>
                  <td><span v-if="!model.hasPrice" class="badge-warn">无价格</span><span v-else class="badge-ok">已定价</span></td>
                </tr>
              </tbody>
            </table>
          </div>
          <p class="table-note">缓存命中成本与节省按当前价格表中的缓存命中单价计算；来源直接提供的总账单仍保留为总成本。</p>
        </ConfigCard>

        <ConfigCard>
          <div class="card-head">
            <h2>价格表</h2>
            <span class="card-sub">{{ pricing.length }} 条</span>
          </div>
          <ModelPricingTable
            :entries="pricing"
            :unknown-models="unknownModels"
            @edit="handleEditPricing"
            @delete="handleDeletePricing"
            @add="handleAddPricing"
            @add-for-unknown="handleAddForUnknown"
            @reset="handleResetPricing"
          />
        </ConfigCard>
      </template>
    </template>

    <PricingDialog
      v-model:open="pricingDialogOpen"
      :editing="editingPricing"
      :preset-pattern="presetPattern"
      :preset-provider="presetProvider"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { Bar, Doughnut, Line } from 'vue-chartjs';
import {
  ArcElement,
  BarElement,
  CategoryScale,
  Chart as ChartJS,
  Filler,
  Legend,
  LineElement,
  LinearScale,
  PointElement,
  Title,
  Tooltip,
} from 'chart.js';
import type { ChartData, ChartOptions, TooltipItem } from 'chart.js';
import PageHead from '../components/ui/PageHead.vue';
import ConfigCard from '../components/ui/ConfigCard.vue';
import AppButton from '../components/ui/AppButton.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import LoadingState from '../components/ui/LoadingState.vue';
import ErrorState from '../components/ui/ErrorState.vue';
import ModelPricingTable from '../components/usage/ModelPricingTable.vue';
import PricingDialog from '../components/usage/PricingDialog.vue';
import { useUsageStore } from '../stores/usage';
import { useToast } from '../composables/useToast';
import { resolveUsageRange } from '../api/usage';
import type { ModelDailyTrendPoint, ModelPricing } from '../api/usage';
import { formatCost, formatCount, formatTokens, nativeMicroToUsdMicro } from '../utils/usage-format';

ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  BarElement,
  ArcElement,
  Title,
  Tooltip,
  Legend,
  Filler,
);

const store = useUsageStore();
const { showError, showSuccess } = useToast();
const REFRESH_INTERVAL = 30000;
let refreshTimer: number | null = null;

const PALETTE = ['#007AFF', '#34C759', '#FF9500', '#AF52DE', '#FF3B30', '#5AC8FA', '#FF2D55', '#FFD60A'];
const OTHER_COLOR = '#8E8E93';

function readToken(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback;
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
}

const chartTheme = computed(() => ({
  text: readToken('--secondary', '#6E6E73'),
  grid: readToken('--separator', '#D2D2D7'),
  accent: readToken('--accent', '#007AFF'),
  success: readToken('--success', '#34C759'),
}));

const summary = computed(() => store.summary);
const modelTrends = computed(() => store.modelTrends);
const modelStats = computed(() => store.modelStats);
const providerStats = computed(() => store.providerStats);
const pricing = computed(() => store.pricing);
const unknownModels = computed(() => store.unknownModels);
const loading = computed(() => store.loading);
const error = computed(() => store.error);
const syncing = computed(() => store.syncing);
const lastSyncedAt = computed(() => store.lastSyncedAt);
const knownProviders = computed(() => store.knownProviders);

const filterAppType = computed({ get: () => store.filter.appType, set: (value: string) => store.patchFilter({ appType: value }) });
const filterSource = computed({ get: () => store.filter.source, set: (value: string) => store.patchFilter({ source: value }) });
const filterProvider = computed({ get: () => store.filter.provider, set: (value: string) => store.patchFilter({ provider: value }) });

const rangePresets = [
  { id: 'today', label: '今日' },
  { id: '7d', label: '近 7 天' },
  { id: '30d', label: '近 30 天' },
  { id: 'month', label: '本月' },
] as const;
type RangePreset = (typeof rangePresets)[number]['id'] | 'custom';
const activeRange = ref<RangePreset>('30d');
const customStartDate = ref(store.filter.startDate);
const customEndDate = ref(store.filter.endDate);

const multiCurrencyText = computed(() => {
  const entries = Object.entries(summary.value?.totalCostByCurrency ?? {}).filter(([currency, amount]) => currency && amount > 0);
  return entries.length > 1 ? `含 ${entries.map(([currency, amount]) => formatCost(amount, currency)).join(' · ')}` : '';
});

const dateRangeLabel = computed(() => {
  if (store.filter.startDate && store.filter.endDate) return `${store.filter.startDate} ~ ${store.filter.endDate}`;
  const actual = summary.value?.dateRange;
  if (actual?.start && actual?.end) return `${actual.start} ~ ${actual.end}`;
  return '全部时间';
});

const sourceLabel = computed(() => {
  if (store.filter.source === 'session_log') return '会话日志';
  if (store.filter.source === 'proxy') return '实时代理';
  return '全部数据源';
});

const summaryCacheHitRate = computed(() => {
  const fresh = summary.value?.totalBillableInput ?? 0;
  const cached = summary.value?.totalCacheRead ?? 0;
  return fresh + cached > 0 ? cached / (fresh + cached) : 0;
});

const cacheAdjustedTotal = computed(() => modelStats.value.reduce((total, model) => total + (model.cacheAdjustedTokens ?? 0), 0));

const modelStatsSorted = computed(() => [...modelStats.value]
  .map((model) => ({ ...model, _usd: nativeMicroToUsdMicro(model.totalCost, model.currencyCode) }))
  .sort((a, b) => b._usd - a._usd));

function selectRange(preset: RangePreset): void {
  activeRange.value = preset;
  if (preset === 'custom') return;
  const range = resolveUsageRange(preset);
  customStartDate.value = range.startDate;
  customEndDate.value = range.endDate;
  store.patchFilter(range);
}

function applyCustomRange(): void {
  if (!customStartDate.value || !customEndDate.value) {
    showError('请选择完整的开始和结束日期');
    return;
  }
  if (customStartDate.value > customEndDate.value) {
    showError('开始日期不能晚于结束日期');
    return;
  }
  activeRange.value = 'custom';
  store.patchFilter({ startDate: customStartDate.value, endDate: customEndDate.value });
}

type TrendMode = 'cost' | 'tokens';
interface TrendSeries {
  key: string;
  label: string;
  points: Map<string, ModelDailyTrendPoint>;
  totalCostUSD: number;
  totalTokens: number;
}

const trendMode = ref<TrendMode>('cost');
const selectedTrendSeries = ref('all');
const trendLabels = computed(() => Array.from(new Set(modelTrends.value.map((point) => point.day))).sort());
const trendSeries = computed<TrendSeries[]>(() => {
  const bySeries = new Map<string, TrendSeries>();
  for (const point of modelTrends.value) {
    const key = `${point.normalizedModel}\u0000${point.provider}\u0000${point.currencyCode}`;
    let series = bySeries.get(key);
    if (!series) {
      const name = point.displayName || point.normalizedModel;
      series = {
        key,
        label: point.provider ? `${name} · ${point.provider}` : name,
        points: new Map(),
        totalCostUSD: 0,
        totalTokens: 0,
      };
      bySeries.set(key, series);
    }
    series.points.set(point.day, point);
    series.totalCostUSD += point.totalCostUSD ?? 0;
    series.totalTokens += point.totalTokens ?? 0;
  }
  return Array.from(bySeries.values()).sort((a, b) => a.label.localeCompare(b.label));
});

const visibleTrendSeries = computed(() => {
  if (selectedTrendSeries.value !== 'all') {
    const selected = trendSeries.value.find((series) => series.key === selectedTrendSeries.value);
    return selected ? [selected] : [];
  }
  const value = (series: TrendSeries) => (trendMode.value === 'cost' ? series.totalCostUSD : series.totalTokens);
  return [...trendSeries.value].sort((a, b) => value(b) - value(a)).slice(0, 8);
});

const trendChartData = computed<ChartData<'line'>>(() => ({
  labels: trendLabels.value,
  datasets: visibleTrendSeries.value.map((series, index) => ({
    label: series.label,
    data: trendLabels.value.map((day) => {
      const point = series.points.get(day);
      return trendMode.value === 'cost' ? (point?.totalCostUSD ?? 0) / 1_000_000 : point?.totalTokens ?? 0;
    }),
    borderColor: PALETTE[index % PALETTE.length],
    backgroundColor: PALETTE[index % PALETTE.length],
    borderWidth: 2,
    tension: 0.25,
    fill: false,
    pointRadius: 2,
    pointHoverRadius: 4,
  })),
}));

const trendChartOptions = computed<ChartOptions<'line'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { mode: 'index', intersect: false },
  plugins: {
    legend: { position: 'top', labels: { color: chartTheme.value.text, font: { size: 11 }, boxWidth: 12, usePointStyle: true } },
    tooltip: {
      callbacks: {
        label: (context: TooltipItem<'line'>) => {
          const value = typeof context.parsed.y === 'number' ? context.parsed.y : 0;
          return trendMode.value === 'cost'
            ? `${context.dataset.label}: ${formatCost(Math.round(value * 1_000_000), 'USD')}`
            : `${context.dataset.label}: ${formatTokens(value)}`;
        },
      },
    },
  },
  scales: {
    x: { ticks: { color: chartTheme.value.text, font: { size: 11 } }, grid: { color: chartTheme.value.grid, display: false } },
    y: {
      ticks: {
        color: chartTheme.value.text,
        font: { size: 11 },
        callback: (value) => trendMode.value === 'cost'
          ? formatCost(Math.round(Number(value) * 1_000_000), 'USD')
          : formatTokens(Number(value)),
      },
      title: { display: true, text: trendMode.value === 'cost' ? '成本（USD）' : '用量（m）', color: chartTheme.value.text },
      grid: { color: chartTheme.value.grid },
    },
  },
}));

const modelPieData = computed<ChartData<'doughnut'>>(() => {
  const stats = modelStats.value
    .map((model) => ({ name: modelSeriesLabel(model), usd: nativeMicroToUsdMicro(model.totalCost, model.currencyCode) }))
    .filter((model) => model.usd > 0)
    .sort((a, b) => b.usd - a.usd);
  const top = stats.slice(0, 7);
  const rest = stats.slice(7).reduce((total, model) => total + model.usd, 0);
  const labels = top.map((model) => model.name);
  const data = top.map((model) => model.usd);
  if (rest > 0) { labels.push('其他'); data.push(rest); }
  return {
    labels,
    datasets: [{ data, backgroundColor: [...top.map((_, index) => PALETTE[index % PALETTE.length]), ...(rest > 0 ? [OTHER_COLOR] : [])], borderColor: readToken('--card', '#FFFFFF'), borderWidth: 2, hoverOffset: 8 }],
  };
});

const doughnutOptions = computed<ChartOptions<'doughnut'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  cutout: '60%',
  plugins: {
    legend: { position: 'right', labels: { color: chartTheme.value.text, font: { size: 11 }, boxWidth: 12, usePointStyle: true } },
    tooltip: { callbacks: { label: (context: TooltipItem<'doughnut'>) => `${context.label}: ${formatCost(Number(context.parsed) || 0, 'USD')}` } },
  },
}));

const providerBarData = computed<ChartData<'bar'>>(() => {
  const stats = [...providerStats.value].filter((provider) => provider.totalCostUSD > 0).sort((a, b) => b.totalCostUSD - a.totalCostUSD);
  return {
    labels: stats.map((provider) => provider.provider || '未知'),
    datasets: [{ label: '成本（USD）', data: stats.map((provider) => provider.totalCostUSD / 1_000_000), backgroundColor: chartTheme.value.accent, borderRadius: 4, maxBarThickness: 40 }],
  };
});

const barOptions = computed<ChartOptions<'bar'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { display: false }, tooltip: { callbacks: { label: (context: TooltipItem<'bar'>) => `成本：${formatCost(Math.round((context.parsed.y || 0) * 1_000_000), 'USD')}` } } },
  scales: {
    x: { ticks: { color: chartTheme.value.text, font: { size: 11 } }, grid: { display: false } },
    y: { ticks: { color: chartTheme.value.text, font: { size: 11 }, callback: (value) => formatCost(Math.round(Number(value) * 1_000_000), 'USD') }, grid: { color: chartTheme.value.grid } },
  },
}));

const tokenStackData = computed<ChartData<'bar'>>(() => {
  const top = [...modelStats.value]
    .map((model) => ({
      name: modelSeriesLabel(model),
      input: model.billableInput ?? 0,
      output: model.outputTokens ?? 0,
      cacheRead: model.cacheRead ?? 0,
      cacheCreation: model.cacheCreation ?? 0,
      total: model.totalTokens ?? 0,
    }))
    .filter((model) => model.total > 0)
    .sort((a, b) => b.total - a.total)
    .slice(0, 8);
  return {
    labels: top.map((model) => model.name),
    datasets: [
      { label: '新输入', data: top.map((model) => model.input), backgroundColor: PALETTE[0] },
      { label: '输出', data: top.map((model) => model.output), backgroundColor: PALETTE[1] },
      { label: '缓存读取', data: top.map((model) => model.cacheRead), backgroundColor: PALETTE[2] },
      { label: '缓存写入', data: top.map((model) => model.cacheCreation), backgroundColor: PALETTE[3] },
    ],
  };
});

const stackedBarOptions = computed<ChartOptions<'bar'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: { position: 'top', labels: { color: chartTheme.value.text, font: { size: 11 }, boxWidth: 12, usePointStyle: true } },
    tooltip: { callbacks: { label: (context: TooltipItem<'bar'>) => `${context.dataset.label}: ${formatTokens(context.parsed.y || 0)}` } },
  },
  scales: {
    x: { stacked: true, ticks: { color: chartTheme.value.text, font: { size: 11 } }, grid: { display: false } },
    y: { stacked: true, ticks: { color: chartTheme.value.text, font: { size: 11 }, callback: (value) => formatTokens(Number(value)) }, grid: { color: chartTheme.value.grid } },
  },
}));

function modelSeriesLabel(model: { displayName?: string; normalizedModel: string; provider?: string }): string {
  const name = model.displayName || model.normalizedModel;
  return model.provider ? `${name} · ${model.provider}` : name;
}

function modelRowKey(model: { normalizedModel: string; provider?: string; currencyCode?: string; appType?: string }): string {
  return [model.normalizedModel, model.provider, model.currencyCode, model.appType].join(':');
}

function formatPercent(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0.0%';
  return `${(value * 100).toFixed(value * 100 >= 10 ? 1 : 2)}%`;
}

async function handleRetry(): Promise<void> { await store.retry(); }

async function handleSync(): Promise<void> {
  try {
    const result = await store.syncNow();
    if (result.errors?.length) showSuccess(`同步完成：新增 ${result.recordsAdded ?? 0} 条；${result.errors.length} 个源失败`);
    else showSuccess(`同步完成：新增 ${result.recordsAdded ?? 0} 条记录`);
  } catch (err) {
    showError(`同步失败：${errToString(err)}`);
  }
}

function handleResetFilter(): void {
  store.resetFilter();
  activeRange.value = '30d';
  customStartDate.value = store.filter.startDate;
  customEndDate.value = store.filter.endDate;
  selectedTrendSeries.value = 'all';
}

const pricingDialogOpen = ref(false);
const editingPricing = ref<ModelPricing | null>(null);
const presetPattern = ref('');
const presetProvider = ref('');

function handleAddPricing(): void {
  editingPricing.value = null;
  presetPattern.value = '';
  presetProvider.value = '';
  pricingDialogOpen.value = true;
}

function handleEditPricing(entry: ModelPricing): void {
  editingPricing.value = entry;
  presetPattern.value = '';
  presetProvider.value = '';
  pricingDialogOpen.value = true;
}

function handleAddForUnknown(normalizedModel: string, _sampleRaw: string): void {
  editingPricing.value = null;
  presetPattern.value = normalizedModel;
  presetProvider.value = '';
  pricingDialogOpen.value = true;
}

async function handleDeletePricing(entry: ModelPricing): Promise<void> {
  try {
    await store.removePricing(entry.id);
    showSuccess(`已删除：${entry.displayName || entry.modelPattern}`);
  } catch (err) {
    showError(`删除失败：${errToString(err)}`);
  }
}

async function handleResetPricing(): Promise<void> {
  if (!confirm('确认恢复全部内置价格？自定义条目会被移除，历史的本地估算成本会按默认价格重算。')) return;
  try {
    await store.resetPricing();
    showSuccess('内置价格已恢复，历史估算成本已重算');
  } catch (err) {
    showError(`恢复失败：${errToString(err)}`);
  }
}

function formatRelative(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const seconds = Math.floor((Date.now() - date.getTime()) / 1000);
  if (seconds < 60) return '刚刚';
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`;
  return `${Math.floor(seconds / 86400)} 天前`;
}

function errToString(err: unknown): string {
  if (!err) return '未知错误';
  if (typeof err === 'string') return err;
  if (err instanceof Error) return err.message;
  if (Array.isArray(err)) return err.map(String).join('; ');
  try { return JSON.stringify(err); } catch { return String(err); }
}

watch(
  () => store.filter,
  () => { void store.fetchAll({ silent: false }); },
  { deep: true },
);

watch(trendMode, () => { selectedTrendSeries.value = 'all'; });

onMounted(async () => {
  await store.fetchAll({ silent: false });
  await Promise.all([store.fetchPricing(), store.fetchUnknownModels(), store.fetchSyncState({ silent: true })]);
  refreshTimer = window.setInterval(() => { void store.fetchAll({ silent: true }); }, REFRESH_INTERVAL);
});

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer);
  refreshTimer = null;
});
</script>

<style scoped>
.view-usage { display: flex; flex-direction: column; gap: 22px; overflow: auto; padding: 32px 36px; }
.head-actions, .range-row, .range-presets, .trend-tabs, .trend-toolbar { display: flex; align-items: center; gap: 10px; }
.last-synced, .summary-sub, .card-sub, .trend-hint, .metric-sub, .range-detail { color: var(--tertiary); font-size: 12px; }
.last-synced, .mono { font-family: var(--mono); }
.sync-icon { display: inline-block; width: 12px; height: 12px; margin-right: 4px; border: 1.5px solid currentColor; border-top-color: transparent; border-radius: 50%; vertical-align: -2px; }
.sync-icon.spinning { animation: sync-spin .8s linear infinite; }
@keyframes sync-spin { to { transform: rotate(360deg); } }

.summary-card { gap: 14px; }
.summary-head, .card-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.summary-title { display: flex; flex-direction: column; gap: 2px; }
.summary-title h2, .card-head h2 { margin: 0; color: var(--label); font-size: 16px; font-weight: 600; }
.summary-live { display: inline-flex; align-items: center; gap: 6px; color: var(--secondary); font-size: 12px; }
.live-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--success); box-shadow: 0 0 0 3px color-mix(in srgb, var(--success) 18%, transparent); }
.summary-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 14px 20px; }
.metric { display: flex; min-width: 0; flex-direction: column; gap: 4px; padding: 2px 0; }
.metric + .metric { border-left: 1px solid var(--separator); padding-left: 20px; }
.metric-label { color: var(--tertiary); font-size: 12px; letter-spacing: .2px; cursor: help; }
.metric-value { overflow: hidden; color: var(--label); font-size: 20px; font-weight: 600; line-height: 1.2; text-overflow: ellipsis; font-variant-numeric: tabular-nums; }
.metric-primary .metric-value { font-size: 26px; }
.metric-value.accent { color: var(--accent); }

.range-row { flex-wrap: wrap; padding-bottom: 12px; border-bottom: 1px solid var(--separator); }
.range-label { color: var(--secondary); font-size: 12px; font-weight: 600; }
.range-presets { flex-wrap: wrap; }
.range-btn, .trend-tab { border: 1px solid var(--separator); border-radius: 999px; padding: 5px 10px; background: var(--control); color: var(--secondary); font: inherit; font-size: 12px; cursor: pointer; transition: .15s ease; }
.range-btn:hover, .trend-tab:hover { border-color: var(--accent); color: var(--accent); }
.range-btn.active, .trend-tab.active { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 12%, var(--control)); color: var(--accent); font-weight: 600; }
.range-detail { margin-left: auto; }
.custom-range, .filter-row { display: flex; align-items: flex-end; flex-wrap: wrap; gap: 12px; }
.custom-range { padding-top: 12px; }
.filter-row { padding-top: 12px; }
.filter-group { display: flex; flex-direction: column; gap: 6px; }
.filter-group > span { color: var(--secondary); font-size: 12px; font-weight: 500; }
.filter-actions { margin-left: auto; }
.filter-input, .filter-select { min-width: 138px; border: 1px solid transparent; border-radius: 7px; outline: none; padding: 6px 10px; background: var(--control); color: var(--label); font: inherit; font-size: 13px; transition: background-color .12s, box-shadow .12s; }
.filter-select { padding-right: 28px; cursor: pointer; }
.filter-input:focus, .filter-select:focus { background: var(--card); box-shadow: 0 0 0 2px rgba(0, 122, 255, .25); }
.filter-warn { margin-top: 12px; border-radius: 7px; padding: 8px 10px; background: rgba(255,149,0,.08); color: var(--warning-strong); font-size: 12px; line-height: 1.5; }

.trend-head { align-items: center; }
.trend-toolbar { justify-content: space-between; margin: 12px 0 6px; flex-wrap: wrap; }
.trend-select-label { display: flex; align-items: center; gap: 8px; color: var(--secondary); font-size: 12px; }
.chart-box { position: relative; width: 100%; height: 320px; }
.chart-box-short { height: 260px; }
.chart-empty { padding: 40px 20px; color: var(--tertiary); text-align: center; font-size: 13px; }
.grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 22px; }

.table-wrap { max-height: 520px; overflow: auto; border: 1px solid var(--separator); border-radius: 10px; }
.usage-table { width: 100%; min-width: 1360px; border-collapse: collapse; font-size: 13px; }
.usage-table thead { position: sticky; top: 0; z-index: 1; }
.usage-table th { border-bottom: 1px solid var(--separator); padding: 9px 12px; background: var(--sidebar); color: var(--secondary); text-align: left; font-size: 11px; font-weight: 600; letter-spacing: .35px; cursor: help; }
.usage-table th.num, .usage-table td.num { text-align: right; }
.usage-table td { border-bottom: 1px solid var(--separator); padding: 8px 12px; color: var(--label); vertical-align: top; }
.usage-table tr:last-child td { border-bottom: 0; }
.usage-table tr:hover td { background: color-mix(in srgb, var(--accent) 4%, transparent); }
.badge-warn, .badge-ok { display: inline-block; border-radius: 4px; padding: 2px 8px; font-size: 11px; font-weight: 600; }
.badge-warn { background: rgba(255,149,0,.12); color: var(--warning-strong); }
.badge-ok { background: rgba(52,199,89,.12); color: var(--success-strong); }
.table-note { margin: 10px 0 0; color: var(--tertiary); font-size: 12px; line-height: 1.5; }

@media (max-width: 1100px) {
  .summary-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .metric:nth-child(3), .metric:nth-child(5), .metric:nth-child(7), .metric:nth-child(9) { border-left: 0; padding-left: 0; }
}
@media (max-width: 960px) { .grid-2 { grid-template-columns: 1fr; } }
@media (max-width: 720px) {
  .view-usage { padding: 22px 18px; }
  .summary-grid { grid-template-columns: 1fr; }
  .metric + .metric { border-top: 1px solid var(--separator); border-left: 0; padding-top: 10px; padding-left: 0; }
  .range-detail { width: 100%; margin-left: 0; }
  .filter-actions { margin-left: 0; }
}
</style>
