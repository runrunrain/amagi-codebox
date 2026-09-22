<template>
  <ConfigCard class="quota-card" :class="{ 'quota-card-gray': !isOk, 'quota-card-compact': compact }">
    <!-- 卡头：家族显示名 · 副标 + 档位 badge（+ Codex credits badge） -->
    <div class="q-head">
      <div class="q-title">
        <h3 class="q-name">{{ displayTitle }}</h3>
        <span v-if="subLabel" class="q-sub">{{ subLabel }}</span>
      </div>
      <span v-if="level" class="q-level" :title="`档位 ${level}`">{{ level }} 档</span>
      <span v-if="creditsBadge" class="q-credits" :class="{ 'q-credits-none': !creditsBadge.has }">
        {{ creditsBadge.text }}
      </span>
    </div>

    <!-- ok · 窗口型（GLM / Codex）：primary + secondary 上下两条 -->
    <template v-if="isOk && primary">
      <div class="q-window">
        <div class="q-window-head">
          <span class="q-window-label">{{ primaryLabel }}</span>
          <span class="q-window-reset mono">{{ primaryCountdown }}</span>
        </div>
        <QuotaBar :used-percent="primary.used_percent" :label="percentText(primary)" />
        <!-- GLM 有 remaining 时条下追加「剩余 N prompts」 -->
        <div v-if="hasGlmRemaining" class="q-remaining mono">
          剩余 {{ Math.round(primary.remaining ?? 0) }} prompts
        </div>
      </div>
      <div v-if="secondary" class="q-window">
        <div class="q-window-head">
          <span class="q-window-label">{{ secondaryLabel }}</span>
          <span class="q-window-reset mono">{{ secondaryCountdown }}</span>
        </div>
        <QuotaBar :used-percent="secondary.used_percent" :label="percentText(secondary)" />
      </div>
      <div v-if="tertiary" class="q-window">
        <div class="q-window-head">
          <span class="q-window-label">{{ tertiaryLabel }}</span>
          <span class="q-window-reset mono">{{ tertiaryCountdown }}</span>
        </div>
        <QuotaBar :used-percent="tertiary.used_percent" :label="percentText(tertiary)" />
      </div>
    </template>

    <!-- ok · 余额型（DeepSeek / OpenRouter）：大数字排版，不渲染进度条（无分母不硬造） -->
    <div v-else-if="isOk && entry.balance" class="q-balance">
      <span class="q-balance-total mono accent">{{ balanceTotal }}</span>
      <span v-if="balanceDetail" class="q-balance-line mono">{{ balanceDetail }}</span>
      <span v-if="entry.balance.topped_up" class="q-balance-line mono">充值 {{ balanceToppedUp }}</span>
      <span v-if="entry.balance.granted" class="q-balance-line mono">赠送 {{ balanceGranted }}</span>
    </div>

    <!-- 灰卡态：no_plan / no_key / unsupported / error（保留 provider 名与刷新入口） -->
    <div v-else class="q-gray">
      <p class="q-gray-text">{{ grayText }}</p>
      <AppButton v-if="entry.status === 'error'" variant="ghost" size="small" :disabled="probing" @click="emit('refresh')">
        重试
      </AppButton>
    </div>

    <!-- 卡脚（右下角常显）：[↻] HH:mm · 来源；ok 且 >30min 黄点「数据来自 HH:mm」 -->
    <div class="q-foot">
      <span v-if="stale" class="q-stale" :title="`数据来自 ${probedClock}，已超过 30 分钟`">
        <span class="q-stale-dot" />数据来自 {{ probedClock }}
      </span>
      <button class="q-refresh" type="button" :disabled="probing" title="刷新该服务额度" @click="emit('refresh')">
        <span class="q-refresh-icon" :class="{ spinning: probing }">↻</span>
        <span class="mono">{{ probedClock || '—' }} · {{ sourceText }}</span>
      </button>
    </div>
  </ConfigCard>
</template>

<script setup lang="ts">
/**
 * QuotaCard — 单服务额度卡（设计 §6 状态矩阵）。
 * 按 Family 分形态：窗口型（GLM/Codex 1–2 条 / Zen 3 条）与余额型
 *（DeepSeek / OpenRouter，原币种排版；OpenRouter 剩余+已用明细）。
 * 灰卡态（no_plan/no_key/unsupported/error）保留 provider 名与刷新入口。
 * compact：Web 平面 strip 弹层内的精简渲染（同分支、收紧留白）。
 * 倒计时挂载时计算一次，不轮询（设计 §6）。
 */
import { computed } from 'vue';
import ConfigCard from '../ui/ConfigCard.vue';
import AppButton from '../ui/AppButton.vue';
import QuotaBar from './QuotaBar.vue';
import {
  QUOTA_FAMILY_GLM_BIGMODEL,
  QUOTA_FAMILY_GLM_ZAI,
  codexCreditsBadge,
  familySubLabel,
  formatBalanceAmount,
  formatBalanceDetail,
  formatBalanceHeadline,
  formatClock,
  formatResetCountdown,
  grayCardText,
  isStaleOk,
  primaryWindowOf,
  secondaryWindowOf,
  sourceLabel,
  tertiaryWindowOf,
  windowLabel,
} from './quotaModel';
import type { ProviderQuotaEntry, QuotaWindow } from './quotaModel';

const props = withDefaults(
  defineProps<{
    entry: ProviderQuotaEntry;
    /** 卡片标题（父级决定：家族显示名 / provider 名） */
    title?: string;
    /** 单卡探测 spinner（禁用刷新/重试按钮） */
    probing?: boolean;
    /** strip 弹层精简渲染 */
    compact?: boolean;
  }>(),
  { title: '', probing: false, compact: false },
);

const emit = defineEmits<{ refresh: [] }>();

const isOk = computed(() => props.entry.status === 'ok');
const displayTitle = computed(() => props.title || props.entry.provider || '—');
const subLabel = computed(() => familySubLabel(props.entry.family));
const level = computed(() => (isOk.value ? (props.entry.level ?? '').trim() : ''));
const creditsBadge = computed(() => codexCreditsBadge(props.entry));
const grayText = computed(() => grayCardText(props.entry));
const probedClock = computed(() => formatClock(props.entry.probed_at));
const sourceText = computed(() => sourceLabel(props.entry.source));
const stale = computed(() => isStaleOk(props.entry));

// 窗口型：GLM time_limit 已在 primaryWindowOf 内归并到主窗口
const primary = computed(() => primaryWindowOf(props.entry));
const secondary = computed(() => secondaryWindowOf(props.entry));
const tertiary = computed(() => tertiaryWindowOf(props.entry));
const primaryLabel = computed(() => windowLabel(primary.value));
const secondaryLabel = computed(() => windowLabel(secondary.value));
const tertiaryLabel = computed(() => windowLabel(tertiary.value));
// 倒计时挂载时计算一次（computed 依赖不变即不重算，无轮询）
const primaryCountdown = computed(() => formatResetCountdown(primary.value?.resets_at));
const secondaryCountdown = computed(() => formatResetCountdown(secondary.value?.resets_at));
const tertiaryCountdown = computed(() => formatResetCountdown(tertiary.value?.resets_at));
const hasGlmRemaining = computed(
  () =>
    (props.entry.family === QUOTA_FAMILY_GLM_BIGMODEL || props.entry.family === QUOTA_FAMILY_GLM_ZAI) &&
    typeof primary.value?.remaining === 'number',
);

// 余额排版（原币种，零换算；OpenRouter 口径大数字=剩余 + 已用/共明细）
const balanceTotal = computed(() => formatBalanceHeadline(props.entry.balance));
const balanceDetail = computed(() => formatBalanceDetail(props.entry.balance));
const balanceToppedUp = computed(() =>
  props.entry.balance ? formatBalanceAmount({ ...props.entry.balance, total: props.entry.balance.topped_up ?? 0 }) : '',
);
const balanceGranted = computed(() =>
  props.entry.balance ? formatBalanceAmount({ ...props.entry.balance, total: props.entry.balance.granted ?? 0 }) : '',
);

function percentText(w: QuotaWindow | null): string {
  if (!w) return '';
  return `${Math.round(Math.max(0, Math.min(100, w.used_percent)))}%`;
}
</script>

<style scoped>
.quota-card { gap: 12px; }

.q-head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.q-title { display: flex; align-items: baseline; gap: 8px; min-width: 0; flex: 1; }
.q-name { margin: 0; font-size: 15px; font-weight: 600; color: var(--label); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.q-sub { color: var(--tertiary); font-size: 12px; font-family: var(--mono, monospace); }

.q-level {
  flex-shrink: 0;
  border-radius: 5px;
  padding: 2px 7px;
  background: color-mix(in srgb, var(--accent) 12%, transparent);
  color: var(--accent-strong, #0a5cb8);
  font-size: 11px;
  font-weight: 600;
}

.q-credits {
  flex-shrink: 0;
  border-radius: 5px;
  padding: 2px 7px;
  background: rgba(52, 199, 89, 0.12);
  color: var(--success-strong, #1d6a3a);
  font-size: 11px;
  font-weight: 600;
}
.q-credits-none {
  background: var(--control);
  color: var(--tertiary);
}

.q-window { display: flex; flex-direction: column; gap: 5px; }
.q-window-head { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.q-window-label { color: var(--secondary); font-size: 12px; font-weight: 500; }
.q-window-reset { color: var(--tertiary); font-size: 11px; }
.q-remaining { color: var(--tertiary); font-size: 11px; }

.q-balance { display: flex; flex-direction: column; gap: 4px; padding: 6px 0 2px; }
.q-balance-total { color: var(--label); font-size: 30px; font-weight: 600; line-height: 1.1; font-variant-numeric: tabular-nums; }
.q-balance-total.accent { color: var(--accent); }
.q-balance-line { color: var(--tertiary); font-size: 12px; }

.q-gray { display: flex; flex-direction: column; gap: 10px; padding: 10px 0 6px; }
.q-gray-text { margin: 0; color: var(--secondary); font-size: 13px; line-height: 1.6; }

/* 灰卡整体降饱和（保留结构，状态一眼可辨） */
.quota-card-gray :deep(.q-name) { color: var(--secondary); }
.quota-card-gray { background: var(--sidebar); box-shadow: none; }

.q-foot {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: auto;
  padding-top: 4px;
}

.q-stale { display: inline-flex; align-items: center; gap: 5px; color: var(--warning-strong, #b25000); font-size: 11px; }
.q-stale-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--warning, #FF9500); }

.q-refresh {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border: none;
  background: transparent;
  color: var(--tertiary);
  font-size: 11px;
  font-family: inherit;
  cursor: pointer;
  padding: 3px 4px;
  border-radius: 6px;
  transition: color 0.15s, background 0.15s;
}
.q-refresh:hover:not(:disabled) { color: var(--accent); background: var(--control); }
.q-refresh:disabled { cursor: default; opacity: 0.7; }

.q-refresh-icon { display: inline-block; font-size: 12px; line-height: 1; }
.q-refresh-icon.spinning { animation: q-spin 0.8s linear infinite; }
@keyframes q-spin { to { transform: rotate(360deg); } }

/* strip 弹层精简渲染：收紧留白、缩小余额数字 */
.quota-card-compact { padding: 14px 16px 8px; gap: 10px; }
.quota-card-compact .q-balance-total { font-size: 24px; }
</style>
