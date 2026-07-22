<template>
  <div class="other-tools-panel">
    <!-- 初始加载态：开关状态尚未拿到时显示骨架，避免开关闪烁误触发后端 -->
    <LoadingState v-if="initialLoading" message="加载其他工具配置中..." />

    <!-- 初始错误态：可重试 -->
    <ErrorState
      v-else-if="initialError"
      :message="initialError"
      :on-retry="handleRetry"
    />

    <!-- 主内容：当前仅 Headroom 全局压缩卡片，预留后续工具扩展位 -->
    <template v-else>
      <ConfigCard class="headroom-card">
        <!-- 头部：标题 + 独立全局开关 -->
        <div class="hr-head">
          <div class="hr-title">
            <div class="hr-title-row">
              <span
                class="status-dot"
                :class="status.enabled && status.running ? 'on' : status.enabled ? 'pending' : 'off'"
                :aria-label="status.enabled && status.running ? '运行中' : status.enabled ? '已启用待启动' : '已关闭'"
              ></span>
              <h2>Headroom 全局压缩</h2>
              <span v-if="status.enabled && status.running" class="badge badge-ok">运行中</span>
              <span v-else-if="status.enabled" class="badge badge-warn">已启用 · 代理未运行</span>
              <span v-else class="badge badge-muted">已关闭</span>
            </div>
            <p class="hr-sub">
              对独立 Codex 桌面版 / CLI / IDE 同时生效；代理仅在 CodeBox 运行时可用，端口 {{ status.port || fallbackPort }} / 目标 OpenAI upstream。
            </p>
          </div>
          <Switch
            :model-value="status.enabled"
            :disabled="toggling"
            aria-label="Codex 全局 Headroom 开关"
            @update:model-value="handleToggleEnabled"
          />
        </div>

        <!-- 切换中：开关切换 / 第二实例启停期间显示 -->
        <div v-if="toggling" class="hr-inline-state">
          <div class="spinner-sm" />
          <span>{{ status.enabled ? '正在关闭全局压缩...' : '正在启用全局压缩...' }}</span>
        </div>

        <!-- 状态行：端口 / 运行状态 / 目标 upstream（只读） -->
        <div class="hr-status-grid">
          <div class="status-cell">
            <span class="cell-label">代理端口</span>
            <span class="cell-value mono">{{ status.port || fallbackPort }}</span>
          </div>
          <div class="status-cell">
            <span class="cell-label">实例状态</span>
            <span class="cell-value">
              <span
                class="dot-mini"
                :class="status.running ? 'on' : 'off'"
              ></span>
              {{ status.running ? '存活（第二实例）' : '未运行' }}
            </span>
          </div>
          <div class="status-cell status-cell-wide">
            <span class="cell-label">目标 upstream（只读）</span>
            <span class="cell-value mono upstream-value">{{ effectiveTarget }}</span>
          </div>
        </div>

        <!-- target 高级输入：可配置，独立保存按钮 -->
        <div class="hr-advanced">
          <details class="adv-details">
            <summary class="adv-summary">
              <span class="adv-chevron" aria-hidden="true">›</span>
              高级：自定义 OpenAI target
            </summary>
            <div class="adv-body">
              <label class="adv-label" for="hr-target-input">
                OpenAI target base URL
              </label>
              <input
                id="hr-target-input"
                v-model="targetDraft"
                type="text"
                class="adv-input mono"
                :placeholder="`默认 ${defaultTarget}`"
                :disabled="savingTarget"
                spellcheck="false"
                autocomplete="off"
              />
              <p class="adv-hint">
                ChatGPT 账号登录时压缩是否生效需主上实测确认；target 可按需调整（留空回退默认）。
                端口固定为 {{ fallbackPort }}（与会话级 8787 完全隔离）。
              </p>
              <div class="adv-actions">
                <AppButton
                  variant="ghost"
                  size="small"
                  :disabled="savingTarget || !targetDirty"
                  @click="resetTargetDraft"
                >
                  重置
                </AppButton>
                <AppButton
                  variant="primary"
                  size="small"
                  :disabled="savingTarget || !targetDirty"
                  @click="handleSaveTarget"
                >
                  {{ savingTarget ? '保存中...' : '保存 target' }}
                </AppButton>
              </div>
            </div>
          </details>
        </div>

        <!-- by_client 统计区分展示（核心）：分项列出每个 client 的压缩数据 -->
        <div class="hr-clients">
          <div class="clients-head">
            <div class="clients-title">
              <h3>压缩统计（按客户端区分）</h3>
              <span class="clients-sub">来源 ~/.headroom/savings_events.jsonl · 全局共享 ledger</span>
            </div>
            <AppButton
              variant="ghost"
              size="small"
              :disabled="savingsRefreshing"
              @click="refreshSavings(true)"
            >
              {{ savingsRefreshing ? '刷新中...' : '刷新' }}
            </AppButton>
          </div>

          <!-- 统计加载态 -->
          <div v-if="savingsRefreshing && !savingsReport" class="hr-inline-state">
            <div class="spinner-sm" />
            <span>正在读取压缩统计...</span>
          </div>

          <!-- 统计空态 / 错误态：不刷屏弹 toast，仅在卡片内静默提示 -->
          <div
            v-else-if="savingsError || !savingsReport || !hasClientData"
            class="hr-inline-state hr-inline-state-muted"
          >
            <span class="state-dot" :class="savingsError ? 'err' : 'muted'"></span>
            <span>{{ savingsEmptyMessage }}</span>
          </div>

          <!-- 成功态：客户端分项卡片 -->
          <div v-else class="clients-grid">
            <article
              v-for="c in savingsReport.by_client"
              :key="c.client"
              class="client-card"
              :class="{ 'client-card-codex': isCodexClient(c.client) }"
            >
              <header class="client-card-head">
                <span class="client-name mono">{{ c.client }}</span>
                <span
                  v-if="isCodexClient(c.client)"
                  class="tag tag-codex"
                  title="来自独立 codex 全局 headroom 的流量"
                >codex 全局</span>
                <span
                  v-else-if="isClaudeClient(c.client)"
                  class="tag tag-claude"
                  title="来自 claude 会话级 headroom 的流量"
                >claude 会话</span>
                <span v-else class="tag tag-other">其他</span>
              </header>
              <dl class="client-metrics">
                <div class="metric">
                  <dt>节省 Token</dt>
                  <dd class="mono accent">{{ formatNumber(c.tokens_saved) }}</dd>
                </div>
                <div class="metric">
                  <dt>压缩次数</dt>
                  <dd class="mono">{{ formatNumber(c.calls) }}</dd>
                </div>
                <div class="metric">
                  <dt>节省比例</dt>
                  <dd class="mono">{{ formatPercent(c.savings_percent) }}</dd>
                </div>
                <div class="metric">
                  <dt>避免成本</dt>
                  <dd class="mono">{{ formatCost(c.cost_usd) }}</dd>
                </div>
              </dl>
            </article>
          </div>

          <!-- ledger 汇总：所有 client 合计，便于横向对比 -->
          <div v-if="hasClientData && aggregated" class="clients-total">
            <span class="total-label">全部客户端合计</span>
            <span class="total-item">
              <span class="total-k">节省</span>
              <span class="mono accent">{{ formatNumber(aggregated.tokens_saved) }}</span>
              <span class="total-unit">token</span>
            </span>
            <span class="total-item">
              <span class="total-k">压缩</span>
              <span class="mono">{{ formatNumber(aggregated.calls) }}</span>
              <span class="total-unit">次</span>
            </span>
            <span class="total-item">
              <span class="total-k">避免成本</span>
              <span class="mono">{{ formatCost(aggregated.cost_usd) }}</span>
            </span>
          </div>
        </div>

        <!-- 底部提示：作用范围与边界 -->
        <ul class="hr-notes">
          <li>全局开关对独立 Codex 桌面版 / CLI / IDE 同时生效；会话级 Claude 压缩（端口 8787）不受影响。</li>
          <li>代理仅在 CodeBox 运行时可用；退出 CodeBox 后 Codex 流量将直连 target。</li>
          <li>统计 ledger 全局共享（~/.headroom/savings_events.jsonl），按 client 字段区分来源。</li>
        </ul>
      </ConfigCard>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue';
import ConfigCard from '../ui/ConfigCard.vue';
import Switch from '../ui/Switch.vue';
import AppButton from '../ui/AppButton.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import { useToast } from '../../composables/useToast';

import * as headroomGlobalApi from '../../api/headroomGlobal';
import { getHeadroomSavings } from '../../api/headroom';
import { main, headroom } from '../../../wailsjs/go/models';

type CodexGlobalHeadroomStatus = main.CodexGlobalHeadroomStatus;
type SavingsReport = headroom.SavingsReport;
type ClientSavings = headroom.ClientSavings;

const { showSuccess, showError } = useToast();

// 后端回退默认值（与 SetCodexGlobalHeadroom 的回退策略对齐，仅用于展示）
const defaultTarget = 'https://api.openai.com/v1';
const fallbackPort = 8788;

// 初始加载 / 错误态
const initialLoading = ref(true);
const initialError = ref('');

// 开关状态
const status = ref<CodexGlobalHeadroomStatus>({
  enabled: false,
  target: '',
  port: fallbackPort,
  running: false,
});
const toggling = ref(false);

// target 高级输入
const targetDraft = ref('');
const savingTarget = ref(false);

// by_client 统计
const savingsReport = ref<SavingsReport | null>(null);
const savingsRefreshing = ref(false);
const savingsError = ref(false);

// 定时刷新：10s，与 LogsView 的 headroom 轮询对齐
const SAVINGS_REFRESH_INTERVAL = 10000;
let savingsTimer: number | null = null;

// --- computed ---

// 展示用 target：空回退到默认
const effectiveTarget = computed(() => status.value.target?.trim() || defaultTarget);

// target 是否被编辑过（用于启用保存按钮）
const targetDirty = computed(() => {
  const draft = targetDraft.value.trim();
  const current = (status.value.target || '').trim();
  // 留空也算一种"编辑"（表示要回退默认），只要与当前持久化值不同就启用保存
  return draft !== current;
});

// by_client 是否有可用数据
const hasClientData = computed(() => {
  return !!(savingsReport.value?.by_client && savingsReport.value.by_client.length > 0);
});

// 空态文案：区分"调用失败"与"已就绪但无数据"
const savingsEmptyMessage = computed(() => {
  if (savingsError.value) {
    return 'Headroom 未安装或统计文件不可读，暂无压缩数据';
  }
  return 'Headroom 已就绪，暂无压缩数据（Codex 接入后此处会出现 codex 条目）';
});

// 所有 client 合计（横向对比用）
const aggregated = computed(() => {
  const list = savingsReport.value?.by_client;
  if (!list || list.length === 0) return null;
  return list.reduce<{ tokens_saved: number; calls: number; cost_usd: number }>(
    (acc, c: ClientSavings) => {
      acc.tokens_saved += c.tokens_saved || 0;
      acc.calls += c.calls || 0;
      acc.cost_usd += c.cost_usd || 0;
      return acc;
    },
    { tokens_saved: 0, calls: 0, cost_usd: 0 },
  );
});

// --- client 分类：不硬编码业务判定，仅做标签友好化 ---
function isCodexClient(client: string): boolean {
  const name = (client || '').toLowerCase();
  // headroom 的 client 字段通常为 "codex" / "codex-cli" / "codex-cli-rs" 等
  return name === 'codex' || name.startsWith('codex');
}

function isClaudeClient(client: string): boolean {
  const name = (client || '').toLowerCase();
  return name === 'claude-code' || name.startsWith('claude');
}

// --- 格式化（与 LogsView 保持一致） ---
function formatNumber(n: number): string {
  return Number(n || 0).toLocaleString('en-US');
}
function formatPercent(p: number): string {
  return `${Number(p || 0).toFixed(1)}%`;
}
function formatCost(usd: number): string {
  return `$${Number(usd || 0).toFixed(2)}`;
}

// --- 加载 ---

async function loadStatus() {
  const s = await headroomGlobalApi.getCodexGlobalHeadroom();
  status.value = s;
  // 初始化 target 草稿为当前持久化值（可能为空，表示回退默认）
  targetDraft.value = s.target || '';
}

async function refreshSavings(showLoading = false) {
  if (showLoading) savingsRefreshing.value = true;
  try {
    const report = await getHeadroomSavings();
    savingsReport.value = report;
    savingsError.value = false;
  } catch (err) {
    // 已有数据则保留（略陈旧但有信息量），仅在从未拿到过数据时进入错误空态
    if (!savingsReport.value) {
      savingsError.value = true;
    }
    if (showLoading) {
      console.warn('[OtherToolsPanel] getHeadroomSavings failed:', err);
    }
  } finally {
    if (showLoading) savingsRefreshing.value = false;
  }
}

async function handleRetry() {
  initialLoading.value = true;
  initialError.value = '';
  try {
    await Promise.all([loadStatus(), refreshSavings(false)]);
  } catch (err) {
    initialError.value = String(err);
  } finally {
    initialLoading.value = false;
  }
}

// --- 开关切换 ---

async function handleToggleEnabled(nextEnabled: boolean) {
  if (toggling.value) return;
  toggling.value = true;
  // 乐观更新开关视觉，失败时回滚
  const prev = status.value.enabled;
  status.value = { ...status.value, enabled: nextEnabled };
  try {
    const updated = await headroomGlobalApi.setCodexGlobalHeadroom(
      nextEnabled,
      status.value.target || '',
      status.value.port || fallbackPort,
    );
    status.value = updated;
    targetDraft.value = updated.target || '';
    showSuccess(
      nextEnabled
        ? `已启用 Codex 全局压缩（端口 ${updated.port || fallbackPort}）`
        : '已关闭 Codex 全局压缩',
    );
    // 切换后立即刷新统计，便于观察 codex 条目变化
    refreshSavings(false);
  } catch (err) {
    status.value = { ...status.value, enabled: prev };
    showError('切换全局压缩失败: ' + err);
  } finally {
    toggling.value = false;
  }
}

// --- target 高级保存 ---

function resetTargetDraft() {
  targetDraft.value = status.value.target || '';
}

async function handleSaveTarget() {
  if (savingTarget.value || !targetDirty.value) return;
  savingTarget.value = true;
  try {
    const updated = await headroomGlobalApi.setCodexGlobalHeadroom(
      status.value.enabled,
      targetDraft.value.trim(),
      status.value.port || fallbackPort,
    );
    status.value = updated;
    targetDraft.value = updated.target || '';
    showSuccess('Codex 全局压缩 target 已保存');
  } catch (err) {
    showError('保存 target 失败: ' + err);
  } finally {
    savingTarget.value = false;
  }
}

// --- 生命周期 ---

onMounted(async () => {
  initialLoading.value = true;
  initialError.value = '';
  try {
    await Promise.all([loadStatus(), refreshSavings(false)]);
  } catch (err) {
    initialError.value = String(err);
  } finally {
    initialLoading.value = false;
  }
  // 定时刷新统计（不显示 loading 避免闪烁）
  savingsTimer = window.setInterval(() => {
    refreshSavings(false);
  }, SAVINGS_REFRESH_INTERVAL);
});

onBeforeUnmount(() => {
  if (savingsTimer !== null) {
    clearInterval(savingsTimer);
    savingsTimer = null;
  }
});
</script>

<style scoped>
.other-tools-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* --- Headroom 卡片 --- */
.headroom-card {
  gap: 16px;
}

.hr-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.hr-title {
  display: flex;
  flex-direction: column;
  gap: 6px;
  flex: 1;
  min-width: 240px;
}

.hr-title-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.hr-title-row h2 {
  font-size: 16px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
  letter-spacing: -0.2px;
}

.hr-sub {
  font-size: 12px;
  color: var(--secondary);
  margin: 0;
  line-height: 1.5;
}

/* 状态点：三态（运行 / 已启用待启动 / 关闭） */
.status-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--tertiary);
  flex-shrink: 0;
}
.status-dot.on {
  background: var(--success);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--success) 18%, transparent);
}
.status-dot.pending {
  background: var(--warning);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--warning) 18%, transparent);
}
.status-dot.off {
  background: var(--tertiary);
}

/* 状态徽章 */
.badge {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 999px;
  font-weight: 500;
  letter-spacing: 0.2px;
}
.badge-ok {
  background: color-mix(in srgb, var(--success) 14%, transparent);
  color: var(--success-strong);
}
.badge-warn {
  background: color-mix(in srgb, var(--warning) 14%, transparent);
  color: var(--warning-strong);
}
.badge-muted {
  background: var(--control);
  color: var(--tertiary);
}

/* 切换 / 加载内联条 */
.hr-inline-state {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 4px;
  font-size: 13px;
  color: var(--secondary);
}
.hr-inline-state-muted {
  color: var(--tertiary);
}

.state-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}
.state-dot.muted {
  background: var(--tertiary);
}
.state-dot.err {
  background: var(--danger);
}

.spinner-sm {
  width: 14px;
  height: 14px;
  border: 2px solid var(--separator);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: hr-spin 0.8s linear infinite;
  flex-shrink: 0;
}

@keyframes hr-spin {
  to { transform: rotate(360deg); }
}

/* --- 状态行：端口 / 实例状态 / upstream --- */
.hr-status-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 20px;
  padding: 12px 0;
  border-top: 1px solid var(--separator);
  border-bottom: 1px solid var(--separator);
}

.status-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.status-cell-wide {
  grid-column: 1 / -1;
}

.cell-label {
  font-size: 11px;
  color: var(--tertiary);
  letter-spacing: 0.3px;
  text-transform: uppercase;
}

.cell-value {
  font-size: 13px;
  color: var(--label);
  display: inline-flex;
  align-items: center;
  gap: 6px;
  word-break: break-all;
}

.upstream-value {
  font-size: 12.5px;
  color: var(--secondary);
}

.dot-mini {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}
.dot-mini.on {
  background: var(--success);
}
.dot-mini.off {
  background: var(--tertiary);
}

.mono {
  font-family: var(--mono);
}

.accent {
  color: var(--accent);
}

/* --- 高级：target 输入 --- */
.hr-advanced {
  padding: 2px 0;
}

.adv-details {
  border: 1px solid var(--separator);
  border-radius: 10px;
  background: var(--control);
  overflow: hidden;
}

.adv-summary {
  list-style: none;
  cursor: pointer;
  padding: 10px 14px;
  font-size: 13px;
  font-weight: 500;
  color: var(--secondary);
  display: flex;
  align-items: center;
  gap: 8px;
  user-select: none;
}
.adv-summary::-webkit-details-marker {
  display: none;
}

.adv-chevron {
  display: inline-block;
  transition: transform 0.15s ease;
  color: var(--tertiary);
  font-size: 14px;
  line-height: 1;
}
.adv-details[open] .adv-chevron {
  transform: rotate(90deg);
}

.adv-body {
  padding: 12px 14px 14px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  background: var(--card);
  border-top: 1px solid var(--separator);
}

.adv-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--label);
}

.adv-input {
  width: 100%;
  padding: 8px 12px;
  font-size: 12.5px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  transition: border-color 0.15s;
}
.adv-input:focus {
  outline: none;
  border-color: var(--accent);
}
.adv-input:disabled {
  background: var(--control);
  color: var(--tertiary);
}

.adv-hint {
  font-size: 11.5px;
  color: var(--tertiary);
  margin: 0;
  line-height: 1.5;
}

.adv-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 4px;
}

/* --- by_client 统计 --- */
.hr-clients {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.clients-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.clients-title {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.clients-title h3 {
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
}

.clients-sub {
  font-size: 11.5px;
  color: var(--tertiary);
}

.clients-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 12px;
}

.client-card {
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 12px 14px;
  background: var(--card);
  display: flex;
  flex-direction: column;
  gap: 10px;
  transition: border-color 0.15s;
}

/* codex 全局条目高亮：让主上一眼区分 codex 流量 */
.client-card-codex {
  border-color: color-mix(in srgb, var(--accent) 40%, var(--separator));
  background: color-mix(in srgb, var(--accent) 4%, var(--card));
}

.client-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.client-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--label);
  word-break: break-all;
}

.tag {
  font-size: 10.5px;
  padding: 2px 7px;
  border-radius: 999px;
  font-weight: 500;
  letter-spacing: 0.2px;
  white-space: nowrap;
  flex-shrink: 0;
}
.tag-codex {
  background: color-mix(in srgb, var(--accent) 14%, transparent);
  color: var(--accent-strong);
}
.tag-claude {
  background: color-mix(in srgb, var(--success) 14%, transparent);
  color: var(--success-strong);
}
.tag-other {
  background: var(--control);
  color: var(--tertiary);
}

.client-metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px 12px;
  margin: 0;
}

.metric {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.metric dt {
  font-size: 11px;
  color: var(--tertiary);
  letter-spacing: 0.2px;
}

.metric dd {
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
  font-variant-numeric: tabular-nums;
  line-height: 1.2;
  word-break: break-all;
}

/* ledger 合计行 */
.clients-total {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 16px;
  padding: 10px 12px;
  border-radius: 10px;
  background: var(--control);
  font-size: 12px;
}

.total-label {
  font-size: 11px;
  color: var(--tertiary);
  text-transform: uppercase;
  letter-spacing: 0.3px;
  margin-right: 4px;
}

.total-item {
  display: inline-flex;
  align-items: baseline;
  gap: 4px;
}

.total-k {
  color: var(--secondary);
}

.total-unit {
  font-size: 11px;
  color: var(--tertiary);
}

/* --- 底部提示 --- */
.hr-notes {
  list-style: none;
  padding: 10px 0 0;
  margin: 0;
  border-top: 1px solid var(--separator);
}

.hr-notes li {
  font-size: 11.5px;
  color: var(--tertiary);
  padding-left: 14px;
  position: relative;
  margin-bottom: 4px;
  line-height: 1.5;
}

.hr-notes li::before {
  content: '•';
  position: absolute;
  left: 0;
  color: var(--tertiary);
}

/* --- 响应式 --- */
@media (min-width: 720px) {
  .hr-status-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
  .status-cell-wide {
    grid-column: auto;
  }
}

@media (max-width: 480px) {
  .client-metrics {
    grid-template-columns: 1fr;
  }
  .clients-grid {
    grid-template-columns: 1fr;
  }
}

/* reduced-motion：停用 spinner 旋转 */
@media (prefers-reduced-motion: reduce) {
  .spinner-sm {
    animation: none;
  }
  .adv-chevron {
    transition: none;
  }
}
</style>
