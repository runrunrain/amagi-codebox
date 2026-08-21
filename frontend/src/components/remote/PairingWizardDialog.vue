<!--
  PairingWizardDialog（RC1-6 桌面端互联 · 配对向导）
  交互稿 §2.1：地址(host:port) + 显示名 → 连接测试（ProbeHost 显示宿主摘要）
  → 引导语 + 输入配对码 → CompletePairing → 成功页（设备名确认）。
  失败分支按 12 稳定错误码给文案（remoteClientShared.ts）：
  码错/过期（重新输入）、已撤销（重新配对提示）、网络（重试）。
-->
<template>
  <Dialog
    :open="open"
    title="添加远程主机"
    :description="stepDescription"
    @update:open="handleClose"
  >
    <div class="pw-body">
      <!-- 步骤一：地址 + 显示名 -->
      <template v-if="step === 'form'">
        <div class="pw-field">
          <label class="pw-label" for="pw-address">主机地址</label>
          <TextInput
            id="pw-address"
            v-model="address"
            mono
            placeholder="例如 192.168.1.10:9622"
            :disabled="probing"
            @keydown.enter="handleProbe"
          />
          <p class="pw-hint">对方 CodeBox 的监听地址（host:port），可在对方「设置 › 远程访问」查看。</p>
        </div>
        <div class="pw-field">
          <label class="pw-label" for="pw-name">显示名</label>
          <TextInput
            id="pw-name"
            v-model="displayName"
            placeholder="例如 win-desktop（留空则使用地址）"
            :disabled="probing"
          />
        </div>
        <StatusBanner v-if="errorCopy" type="error" :message="errorCopy" />
        <p v-if="errorDetail" class="pw-detail">{{ errorDetail }}</p>
      </template>

      <!-- 步骤二：宿主摘要（连接测试通过） -->
      <template v-else-if="step === 'summary'">
        <div class="pw-summary">
          <div class="pw-summary-row">
            <span class="pw-summary-key">地址</span>
            <span class="pw-summary-val mono">{{ address }}</span>
          </div>
          <div class="pw-summary-row">
            <span class="pw-summary-key">服务版本</span>
            <span class="pw-summary-val mono">{{ summary?.serverVersion || '—（配对后可见）' }}</span>
          </div>
          <div class="pw-summary-row">
            <span class="pw-summary-key">API 版本</span>
            <span class="pw-summary-val mono">{{ summary?.apiVersion || '—（配对后可见）' }}</span>
          </div>
          <div class="pw-summary-row pw-summary-clis">
            <span class="pw-summary-key">可用 CLI</span>
            <span class="pw-summary-val">
              <template v-if="availableClis.length > 0">
                <Badge
                  v-for="cli in availableClis"
                  :key="cli"
                  type="scope"
                  :text="cli"
                  class="pw-cli-badge"
                />
              </template>
              <span v-else>—（对方要求配对后可见）</span>
            </span>
          </div>
        </div>
        <p class="pw-guide">
          连接测试通过<template v-if="!summary">（对方服务可达；宿主版本与 CLI 列表按契约需配对后可见）</template>。
          请在对方 CodeBox 打开配对窗口（设置 › 远程访问 › 配对），
          获取一次性配对码后继续。
        </p>
      </template>

      <!-- 步骤三：输入配对码 -->
      <template v-else-if="step === 'code'">
        <div class="pw-field">
          <label class="pw-label" for="pw-code">配对码</label>
          <TextInput
            id="pw-code"
            v-model="pairingCode"
            mono
            placeholder="对方 CodeBox 显示的一次性配对码"
            :disabled="completing"
            @keydown.enter="handleComplete"
          />
          <p class="pw-hint">配对码短时有效且一次性使用；过期或被使用后需重新获取。</p>
        </div>
        <StatusBanner v-if="errorCopy" type="error" :message="errorCopy" />
        <p v-if="errorDetail" class="pw-detail">{{ errorDetail }}</p>
      </template>

      <!-- 成功页 -->
      <template v-else>
        <div class="pw-success">
          <div class="pw-success-icon" aria-hidden="true">✓</div>
          <h4 class="pw-success-title">配对成功</h4>
          <p class="pw-success-text">
            已与「{{ pairedDeviceName }}」完成配对，本设备已登记到对方的可信设备列表。
          </p>
        </div>
      </template>
    </div>

    <template #footer>
      <!-- 步骤一 -->
      <template v-if="step === 'form'">
        <AppButton variant="ghost" :disabled="probing" @click="handleClose(false)">取消</AppButton>
        <AppButton variant="primary" :disabled="probing || !canProbe" @click="handleProbe">
          {{ probing ? '连接测试…' : '连接测试' }}
        </AppButton>
      </template>
      <!-- 步骤二 -->
      <template v-else-if="step === 'summary'">
        <AppButton variant="ghost" @click="backToForm">上一步</AppButton>
        <AppButton variant="primary" @click="step = 'code'">下一步</AppButton>
      </template>
      <!-- 步骤三 -->
      <template v-else-if="step === 'code'">
        <AppButton variant="ghost" :disabled="completing" @click="step = 'summary'">上一步</AppButton>
        <AppButton variant="primary" :disabled="completing || !pairingCode.trim()" @click="handleComplete">
          {{ completing ? '配对中…' : '完成配对' }}
        </AppButton>
      </template>
      <!-- 成功页 -->
      <template v-else>
        <AppButton variant="primary" @click="handleClose(true)">完成</AppButton>
      </template>
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import Dialog from '../ui/Dialog.vue';
import TextInput from '../ui/TextInput.vue';
import AppButton from '../ui/AppButton.vue';
import Badge from '../ui/Badge.vue';
import StatusBanner from '../ui/StatusBanner.vue';
import { probeRemoteHost, completeRemotePairing, renameRemoteHost } from '../../api/remoteClient';
import type { PairingResult } from '../../api/remoteClient';
import { copyForRemoteError, detailForRemoteError } from './remoteClientShared';
import type { contract } from '../../../wailsjs/go/models';

interface Props {
  open?: boolean;
}

const props = withDefaults(defineProps<Props>(), { open: false });

const emit = defineEmits<{
  'update:open': [value: boolean];
  paired: [result: PairingResult];
}>();

type WizardStep = 'form' | 'summary' | 'code' | 'success';

const step = ref<WizardStep>('form');
const address = ref('');
const displayName = ref('');
const pairingCode = ref('');
const probing = ref(false);
const completing = ref(false);
const summary = ref<contract.HostSummary | null>(null);
const pairedResult = ref<PairingResult | null>(null);
const errorCopy = ref('');
const errorDetail = ref('');

const stepDescription = computed(() => {
  switch (step.value) {
    case 'form':
      return '第一步：输入对方 CodeBox 的地址并测试连接';
    case 'summary':
      return '第二步：确认对方宿主信息';
    case 'code':
      return '第三步：输入一次性配对码完成配对';
    default:
      return '';
  }
});

const canProbe = computed(() => address.value.trim().length > 0);

const availableClis = computed(() => {
  const list = summary.value?.cliAvailability ?? [];
  return list.filter((c) => c.available).map((c) => c.cliType);
});

const pairedDeviceName = computed(() => pairedResult.value?.DeviceName || '对方设备');

function clearError() {
  errorCopy.value = '';
  errorDetail.value = '';
}

function showFailure(err: unknown) {
  errorCopy.value = copyForRemoteError(err);
  errorDetail.value = detailForRemoteError(err);
}

function backToForm() {
  clearError();
  step.value = 'form';
}

/** 连接测试：ProbeHost（不要求先登记）。 */
async function handleProbe() {
  if (probing.value || !canProbe.value) return;
  probing.value = true;
  clearError();
  try {
    const res = await probeRemoteHost(address.value.trim());
    // host/summary 按契约需设备凭据（deviceCookie）：未配对主机探测可达
    // 但 Summary 为空属正常路径，如实展示并放行到输码步骤。
    if (res.State === 'reachable') {
      summary.value = res.Summary ?? null;
      if (!displayName.value.trim()) {
        displayName.value = address.value.trim();
      }
      step.value = 'summary';
      return;
    }
    if (res.State === 'revoked') {
      errorCopy.value = '本设备授权已被对方撤销：请在对方 CodeBox 恢复信任后重新配对。';
    } else {
      errorCopy.value = '无法连接到该地址：请确认对方 CodeBox 已启动、地址与端口正确且网络可达后重试。';
    }
    errorDetail.value = `探测结果：${res.State || 'unknown'}`;
  } catch (err) {
    showFailure(err);
  } finally {
    probing.value = false;
  }
}

/** 完成配对：CompletePairing（域层保证失败零残留）。 */
async function handleComplete() {
  if (completing.value || !pairingCode.value.trim()) return;
  completing.value = true;
  clearError();
  try {
    const res = await completeRemotePairing(address.value.trim(), pairingCode.value.trim());
    pairedResult.value = res;
    step.value = 'success';
    // F-1 修复：把向导里采集的显示名持久化到登记簿条目（best-effort，
    // 改名失败不影响配对成功语义，仅回退为地址名）。
    const name = displayName.value.trim();
    if (name && name !== res.HostPort) {
      try { await renameRemoteHost(res.EntryID, name); } catch { /* 保留地址名 */ }
    }
  } catch (err) {
    showFailure(err);
  } finally {
    completing.value = false;
  }
}

function handleClose(completed: boolean) {
  emit('update:open', false);
  if (completed && pairedResult.value) {
    emit('paired', pairedResult.value);
  }
}

// 每次打开重置向导状态（幂等，避免残留上次输入）。
watch(
  () => props.open,
  (val) => {
    if (val) {
      step.value = 'form';
      address.value = '';
      displayName.value = '';
      pairingCode.value = '';
      summary.value = null;
      pairedResult.value = null;
      clearError();
    }
  },
);
</script>

<style scoped>
.pw-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.pw-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.pw-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

.pw-hint {
  margin: 0;
  font-size: 12px;
  color: var(--tertiary);
  line-height: 1.5;
}

.pw-detail {
  margin: 0;
  font-size: 11px;
  color: var(--tertiary);
  font-family: var(--mono);
  word-break: break-all;
}

.pw-summary {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  background: var(--control);
  border-radius: 10px;
}

.pw-summary-row {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 13px;
}

.pw-summary-key {
  width: 64px;
  flex-shrink: 0;
  color: var(--secondary);
}

.pw-summary-val {
  flex: 1;
  color: var(--label);
  min-width: 0;
  word-break: break-all;
}

.pw-summary-val.mono {
  font-family: var(--mono);
  font-size: 12px;
}

.pw-cli-badge {
  margin-right: 6px;
}

.pw-guide {
  margin: 0;
  font-size: 13px;
  color: var(--secondary);
  line-height: 1.6;
}

.pw-success {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 12px 0 4px;
  text-align: center;
}

.pw-success-icon {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: var(--success);
  color: #fff;
  font-size: 20px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
}

.pw-success-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--label);
}

.pw-success-text {
  margin: 0;
  font-size: 13px;
  color: var(--secondary);
  line-height: 1.6;
}
</style>
