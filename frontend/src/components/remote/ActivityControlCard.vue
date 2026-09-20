<!--
  卡⑤ 活动控制 / 控制权管理卡（PG-05）· M3 接入真实控制（替换占位）。
  数据：GetSessionControlHolds（device-only 投影，无人/桌面持有不列）。
  收回：ReleaseSessionControl 经 PG-06 确认（后果文案冻结：设备失去控制权后
  可重新接管）；桌面根权威语义——组合链 takeover→released，终态无人持有。
  服务未运行时沿用可信设备卡的提示模式（设备无法持有控制权，卡片保持诚实）。
-->
<template>
  <section class="rc-card" aria-labelledby="rc-act-title">
    <header class="rc-card-head">
      <h2 id="rc-act-title" class="rc-card-title">控制权管理</h2>
      <p class="rc-card-sub">会话 × 持有设备 × 主动收回 · 桌面根权威</p>
    </header>

    <p v-if="serviceOff" class="act-off-hint" data-testid="holds-service-off">
      远程服务未运行 · 远程设备当前无法持有控制权。
    </p>

    <div v-if="loading" class="act-loading" aria-live="polite">
      <div class="act-skeleton" />
      <p class="act-loading-text">正在读取控制权持有…</p>
    </div>

    <div v-else-if="loadError" class="rc-error" role="alert">
      <span>{{ loadError.message }}</span>
      <span class="rc-error-detail">{{ loadError.detail }}</span>
      <button type="button" class="rc-link" data-testid="holds-retry" @click="$emit('refresh')">重试</button>
    </div>

    <div v-else-if="!holds || holds.length === 0" class="act-empty" data-testid="holds-empty">
      <span class="act-empty-icon" aria-hidden="true">
        <svg width="40" height="40" viewBox="0 0 40 40" fill="none">
          <rect x="5" y="8" width="30" height="22" rx="3" stroke="currentColor" stroke-width="1.6" />
          <path d="M12 16l5 4-5 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
          <line x1="20" y1="24" x2="28" y2="24" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
        </svg>
      </span>
      <p class="act-empty-title">暂无设备持有控制权</p>
      <p class="act-empty-desc">{{ emptyText }}</p>
    </div>

    <div v-else class="act-list" data-testid="holds-list">
      <div
        v-for="h in holds"
        :key="h.sessionID"
        class="act-row"
        :data-testid="`hold-row-${h.sessionID}`"
      >
        <div class="act-row-main">
          <span class="act-sid" :title="h.sessionID">{{ shortSessionID(h.sessionID) }}</span>
          <span class="act-device">{{ holderDisplayName(h) }}</span>
        </div>
        <span class="act-badge" :class="holdBadge(h).tone" :data-testid="`hold-badge-${h.sessionID}`">
          {{ holdBadge(h).text }}
        </span>
        <button
          type="button"
          class="act-release"
          :data-testid="`hold-release-${h.sessionID}`"
          :disabled="releasing"
          @click="askRelease(h)"
        >
          收回控制权
        </button>
      </div>
    </div>

    <ConfirmDialog
      :open="releaseTarget !== null"
      title="收回控制权"
      :consequence="releaseConsequenceText"
      irreversible-note="此操作即时生效：该设备收到桌面接管通知后即可重新接管会话。"
      confirm-text="收回控制权"
      :busy="releasing"
      busy-text="收回中…"
      @confirm="confirmRelease"
      @cancel="releaseTarget = null"
    />
  </section>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import {
  EMPTY_HOLDS_TEXT,
  holdBadge,
  holderDisplayName,
  performRelease,
  releaseConsequence,
  shortSessionID,
  type ControlHold,
} from './controlHoldsModel';
import type { ClassifiedError } from './remoteShared';
import ConfirmDialog from './ConfirmDialog.vue';
import { useToast } from '../../composables/useToast';

interface Props {
  /** GetSessionControlHolds 投影（device-only；null=尚未加载） */
  holds: ControlHold[] | null;
  loading: boolean;
  loadError: ClassifiedError | null;
  /** 服务关闭：设备无法持有控制权（提示模式与可信设备卡一致） */
  serviceOff: boolean;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  (e: 'refresh'): void;
  /** 收回成功后由父级刷新列表（含事件/健康卡联动） */
  (e: 'changed'): void;
}>();

const { showSuccess, showError } = useToast();

const releaseTarget = ref<ControlHold | null>(null);
const releasing = ref(false);

const emptyText = EMPTY_HOLDS_TEXT;

const releaseConsequenceText = computed(() =>
  releaseTarget.value ? releaseConsequence(releaseTarget.value) : '',
);

function askRelease(h: ControlHold) {
  releaseTarget.value = h;
}

async function confirmRelease() {
  const target = releaseTarget.value;
  if (!target || releasing.value) return;
  releasing.value = true;
  const outcome = await performRelease(target.sessionID);
  releasing.value = false;
  if (outcome.ok) {
    releaseTarget.value = null;
    showSuccess(`已收回会话 ${shortSessionID(target.sessionID)} 的控制权`);
    emit('changed');
  } else {
    // 保留确认对话开启：用户可重试或取消；错误经 toast 透出。
    showError(outcome.errorMessage ?? '收回控制权失败');
  }
}
</script>

<style scoped>
.act-loading {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.act-skeleton {
  height: 44px;
  border-radius: 8px;
  background: var(--vt-surface-raised);
}

.act-loading-text {
  margin: 0;
  font-size: 13px;
  color: var(--vt-text-secondary);
}

.act-off-hint {
  margin: 0 0 10px;
  font-size: 12px;
  color: var(--vt-text-secondary);
}

.act-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.act-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--vt-border);
  border-radius: 8px;
  background: var(--vt-surface-raised);
}

.act-row-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.act-sid {
  font-family: var(--vt-font-mono);
  font-size: 12px;
  color: var(--vt-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.act-device {
  font-size: 13px;
  font-weight: 500;
  color: var(--vt-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.act-badge {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 12px;
  white-space: nowrap;
}

.act-badge.ok {
  border: 1px solid var(--vt-success);
  color: var(--vt-success);
}

.act-badge.warn {
  border: 1px solid var(--vt-warning);
  color: var(--vt-warning);
}

.act-release {
  flex-shrink: 0;
  padding: 6px 12px;
  border: 1px solid var(--vt-border-strong);
  border-radius: 6px;
  background: transparent;
  color: var(--vt-text);
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s ease;
}

.act-release:hover:not(:disabled) {
  background: var(--vt-surface);
}

.act-release:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.act-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: 24px 12px;
  gap: 6px;
}

.act-empty-icon {
  color: var(--vt-text-secondary);
  display: inline-flex;
}

.act-empty-title {
  margin: 4px 0 0;
  font-size: 15px;
  font-weight: 600;
  font-family: var(--vt-font-display);
  color: var(--vt-text);
}

.act-empty-desc {
  margin: 0;
  max-width: 420px;
  font-size: 13px;
  line-height: 1.55;
  color: var(--vt-text-secondary);
}
</style>
