<!--
  卡⑥ 本地可见记录卡（PG-05 + NFR-17/AC-17）
  - ListRemoteSecurityEvents(50)：sanitized 投影，仅展示 类型 / 时间 / 结果 三要素。
  - GetRemoteSecurityHealth：有界健康问题；已关闭且未确认的 issue 提供 Acknowledge。
-->
<template>
  <section class="rc-card" aria-labelledby="rc-log-title">
    <header class="rc-card-head">
      <h2 id="rc-log-title" class="rc-card-title">本地可见记录</h2>
      <p class="rc-card-sub">最近安全事件（仅本机可见 · 脱敏投影）</p>
    </header>

    <!-- 有界健康问题 -->
    <div v-if="visibleIssues.length > 0" class="log-health" data-testid="health-issues">
      <div v-for="issue in visibleIssues" :key="issue.code" class="log-issue">
        <span class="log-issue-icon" aria-hidden="true">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path d="M8 1.8 14.8 13.8H1.2L8 1.8Z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
            <line x1="8" y1="6.4" x2="8" y2="9.6" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
            <circle cx="8" cy="11.7" r="0.9" fill="currentColor" />
          </svg>
        </span>
        <div class="log-issue-body">
          <span class="log-issue-code">{{ issueLabel(issue.code) }}</span>
          <span class="log-issue-meta">
            {{ issue.active ? '进行中' : '已关闭' }} · 发生 {{ issue.occurrences }} 次 · 最近 {{ formatEventTime(issue.lastObservedAt) }}
          </span>
        </div>
        <button
          v-if="!issue.active && !issue.acknowledged"
          type="button"
          class="rc-btn rc-btn-secondary log-issue-ack"
          :data-testid="`ack-${issue.code}`"
          :disabled="ackingCode !== null"
          @click="acknowledge(issue.code)"
        >
          {{ ackingCode === issue.code ? '确认中…' : '知道了' }}
        </button>
        <span v-else-if="issue.acknowledged" class="log-issue-acked">已确认</span>
      </div>
    </div>

    <!-- Major-06：服务关闭不隐藏本地记录（durable sink 不依赖 running） -->
    <!-- R2-N02：如实语义——停止期间的动作（如关态撤销）也会真实写入本机记录 -->
    <p v-if="serviceOff" class="log-off-hint" data-testid="events-service-off">
      远程服务未运行 · 以下为本机持久记录（服务停止前及停止期间写入），不依赖监听器在线。
    </p>

    <div v-if="loading" class="log-loading" aria-live="polite">
      <div class="log-skeleton" />
      <div class="log-skeleton" />
      <p class="log-loading-text">正在读取本地记录…</p>
    </div>

    <div v-else-if="loadError" class="rc-error" role="alert">
      <span>{{ loadError.message }}</span>
      <span class="rc-error-detail">{{ loadError.detail }}</span>
      <button type="button" class="rc-link" data-testid="events-retry" @click="$emit('refresh')">重试</button>
    </div>

    <div v-else-if="!events || events.length === 0" class="log-empty">
      <p class="log-empty-title">暂无安全事件记录</p>
      <p class="log-empty-desc">开启服务、配对、撤销设备等动作会写入本机记录后可在此查看。</p>
    </div>

    <ul v-else class="log-list" data-testid="events-list">
      <li v-for="ev in events" :key="ev.eventId" class="log-item">
        <span class="log-time mono">{{ formatEventTime(ev.occurredAt) }}</span>
        <span class="log-kind">{{ kindLabel(ev.kind) }}</span>
        <span class="log-outcome" :class="outcomeClass(ev.outcome)">{{ outcomeLabel(ev.outcome) }}</span>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import type { remote } from '../../../wailsjs/go/models';
import { acknowledgeRemoteSecurityHealth } from '../../api/remote';
import { classifyRemoteError, formatEventTime, type ClassifiedError } from './remoteShared';
import { useToast } from '../../composables/useToast';

interface Props {
  events: remote.SecurityEventRecord[] | null;
  health: remote.SecurityHealthSnapshot | null;
  loading: boolean;
  loadError: ClassifiedError | null;
  /** 服务关闭：记录仍可见（Major-06），仅提示展示的是停止前的记录 */
  serviceOff: boolean;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  (e: 'refresh'): void;
  (e: 'health-changed', snapshot: remote.SecurityHealthSnapshot): void;
}>();

const { showSuccess, showError } = useToast();

const ackingCode = ref<string | null>(null);

/** 有界展示：active 或未确认的 issue 才占用视觉优先级 */
const visibleIssues = computed(() => {
  const issues = props.health?.issues ?? [];
  return issues.filter((i) => i.active || !i.acknowledged);
});

const KIND_LABELS: Record<string, string> = {
  remote_service_started: '开启远程服务',
  remote_service_stopped: '停止远程服务',
  remote_listen_configuration_changed: '监听配置变更',
  legacy_token_rotated: '旧版 Token 轮换',
  pairing_window_opened: '配对窗口开启',
  pairing_window_canceled: '配对窗口取消',
  pairing_window_expired: '配对窗口过期',
  pairing_attempt_rejected: '配对尝试被拒绝',
  pairing_window_locked: '配对窗口锁定',
  device_paired: '设备配对成功',
  device_revoked: '设备被撤销',
  store_durability_degraded: '记录存储降级',
};

function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind ?? '未知事件';
}

function outcomeLabel(outcome: string | undefined): string {
  if (!outcome) return '—';
  if (outcome === 'accepted') return '成功';
  if (outcome === 'outcome_unknown') return '未知';
  return outcome;
}

function outcomeClass(outcome: string | undefined): string {
  if (outcome === 'accepted') return 'is-ok';
  if (!outcome) return 'is-none';
  return 'is-bad';
}

const ISSUE_LABELS: Record<string, string> = {
  durable_sink_degraded: '事件记录存储降级',
  durable_sink_closed: '事件记录存储已关闭',
  volatile_overflow: '内存事件缓冲溢出',
};

function issueLabel(code: string): string {
  return ISSUE_LABELS[code] ?? code;
}

async function acknowledge(code: string) {
  if (ackingCode.value) return;
  ackingCode.value = code;
  try {
    const snapshot = await acknowledgeRemoteSecurityHealth(code);
    showSuccess('已确认该健康问题');
    emit('health-changed', snapshot);
  } catch (err) {
    const c = classifyRemoteError(err);
    showError(c.message);
  } finally {
    ackingCode.value = null;
  }
}
</script>

<style scoped>
.log-health {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 14px;
}

.log-issue {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  background: var(--vt-surface);
  border: 1px solid var(--vt-warning);
  border-left: 4px solid var(--vt-warning);
  border-radius: 8px;
}

.log-issue-icon {
  color: var(--vt-warning);
  display: inline-flex;
  flex-shrink: 0;
}

.log-issue-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
  min-width: 0;
}

.log-issue-code {
  font-size: 13px;
  font-weight: 600;
  color: var(--vt-text);
}

.log-issue-meta {
  font-size: 12px;
  color: var(--vt-text-secondary);
  font-variant-numeric: tabular-nums;
}

.log-issue-ack {
  flex-shrink: 0;
}

.log-issue-acked {
  font-size: 13px;
  color: var(--vt-success);
  flex-shrink: 0;
}

.log-loading {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.log-off-hint {
  margin: 0 0 10px;
  font-size: 12px;
  color: var(--vt-text-secondary);
}

.log-skeleton {
  height: 32px;
  border-radius: 8px;
  background: var(--vt-surface-raised);
}

.log-loading-text {
  margin: 0;
  font-size: 13px;
  color: var(--vt-text-secondary);
}

.log-empty {
  text-align: center;
  padding: 20px 12px;
}

.log-empty-title {
  margin: 0 0 4px;
  font-size: 15px;
  font-weight: 600;
  font-family: var(--vt-font-display);
  color: var(--vt-text);
}

.log-empty-desc {
  margin: 0;
  font-size: 13px;
  color: var(--vt-text-secondary);
}

.log-list {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 320px;
  overflow-y: auto;
}

.log-item {
  display: flex;
  align-items: baseline;
  gap: 12px;
  padding: 8px 0;
  border-top: 1px solid var(--vt-border);
  font-size: 13px;
}

.log-item:first-child {
  border-top: none;
}

.log-time {
  color: var(--vt-text-secondary);
  flex-shrink: 0;
  font-size: 12px;
}

.log-kind {
  color: var(--vt-text);
  flex: 1;
  min-width: 0;
}

.log-outcome {
  flex-shrink: 0;
  font-size: 12px;
}

.log-outcome.is-ok {
  color: var(--vt-success);
}

.log-outcome.is-bad {
  color: var(--vt-danger);
}

.log-outcome.is-none {
  color: var(--vt-text-secondary);
}
</style>
