<template>
  <section class="view-usage">
    <!-- P2 分页化（设计 §5）：壳层只保留 PageHead / 提示条 / 页切换 / 子路由 -->
    <div class="usage-head">
      <PageHead :title="pageTitle" :description="pageDescription" />
      <!-- actions 随子页切换：stats=同步按钮组（原头部内容）；quota=全部刷新+数据时间 -->
      <div v-if="isStatsPage" class="head-actions">
        <span v-if="lastSyncedAt" class="last-synced" :title="`上次同步：${lastSyncedAt}`">
          已同步 · {{ formatRelative(lastSyncedAt) }}
        </span>
        <AppButton variant="ghost" size="small" :disabled="syncing" @click="handleSync">
          <span class="sync-icon" :class="{ spinning: syncing }" />
          {{ syncing ? '同步中...' : '立即同步' }}
        </AppButton>
      </div>
      <div v-else class="head-actions">
        <span v-if="latestQuotaClock" class="last-synced" title="全部条目中最新的探测时间">数据时间 {{ latestQuotaClock }}</span>
        <AppButton variant="ghost" size="small" :disabled="quotaStore.probingAll" @click="handleProbeAll">
          <span class="sync-icon" :class="{ spinning: quotaStore.probingAll }" />
          {{ quotaStore.probingAll ? '刷新中...' : '全部刷新' }}
        </AppButton>
      </div>
    </div>

    <!-- RC1-6：远程模式提示条（使用统计/额度查询均为本机功能） -->
    <RemoteScopeBanner subject="使用统计" />

    <!-- 页切换 Segmented：视觉语言对齐统计图表切换的 pill -->
    <div class="page-switch">
      <Segmented
        :model-value="activePage"
        :options="pageOptions"
        variant="pill"
        @update:model-value="onPageSelect"
      />
    </div>

    <router-view />
  </section>
</template>

<script setup lang="ts">
/**
 * UsageView — /usage 分页壳（设计 §5）。
 * /usage → redirect /usage/stats（路由层）；本组件承载
 * PageHead（title/actions 按子页切换）+ Segmented 页切换 + <router-view/>。
 * 统计内容在 UsageStatsPanel（原 808 行纯搬运），额度页在 QuotaPanel。
 */
import { computed } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import PageHead from '../components/ui/PageHead.vue';
import RemoteScopeBanner from '../components/remote/RemoteScopeBanner.vue';
import AppButton from '../components/ui/AppButton.vue';
import Segmented from '../components/ui/Segmented.vue';
import { useUsageStore } from '../stores/usage';
import { useQuotaStore } from '../stores/quota';
import { useToast } from '../composables/useToast';

const route = useRoute();
const router = useRouter();
const usageStore = useUsageStore();
const quotaStore = useQuotaStore();
const { showError, showSuccess } = useToast();

const pageOptions = [
  { value: 'stats', label: '使用统计' },
  { value: 'quota', label: '额度查询' },
];

const isStatsPage = computed(() => route.name !== 'UsageQuota');
const activePage = computed(() => (isStatsPage.value ? 'stats' : 'quota'));

const pageTitle = computed(() => (isStatsPage.value ? '使用统计' : '额度查询'));
const pageDescription = computed(() =>
  isStatsPage.value
    ? '模型用量、缓存经济性与成本趋势；默认只统计会话日志'
    : 'GLM Coding Plan / DeepSeek / Codex 订阅额度快照与刷新',
);

function onPageSelect(page: string): void {
  void router.push(page === 'quota' ? '/usage/quota' : '/usage/stats');
}

// ---- stats 页 actions（原 PageHead actions 内容迁移）----
const syncing = computed(() => usageStore.syncing);
const lastSyncedAt = computed(() => usageStore.lastSyncedAt);

async function handleSync(): Promise<void> {
  try {
    const result = await usageStore.syncNow();
    if (result.errors?.length) showSuccess(`同步完成：新增 ${result.recordsAdded ?? 0} 条；${result.errors.length} 个源失败`);
    else showSuccess(`同步完成：新增 ${result.recordsAdded ?? 0} 条记录`);
  } catch (err) {
    showError(`同步失败：${errToString(err)}`);
  }
}

// ---- quota 页 actions：全部刷新 + 数据时间 ----
/** 数据时间：全部条目中最新的 probed_at（HH:mm） */
const latestQuotaClock = computed(() => {
  let latest = 0;
  for (const entry of Object.values(quotaStore.entries)) {
    const ms = Date.parse(entry?.probed_at ?? '');
    if (Number.isFinite(ms) && ms > latest) latest = ms;
  }
  if (!latest) return '';
  const d = new Date(latest);
  const pad = (n: number) => (n < 10 ? `0${n}` : String(n));
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
});

async function handleProbeAll(): Promise<void> {
  try {
    const result = await quotaStore.probeAll();
    showSuccess(`已刷新 ${Object.keys(result ?? {}).length} 家`);
  } catch (err) {
    showError(`额度刷新失败：${errToString(err)}`);
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
</script>

<style scoped>
.view-usage { display: flex; flex-direction: column; gap: 22px; overflow: auto; padding: 32px 36px; }

.usage-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.head-actions { display: flex; align-items: center; gap: 10px; padding-top: 6px; }
.last-synced { color: var(--tertiary); font-size: 12px; }
.mono, .last-synced { font-family: var(--mono); }
.sync-icon { display: inline-block; width: 12px; height: 12px; margin-right: 4px; border: 1.5px solid currentColor; border-top-color: transparent; border-radius: 50%; vertical-align: -2px; }
.sync-icon.spinning { animation: sync-spin .8s linear infinite; }
@keyframes sync-spin { to { transform: rotate(360deg); } }

.page-switch { max-width: 260px; }
.page-switch :deep(.seg) { padding: 6px 10px; font-size: 12px; }

@media (max-width: 720px) {
  .view-usage { padding: 22px 18px; }
  .usage-head { flex-direction: column; }
}
</style>
