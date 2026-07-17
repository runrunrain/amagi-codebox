<template>
  <section class="view-usage">
    <!-- Loading：首次 fetchAll 进行时（summary 为空时才全屏 loading） -->
    <LoadingState v-if="loading && !summary" message="加载使用统计中..." />

    <!-- Error：首次失败且 summary 仍为 null -->
    <ErrorState
      v-else-if="error && !summary"
      :message="error"
      :on-retry="handleRetry"
    />

    <!-- 主内容 / Main content -->
    <template v-else>
      <PageHead title="使用统计" description="AI 模型用量与成本统计 · 默认仅展示 session_log 数据源">
        <template #actions>
          <div class="head-actions">
            <span v-if="lastSyncedAt" class="last-synced" :title="`上次同步：${lastSyncedAt}`">
              已同步 · {{ formatRelative(lastSyncedAt) }}
            </span>
            <AppButton
              variant="ghost"
              size="small"
              :disabled="syncing"
              @click="handleSync"
            >
              <span class="sync-icon" :class="{ spinning: syncing }" />
              {{ syncing ? '同步中...' : '立即同步' }}
            </AppButton>
          </div>
        </template>
      </PageHead>

      <!-- Empty：有 summary 但完全无数据 -->
      <EmptyState
        v-if="summary && summary.totalRequests === 0"
        title="暂无使用数据"
        description="从 ~/.claude / ~/.codex / ~/.local/share/opencode 拉取历史会话用量"
      >
        <template #action>
          <AppButton
            variant="primary"
            :disabled="syncing"
            @click="handleSync"
          >
            {{ syncing ? '同步中...' : '立即同步' }}
          </AppButton>
        </template>
      </EmptyState>

      <!-- Success：完整仪表盘 -->
      <template v-else>
        <!-- 1. 仪表盘汇总卡片 / Summary dashboard card -->
        <ConfigCard class="summary-card">
          <div class="summary-head">
            <div class="summary-title">
              <h2>累计统计</h2>
              <span class="summary-sub">
                {{ dateRangeLabel }} · {{ sourceLabel }}
              </span>
            </div>
            <span v-if="!loading && summary" class="summary-live">
              <span class="live-dot" />数据已就绪
            </span>
          </div>
          <div class="summary-grid">
            <div class="metric metric-primary">
              <span class="metric-label">累计成本</span>
              <span class="metric-value mono accent">{{ formatCost(summary?.totalCostUSD ?? 0, 'USD') }}</span>
              <span v-if="multiCurrencyText" class="metric-sub mono">{{ multiCurrencyText }}</span>
            </div>
            <div class="metric">
              <span class="metric-label">累计请求</span>
              <span class="metric-value mono">{{ formatCount(summary?.totalRequests ?? 0) }}</span>
            </div>
            <div class="metric">
              <span class="metric-label">Input Tokens</span>
              <span class="metric-value mono">{{ formatTokens(summary?.totalInputTokens ?? 0) }}</span>
            </div>
            <div class="metric">
              <span class="metric-label">Output Tokens</span>
              <span class="metric-value mono">{{ formatTokens(summary?.totalOutputTokens ?? 0) }}</span>
            </div>
            <div class="metric">
              <span class="metric-label">Cache Read</span>
              <span class="metric-value mono">{{ formatTokens(summary?.totalCacheRead ?? 0) }}</span>
            </div>
            <div class="metric">
              <span class="metric-label">Cache Creation</span>
              <span class="metric-value mono">{{ formatTokens(summary?.totalCacheCreation ?? 0) }}</span>
            </div>
            <div class="metric">
              <span class="metric-label">Billable Input</span>
              <span class="metric-value mono">{{ formatTokens(summary?.totalBillableInput ?? 0) }}</span>
            </div>
          </div>
        </ConfigCard>

        <!-- 2. 筛选器卡片 / Filter card -->
        <ConfigCard>
          <div class="filter-row">
            <div class="filter-group">
              <label>开始日期</label>
              <input v-model="filterStartDate" type="date" class="filter-input" />
            </div>
            <div class="filter-group">
              <label>结束日期</label>
              <input v-model="filterEndDate" type="date" class="filter-input" />
            </div>
            <div class="filter-group">
              <label>客户端</label>
              <select v-model="filterAppType" class="filter-select">
                <option value="">全部</option>
                <option value="claudecode">Claude Code</option>
                <option value="opencode">OpenCode</option>
                <option value="codex">Codex</option>
              </select>
            </div>
            <div class="filter-group">
              <label>数据源</label>
              <select v-model="filterSource" class="filter-select">
                <option value="">全部（含 proxy 实时）</option>
                <option value="session_log">仅 session_log（推荐）</option>
                <option value="proxy">仅 proxy 实时</option>
              </select>
            </div>
            <div class="filter-group">
              <label>供应商</label>
              <select v-model="filterProvider" class="filter-select">
                <option value="">全部</option>
                <option v-for="p in knownProviders" :key="p" :value="p">{{ p }}</option>
              </select>
            </div>
            <div class="filter-group filter-actions">
              <label>&nbsp;</label>
              <AppButton variant="ghost" size="small" @click="handleResetFilter">重置</AppButton>
            </div>
          </div>
          <div v-if="filterSource === ''" class="filter-warn">
            提示：「全部」会把 session_log 与 proxy 实时记录合并，可能产生双计；默认仅看 session_log 更准确。
          </div>
        </ConfigCard>

        <!-- 3. 日趋势折线图（双轴：成本 + Token） -->
        <ConfigCard>
          <div class="card-head">
            <h2>成本与 Token 趋势</h2>
            <span class="card-sub">最近 {{ trendDays }} 天</span>
          </div>
          <div v-if="trends.length === 0" class="chart-empty">暂无趋势数据</div>
          <div v-else class="chart-box">
            <Line :data="trendChartData" :options="trendChartOptions" />
          </div>
        </ConfigCard>

        <!-- 4. 双列：模型占比 + 供应商对比 -->
        <div class="grid-2">
          <ConfigCard>
            <div class="card-head">
              <h2>模型成本占比</h2>
              <span class="card-sub">USD 折算</span>
            </div>
            <div v-if="modelStats.length === 0" class="chart-empty">暂无模型数据</div>
            <div v-else class="chart-box chart-box-short">
              <Doughnut :data="modelPieData" :options="doughnutOptions" />
            </div>
          </ConfigCard>
          <ConfigCard>
            <div class="card-head">
              <h2>供应商对比</h2>
              <span class="card-sub">USD 折算</span>
            </div>
            <div v-if="providerStats.length === 0" class="chart-empty">暂无供应商数据</div>
            <div v-else class="chart-box chart-box-short">
              <Bar :data="providerBarData" :options="barOptions" />
            </div>
          </ConfigCard>
        </div>

        <!-- 5. Token 四维堆叠柱 / Token four-dimension stacked bar -->
        <ConfigCard>
          <div class="card-head">
            <h2>Token 分布（按模型）</h2>
            <span class="card-sub">四维 token 堆叠</span>
          </div>
          <div v-if="modelStats.length === 0" class="chart-empty">暂无模型数据</div>
          <div v-else class="chart-box">
            <Bar :data="tokenStackData" :options="stackedBarOptions" />
          </div>
        </ConfigCard>

        <!-- 6. 模型明细表 / Model detail table -->
        <ConfigCard>
          <div class="card-head">
            <h2>模型明细</h2>
            <span class="card-sub">{{ modelStats.length }} 个模型</span>
          </div>
          <div v-if="modelStats.length === 0" class="chart-empty">暂无模型数据</div>
          <div v-else class="table-wrap">
            <table class="usage-table">
              <thead>
                <tr>
                  <th>模型</th>
                  <th>供应商</th>
                  <th class="num">请求</th>
                  <th class="num">Input</th>
                  <th class="num">Output</th>
                  <th class="num">Cache R</th>
                  <th class="num">Cache W</th>
                  <th class="num">成本（原生币种）</th>
                  <th>状态</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="m in modelStatsSorted" :key="m.normalizedModel">
                  <td class="mono" :title="m.normalizedModel">{{ m.displayName || m.normalizedModel }}</td>
                  <td>{{ m.provider || '—' }}</td>
                  <td class="num mono">{{ formatCount(m.requests) }}</td>
                  <td class="num mono">{{ formatTokens(m.inputTokens) }}</td>
                  <td class="num mono">{{ formatTokens(m.outputTokens) }}</td>
                  <td class="num mono">{{ formatTokens(m.cacheRead) }}</td>
                  <td class="num mono">{{ formatTokens(m.cacheCreation) }}</td>
                  <td class="num mono">{{ formatCost(m.totalCost, m.currencyCode) }}</td>
                  <td>
                    <span v-if="!m.hasPrice" class="badge-warn">无价格</span>
                    <span v-else class="badge-ok">已定价</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </ConfigCard>

        <!-- 7. 价格表管理 / Pricing management -->
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

    <!-- 价格编辑对话框 / Pricing editor dialog -->
    <PricingDialog
      v-model:open="pricingDialogOpen"
      :editing="editingPricing"
      :preset-pattern="presetPattern"
      :preset-provider="presetProvider"
    />
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue';
import { Line, Bar, Doughnut } from 'vue-chartjs';
import {
  Chart as ChartJS,
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
import {
  formatCost,
  formatCount,
  formatTokens,
  nativeMicroToUsdMicro,
} from '../utils/usage-format';
import type { ModelPricing } from '../api/usage';

// 单次注册 Chart.js 组件 / Register Chart.js components once.
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
const { showSuccess, showError } = useToast();

// 30 秒后台刷新：仿 LogsView 2s/Headroom 10s 模式，usage 数据较静态故放宽到 30s。
const REFRESH_INTERVAL = 30000;
let refreshTimer: number | null = null;

// === 颜色与主题 / Palette & theme ===
// 8 色分类色板，与 tokens.css 系统色调对齐（accent/success/warning/purple 等）。
const PALETTE: string[] = [
  '#007AFF', // accent blue
  '#34C759', // success green
  '#FF9500', // warning orange
  '#AF52DE', // purple
  '#FF3B30', // danger red
  '#5AC8FA', // cyan
  '#FF2D55', // pink
  '#FFD60A', // yellow
];

// 灰色用于"其他"桶 / Gray for the "other" bucket.
const OTHER_COLOR = '#8E8E93';

function readToken(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback;
  try {
    const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
    return v || fallback;
  } catch {
    return fallback;
  }
}

const chartTheme = computed(() => {
  // 在每次图表数据变化时刷新主题色，便于后续支持 dark mode 自动切换。
  return {
    text: readToken('--secondary', '#6E6E73'),
    label: readToken('--label', '#1D1D1F'),
    grid: readToken('--separator', '#D2D2D7'),
    accent: readToken('--accent', '#007AFF'),
    success: readToken('--success', '#34C759'),
  };
});

// === Store-derived refs (便于模板直接引用) ===
const summary = computed(() => store.summary);
const trends = computed(() => store.trends);
const modelStats = computed(() => store.modelStats);
const providerStats = computed(() => store.providerStats);
const pricing = computed(() => store.pricing);
const unknownModels = computed(() => store.unknownModels);
const loading = computed(() => store.loading);
const error = computed(() => store.error);
const syncing = computed(() => store.syncing);
const lastSyncedAt = computed(() => store.lastSyncedAt);
const knownProviders = computed(() => store.knownProviders);

const modelStatsSorted = computed(() =>
  [...modelStats.value]
    .map((m) => ({
      ...m,
      _usd: nativeMicroToUsdMicro(m.totalCost, m.currencyCode),
    }))
    .sort((a, b) => b._usd - a._usd),
);

// === 筛选器双向绑定 / Filter bindings ===
const filterStartDate = computed({
  get: () => store.filter.startDate,
  set: (v: string) => store.patchFilter({ startDate: v }),
});
const filterEndDate = computed({
  get: () => store.filter.endDate,
  set: (v: string) => store.patchFilter({ endDate: v }),
});
const filterAppType = computed({
  get: () => store.filter.appType,
  set: (v: string) => store.patchFilter({ appType: v }),
});
const filterSource = computed({
  get: () => store.filter.source,
  set: (v: string) => store.patchFilter({ source: v }),
});
const filterProvider = computed({
  get: () => store.filter.provider,
  set: (v: string) => store.patchFilter({ provider: v }),
});
const trendDays = computed(() => store.trendDays);

// === 展示用派生 / Display-only derived ===
const multiCurrencyText = computed(() => {
  const byCurrency = summary.value?.totalCostByCurrency;
  if (!byCurrency) return '';
  const entries = Object.entries(byCurrency).filter(([c, v]) => c && v > 0);
  if (entries.length <= 1) return ''; // 单币种不显示子文本
  return '含 ' + entries.map(([c, v]) => `${formatCost(v, c)}`).join(' · ');
});

const dateRangeLabel = computed(() => {
  const dr = summary.value?.dateRange;
  if (!dr || (!dr.start && !dr.end)) return '全部时间';
  if (dr.start && dr.end) return `${dr.start} ~ ${dr.end}`;
  return dr.start || dr.end;
});

const sourceLabel = computed(() => {
  const s = store.filter.source;
  if (!s) return '全部数据源';
  if (s === 'session_log') return 'session_log';
  if (s === 'proxy') return 'proxy';
  return s;
});

// === 图表 1：日趋势折线（双轴）===
const trendChartData = computed<ChartData<'line'>>(() => {
  const labels = trends.value.map((p) => p.day);
  const costUsd = trends.value.map((p) => (p.totalCostUSD ?? 0) / 1e6);
  const tokens = trends.value.map((p) => (p.inputTokens ?? 0) + (p.outputTokens ?? 0));
  return {
    labels,
    datasets: [
      {
        label: '成本 (USD)',
        data: costUsd,
        borderColor: chartTheme.value.accent,
        backgroundColor: 'rgba(0, 122, 255, 0.1)',
        borderWidth: 2,
        tension: 0.25,
        fill: true,
        pointRadius: 2,
        pointHoverRadius: 4,
        yAxisID: 'y',
      },
      {
        label: 'Token 总量',
        data: tokens,
        borderColor: chartTheme.value.success,
        backgroundColor: 'rgba(52, 199, 89, 0.1)',
        borderWidth: 2,
        tension: 0.25,
        fill: false,
        pointRadius: 2,
        pointHoverRadius: 4,
        yAxisID: 'y1',
      },
    ],
  };
});

const trendChartOptions = computed<ChartOptions<'line'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: {
    mode: 'index',
    intersect: false,
  },
  plugins: {
    legend: {
      position: 'top',
      labels: {
        color: chartTheme.value.text,
        font: { size: 12 },
        boxWidth: 14,
        boxHeight: 8,
        usePointStyle: true,
      },
    },
    tooltip: {
      callbacks: {
        label: (ctx: TooltipItem<'line'>) => {
          const label = ctx.dataset.label || '';
          const v = typeof ctx.parsed.y === 'number' ? ctx.parsed.y : 0;
          if (label.includes('USD')) {
            return `${label}: $${v.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 4 })}`;
          }
          return `${label}: ${formatTokens(v)}`;
        },
      },
    },
  },
  scales: {
    x: {
      ticks: { color: chartTheme.value.text, font: { size: 11 } },
      grid: { color: chartTheme.value.grid, display: false },
    },
    y: {
      type: 'linear',
      position: 'left',
      title: { display: true, text: '成本 (USD)', color: chartTheme.value.text },
      ticks: {
        color: chartTheme.value.text,
        font: { size: 11 },
        callback: (v) => `$${Number(v).toFixed(2)}`,
      },
      grid: { color: chartTheme.value.grid },
    },
    y1: {
      type: 'linear',
      position: 'right',
      title: { display: true, text: 'Token 总量', color: chartTheme.value.text },
      ticks: {
        color: chartTheme.value.text,
        font: { size: 11 },
        callback: (v) => formatTokens(Number(v)),
      },
      grid: { drawOnChartArea: false },
    },
  },
}));

// === 图表 2：模型占比饼（按 USD 折算）===
const modelPieData = computed<ChartData<'doughnut'>>(() => {
  const usdStats = modelStats.value
    .map((m) => ({
      name: m.displayName || m.normalizedModel,
      usd: nativeMicroToUsdMicro(m.totalCost, m.currencyCode),
    }))
    .filter((m) => m.usd > 0)
    .sort((a, b) => b.usd - a.usd);

  const TOP_N = 7;
  const top = usdStats.slice(0, TOP_N);
  const restSum = usdStats.slice(TOP_N).reduce((s, m) => s + m.usd, 0);

  const labels = top.map((m) => m.name);
  const data = top.map((m) => m.usd);
  if (restSum > 0) {
    labels.push('其他');
    data.push(restSum);
  }

  const colors = top.map((_, i) => PALETTE[i % PALETTE.length]);
  if (restSum > 0) colors.push(OTHER_COLOR);

  return {
    labels,
    datasets: [
      {
        data,
        backgroundColor: colors,
        borderColor: readToken('--card', '#FFFFFF'),
        borderWidth: 2,
        hoverOffset: 8,
      },
    ],
  };
});

const doughnutOptions = computed<ChartOptions<'doughnut'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  cutout: '60%',
  plugins: {
    legend: {
      position: 'right',
      labels: {
        color: chartTheme.value.text,
        font: { size: 11 },
        boxWidth: 12,
        boxHeight: 12,
        usePointStyle: true,
      },
    },
    tooltip: {
      callbacks: {
        label: (ctx: TooltipItem<'doughnut'>) => {
          const label = ctx.label || '';
          const v = typeof ctx.parsed === 'number' ? ctx.parsed : 0;
          return `${label}: ${formatCost(v, 'USD')}`;
        },
      },
    },
  },
}));

// === 图表 3：供应商对比柱（USD 折算）===
const providerBarData = computed<ChartData<'bar'>>(() => {
  const stats = providerStats.value
    .filter((p) => (p.totalCostUSD ?? 0) > 0)
    .sort((a, b) => (b.totalCostUSD ?? 0) - (a.totalCostUSD ?? 0));
  return {
    labels: stats.map((p) => p.provider || '未知'),
    datasets: [
      {
        label: '成本 (USD)',
        data: stats.map((p) => (p.totalCostUSD ?? 0) / 1e6),
        backgroundColor: chartTheme.value.accent,
        borderRadius: 4,
        maxBarThickness: 40,
      },
    ],
  };
});

const barOptions = computed<ChartOptions<'bar'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: { display: false },
    tooltip: {
      callbacks: {
        label: (ctx: TooltipItem<'bar'>) => {
          const v = typeof ctx.parsed.y === 'number' ? ctx.parsed.y : 0;
          return `成本: $${v.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 4 })}`;
        },
      },
    },
  },
  scales: {
    x: {
      ticks: { color: chartTheme.value.text, font: { size: 11 } },
      grid: { display: false },
    },
    y: {
      ticks: {
        color: chartTheme.value.text,
        font: { size: 11 },
        callback: (v) => `$${Number(v).toFixed(2)}`,
      },
      grid: { color: chartTheme.value.grid },
    },
  },
}));

// === 图表 4：Token 四维堆叠柱 / Token four-dimension stacked bar ===
const tokenStackData = computed<ChartData<'bar'>>(() => {
  const top = [...modelStats.value]
    .map((m) => ({
      name: m.displayName || m.normalizedModel,
      input: m.inputTokens ?? 0,
      output: m.outputTokens ?? 0,
      cacheRead: m.cacheRead ?? 0,
      cacheCreation: m.cacheCreation ?? 0,
      total: (m.inputTokens ?? 0) + (m.outputTokens ?? 0) + (m.cacheRead ?? 0) + (m.cacheCreation ?? 0),
    }))
    .filter((m) => m.total > 0)
    .sort((a, b) => b.total - a.total)
    .slice(0, 8);

  return {
    labels: top.map((m) => m.name),
    datasets: [
      { label: 'Input', data: top.map((m) => m.input), backgroundColor: PALETTE[0] },
      { label: 'Output', data: top.map((m) => m.output), backgroundColor: PALETTE[1] },
      { label: 'Cache Read', data: top.map((m) => m.cacheRead), backgroundColor: PALETTE[2] },
      { label: 'Cache Creation', data: top.map((m) => m.cacheCreation), backgroundColor: PALETTE[3] },
    ],
  };
});

const stackedBarOptions = computed<ChartOptions<'bar'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: {
      position: 'top',
      labels: {
        color: chartTheme.value.text,
        font: { size: 11 },
        boxWidth: 12,
        boxHeight: 8,
        usePointStyle: true,
      },
    },
    tooltip: {
      callbacks: {
        label: (ctx: TooltipItem<'bar'>) => {
          const label = ctx.dataset.label || '';
          const v = typeof ctx.parsed.y === 'number' ? ctx.parsed.y : 0;
          return `${label}: ${formatTokens(v)}`;
        },
      },
    },
  },
  scales: {
    x: {
      stacked: true,
      ticks: { color: chartTheme.value.text, font: { size: 11 } },
      grid: { display: false },
    },
    y: {
      stacked: true,
      ticks: {
        color: chartTheme.value.text,
        font: { size: 11 },
        callback: (v) => formatTokens(Number(v)),
      },
      grid: { color: chartTheme.value.grid },
    },
  },
}));

// === 事件处理 / Handlers ===

async function handleRetry(): Promise<void> {
  await store.retry();
}

async function handleSync(): Promise<void> {
  try {
    const result = await store.syncNow();
    const added = result?.recordsAdded ?? 0;
    if (result?.errors && result.errors.length > 0) {
      showSuccess(`同步完成：新增 ${added} 条；${result.errors.length} 个源失败`);
    } else {
      showSuccess(`同步完成：新增 ${added} 条记录`);
    }
  } catch (err) {
    showError('同步失败：' + errToString(err));
  }
}

function handleResetFilter(): void {
  store.resetFilter();
}

// === 价格对话框 / Pricing dialog ===
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
    showError('删除失败：' + errToString(err));
  }
}

async function handleResetPricing(): Promise<void> {
  if (!confirm('确认恢复全部内置价格？自定义条目会保留，内置条目的单价会被重置为默认值。')) return;
  try {
    await store.resetPricing();
    showSuccess('内置价格已恢复');
  } catch (err) {
    showError('恢复失败：' + errToString(err));
  }
}

// === 时间格式 / Time formatting ===

function formatRelative(iso: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const diffMs = Date.now() - d.getTime();
  const sec = Math.floor(diffMs / 1000);
  if (sec < 60) return '刚刚';
  if (sec < 3600) return `${Math.floor(sec / 60)} 分钟前`;
  if (sec < 86400) return `${Math.floor(sec / 3600)} 小时前`;
  return `${Math.floor(sec / 86400)} 天前`;
}

function errToString(err: unknown): string {
  if (!err) return '未知错误';
  if (typeof err === 'string') return err;
  if (err instanceof Error) return err.message;
  if (Array.isArray(err)) return err.map(String).join('; ');
  try {
    return JSON.stringify(err);
  } catch {
    return String(err);
  }
}

// === 生命周期与监听 / Lifecycle & watchers ===

// filter 变化时（用户操作）触发完整刷新；后台 30s 走 silent。
watch(
  () => store.filter,
  () => {
    store.fetchAll({ silent: false });
  },
  { deep: true },
);

onMounted(async () => {
  // 首次加载：拉取主数据 + 价格表 + 未知模型 + 同步状态
  await store.fetchAll({ silent: false });
  // 这些是次要数据，失败不影响四态，静默拉取
  await Promise.all([
    store.fetchPricing(),
    store.fetchUnknownModels(),
    store.fetchSyncState({ silent: true }),
  ]);
  // 后台 30s 静默刷新主数据（保留旧数据，失败不刷屏）
  refreshTimer = window.setInterval(() => {
    store.fetchAll({ silent: true });
  }, REFRESH_INTERVAL);
});

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = null;
  }
});
</script>

<style scoped>
.view-usage {
  padding: 32px 36px;
  gap: 22px;
  overflow: auto;
  display: flex;
  flex-direction: column;
}

.head-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.last-synced {
  font-size: 12px;
  color: var(--tertiary);
  font-family: var(--mono);
}

.sync-icon {
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 1.5px solid currentColor;
  border-top-color: transparent;
  border-radius: 50%;
  margin-right: 4px;
  vertical-align: -2px;
}

.sync-icon.spinning {
  animation: sync-spin 0.8s linear infinite;
}

@keyframes sync-spin {
  to { transform: rotate(360deg); }
}

/* === 仪表盘汇总卡片 / Summary dashboard === */
.summary-card {
  gap: 14px;
}

.summary-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.summary-title {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.summary-title h2 {
  font-size: 16px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
  letter-spacing: -0.2px;
}

.summary-sub {
  font-size: 12px;
  color: var(--tertiary);
}

.summary-live {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--secondary);
}

.live-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--success);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--success) 18%, transparent);
}

.summary-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 14px 20px;
}

.metric {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 2px 0;
}

.metric + .metric {
  border-left: 1px solid var(--separator);
  padding-left: 20px;
}

.metric-label {
  font-size: 12px;
  color: var(--tertiary);
  letter-spacing: 0.2px;
}

.metric-value {
  font-size: 20px;
  font-weight: 600;
  color: var(--label);
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
}

.metric-primary .metric-value {
  font-size: 26px;
}

.metric-value.accent {
  color: var(--accent);
}

.metric-sub {
  font-size: 11px;
  color: var(--tertiary);
  margin-top: 2px;
}

@media (max-width: 1100px) {
  .summary-grid {
    grid-template-columns: repeat(2, 1fr);
  }
  .metric:nth-child(3) { border-left: none; padding-left: 0; }
}

@media (max-width: 600px) {
  .summary-grid {
    grid-template-columns: 1fr;
  }
  .metric + .metric {
    border-left: none;
    padding-left: 0;
    border-top: 1px solid var(--separator);
    padding-top: 10px;
  }
}

/* === 筛选器 / Filters === */
.filter-row {
  display: flex;
  gap: 12px;
  align-items: flex-end;
  flex-wrap: wrap;
}

.filter-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.filter-group label {
  font-size: 12px;
  color: var(--secondary);
  font-weight: 500;
}

.filter-actions {
  margin-left: auto;
}

.filter-input,
.filter-select {
  appearance: none;
  -webkit-appearance: none;
  background: var(--control);
  border: 1px solid transparent;
  border-radius: 7px;
  padding: 6px 10px;
  font-size: 13px;
  color: var(--label);
  font-family: inherit;
  cursor: pointer;
  min-width: 130px;
  transition: background-color 0.12s;
  outline: none;
}

.filter-input {
  cursor: text;
}

.filter-select {
  padding-right: 28px;
  background-image: url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='10' height='10' viewBox='0 0 24 24' fill='none' stroke='%238E8E93' stroke-width='2.5' stroke-linecap='round'><polyline points='6 9 12 15 18 9'/></svg>");
  background-repeat: no-repeat;
  background-position: right 9px center;
}

.filter-input:focus,
.filter-select:focus {
  box-shadow: 0 0 0 2px rgba(0, 122, 255, 0.25);
  background-color: var(--card);
}

.filter-input:hover,
.filter-select:hover {
  background-color: var(--controlHover);
}

.filter-warn {
  font-size: 12px;
  color: var(--warning-strong);
  background: rgba(255, 149, 0, 0.08);
  padding: 8px 10px;
  border-radius: 7px;
  line-height: 1.5;
}

/* === 图表卡片 / Chart cards === */
.card-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 4px;
}

.card-head h2 {
  font-size: 16px;
  font-weight: 600;
  color: var(--label);
}

.card-sub {
  font-size: 12px;
  color: var(--tertiary);
}

.chart-box {
  position: relative;
  height: 320px;
  width: 100%;
}

.chart-box-short {
  height: 260px;
}

.chart-empty {
  padding: 40px 20px;
  text-align: center;
  font-size: 13px;
  color: var(--tertiary);
}

.grid-2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 22px;
}

@media (max-width: 960px) {
  .grid-2 {
    grid-template-columns: 1fr;
  }
}

/* === 明细表 / Detail table === */
.table-wrap {
  max-height: 500px;
  overflow-y: auto;
  border: 1px solid var(--separator);
  border-radius: 10px;
}

.usage-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.usage-table thead {
  position: sticky;
  top: 0;
  z-index: 1;
}

.usage-table th {
  background: var(--sidebar);
  color: var(--secondary);
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  padding: 9px 12px;
  text-align: left;
  border-bottom: 1px solid var(--separator);
}

.usage-table th.num {
  text-align: right;
}

.usage-table td {
  padding: 8px 12px;
  border-bottom: 1px solid var(--separator);
  color: var(--label);
  vertical-align: top;
}

.usage-table tr:last-child td {
  border-bottom: none;
}

.usage-table tr:hover td {
  background: color-mix(in srgb, var(--accent) 4%, transparent);
}

.usage-table td.num {
  text-align: right;
}

.usage-table td.mono,
.mono {
  font-family: var(--mono);
  font-size: 12px;
}

.badge-warn {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 600;
  background: rgba(255, 149, 0, 0.12);
  color: var(--warning-strong);
}

.badge-ok {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 600;
  background: rgba(52, 199, 89, 0.12);
  color: var(--success-strong);
}

/* 响应式：小屏水平滚动表格 / Responsive: scroll table horizontally on narrow screens. */
@media (max-width: 720px) {
  .view-usage {
    padding: 22px 18px;
  }
  .table-wrap {
    overflow-x: auto;
    max-height: none;
  }
  .usage-table {
    min-width: 720px;
  }
}
</style>
