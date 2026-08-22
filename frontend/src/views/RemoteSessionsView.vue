<!--
  RemoteSessionsView（RC1-6 桌面端互联 · 远程会话页）
  交互稿 §2.2：远程模式下复用会话页骨架，列表 = 远端会话（五态徽标）。
  状态模型 §3：loading（不渲染旧数据冒充）/ empty + 主操作 /
  error(可重试) 保留最后成功数据 + 过期标记 / 提交中按钮防重复。
  RC2-5：行操作新增「打开终端」→ RemoteTerminalView；rc:revoked → RiskBanner
  fail-closed。新建会话入口仍属后续里程碑。
-->
<template>
  <section class="view-remote-sessions">
    <div class="rs-head">
      <PageHead
        title="远程会话"
        :description="`主机：${store.currentHostName}`"
      />
      <AppButton
        variant="ghost"
        size="small"
        :disabled="refreshing"
        @click="handleRefresh"
      >
        {{ refreshing ? '刷新中…' : '刷新' }}
      </AppButton>
    </div>

    <!-- error(可重试) + 已有数据：过期标记条 -->
    <StatusBanner
      v-if="store.remoteSessionsStale"
      type="warning"
      :message="`刷新失败，展示的是上次成功同步的数据（${lastSyncedText}）。${staleErrorCopy}`"
      action-text="重试"
      @action="handleRefresh"
    />

    <!-- revoked：fail-closed（交互稿 §3 revoked 行；rc:revoked 事件接入） -->
    <RiskBanner v-if="store.connectRevoked" title="本设备授权已被对方撤销">
      主机「{{ store.currentHostName }}」已撤销本设备的访问授权，连接与终端已全部断开。如需继续访问，请在对方 CodeBox 重新打开配对窗口并完成配对。
    </RiskBanner>

    <!-- loading：骨架（不渲染旧数据冒充） -->
    <LoadingState
      v-if="store.remoteSessionsState === 'loading'"
      message="加载远端会话中…"
    />

    <!-- error 且无数据 -->
    <ErrorState
      v-else-if="store.remoteSessionsState === 'error'"
      title="远端会话加载失败"
      :message="errorMessage"
      :on-retry="handleRefresh"
    />

    <!-- empty + 主操作 -->
    <EmptyState
      v-else-if="store.remoteSessions.length === 0"
      icon="▢"
      title="远端暂无会话"
      description="该主机上没有会话记录。远程新建会话入口将在后续里程碑接入。"
    >
      <template #action>
        <AppButton variant="primary" @click="handleRefresh">刷新列表</AppButton>
      </template>
    </EmptyState>

    <!-- 会话列表（只读展示 + 行操作） -->
    <div v-else class="rs-list">
      <div
        v-for="s in store.remoteSessions"
        :key="s.id"
        class="rs-row"
      >
        <div class="rs-main">
          <div class="rs-title-line">
            <span class="rs-title" :title="s.title">{{ s.title || s.id }}</span>
            <span class="rs-cli" :style="{ color: tagColor(s.cliType) }">{{ appTypeLabel(s.cliType) }}</span>
          </div>
          <div class="rs-sub">
            <span class="rs-id mono" :title="s.id">{{ s.id }}</span>
            <span class="rs-time">最近活动 {{ formatEventTime(s.lastActivityAt) }}</span>
          </div>
        </div>

        <div class="rs-badges">
          <span class="rs-state" :class="`tone-${remoteSessionStateTone(s.state)}`">
            {{ remoteSessionStateLabel(s.state) }}
          </span>
          <!-- 控制权四态徽标（ControlBadge 统一表达；none 无标记） -->
          <ControlBadge :state="s.control?.state ?? ''" :device-name="s.control?.deviceName ?? ''" />
        </div>

        <div class="rs-actions">
          <!-- RC3-3：控制权获取/释放（store.controlActionOf 单一口径；
               409 control.busy 走 toast 提示「他人持有」，不弹窗） -->
          <AppButton
            v-if="store.controlActionOf(s.id)"
            variant="ghost"
            size="small"
            :disabled="busyId === s.id"
            @click="handleControl(s)"
          >
            {{ controlButtonText(s) }}
          </AppButton>
          <AppButton
            variant="primary"
            size="small"
            :disabled="s.state === 'removed'"
            @click="openTerminal(s)"
          >
            打开终端
          </AppButton>
          <AppButton
            variant="ghost"
            size="small"
            :disabled="busyId === s.id || s.state === 'removed'"
            @click="handleRestart(s)"
          >
            {{ busyId === s.id && busyAction === 'restart' ? '重启中…' : '重启' }}
          </AppButton>
          <AppButton
            variant="ghost"
            size="small"
            :disabled="busyId === s.id || s.state !== 'running'"
            @click="handleStop(s)"
          >
            {{ busyId === s.id && busyAction === 'stop' ? '停止中…' : '停止' }}
          </AppButton>
          <AppButton
            variant="danger"
            size="small"
            :disabled="busyId === s.id"
            @click="askDelete(s)"
          >
            删除
          </AppButton>
        </div>
      </div>
    </div>

    <ConfirmDialog
      :open="deleteConfirmOpen"
      title="删除远端会话"
      :message="`确定删除会话「${pendingDelete?.title || pendingDelete?.id || ''}」吗？此操作不可逆，会话及其历史将从主机「${store.currentHostName}」上移除。`"
      danger
      confirm-text="删除"
      @update:open="deleteConfirmOpen = $event"
      @confirm="confirmDelete"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import PageHead from '../components/ui/PageHead.vue';
import AppButton from '../components/ui/AppButton.vue';
import LoadingState from '../components/ui/LoadingState.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import ErrorState from '../components/ui/ErrorState.vue';
import StatusBanner from '../components/ui/StatusBanner.vue';
import ConfirmDialog from '../components/ui/ConfirmDialog.vue';
import RiskBanner from '../components/remote/RiskBanner.vue';
import ControlBadge from '../components/remote/ControlBadge.vue';
import { useRemoteClientStore } from '../stores/remoteClient';
import { useToast } from '../composables/useToast';
import { appTypeLabel, tagColor } from '../utils/format';
import { formatEventTime } from '../components/remote/remoteShared';
import {
  copyForRemoteError,
  remoteSessionStateLabel,
  remoteSessionStateTone,
} from '../components/remote/remoteClientShared';
import type { RemoteSessionSummary } from '../api/remoteClient';

const store = useRemoteClientStore();
const router = useRouter();
const { showSuccess, showError } = useToast();

const refreshing = ref(false);
const busyId = ref('');
const busyAction = ref<'stop' | 'restart' | 'delete' | 'acquire' | 'release' | ''>('');

const deleteConfirmOpen = ref(false);
const pendingDelete = ref<RemoteSessionSummary | null>(null);

const errorMessage = computed(() =>
  store.remoteSessionsError
    ? copyForRemoteError(store.remoteSessionsError)
    : '无法获取远端会话列表，请检查连接后重试。',
);

const staleErrorCopy = computed(() => copyForRemoteError(store.remoteSessionsError));

const lastSyncedText = computed(() => {
  if (!store.lastSyncedAt) return '时间未知';
  return formatEventTime(new Date(store.lastSyncedAt).toISOString());
});

async function handleRefresh() {
  if (refreshing.value) return;
  refreshing.value = true;
  try {
    await store.refreshRemoteSessions();
  } finally {
    refreshing.value = false;
  }
}

function controlButtonText(s: RemoteSessionSummary): string {
  if (busyId.value === s.id) {
    if (busyAction.value === 'acquire') return '获取中…';
    if (busyAction.value === 'release') return '释放中…';
  }
  return store.controlActionOf(s.id) === 'release' ? '释放控制权' : '获取控制权';
}

/** RC3-3：获取/释放控制权。409 control.busy → toast「他人持有」文案，不弹窗。 */
async function handleControl(s: RemoteSessionSummary) {
  const action = store.controlActionOf(s.id);
  if (!action || busyId.value) return;
  busyId.value = s.id;
  busyAction.value = action;
  try {
    if (action === 'acquire') {
      await store.acquireControl(s.id);
      showSuccess('已获取控制权');
    } else {
      await store.releaseControl(s.id);
      showSuccess('已释放控制权');
    }
  } catch (err) {
    // copyForRemoteError 对 control.busy 给出「控制权正被他人持有」文案。
    showError(copyForRemoteError(err));
  } finally {
    busyId.value = '';
    busyAction.value = '';
  }
}

/** RC2-5：打开远程终端（设定终端页目标并跳转；attach 由视图挂载时发起）。 */
function openTerminal(s: RemoteSessionSummary) {
  store.openRemoteTerminal(s.id);
  void router.push('/terminal');
}

async function runRowAction(s: RemoteSessionSummary, action: 'stop' | 'restart', fn: () => Promise<void>) {
  if (busyId.value) return;
  busyId.value = s.id;
  busyAction.value = action;
  try {
    await fn();
    showSuccess(action === 'stop' ? '会话已停止' : '会话已重启');
  } catch (err) {
    showError(copyForRemoteError(err));
  } finally {
    busyId.value = '';
    busyAction.value = '';
  }
}

function handleStop(s: RemoteSessionSummary) {
  void runRowAction(s, 'stop', () => store.stopRemoteSession(s.id));
}

function handleRestart(s: RemoteSessionSummary) {
  void runRowAction(s, 'restart', () => store.restartRemoteSession(s.id));
}

function askDelete(s: RemoteSessionSummary) {
  pendingDelete.value = s;
  deleteConfirmOpen.value = true;
}

async function confirmDelete() {
  const target = pendingDelete.value;
  deleteConfirmOpen.value = false;
  if (!target) return;
  busyId.value = target.id;
  busyAction.value = 'delete';
  try {
    await store.deleteRemoteSession(target.id);
    showSuccess('会话已删除');
  } catch (err) {
    showError(copyForRemoteError(err));
  } finally {
    busyId.value = '';
    busyAction.value = '';
    pendingDelete.value = null;
  }
}

onMounted(() => {
  void handleRefresh();
  store.startSessionPolling();
});

onUnmounted(() => {
  store.stopSessionPolling();
});
</script>

<style scoped>
.view-remote-sessions {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.rs-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.rs-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.rs-row {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 12px 16px;
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 10px;
}

.rs-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.rs-title-line {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.rs-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.rs-cli {
  font-size: 11px;
  font-weight: 600;
  flex-shrink: 0;
}

.rs-sub {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 11px;
  color: var(--tertiary);
}

.rs-id.mono {
  font-family: var(--mono);
}

.rs-badges {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.rs-state {
  font-size: 11px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 999px;
  white-space: nowrap;
}

.rs-state.tone-success {
  color: var(--success-strong);
  background: rgba(52, 199, 89, 0.14);
}

.rs-state.tone-muted {
  color: var(--secondary);
  background: var(--control);
}

.rs-state.tone-warning {
  color: var(--warning-strong);
  background: rgba(255, 149, 0, 0.14);
}

.rs-state.tone-danger {
  color: var(--danger-strong);
  background: rgba(255, 59, 48, 0.12);
}

/* 控制权四态徽标样式收口于 ControlBadge 组件（视觉风格 §4：同一语义同一表达）。 */

.rs-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}
</style>
