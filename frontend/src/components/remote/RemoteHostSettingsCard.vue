<!--
  RemoteHostSettingsCard（RC4-2 · 远程模式下设置页读写宿主应用设置）
  交互稿 §2.4：远程模式下设置页读写远端（legacy 接口过渡）。
  - 状态机与 RemoteProviderPanel 一致：needs-token（EmptyState+入口）/
    loading / error（401 → 重填令牌）/ ready。
  - 掩码纪律：remoteToken 等凭据字段渲染为不可展开 MaskedValue；保存前
    store 一律剥离掩码占位（宿主 PUT 仅消费非密钥字段，剔除=宿主不动 token）。
  - 可编辑字段：remotePort（宿主 handleUpdateSettings 唯一消费字段）；
    其余非密钥字段以只读行透出。
-->
<template>
  <ConfigCard class="rhs">
    <div class="rhs-head">
      <h2 class="rhs-title">远程主机设置 <span class="rhs-badge">legacy</span></h2>
      <p class="rhs-sub">
        当前处于远程模式（主机：{{ store.currentHostName }}）：此处读写宿主的应用设置；
        下方其余设置内容均为本机配置。远程配置管理经 legacy 接口提供，需在主机设置中配置访问令牌。
      </p>
    </div>

    <div class="rhs-token-row">
      <span class="rhs-token-label">legacy 访问令牌</span>
      <span class="rhs-token-value">
        <span class="sess-dot" :class="{ on: hasToken }"></span>
        {{ hasToken ? '已配置（隐藏）' : '未配置' }}
      </span>
      <AppButton size="small" variant="ghost" @click="$emit('configure-token')">
        {{ hasToken ? '替换 / 清除令牌' : '配置令牌' }}
      </AppButton>
    </div>

    <LoadingState v-if="view.state === 'loading'" message="读取宿主设置…" />

    <EmptyState
      v-else-if="view.state === 'needs-token'"
      icon="🔑"
      title="尚未配置访问令牌"
      description="读写宿主应用设置需要 legacy 访问令牌。"
    >
      <template #action>
        <AppButton variant="primary" size="small" @click="$emit('configure-token')">配置访问令牌</AppButton>
      </template>
    </EmptyState>

    <div v-else-if="view.state === 'error'" class="rhs-error" role="alert">
      <template v-if="view.authRejected">
        <p class="rhs-error-title">宿主拒绝了访问令牌（401/403）。</p>
        <p class="rhs-error-sub">请重新配置令牌后重试；若持续失败，请在宿主本地核验令牌与访问策略。</p>
        <div class="rhs-error-actions">
          <AppButton variant="primary" size="small" @click="$emit('configure-token')">重填令牌</AppButton>
          <AppButton variant="ghost" size="small" @click="reload">重试</AppButton>
        </div>
      </template>
      <ErrorState v-else :message="view.error" :on-retry="reload" />
    </div>

    <template v-else-if="view.state === 'ready' && parsedDoc">
      <div class="rhs-field">
        <label class="rhs-field-label" for="rhs-remote-port">远程服务端口（remotePort）</label>
        <div class="rhs-port-input">
          <TextInput
            id="rhs-remote-port"
            v-model="portInput"
            mono
            placeholder="8680"
            :disabled="saving"
          />
        </div>
      </div>

      <!-- 凭据字段（如 remoteToken）：不可展开、不可编辑 -->
      <div v-for="path in credentialEntries" :key="path" class="rhs-field">
        <span class="rhs-field-label">{{ path }}</span>
        <span class="rhs-cred-cell">
          <MaskedValue value="••••••••••" :toggleable="false" />
          <span class="rhs-cred-note">宿主管辖 · 不会随保存上行</span>
        </span>
      </div>

      <!-- 其余非密钥顶层字段只读透出 -->
      <div v-for="row in readonlyRows" :key="row.key" class="rhs-field">
        <span class="rhs-field-label">{{ row.key }}</span>
        <span class="rhs-field-value mono">{{ row.value }}</span>
      </div>

      <details v-if="prettyRest" class="rhs-rest">
        <summary>其余字段（净化后只读）</summary>
        <pre class="rhs-json">{{ prettyRest }}</pre>
      </details>

      <div class="rhs-actions">
        <span v-if="saveError" class="rhs-save-error" role="alert">{{ saveError }}</span>
        <span class="rhs-spacer"></span>
        <AppButton variant="ghost" size="small" :disabled="saving" @click="resetForm">还原</AppButton>
        <AppButton variant="primary" size="small" :disabled="saving || !dirty" @click="save">
          {{ saving ? '保存中…' : '保存到宿主' }}
        </AppButton>
      </div>
    </template>
  </ConfigCard>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import AppButton from '../ui/AppButton.vue';
import ConfigCard from '../ui/ConfigCard.vue';
import EmptyState from '../ui/EmptyState.vue';
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import MaskedValue from '../ui/MaskedValue.vue';
import TextInput from '../ui/TextInput.vue';
import { useRemoteClientStore } from '../../stores/remoteClient';
import { useToast } from '../../composables/useToast';
import { listCredentialEntries, looksLikeCredentialField } from '../../utils/remoteMask';

defineEmits<{
  'configure-token': [];
}>();

const store = useRemoteClientStore();
const { showSuccess, showError } = useToast();

const hasToken = computed(() => store.currentHasLegacyToken);
const view = computed(() => store.remoteSettingsView);

const saving = ref(false);
const saveError = ref('');
const portInput = ref('');

const parsedDoc = computed<Record<string, unknown> | null>(() => {
  if (!store.remoteSettingsDoc) return null;
  try {
    const doc = JSON.parse(store.remoteSettingsDoc) as Record<string, unknown>;
    return doc && typeof doc === 'object' ? doc : null;
  } catch {
    return null;
  }
});

/** 文档内凭据字段（masked 或空值）→ 不可展开 MaskedValue 行。 */
const credentialEntries = computed(() =>
  parsedDoc.value ? listCredentialEntries(parsedDoc.value) : [],
);

/** 非密钥、非 remotePort 的简单值顶层字段 → 只读行。 */
const readonlyRows = computed(() => {
  const doc = parsedDoc.value;
  if (!doc) return [];
  return Object.entries(doc)
    .filter(([k, v]) => k !== 'remotePort' && !looksLikeCredentialField(k) && (typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean'))
    .map(([k, v]) => ({ key: k, value: String(v) }));
});

/** 剩余复杂结构（对象/数组）净化后只读展示。 */
const prettyRest = computed(() => {
  const doc = parsedDoc.value;
  if (!doc) return '';
  const rest = Object.fromEntries(
    Object.entries(doc).filter(
      ([k, v]) =>
        k !== 'remotePort' &&
        !looksLikeCredentialField(k) &&
        v !== null &&
        typeof v === 'object',
    ),
  );
  if (Object.keys(rest).length === 0) return '';
  try {
    return JSON.stringify(rest, null, 2);
  } catch {
    return '';
  }
});

function resetForm() {
  const port = parsedDoc.value?.remotePort;
  portInput.value = typeof port === 'number' ? String(port) : '';
  saveError.value = '';
}

watch(parsedDoc, () => resetForm());

const dirty = computed(() => {
  const port = parsedDoc.value?.remotePort;
  const original = typeof port === 'number' ? String(port) : '';
  return portInput.value.trim() !== original;
});

async function save() {
  if (saving.value || !dirty.value) return;
  const parsed = Number.parseInt(portInput.value.trim(), 10);
  if (!Number.isInteger(parsed) || parsed <= 0 || parsed > 65535) {
    saveError.value = '端口须为 1–65535 的整数';
    return;
  }
  saving.value = true;
  saveError.value = '';
  try {
    const doc = JSON.parse(store.remoteSettingsDoc) as Record<string, unknown>;
    doc.remotePort = parsed;
    await store.saveRemoteSettings(JSON.stringify(doc));
    showSuccess('宿主设置已保存');
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
    showError('保存宿主设置失败: ' + saveError.value);
  } finally {
    saving.value = false;
  }
}

function reload() {
  void store.loadRemoteSettings();
}

// 挂载即拉取（父级仅在远程模式渲染本卡）
void store.loadRemoteSettings();
</script>

<style scoped>
.rhs {
  gap: 14px;
}

.rhs-head {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.rhs-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--label);
  display: flex;
  align-items: center;
  gap: 8px;
}

.rhs-badge {
  font-size: 10px;
  font-weight: 600;
  color: var(--tertiary);
  border: 1px solid var(--separator);
  border-radius: 4px;
  padding: 1px 6px;
  letter-spacing: 0.5px;
}

.rhs-sub {
  margin: 0;
  font-size: 12px;
  color: var(--secondary);
  line-height: 1.6;
}

.rhs-token-row {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 13px;
  padding: 8px 12px;
  border: 1px solid var(--separator);
  border-radius: 10px;
  background: var(--control);
}

.rhs-token-label {
  color: var(--tertiary);
  font-size: 12px;
}

.rhs-token-value {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--secondary);
  flex: 1;
}

.sess-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--tertiary);
  flex-shrink: 0;
}

.sess-dot.on {
  background: var(--success);
}

.rhs-field {
  display: flex;
  align-items: center;
  gap: 14px;
  font-size: 13px;
}

.rhs-field-label {
  color: var(--tertiary);
  font-size: 12px;
  flex-shrink: 0;
  min-width: 150px;
  word-break: break-all;
}

.rhs-field-value {
  color: var(--secondary);
  flex: 1;
  word-break: break-all;
}

.mono {
  font-family: var(--mono);
  font-size: 12px;
}

.rhs-port-input {
  flex: 1;
  max-width: 220px;
}

.rhs-cred-cell {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  flex: 1;
}

.rhs-cred-note {
  font-size: 11px;
  color: var(--tertiary);
}

.rhs-rest summary {
  font-size: 12px;
  color: var(--accent);
  cursor: pointer;
  user-select: none;
}

.rhs-json {
  margin: 8px 0 0;
  font-family: var(--mono);
  font-size: 11px;
  color: var(--secondary);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 12px;
  max-height: 220px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
}

.rhs-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.rhs-spacer {
  flex: 1;
}

.rhs-save-error {
  font-size: 12px;
  color: #a02620;
  word-break: break-all;
}

.rhs-error {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.rhs-error-title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: #a02620;
}

.rhs-error-sub {
  margin: 0;
  font-size: 12px;
  color: var(--secondary);
  line-height: 1.6;
}

.rhs-error-actions {
  display: flex;
  gap: 8px;
}
</style>
