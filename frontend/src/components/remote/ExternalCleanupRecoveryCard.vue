<!--
  卡⓪ 外部进程清理恢复卡（M2-INT R12 · R11-002 产品恢复闭环）
  隐私语义（对齐 backend ExternalCleanupRecoveryStatus / R11-002 privacy status）：
  - 只展示 计数 / 类型（kind）/ 原因 / 状态；不渲染 PID、路径、argv、provider、终端输出。
  - sessionId 仅用于 confirm 调用标识，不上屏。
  交互闭环：
  - running 态：指导关闭旧外部终端 + 「重新核验」（status 调用即触发后端活性复检）。
  - awaiting_confirmation 态：PG-06 显式确认对话（后果/不可撤销/动词按钮/焦点默认取消/
    Esc 取消/提交中防连点）→ ConfirmExternalCleanupRecovery(sessionID, true)。
  - live 拒绝（still running）/ 持久化失败 / 项不存在：分类展示拒绝原因，状态不伪装成功。
  - 成功：状态清除 + toast + Headroom 锁定解除结果如实呈现（fenceReleased）。
  审计可见：每次确认结果写入本机 localStorage 记录（本卡「本机恢复确认记录」）；
  后端 typed audit（App 内存 registry + host 日志）当前无 Wails 读取口、不在统一
  安全事件流内，接入记录卡⑥为后续接线（如实标注，不用本地记录冒充后端审计）。
-->
<template>
  <section class="rc-card rec-card" aria-labelledby="rc-recovery-title">
    <header class="rc-card-head">
      <h2 id="rc-recovery-title" class="rc-card-title">外部进程清理恢复</h2>
      <p class="rc-card-sub">旧外部终端的安全核验与恢复 · 仅显示计数与类型，不含路径或进程细节</p>
    </header>

    <div v-if="loading" class="rec-loading" aria-live="polite">
      <div class="rec-skeleton" />
      <p class="rec-loading-text">正在核验外部进程清理状态…</p>
    </div>

    <div v-else-if="loadError" class="rc-error" role="alert">
      <span>{{ loadError.message }}</span>
      <span class="rc-error-detail">{{ loadError.detail }}</span>
      <button type="button" class="rc-link" data-testid="recovery-retry" @click="() => refresh()">重试</button>
    </div>

    <template v-else-if="status">
      <!-- 健康态：持久可发现，无待恢复项时如实呈现正常 -->
      <p
        v-if="status.items.length === 0 && !status.blocked"
        class="rec-healthy"
        data-testid="recovery-healthy"
      >
        <span class="rec-healthy-icon" aria-hidden="true">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <circle cx="8" cy="8" r="6.4" stroke="currentColor" stroke-width="1.5" />
            <path d="M5.4 8.2 7.3 10.1 10.7 6.2" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </span>
        未发现待恢复的外部进程清理项，Headroom 未被该安全锁定阻塞。
      </p>

      <template v-else>
        <!-- 锁定提示：全局 Headroom/启动 fence 的用户可懂说明 -->
        <div class="rec-blocked" data-testid="recovery-blocked" role="status">
          <span class="rec-blocked-icon" aria-hidden="true">
            <svg width="18" height="18" viewBox="0 0 18 18" fill="none">
              <path d="M9 2 16.2 15H1.8L9 2Z" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" />
              <line x1="9" y1="7" x2="9" y2="11" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
              <circle cx="9" cy="13.2" r="1" fill="currentColor" />
            </svg>
          </span>
          <div class="rec-blocked-body">
            <p class="rec-blocked-title">
              检测到 {{ status.items.length }} 项未完成的外部进程清理
            </p>
            <p class="rec-blocked-desc">
              在完成恢复前，Headroom 与新会话启动保持安全锁定。请关闭对应的旧外部终端，
              然后逐项重新核验并确认恢复。
            </p>
          </div>
        </div>

        <ul class="rec-list" data-testid="recovery-list">
          <li v-for="item in status.items" :key="item.sessionId" class="rec-item">
            <div class="rec-item-main">
              <span class="rec-item-kind">{{ kindLabel(item.kind) }}</span>
              <span class="rec-item-reason">{{ reasonLabel(item.reason) }}</span>
              <span
                class="rec-item-state"
                :class="item.canConfirm ? 'is-awaiting' : 'is-running'"
                :data-testid="`recovery-state-${item.canConfirm ? 'awaiting' : 'running'}`"
              >
                {{ item.canConfirm ? '待确认：旧进程已退出' : '旧终端仍在运行' }}
              </span>
            </div>
            <div class="rec-item-actions">
              <template v-if="!item.canConfirm">
                <p class="rec-item-guide">
                  请先在系统中关闭对应的旧外部终端，然后重新核验。
                </p>
                <button
                  type="button"
                  class="rc-btn rc-btn-secondary"
                  data-testid="recovery-recheck"
                  :disabled="rechecking"
                  @click="recheck"
                >
                  {{ rechecking ? '核验中…' : '重新核验' }}
                </button>
              </template>
              <button
                v-else
                type="button"
                class="rc-btn rc-btn-danger-outline"
                :data-testid="`recovery-confirm-open`"
                :disabled="confirming"
                @click="openConfirm(item)"
              >
                完成恢复确认
              </button>
            </div>
          </li>
        </ul>
      </template>

      <!-- 本机恢复确认记录（desktop 侧本地记录，非后端审计流） -->
      <div v-if="localLog.length > 0" class="rec-log" data-testid="recovery-local-log">
        <h3 class="rec-log-title">本机恢复确认记录</h3>
        <ul class="rec-log-list">
          <li v-for="(entry, idx) in localLog.slice(0, 5)" :key="idx" class="rec-log-item">
            <span class="rec-log-time mono">{{ formatEventTime(entry.occurredAt) }}</span>
            <span class="rec-log-kind">{{ kindLabel(entry.kind) }}</span>
            <span class="rec-log-outcome" :class="entry.outcome === 'completed' ? 'is-ok' : 'is-bad'">
              {{ outcomeLabel(entry) }}
            </span>
          </li>
        </ul>
        <p class="rec-log-note">
          以上为 desktop 侧本地记录；后端恢复审计事件接入上方「本地可见记录」卡为后续接线。
        </p>
      </div>
    </template>

    <!-- PG-06 显式确认对话（复用远程控制中心模板：后果/不可撤销/动词按钮/焦点默认取消/Esc） -->
    <ConfirmDialog
      :open="confirmOpen"
      title="完成外部进程清理恢复"
      consequence="确认后系统会把该项旧外部进程登记为已终止，并重新核算 Headroom 锁定；确认前系统会再次核验进程状态，仍在运行将被拒绝。"
      irreversible-note="该确认不可撤销：请仅在您已亲自关闭对应的旧外部终端后执行。系统不提供强制清除入口。"
      confirm-text="确认已完成清理"
      :busy="confirming"
      busy-text="正在核验并登记…"
      @confirm="doConfirm"
      @cancel="closeConfirm"
    />
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import type { remote } from '../../../wailsjs/go/models';
import {
  getExternalCleanupRecoveryStatus,
  confirmExternalCleanupRecovery,
} from '../../api/remote';
import { classifyRemoteError, formatEventTime, type ClassifiedError } from './remoteShared';
import { useToast } from '../../composables/useToast';
import ConfirmDialog from './ConfirmDialog.vue';

const LOCAL_LOG_KEY = 'amagi.remote.externalCleanupRecoveryLog';
const LOCAL_LOG_MAX = 20;
/** running 态活性轮询间隔：仅当存在 running 项时启用（status 调用即后端活性复检） */
const RUNNING_POLL_MS = 4000;

type LocalOutcome =
  | 'completed'
  | 'still_running'
  | 'persistence_failed'
  | 'not_found'
  | 'recheck_unavailable';

interface LocalLogEntry {
  occurredAt: string;
  kind: number;
  reason: string;
  outcome: LocalOutcome;
  fenceReleased: boolean;
}

const emit = defineEmits<{
  /** 恢复状态发生变化（成功/拒绝后），供父级刷新关联卡 */
  (e: 'changed'): void;
}>();

const { showSuccess, showError, showWarn } = useToast();

const status = ref<remote.ExternalCleanupRecoveryStatus | null>(null);
const loading = ref(true);
const loadError = ref<ClassifiedError | null>(null);
const rechecking = ref(false);

const confirmOpen = ref(false);
const confirming = ref(false);
const confirmTarget = ref<remote.ExternalCleanupRecoveryItem | null>(null);

const localLog = ref<LocalLogEntry[]>(readLocalLog());

let pollTimer: ReturnType<typeof setInterval> | null = null;

function readLocalLog(): LocalLogEntry[] {
  try {
    const raw = localStorage.getItem(LOCAL_LOG_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function appendLocalLog(entry: LocalLogEntry) {
  const next = [entry, ...localLog.value].slice(0, LOCAL_LOG_MAX);
  localLog.value = next;
  try {
    localStorage.setItem(LOCAL_LOG_KEY, JSON.stringify(next));
  } catch {
    // localStorage 不可用不影响主流程；记录丢失如实可接受（不伪造持久成功）
  }
}

const KIND_LABELS: Record<number, string> = {
  1: 'Claude 代理进程',
  2: 'Claude Headroom 进程',
  3: 'Codex Headroom 进程',
};

function kindLabel(kind: number): string {
  return KIND_LABELS[kind] ?? '外部进程';
}

const REASON_LABELS: Record<string, string> = {
  legacy_process_identity: '旧版本遗留进程，身份无法完全核验',
  identity_inspection_uncertain: '进程身份核验不确定',
};

function reasonLabel(reason: string): string {
  return REASON_LABELS[reason] ?? '身份不确定';
}

function outcomeLabel(entry: LocalLogEntry): string {
  switch (entry.outcome) {
    case 'completed':
      return entry.fenceReleased ? '已完成恢复 · Headroom 锁定已解除' : '已完成恢复';
    case 'still_running':
      return '被拒绝：旧进程仍在运行';
    case 'persistence_failed':
      return '失败：本地登记写入未完成';
    case 'not_found':
      return '恢复项已不存在';
    case 'recheck_unavailable':
      return '失败：无法核验进程状态';
    default:
      return entry.outcome;
  }
}

function syncPolling() {
  const hasRunning = (status.value?.items ?? []).some((i) => !i.canConfirm);
  if (hasRunning && !pollTimer) {
    pollTimer = setInterval(() => void refresh({ silent: true }), RUNNING_POLL_MS);
  } else if (!hasRunning && pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

async function refresh(opts: { silent?: boolean } = {}) {
  if (!opts.silent) loading.value = status.value === null;
  try {
    status.value = await getExternalCleanupRecoveryStatus();
    loadError.value = null;
  } catch (err) {
    loadError.value = classifyRemoteError(err);
    if (opts.silent) return; // 轮询失败不覆盖已有状态
  } finally {
    loading.value = false;
  }
  syncPolling();
}

/** running 态「重新核验」：status 调用即触发后端活性复检（IsRunning recheck） */
async function recheck() {
  if (rechecking.value) return;
  rechecking.value = true;
  try {
    status.value = await getExternalCleanupRecoveryStatus();
    loadError.value = null;
    const stillRunning = (status.value?.items ?? []).filter((i) => !i.canConfirm).length;
    if (stillRunning > 0) {
      showWarn('旧外部终端仍在运行：请先关闭对应终端后再核验。');
    }
  } catch (err) {
    loadError.value = classifyRemoteError(err);
  } finally {
    rechecking.value = false;
  }
  syncPolling();
}

function openConfirm(item: remote.ExternalCleanupRecoveryItem) {
  confirmTarget.value = item;
  confirmOpen.value = true;
}

function closeConfirm() {
  if (confirming.value) return; // 提交中禁止误触关闭
  confirmOpen.value = false;
  confirmTarget.value = null;
}

/** 把后端拒绝/失败分类为可执行文案 + 本地记录 outcome（对齐 typed audit 语义） */
function classifyConfirmFailure(raw: string): { message: string; outcome: LocalOutcome } {
  const t = raw.toLowerCase();
  if (t.includes('still running')) {
    return {
      message: '恢复被拒绝：核验发现旧外部进程仍在运行。请先关闭对应的旧外部终端，再重新核验并确认。',
      outcome: 'still_running',
    };
  }
  if (t.includes('recheck unavailable')) {
    return {
      message: '无法核验进程状态：恢复未完成，Headroom 保持锁定。请稍后重试或重启应用。',
      outcome: 'recheck_unavailable',
    };
  }
  if (t.includes('not found')) {
    return {
      message: '该恢复项已不存在（可能已被处理）：已为您刷新状态。',
      outcome: 'not_found',
    };
  }
  if (t.includes('persistence') || t.includes('store')) {
    return {
      message: '本地登记写入失败：恢复未完成，Headroom 保持锁定。请检查配置目录与磁盘权限后重试。',
      outcome: 'persistence_failed',
    };
  }
  return {
    message: '恢复未完成（原因见下）：Headroom 保持锁定，请按提示修正后重试。',
    outcome: 'persistence_failed',
  };
}

async function doConfirm() {
  const target = confirmTarget.value;
  if (!target || confirming.value) return;
  confirming.value = true;
  try {
    // PG-06 已获显式确认 → confirmed=true（无 force-clear 路径）
    const result = await confirmExternalCleanupRecovery(target.sessionId, true);
    appendLocalLog({
      occurredAt: new Date().toISOString(),
      kind: target.kind,
      reason: target.reason,
      outcome: 'completed',
      fenceReleased: result.fenceReleased,
    });
    if (result.fenceReleased) {
      showSuccess('恢复完成：Headroom 安全锁定已解除。');
    } else {
      showSuccess('该项恢复完成；仍有其他待恢复项，Headroom 保持锁定。');
    }
    confirmOpen.value = false;
    confirmTarget.value = null;
    await refresh({ silent: true });
    emit('changed');
  } catch (err) {
    const raw = err instanceof Error ? err.message : String(err);
    const failure = classifyConfirmFailure(raw);
    appendLocalLog({
      occurredAt: new Date().toISOString(),
      kind: target.kind,
      reason: target.reason,
      outcome: failure.outcome,
      fenceReleased: false,
    });
    showError(failure.message);
    confirmOpen.value = false;
    confirmTarget.value = null;
    // 拒绝/失败后如实刷新状态（still_running 会回到 running 态）
    await refresh({ silent: true });
    emit('changed');
  } finally {
    confirming.value = false;
  }
}

onMounted(() => {
  void refresh();
});

onUnmounted(() => {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
});
</script>

<style scoped>
.rec-loading {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.rec-skeleton {
  height: 32px;
  border-radius: 8px;
  background: var(--vt-surface-raised);
}

.rec-loading-text {
  margin: 0;
  font-size: 13px;
  color: var(--vt-text-secondary);
}

.rec-healthy {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  font-size: 13px;
  color: var(--vt-text);
}

.rec-healthy-icon {
  color: var(--vt-success);
  display: inline-flex;
  flex-shrink: 0;
}

.rec-blocked {
  display: flex;
  gap: 10px;
  padding: 12px 14px;
  margin-bottom: 12px;
  background: var(--vt-surface);
  border: 1px solid var(--vt-warning);
  border-left: 4px solid var(--vt-warning);
  border-radius: 8px;
}

.rec-blocked-icon {
  color: var(--vt-warning);
  display: inline-flex;
  flex-shrink: 0;
  margin-top: 1px;
}

.rec-blocked-body {
  min-width: 0;
}

.rec-blocked-title {
  margin: 0 0 2px;
  font-size: 14px;
  font-weight: 600;
  color: var(--vt-text);
}

.rec-blocked-desc {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--vt-text-secondary);
}

.rec-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}

.rec-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px 14px;
  padding: 12px 0;
  border-top: 1px solid var(--vt-border);
}

.rec-item-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.rec-item-kind {
  font-size: 14px;
  font-weight: 600;
  color: var(--vt-text);
}

.rec-item-reason {
  font-size: 12px;
  color: var(--vt-text-secondary);
}

.rec-item-state {
  font-size: 12px;
  font-weight: 600;
  margin-top: 2px;
}

.rec-item-state.is-running {
  color: var(--vt-warning);
}

.rec-item-state.is-awaiting {
  color: var(--vt-control);
}

.rec-item-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.rec-item-guide {
  margin: 0;
  font-size: 12px;
  color: var(--vt-text-secondary);
  max-width: 300px;
  line-height: 1.5;
}

.rec-log {
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--vt-border);
}

.rec-log-title {
  margin: 0 0 8px;
  font-size: 13px;
  font-weight: 600;
  color: var(--vt-text);
}

.rec-log-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.rec-log-item {
  display: flex;
  align-items: baseline;
  gap: 12px;
  padding: 6px 0;
  font-size: 13px;
}

.rec-log-time {
  color: var(--vt-text-secondary);
  flex-shrink: 0;
  font-size: 12px;
}

.rec-log-kind {
  color: var(--vt-text);
  flex-shrink: 0;
}

.rec-log-outcome {
  font-size: 12px;
}

.rec-log-outcome.is-ok {
  color: var(--vt-success);
}

.rec-log-outcome.is-bad {
  color: var(--vt-danger);
}

.rec-log-note {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--vt-text-secondary);
}

@media (prefers-reduced-motion: reduce) {
  .rec-card * {
    transition: none;
  }
}
</style>
