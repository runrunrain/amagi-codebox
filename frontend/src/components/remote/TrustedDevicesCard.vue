<!--
  卡④ 可信设备卡（PG-05）：DeviceRow 列表 + 空态 + PG-06 撤销确认。
  RevokeRemoteDevice 必须经 ConfirmDialog 显式确认（confirm=true）；结果写本地记录（后端负责），
  前端刷新设备与事件卡。撤销即时生效回执含终止连接数。
-->
<template>
  <section class="rc-card" aria-labelledby="rc-dev-title">
    <header class="rc-card-head">
      <h2 id="rc-dev-title" class="rc-card-title">可信设备</h2>
      <p class="rc-card-sub">撤销即时生效 · 需确认 · 写入本地记录</p>
    </header>

    <!-- Major-06：服务关闭不阻断设备治理（根权威语义），仅提示网络动作受限 -->
    <p v-if="serviceOff" class="dev-off-hint" data-testid="devices-service-off">
      远程服务未运行 · 已配对设备仍可查看与撤销；重新连接需开启服务。
    </p>

    <div v-if="loading" class="dev-loading" aria-live="polite">
      <div class="dev-skeleton" />
      <div class="dev-skeleton" />
      <p class="dev-loading-text">正在读取设备列表…</p>
    </div>

    <div v-else-if="loadError" class="rc-error" role="alert">
      <span>{{ loadError.message }}</span>
      <span class="rc-error-detail">{{ loadError.detail }}</span>
      <button type="button" class="rc-link" data-testid="devices-retry" @click="$emit('refresh')">重试</button>
    </div>

    <div v-else-if="activeDevices.length === 0" class="dev-empty">
      <span class="dev-empty-icon" aria-hidden="true">
        <svg width="40" height="40" viewBox="0 0 40 40" fill="none">
          <rect x="8" y="5" width="24" height="30" rx="4" stroke="currentColor" stroke-width="1.6" />
          <line x1="15" y1="30" x2="25" y2="30" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
          <line x1="14" y1="12" x2="26" y2="12" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
          <line x1="14" y1="17" x2="23" y2="17" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
        </svg>
      </span>
      <p class="dev-empty-title">暂无已配对设备</p>
      <p class="dev-empty-desc">通过上方配对卡发起短时配对窗口，扫码或输入配对码添加新设备。</p>
    </div>

    <div v-else class="dev-list">
      <DeviceRow
        v-for="d in activeDevices"
        :key="d.id"
        :device="d"
        :busy="revoking"
        @revoke="askRevoke"
      />
    </div>

    <ConfirmDialog
      :open="revokeTarget !== null"
      title="撤销设备访问"
      :consequence="revokeConsequence"
      irreversible-note="此操作不可撤销：该设备需重新完成配对才能再次访问。"
      confirm-text="撤销设备"
      :busy="revoking"
      busy-text="撤销中…"
      @confirm="confirmRevoke"
      @cancel="revokeTarget = null"
    />

    <div v-if="revokeError" class="rc-error" role="alert">
      <span>{{ revokeError.message }}</span>
      <span class="rc-error-detail">{{ revokeError.detail }}</span>
      <button type="button" class="rc-link" @click="retryRevoke">重试</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import type { remote } from '../../../wailsjs/go/models';
import { revokeRemoteDevice } from '../../api/remote';
import { classifyRemoteError, type ClassifiedError } from './remoteShared';
import { useToast } from '../../composables/useToast';
import DeviceRow from './DeviceRow.vue';
import ConfirmDialog from './ConfirmDialog.vue';

interface Props {
  devices: remote.DeviceInfo[] | null;
  loading: boolean;
  loadError: ClassifiedError | null;
  /** 服务关闭：设备仍可见可撤销（Major-06），仅展示网络动作受限提示 */
  serviceOff: boolean;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  (e: 'refresh'): void;
  (e: 'changed'): void;
}>();

const { showSuccess, showError, showInfo } = useToast();

const revokeTarget = ref<remote.DeviceInfo | null>(null);
const revoking = ref(false);
const revokeError = ref<ClassifiedError | null>(null);

/** 仅展示未撤销设备（防御：后端列表可能含已撤销记录） */
const activeDevices = computed(() =>
  (props.devices ?? []).filter((d) => !d.revokedAt),
);

const revokeConsequence = computed(() =>
  revokeTarget.value
    ? `设备「${revokeTarget.value.name || '未命名设备'}」将立即失去访问，其活动连接会被断开。`
    : '',
);

function askRevoke(device: remote.DeviceInfo) {
  revokeError.value = null;
  revokeTarget.value = device;
}

async function confirmRevoke() {
  const target = revokeTarget.value;
  if (!target || revoking.value) return;
  revoking.value = true;
  revokeError.value = null;
  try {
    // PG-06 已获显式确认 → confirm=true
    const result = await revokeRemoteDevice(target.id, true);
    revokeTarget.value = null;
    if (result.alreadyRevoked) {
      showInfo('该设备此前已被撤销');
    } else {
      showSuccess(`已撤销设备 · 断开 ${result.terminationRequestedConnections} 个连接`);
    }
    if (result.durabilityDegraded) {
      showError('本地记录存储降级：撤销已生效，但事件可能未完整持久化，请检查磁盘。');
    }
    emit('changed');
  } catch (err) {
    const c = classifyRemoteError(err);
    revokeError.value = c;
    showError(c.message);
  } finally {
    revoking.value = false;
  }
}

function retryRevoke() {
  if (revokeTarget.value) {
    confirmRevoke();
  } else {
    emit('refresh');
  }
}
</script>

<style scoped>
.dev-loading {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.dev-off-hint {
  margin: 0 0 10px;
  font-size: 12px;
  color: var(--vt-text-secondary);
}

.dev-skeleton {
  height: 44px;
  border-radius: 8px;
  background: var(--vt-surface-raised);
}

.dev-loading-text {
  margin: 0;
  font-size: 13px;
  color: var(--vt-text-secondary);
}

.dev-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: 24px 12px;
  gap: 6px;
}

.dev-empty-icon {
  color: var(--vt-text-secondary);
  display: inline-flex;
}

.dev-empty-title {
  margin: 4px 0 0;
  font-size: 15px;
  font-weight: 600;
  font-family: var(--vt-font-display);
  color: var(--vt-text);
}

.dev-empty-desc {
  margin: 0;
  max-width: 380px;
  font-size: 13px;
  line-height: 1.55;
  color: var(--vt-text-secondary);
}
</style>
