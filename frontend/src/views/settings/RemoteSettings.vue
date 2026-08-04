<!--
  PG-05 桌面远程控制中心（设置 > 远程访问）· 六卡重写
  设计权威：前端视觉交互设计 v1.2 §PG-05/§PG-06/§5/§6 + 视觉风格 v2.2 VT 令牌。
  自上而下：⓪ 外部进程清理恢复卡 → ① 远程服务开关卡 → ② LAN 暴露确认卡 → ③ 配对卡
           → ④ 可信设备卡 → ⑤ 活动控制卡（M3 占位，诚实空态）→ ⑥ 本地可见记录卡。
  硬规则：不展示主凭据/Provider 密钥的"方便复制"入口（§4.2，本页不渲染 Token）；
  危险动作一律 PG-06 确认 + 本地记录；配对 QR 为主路径。
-->
<template>
  <div class="remote-cc">
    <header class="rc-page-head">
      <h1 class="rc-page-title">远程访问</h1>
      <p class="rc-page-sub">桌面根权威：开门、配对、设备、控制权、记录</p>
    </header>

    <div v-if="initialLoading" class="rc-page-loading" aria-live="polite">正在读取远程服务状态…</div>

    <template v-else>
      <div v-if="statusError" class="rc-error" role="alert">
        <span>{{ statusError.message }}</span>
        <span class="rc-error-detail">{{ statusError.detail }}</span>
        <button type="button" class="rc-link" data-testid="status-retry" @click="loadAll">重试</button>
      </div>

      <!-- ⓪ 外部进程清理恢复卡（M2-INT R12：legacy/uncertainty 恢复闭环入口，持久可发现） -->
      <ExternalCleanupRecoveryCard />

      <!-- ① 远程服务开关卡 -->
      <RemoteServiceCard
        :running="status.running"
        :host="status.host"
        :port="status.port"
        :lan-confirmed="lanConfirmed"
        @changed="loadAll"
        @goto-lan-confirm="gotoLanConfirm"
      />

      <!-- ② LAN 暴露确认卡 -->
      <LanExposureCard ref="lanCardRef" @confirmed="onLanConfirmed" />

      <!-- ③ 配对卡 -->
      <PairingCard :running="status.running" @changed="onPairingChanged" />

      <!-- ④ 可信设备卡 -->
      <TrustedDevicesCard
        :devices="devices"
        :loading="devicesLoading"
        :load-error="devicesError"
        :service-off="!status.running"
        @refresh="loadDevices"
        @changed="onDevicesChanged"
      />

      <!-- ⑤ 活动控制卡（M3 占位） -->
      <ActivityControlCard />

      <!-- ⑥ 本地可见记录卡 -->
      <SecurityLogCard
        :events="events"
        :health="health"
        :loading="eventsLoading"
        :load-error="eventsError"
        :service-off="!status.running"
        @refresh="loadEvents"
        @health-changed="onHealthChanged"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import '../../styles/vt-tokens.css';
import '../../components/remote/remote-cards.css';
import type { remote } from '../../../wailsjs/go/models';
import {
  getRemoteStatus,
  listRemoteDevices,
  listRemoteSecurityEvents,
  getRemoteSecurityHealth,
} from '../../api/remote';
import { classifyRemoteError, type ClassifiedError } from '../../components/remote/remoteShared';
import RemoteServiceCard from '../../components/remote/RemoteServiceCard.vue';
import ExternalCleanupRecoveryCard from '../../components/remote/ExternalCleanupRecoveryCard.vue';
import LanExposureCard from '../../components/remote/LanExposureCard.vue';
import PairingCard from '../../components/remote/PairingCard.vue';
import TrustedDevicesCard from '../../components/remote/TrustedDevicesCard.vue';
import ActivityControlCard from '../../components/remote/ActivityControlCard.vue';
import SecurityLogCard from '../../components/remote/SecurityLogCard.vue';

const LAN_STORAGE_KEY = 'amagi.remote.lanExposureConfirmedAt';

const initialLoading = ref(true);
const status = ref({ host: '0.0.0.0', port: 8680, running: false });
const statusError = ref<ClassifiedError | null>(null);

const devices = ref<remote.DeviceInfo[] | null>(null);
const devicesLoading = ref(false);
const devicesError = ref<ClassifiedError | null>(null);

const events = ref<remote.SecurityEventRecord[] | null>(null);
const eventsLoading = ref(false);
const eventsError = ref<ClassifiedError | null>(null);

const health = ref<remote.SecurityHealthSnapshot | null>(null);

const lanConfirmed = ref(false);
const lanCardRef = ref<InstanceType<typeof LanExposureCard> | null>(null);

function readLanRecord(): boolean {
  try {
    return !!localStorage.getItem(LAN_STORAGE_KEY);
  } catch {
    return false;
  }
}

function onLanConfirmed(_at: string) {
  lanConfirmed.value = true;
}

function gotoLanConfirm() {
  lanCardRef.value?.scrollIntoView();
}

async function loadStatus() {
  try {
    const s = (await getRemoteStatus()) as Record<string, unknown>;
    status.value = {
      host: typeof s.host === 'string' && s.host ? s.host : '0.0.0.0',
      port: Number(s.port) || 8680,
      running: !!s.running,
    };
    statusError.value = null;
  } catch (err) {
    statusError.value = classifyRemoteError(err);
  }
}

async function loadDevices() {
  devicesLoading.value = true;
  try {
    devices.value = await listRemoteDevices();
    devicesError.value = null;
  } catch (err) {
    devicesError.value = classifyRemoteError(err);
    devices.value = devices.value ?? [];
  } finally {
    devicesLoading.value = false;
  }
}

async function loadEvents() {
  eventsLoading.value = true;
  try {
    // sanitized 投影，上限 50 条（契约指定）
    events.value = await listRemoteSecurityEvents(50);
    eventsError.value = null;
  } catch (err) {
    eventsError.value = classifyRemoteError(err);
    events.value = events.value ?? [];
  } finally {
    eventsLoading.value = false;
  }
}

async function loadHealth() {
  try {
    health.value = await getRemoteSecurityHealth();
  } catch {
    // 健康快照失败不阻塞页面；事件卡已有独立错误区
    health.value = null;
  }
}

/** Major-06：设备/事件是持久安全状态（后端 ListDevices/RevokeDevice/ListSecurityEvents
 *  只要求 security ready，不要求 server running），服务关闭时仍加载与展示；
 *  仅配对窗口与网络动作受 running 限制（配对卡自行按 running 禁用）。 */
async function loadAll() {
  lanConfirmed.value = readLanRecord();
  await loadStatus();
  await Promise.all([loadHealth(), loadDevices(), loadEvents()]);
}

function onPairingChanged() {
  void loadDevices();
  void loadEvents();
}

function onDevicesChanged() {
  void loadDevices();
  void loadEvents();
  void loadHealth();
}

function onHealthChanged(snapshot: remote.SecurityHealthSnapshot) {
  health.value = snapshot;
}

onMounted(async () => {
  await loadAll();
  initialLoading.value = false;
});
</script>

<style scoped>
.remote-cc {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 760px;
  padding-bottom: 24px;
}

/* 页面级展示标题：衬线点睛（P4 §5.2），正文仍无衬线 */
.rc-page-title {
  margin: 0 0 2px;
  font-family: var(--vt-font-display);
  font-size: 22px;
  font-weight: 500;
  letter-spacing: -0.01em;
  color: var(--vt-text);
}

.rc-page-sub {
  margin: 0 0 4px;
  font-size: 13px;
  color: var(--vt-text-secondary);
}

.rc-page-loading {
  padding: 32px 0;
  font-size: 14px;
  color: var(--vt-text-secondary);
}
</style>
