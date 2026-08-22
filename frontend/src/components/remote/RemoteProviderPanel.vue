<!--
  RemoteProviderPanel（RC4-2 · 远程模式下 Provider Center 服务提供商区数据面）
  交互稿 §2.4：远程模式下 ProviderCenter 读写远端（legacy 接口过渡）。
  - 数据源切换：仅远程模式挂载；切回本机时父级即回本地 ProviderGrid。
  - 状态机（交互稿 §3）：loading / needs-token（EmptyState+配置入口）/
    error（401 → authRejected 提示重填令牌）/ empty / ready。
  - 验收 5 掩码纪律：凭据字段一律 MaskedValue（toggleable=false，不可展开、
    无复制入口）；编辑仅覆盖非凭据字段；保存前 store 剥离掩码占位，
    含非统一密钥掩码字段（auth_key 等）时禁用保存（剔除会在全量替换语义下
    静默清空宿主值——与 Go 上行拦截同一非破坏取舍）。
-->
<template>
  <div class="rpp">
    <!-- 访问令牌行：状态（布尔投影，令牌本体永不回显）+ 配置入口 -->
    <div class="rpp-token-row">
      <span class="rpp-token-label">legacy 访问令牌</span>
      <span class="rpp-token-value">
        <span class="sess-dot" :class="{ on: hasToken }"></span>
        {{ hasToken ? '已配置（隐藏）' : '未配置' }}
      </span>
      <div class="rpp-token-actions">
        <AppButton size="small" variant="ghost" @click="$emit('configure-token')">
          {{ hasToken ? '替换 / 清除令牌' : '配置令牌' }}
        </AppButton>
      </div>
    </div>

    <!-- 详情模式 -->
    <template v-if="store.remoteProviderName">
      <LoadingState v-if="docView.state === 'loading'" message="加载远程提供商详情…" />

      <EmptyState
        v-else-if="docView.state === 'needs-token'"
        icon="🔑"
        title="尚未配置访问令牌"
        description="读取远程提供商详情需要 legacy 访问令牌。"
      >
        <template #action>
          <AppButton variant="primary" size="small" @click="$emit('configure-token')">配置访问令牌</AppButton>
        </template>
      </EmptyState>

      <div v-else-if="docView.state === 'error'" class="rpp-error" role="alert">
        <template v-if="docView.authRejected">
          <p class="rpp-error-title">宿主拒绝了访问令牌（401/403）。</p>
          <p class="rpp-error-sub">请重新配置令牌后重试；若持续失败，请在宿主本地核验令牌与访问策略。</p>
          <div class="rpp-error-actions">
            <AppButton variant="primary" size="small" @click="$emit('configure-token')">重填令牌</AppButton>
            <AppButton variant="ghost" size="small" @click="reloadDetail">重试</AppButton>
          </div>
        </template>
        <ErrorState v-else :message="docView.error" :on-retry="reloadDetail" />
      </div>

      <template v-else-if="docView.state === 'ready' && parsedDoc">
        <button type="button" class="rpp-back" @click="store.closeRemoteProvider()">
          <svg class="ic" viewBox="0 0 24 24" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="15 18 9 12 15 6" />
          </svg>
          返回远程列表
        </button>

        <div class="rpp-detail-title">
          <span class="rpp-name">{{ store.remoteProviderName }}</span>
          <span class="prov-formats">
            <span v-if="hasAnthropic" class="fmt A" title="Anthropic 格式">A</span>
            <span v-if="hasOpenAI" class="fmt O" title="OpenAI 格式">O</span>
            <span v-if="!hasAnthropic && !hasOpenAI" class="fmt legacy">{{ typeInitial }}</span>
          </span>
          <span class="rpp-remote-tag">宿主：{{ store.currentHostName }}</span>
        </div>

        <!-- 可编辑区：仅非凭据字段（过渡期限制，见文件头） -->
        <section class="rpp-section">
          <h3>基本信息 <span class="rpp-section-note">（可编辑，保存即整体替换宿主该提供商配置）</span></h3>
          <div class="rpp-field">
            <label class="rpp-field-label" for="rpp-default-model">默认模型</label>
            <TextInput id="rpp-default-model" v-model="form.defaultModel" mono :disabled="saving || blocked" />
          </div>
          <div v-if="hasAnthropic" class="rpp-field">
            <label class="rpp-field-label" for="rpp-anthropic-url">Anthropic Base URL</label>
            <TextInput id="rpp-anthropic-url" v-model="form.anthropicBaseUrl" mono :disabled="saving || blocked" />
          </div>
          <div v-if="hasOpenAI" class="rpp-field">
            <label class="rpp-field-label" for="rpp-openai-url">OpenAI Base URL</label>
            <TextInput id="rpp-openai-url" v-model="form.openaiBaseUrl" mono :disabled="saving || blocked" />
          </div>
          <div v-if="hasOpenAI" class="rpp-field">
            <label class="rpp-field-label" for="rpp-openai-org">Organization</label>
            <TextInput id="rpp-openai-org" v-model="form.openaiOrg" mono :disabled="saving || blocked" />
          </div>
          <div v-if="hasOpenAI" class="rpp-field">
            <label class="rpp-field-label" for="rpp-openai-wire-api">接口协议</label>
            <select id="rpp-openai-wire-api" v-model="form.openaiWireApi" class="rpp-select" :disabled="saving || blocked">
              <option value="">自动（默认）</option>
              <option value="chat">Chat Completions (/chat/completions)</option>
              <option value="responses">Responses (/responses)</option>
            </select>
          </div>
        </section>

        <!-- 凭据区：一律 MaskedValue 且不可展开/复制（验收 5） -->
        <section v-if="credentialEntries.length > 0" class="rpp-section">
          <h3>凭据 <span class="rpp-section-note">（由宿主管辖：远程不可查看、不可编辑）</span></h3>
          <div v-for="path in credentialEntries" :key="path" class="rpp-field">
            <span class="rpp-field-label">{{ path }}</span>
            <span class="rpp-cred-cell">
              <MaskedValue value="••••••••••" :toggleable="false" />
              <span class="rpp-cred-note">远端管理</span>
            </span>
          </div>
          <p v-if="blocked" class="rpp-blocked" role="alert">
            该提供商含由宿主管辖的凭据字段（{{ blockedFields.join('、') }}）：远程保存会在替换时清空宿主值，
            已禁用远程编辑。请在宿主本地修改这些字段。
          </p>
        </section>

        <!-- 其余字段：净化后 JSON 只读透出（掩码占位如实展示） -->
        <section class="rpp-section">
          <h3>宿主原始配置 <span class="rpp-section-note">（净化后只读视图，凭据字段已掩码）</span></h3>
          <pre class="rpp-json">{{ prettyDoc }}</pre>
        </section>

        <div class="rpp-actions">
          <span v-if="saveError" class="rpp-save-error" role="alert">{{ saveError }}</span>
          <span class="rpp-spacer"></span>
          <AppButton variant="ghost" size="small" :disabled="saving" @click="resetForm">还原</AppButton>
          <AppButton
            variant="primary"
            size="small"
            :disabled="saving || blocked || !hasChanges"
            :title="blocked ? '含宿主管辖凭据字段，远程保存已禁用' : ''"
            @click="save"
          >{{ saving ? '保存中…' : '保存到宿主' }}</AppButton>
        </div>
      </template>
    </template>

    <!-- 列表模式 -->
    <template v-else>
      <LoadingState v-if="listView.state === 'loading'" message="加载远程提供商列表…" />

      <EmptyState
        v-else-if="listView.state === 'needs-token'"
        icon="🔑"
        title="尚未配置访问令牌"
        description="远程配置管理经 legacy 接口提供，读取宿主提供商列表需要先配置访问令牌。"
      >
        <template #action>
          <AppButton variant="primary" size="small" @click="$emit('configure-token')">配置访问令牌</AppButton>
        </template>
      </EmptyState>

      <div v-else-if="listView.state === 'error'" class="rpp-error" role="alert">
        <template v-if="listView.authRejected">
          <p class="rpp-error-title">宿主拒绝了访问令牌（401/403）。</p>
          <p class="rpp-error-sub">请重新配置令牌后重试；若持续失败，请在宿主本地核验令牌与访问策略。</p>
          <div class="rpp-error-actions">
            <AppButton variant="primary" size="small" @click="$emit('configure-token')">重填令牌</AppButton>
            <AppButton variant="ghost" size="small" @click="reload">重试</AppButton>
          </div>
        </template>
        <ErrorState v-else :message="listView.error" :on-retry="reload" />
      </div>

      <template v-else-if="listView.state === 'ready'">
        <div class="pc-zone-label">
          <span>宿主提供商</span>
          <span class="zn-sep">·</span>
          <span>{{ store.currentHostName }}</span>
          <span class="zn-count">· {{ store.remoteProviders.length }} 个</span>
        </div>
        <EmptyState
          v-if="store.remoteProviders.length === 0"
          icon="⌀"
          title="宿主暂无服务提供商"
          description="该主机上尚未配置任何服务提供商。可在宿主本地添加后刷新。"
        >
          <template #action>
            <AppButton variant="ghost" size="small" @click="reload">刷新</AppButton>
          </template>
        </EmptyState>
        <div v-else class="rpp-grid">
          <article
            v-for="p in store.remoteProviders"
            :key="p.name"
            class="rpp-card"
            @click="store.openRemoteProvider(p.name)"
          >
            <header class="rpp-card-head">
              <h3 class="rpp-card-name">{{ p.name }}</h3>
              <span class="prov-formats">
                <span class="fmt" :class="p.type === 'openai' ? 'O' : 'A'" :title="p.type">{{
                  p.type === 'openai' ? 'O' : 'A'
                }}</span>
              </span>
            </header>
            <div class="rpp-row">
              <span class="rpp-row-label">Base URL</span>
              <span class="rpp-row-value mono">{{ p.baseURL || '未设置' }}</span>
            </div>
            <div class="rpp-row">
              <span class="rpp-row-label">默认模型</span>
              <span class="rpp-row-value" :class="{ placeholder: !p.model }">{{ p.model || '-' }}</span>
            </div>
          </article>
        </div>
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import MaskedValue from '../ui/MaskedValue.vue';
import TextInput from '../ui/TextInput.vue';
import { useRemoteClientStore } from '../../stores/remoteClient';
import { useToast } from '../../composables/useToast';
import { scanMaskedCredentialFields, listCredentialEntries } from '../../utils/remoteMask';

defineEmits<{
  'configure-token': [];
}>();

const store = useRemoteClientStore();
const { showSuccess, showError } = useToast();

const hasToken = computed(() => store.currentHasLegacyToken);
const listView = computed(() => store.remoteProvidersView);
const docView = computed(() => store.remoteProviderDocView);

const saving = ref(false);
const saveError = ref('');

interface DetailDoc {
  default_model?: string;
  anthropic?: { enabled?: boolean; base_url?: string } | null;
  openai?: { enabled?: boolean; base_url?: string; organization?: string; wire_api?: string } | null;
  type?: string;
  [key: string]: unknown;
}

const parsedDoc = computed<DetailDoc | null>(() => {
  if (!store.remoteProviderDoc) return null;
  try {
    return JSON.parse(store.remoteProviderDoc) as DetailDoc;
  } catch {
    return null;
  }
});

const hasAnthropic = computed(() => !!parsedDoc.value?.anthropic?.enabled);
const hasOpenAI = computed(() => !!parsedDoc.value?.openai?.enabled);
const typeInitial = computed(() =>
  (parsedDoc.value?.type || 'anthropic').toLowerCase() === 'openai' ? 'O' : 'A',
);

/** 掩码扫描：统一密钥字段可剔除（保留语义），其余阻断保存。 */
const maskedScan = computed(() =>
  parsedDoc.value ? scanMaskedCredentialFields(parsedDoc.value) : { apiKeyMasked: [], otherMasked: [] },
);
const blockedFields = computed(() => maskedScan.value.otherMasked);
const blocked = computed(() => blockedFields.value.length > 0);

/** 文档内全部凭据字段（渲染为不可展开 MaskedValue 行）。 */
const credentialEntries = computed(() =>
  parsedDoc.value ? listCredentialEntries(parsedDoc.value) : [],
);

const prettyDoc = computed(() => {
  if (!parsedDoc.value) return '';
  try {
    return JSON.stringify(parsedDoc.value, null, 2);
  } catch {
    return store.remoteProviderDoc;
  }
});

// ---- 编辑表单（仅非凭据字段；随详情文档装载初始化）----
const form = ref({
  defaultModel: '',
  anthropicBaseUrl: '',
  openaiBaseUrl: '',
  openaiOrg: '',
  /** OpenAI 接口协议：''（自动）| 'chat' | 'responses'，对应 openai.wire_api */
  openaiWireApi: '',
});

function resetForm() {
  const doc = parsedDoc.value;
  form.value = {
    defaultModel: doc?.default_model ?? '',
    anthropicBaseUrl: doc?.anthropic?.base_url ?? '',
    openaiBaseUrl: doc?.openai?.base_url ?? '',
    openaiOrg: doc?.openai?.organization ?? '',
    openaiWireApi: normalizedWireApi(doc?.openai?.wire_api),
  };
  saveError.value = '';
}

watch(parsedDoc, () => resetForm());

const hasChanges = computed(() => {
  const doc = parsedDoc.value;
  if (!doc) return false;
  return (
    form.value.defaultModel !== (doc.default_model ?? '') ||
    form.value.anthropicBaseUrl !== (doc.anthropic?.base_url ?? '') ||
    form.value.openaiBaseUrl !== (doc.openai?.base_url ?? '') ||
    form.value.openaiOrg !== (doc.openai?.organization ?? '') ||
    form.value.openaiWireApi !== normalizedWireApi(doc.openai?.wire_api)
  );
});

/** wire_api 归一化：trim + 小写后仅接受 chat/responses，其余归一为 ''（自动） */
function normalizedWireApi(raw: string | undefined | null): string {
  const wa = (raw ?? '').trim().toLowerCase();
  return wa === 'chat' || wa === 'responses' ? wa : '';
}

/** 应用表单编辑到净化后文档副本，返回待上传 JSON（含掩码占位，store 负责剥离）。 */
function buildEditedDoc(): string {
  const doc = JSON.parse(store.remoteProviderDoc) as DetailDoc;
  doc.default_model = form.value.defaultModel;
  if (doc.anthropic) doc.anthropic.base_url = form.value.anthropicBaseUrl;
  if (doc.openai) {
    doc.openai.base_url = form.value.openaiBaseUrl;
    doc.openai.organization = form.value.openaiOrg;
    // wire_api 仅非空写入，空值删除键保持 omitempty 语义
    if (form.value.openaiWireApi) {
      doc.openai.wire_api = form.value.openaiWireApi;
    } else {
      delete doc.openai.wire_api;
    }
  }
  return JSON.stringify(doc);
}

async function save() {
  if (saving.value || blocked.value || !hasChanges.value) return;
  saving.value = true;
  saveError.value = '';
  try {
    await store.saveRemoteProvider(store.remoteProviderName, buildEditedDoc());
    showSuccess('已保存到宿主');
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
    showError('远程保存失败: ' + saveError.value);
  } finally {
    saving.value = false;
  }
}

function reload() {
  void store.loadRemoteProviders();
}

function reloadDetail() {
  const name = store.remoteProviderName;
  if (name) void store.openRemoteProvider(name);
  else void store.loadRemoteProviders();
}

// 挂载即拉取（父级仅在远程模式渲染本面板）
void store.loadRemoteProviders();
</script>

<style scoped>
.rpp {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* 令牌行 */
.rpp-token-row {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 13px;
  padding: 10px 12px;
  border: 1px solid var(--separator);
  border-radius: 10px;
  background: var(--control);
}

.rpp-token-label {
  color: var(--tertiary);
  font-size: 12px;
}

.rpp-token-value {
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

/* 区域标签（对齐 ProviderGrid .pc-zone-label） */
.pc-zone-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  font-weight: 600;
  color: var(--tertiary);
  padding: 0 2px;
  letter-spacing: 0.3px;
}

.pc-zone-label .zn-sep {
  color: var(--separator);
}

.pc-zone-label .zn-count {
  color: var(--secondary);
  font-weight: 500;
}

/* 列表卡片（对齐本地 .prov-grid 视觉） */
.rpp-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(286px, 1fr));
  gap: 14px;
}

.rpp-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 16px;
  cursor: pointer;
  transition: box-shadow 0.15s, border-color 0.15s;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.rpp-card:hover {
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.08);
  border-color: #c5c5cc;
}

.rpp-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.rpp-card-name {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--label);
  letter-spacing: -0.2px;
  word-break: break-all;
}

.prov-formats {
  display: flex;
  gap: 5px;
  flex-shrink: 0;
}

.fmt {
  width: 20px;
  height: 20px;
  border-radius: 5px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  font-weight: 700;
  color: #fff;
}

.fmt.A {
  background: var(--purple);
}

.fmt.O {
  background: var(--success);
}

.fmt.legacy {
  background: var(--tertiary);
}

.rpp-row {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 12px;
}

.rpp-row-label {
  color: var(--tertiary);
  flex-shrink: 0;
  min-width: 56px;
}

.rpp-row-value {
  color: var(--secondary);
  flex: 1;
  word-break: break-all;
}

.rpp-row-value.mono {
  font-family: var(--mono);
  font-size: 11px;
}

.rpp-row-value.placeholder {
  color: var(--tertiary);
}

/* 详情 */
.rpp-back {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: none;
  border: none;
  cursor: pointer;
  color: var(--accent);
  font-family: inherit;
  font-size: 13px;
  padding: 4px 0;
  align-self: flex-start;
}

.rpp-back:hover {
  text-decoration: underline;
}

.ic {
  width: 16px;
  height: 16px;
  stroke: currentColor;
}

.rpp-detail-title {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.rpp-name {
  font-size: 22px;
  font-weight: 600;
  color: var(--label);
  letter-spacing: -0.3px;
  word-break: break-all;
}

.rpp-remote-tag {
  margin-left: auto;
  font-size: 11px;
  color: var(--tertiary);
}

.rpp-section {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding-top: 14px;
  border-top: 1px solid var(--separator);
}

.rpp-section:first-of-type {
  padding-top: 0;
  border-top: none;
}

.rpp-section h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
}

.rpp-section-note {
  font-size: 11px;
  font-weight: 400;
  color: var(--tertiary);
}

.rpp-field {
  display: flex;
  align-items: center;
  gap: 14px;
  font-size: 13px;
}

.rpp-field-label {
  color: var(--tertiary);
  font-size: 12px;
  flex-shrink: 0;
  min-width: 150px;
  word-break: break-all;
}

.rpp-field :deep(.text-input) {
  flex: 1;
}

.rpp-select {
  flex: 1;
  height: 34px;
  padding: 0 10px;
  font-size: 13px;
  font-family: inherit;
  color: var(--label);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  outline: none;
}

.rpp-select:focus {
  border-color: var(--accent);
}

.rpp-select:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.rpp-cred-cell {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  flex: 1;
}

.rpp-cred-note {
  font-size: 11px;
  color: var(--tertiary);
}

.rpp-blocked {
  margin: 0;
  font-size: 12px;
  color: #a02620;
  line-height: 1.6;
  background: rgba(255, 59, 48, 0.08);
  border: 1px solid rgba(255, 59, 48, 0.3);
  border-radius: 8px;
  padding: 8px 12px;
}

.rpp-json {
  margin: 0;
  font-family: var(--mono);
  font-size: 11px;
  color: var(--secondary);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 12px;
  max-height: 260px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
}

.rpp-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.rpp-spacer {
  flex: 1;
}

.rpp-save-error {
  font-size: 12px;
  color: #a02620;
  word-break: break-all;
}

/* 错误态（401 专属块） */
.rpp-error {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.rpp-error-title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: #a02620;
}

.rpp-error-sub {
  margin: 0;
  font-size: 12px;
  color: var(--secondary);
  line-height: 1.6;
}

.rpp-error-actions {
  display: flex;
  gap: 8px;
}
</style>
