<template>
  <!--
    Provider 详情模式（对照 demo .pc-detail + legacy ProviderDetail.vue）。
    照搬 legacy 业务逻辑，换苹果风视觉。
    - 基本信息：默认模型 + 当前有效 Base URL
    - API 密钥：状态 + 掩码显示/隐藏 + 编辑/删除（danger 红）
    - API 格式：按 provider formats 动态显示 Anthropic 块(A紫)/OpenAI 块(O绿)
  -->
  <ConfigCard class="pc-detail">
    <!-- Initial loading state -->
    <LoadingState v-if="initialLoading" message="加载提供商详情中..." />

    <!-- Initial error state -->
    <ErrorState
      v-else-if="initialError"
      :message="initialError"
      :on-retry="handleRetry"
    />

    <!-- Main content -->
    <template v-else>
    <!-- 返回按钮 -->
    <button class="pc-back" @click="onBack">
      <svg class="ic" viewBox="0 0 24 24" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="15 18 9 12 15 6" />
      </svg>
      返回列表
    </button>

    <!-- 标题行：名称 + 格式徽章 + 编辑按钮 -->
    <div class="detail-title-row">
      <span class="prov-name">{{ entryView.id }}</span>
      <span class="prov-formats">
        <span v-if="hasAnthropic" class="fmt A" title="Anthropic 格式">A</span>
        <span v-if="hasOpenAI" class="fmt O" title="OpenAI 格式">O</span>
        <span
          v-if="!hasAnthropic && !hasOpenAI"
          class="fmt legacy"
        >{{ typeInitial }}</span>
      </span>
      <button
        class="btn-ghost sm edit-btn"
        type="button"
        title="编辑提供商"
        @click="openEditDialog"
      >编辑</button>
    </div>

    <!-- 基本信息 -->
    <section class="detail-section">
      <h3>基本信息</h3>
      <div class="detail-row">
        <span class="dr-label">默认模型</span>
        <span class="dr-value" :class="{ placeholder: !entryView.provider.default_model }">
          {{ entryView.provider.default_model || '未设置' }}
        </span>
      </div>
      <div class="detail-row">
        <span class="dr-label">当前有效 Base URL</span>
        <span class="dr-value mono">{{ effectiveBaseUrl || '—' }}</span>
      </div>
    </section>

    <!-- API 密钥 -->
    <section class="detail-section">
      <h3>API 密钥</h3>
      <div class="detail-row">
        <span class="dr-label">密钥状态</span>
        <span class="dr-value">
          <span class="key-status-inline">
            <span class="sess-dot" :style="{ background: entryView.keyConfigured ? '#34C759' : '#8E8E93' }"></span>
            {{ entryView.keyConfigured ? '已配置' : '未配置' }}
          </span>
        </span>
      </div>
      <div class="detail-row">
        <span class="dr-label">密钥</span>
        <span class="dr-value key-value-cell">
          <template v-if="entryView.keyConfigured && !editing">
            <!--
              显隐完全由 ProviderDetailView 控制：
              未显示时给掩码（applyKeyMask 风格），显示时拉取并显示真实密钥。
              注意：已配置且正在编辑(editing)时本分支不渲染，改由下方 v-else-if="editing" 输入框分支接管。
            -->
            <span class="key-mask mono">{{ keyMasked }}</span>
            <div class="key-actions">
              <button class="btn-ghost sm" @click="toggleKeyVisible">
                {{ keyVisible ? '隐藏' : '显示' }}
              </button>
              <button class="btn-ghost sm" @click="startEdit">编辑</button>
              <button
                v-if="!confirmDelete"
                class="btn-ghost sm danger"
                @click="confirmDelete = true"
              >删除</button>
              <template v-else>
                <span class="confirm-text">确认删除？</span>
                <button class="btn-danger sm" :disabled="loading" @click="deleteApiKey">确认</button>
                <button class="btn-ghost sm" @click="confirmDelete = false">取消</button>
              </template>
            </div>
          </template>
          <template v-else-if="editing">
            <input
              :type="inputVisible ? 'text' : 'password'"
              v-model="inputValue"
              class="key-input"
              placeholder="输入 API 密钥"
            />
            <div class="key-actions">
              <button class="btn-ghost sm" @click="inputVisible = !inputVisible">
                {{ inputVisible ? '隐藏' : '明文' }}
              </button>
              <button class="btn-primary sm" :disabled="!inputValue || loading" @click="saveApiKey">保存</button>
              <button class="btn-ghost sm" @click="cancelEdit">取消</button>
            </div>
          </template>
          <template v-else>
            <span class="key-placeholder">未配置</span>
            <button class="btn-primary sm" @click="startEdit">配置密钥</button>
          </template>
        </span>
      </div>
    </section>

    <!-- API 格式：Anthropic 块 -->
    <section v-if="hasAnthropic" class="detail-section">
      <div class="fmt-block-head">
        <div class="fmt-block-title">
          <span class="fmt A">A</span>
          <span>Anthropic 格式</span>
        </div>
        <Switch :modelValue="true" disabled />
      </div>
      <div class="fmt-field">
        <span class="ff-label">Base URL</span>
        <span class="ff-value">{{ anthropicBaseUrl || '未设置' }}</span>
      </div>
    </section>

    <!-- API 格式：OpenAI 块 -->
    <section v-if="hasOpenAI" class="detail-section">
      <div class="fmt-block-head">
        <div class="fmt-block-title">
          <span class="fmt O">O</span>
          <span>OpenAI 格式</span>
        </div>
        <Switch :modelValue="true" disabled />
      </div>
      <div class="fmt-field">
        <span class="ff-label">Base URL</span>
        <span class="ff-value">{{ openaiBaseUrl || '未设置' }}</span>
      </div>
      <div class="fmt-field">
        <span class="ff-label">Organization</span>
        <span class="ff-value">{{ openaiOrg || '（可选，未设置）' }}</span>
      </div>
    </section>

    <!-- 无任何格式时的兼容提示 -->
    <section v-if="!hasAnthropic && !hasOpenAI" class="detail-section">
      <p class="compat-hint">该提供商未启用 Anthropic / OpenAI 双格式配置，可能为 legacy 单类型（{{ typeLabel }}）。完整编辑能力将在 P7 批次补齐。</p>
    </section>
    </template>

    <!-- 编辑提供商弹窗（复用改造后的 AddProviderDialog，双模式） -->
    <AddProviderDialog
      v-model:open="editDialogOpen"
      :edit-target="editTarget"
      @saved="handleEditSaved"
    />
  </ConfigCard>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue';
import ConfigCard from '../components/ui/ConfigCard.vue';
import Switch from '../components/ui/Switch.vue';
import LoadingState from '../components/ui/LoadingState.vue';
import ErrorState from '../components/ui/ErrorState.vue';
import AddProviderDialog from '../components/provider/AddProviderDialog.vue';
import { useProviderStore } from '../stores/provider';
import { useToast } from '../composables/useToast';
import {
  HasAPIKey,
  GetAPIKey,
  SetAPIKey,
  DeleteAPIKey,
  Save as SaveSecrets,
} from '../../wailsjs/go/secrets/SecretsService';

const emit = defineEmits<{
  back: [];
  saved: [];
}>();

const store = useProviderStore();
const { showSuccess, showError } = useToast();

const loading = ref(false);

// Initial loading and error states
const initialLoading = ref(true);
const initialError = ref('');

// 密钥状态
const actualKey = ref('')

// Remove loadProviderDetail since it doesn't exist in API
// We'll just load the key
const keyVisible = ref(false);
const editing = ref(false);
const inputValue = ref('');
const inputVisible = ref(false);
const confirmDelete = ref(false);

// 编辑提供商弹窗状态（自身管理，对照设计 7.2）
const editDialogOpen = ref(false);

// Retry function for ErrorState
const handleRetry = async () => {
  initialLoading.value = true
  initialError.value = ''
  try {
    await loadKey()
  } catch (err) {
    initialError.value = String(err)
  } finally {
    initialLoading.value = false
  }
}

const entry = computed(() => store.activeProvider);

// 模板访问的非空入口（ProviderDetailView 仅在 activeProvider 存在时渲染，
// 这里用 unwrap + 兜底默认对象避免 vue-tsc 的 null 报错）
const entryView = computed(() => {
  return (
    entry.value || {
      id: '',
      provider: {} as any,
      keyConfigured: false,
    }
  );
});

/**
 * 编辑弹窗目标：当前详情页打开的 provider。
 * AddProviderDialog 在 editTarget 非空时进入编辑模式（设计 6.1 / 7.2）。
 * entry 为空时返回 null，弹窗退化为新增模式（实际不会触发，因详情页仅在 activeProvider 存在时渲染）。
 */
const editTarget = computed(() => {
  const e = entry.value;
  if (!e) return null;
  return { id: e.id, provider: e.provider };
});

function openEditDialog() {
  editDialogOpen.value = true;
}

/**
 * 编辑保存后回调：store.updateProvider 内部已处理 providers 列表刷新 +
 * activeProviderId 切换（改名时）+ 各引擎 presets 刷新（改名时）。
 * 这里仅补刷当前密钥显示（密钥可能已更新）+ 提示。
 */
async function handleEditSaved() {
  await loadKey();
  showSuccess('提供商已更新');
  emit('saved');
}

const hasAnthropic = computed(() => {
  const p = entry.value?.provider as any;
  return !!(p && p.anthropic && p.anthropic.enabled);
});
const hasOpenAI = computed(() => {
  const p = entry.value?.provider as any;
  return !!(p && p.openai && p.openai.enabled);
});
const anthropicBaseUrl = computed(() => (entry.value?.provider as any)?.anthropic?.base_url || '');
const openaiBaseUrl = computed(() => (entry.value?.provider as any)?.openai?.base_url || '');
const openaiOrg = computed(() => (entry.value?.provider as any)?.openai?.organization || '');

/** 当前有效 Base URL：优先 anthropic/openai 子块，回退 legacy base_url */
const effectiveBaseUrl = computed(() => {
  const p = entry.value?.provider;
  if (!p) return '';
  const a = (p as any).anthropic;
  const o = (p as any).openai;
  if (a && a.enabled && a.base_url) return a.base_url;
  if (o && o.enabled && o.base_url) return o.base_url;
  return p.base_url || '';
});

const typeLabel = computed(() => {
  const t = (entry.value?.provider as any)?.type || 'anthropic';
  return t.toUpperCase();
});
const typeInitial = computed(() => {
  const t = ((entry.value?.provider as any)?.type || 'anthropic').toLowerCase();
  return t === 'openai' ? 'O' : 'A';
});

// 照搬 legacy loadKeys：统一密钥 + legacy 格式 key 回退（仅用于显示）
async function loadKey() {
  const id = entry.value?.id;
  if (!id) return;
  actualKey.value = '';
  keyVisible.value = false;
  if (!entry.value?.keyConfigured) return;
  try {
    // 优先读统一 key
    if (await HasAPIKey(id)) {
      actualKey.value = await GetAPIKey(id);
      return;
    }
    // 兼容回退
    if (await HasAPIKey(id + ':anthropic')) {
      actualKey.value = await GetAPIKey(id + ':anthropic');
      return;
    }
    if (await HasAPIKey(id + ':openai')) {
      actualKey.value = await GetAPIKey(id + ':openai');
    }
  } catch (err) {
    console.error('[ProviderDetail.loadKey]', err);
  }
}

function startEdit() {
  editing.value = true;
  inputValue.value = '';
  inputVisible.value = false;
}
function cancelEdit() {
  editing.value = false;
  inputValue.value = '';
  inputVisible.value = false;
}

async function toggleKeyVisible() {
  if (!keyVisible.value) {
    await loadKey();
    keyVisible.value = true;
  } else {
    keyVisible.value = false;
  }
}

// applyKeyMask（对照 demo applyKeyMask + legacy maskedApiKey）：
// 未显示时返回固定掩码（保留末 4 位若有真实值），显示时返回真实密钥。
const keyMasked = computed(() => {
  if (!keyVisible.value) {
    const k = actualKey.value;
    if (!k) return '••••••••';
    return '••••••••' + k.slice(-4);
  }
  return actualKey.value || '••••••••';
});

async function saveApiKey() {
  const id = entry.value?.id;
  if (!id || !inputValue.value) return;
  loading.value = true;
  try {
    await SetAPIKey(id, inputValue.value);
    await SaveSecrets();
    // 清理 legacy 格式 key（best-effort）
    for (const suffix of [':anthropic', ':openai']) {
      try {
        await DeleteAPIKey(id + suffix);
      } catch {
        /* key may not exist */
      }
    }
    try {
      await SaveSecrets();
    } catch {
      /* ignore */
    }
    await store.loadProviders();
    await loadKey();
    cancelEdit();
    showSuccess('API 密钥已保存');
    emit('saved');
  } catch (err) {
    showError('保存失败: ' + err);
  } finally {
    loading.value = false;
  }
}

async function deleteApiKey() {
  const id = entry.value?.id;
  if (!id) return;
  loading.value = true;
  try {
    const keysToDelete = [id, id + ':anthropic', id + ':openai'];
    for (const key of keysToDelete) {
      try {
        await DeleteAPIKey(key);
      } catch {
        /* key may not exist */
      }
    }
    await SaveSecrets();
    actualKey.value = '';
    keyVisible.value = false;
    confirmDelete.value = false;
    await store.loadProviders();
    showSuccess('API 密钥已删除');
    emit('saved');
  } catch (err) {
    showError('删除失败: ' + err);
  } finally {
    loading.value = false;
  }
}

function onBack() {
  emit('back');
}

onMounted(async () => {
  initialLoading.value = true;
  initialError.value = '';
  try {
    await loadKey();
  } catch (err) {
    initialError.value = String(err);
  } finally {
    initialLoading.value = false;
  }
});

watch(
  () => entry.value?.id,
  () => {
    loadKey();
  }
);
</script>

<style scoped>
.pc-detail {
  padding: 18px 22px 24px;
  gap: 18px;
}

.pc-back {
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

.pc-back:hover {
  text-decoration: underline;
}

.ic {
  width: 16px;
  height: 16px;
  stroke: currentColor;
}

.detail-title-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.prov-name {
  font-size: 22px;
  font-weight: 600;
  color: var(--label);
  letter-spacing: -0.3px;
}

.prov-formats {
  display: flex;
  gap: 5px;
}

/* 编辑按钮：推到标题行最右侧，与格式徽章保持呼吸距离 */
.edit-btn {
  margin-left: auto;
}

/* A/O 格式徽章：A 紫 O 绿（非品牌色）*/
.fmt {
  width: 22px;
  height: 22px;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
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

.detail-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-top: 14px;
  border-top: 1px solid var(--separator);
}

.detail-section:first-of-type {
  padding-top: 0;
  border-top: none;
}

.detail-section h3 {
  margin: 0 0 4px;
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
}

.detail-row {
  display: flex;
  align-items: center;
  gap: 14px;
  font-size: 13px;
  padding: 4px 0;
}

.dr-label {
  color: var(--tertiary);
  font-size: 12px;
  flex-shrink: 0;
  min-width: 110px;
}

.dr-value {
  color: var(--secondary);
  flex: 1;
  word-break: break-all;
}

.dr-value.mono {
  font-family: var(--mono);
  font-size: 12px;
}

.dr-value.placeholder {
  color: var(--tertiary);
}

.key-status-inline {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.sess-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.key-value-cell {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.key-placeholder {
  color: var(--tertiary);
  font-size: 13px;
}

.key-mask {
  font-family: var(--mono);
  font-size: 13px;
  color: var(--secondary);
  letter-spacing: 0.5px;
}

.mono {
  font-family: var(--mono);
}

.key-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.key-input {
  flex: 1;
  min-width: 200px;
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 7px 11px;
  font-size: 13px;
  color: var(--label);
  font-family: var(--mono);
  outline: none;
}

.key-input:focus {
  border-color: var(--accent);
}

.confirm-text {
  font-size: 12px;
  color: var(--warning);
  font-weight: 600;
}

/* 格式块 */
.fmt-block-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.fmt-block-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 500;
  color: var(--label);
}

.fmt-field {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 6px 0;
  font-size: 13px;
}

.ff-label {
  color: var(--tertiary);
  font-size: 12px;
}

.ff-value {
  font-family: var(--mono);
  font-size: 12px;
  color: var(--secondary);
  word-break: break-all;
  text-align: right;
}

.compat-hint {
  margin: 0;
  font-size: 12px;
  color: var(--tertiary);
  line-height: 1.6;
}

/* ---- 按钮 ---- */
.btn-ghost,
.btn-primary,
.btn-danger {
  font-family: inherit;
  font-size: 13px;
  font-weight: 500;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.12s, opacity 0.12s;
  border: none;
}

.btn-ghost {
  background: var(--control);
  color: var(--label);
  padding: 7px 13px;
}

.btn-ghost:hover:not(:disabled) {
  background: var(--controlHover);
}

.btn-ghost.danger {
  color: var(--danger);
}

.btn-primary {
  background: var(--accent);
  color: #fff;
  padding: 7px 14px;
}

.btn-primary:hover:not(:disabled) {
  background: var(--accentHover);
}

.btn-danger {
  background: var(--danger);
  color: #fff;
  padding: 7px 14px;
}

.btn-danger:hover:not(:disabled) {
  opacity: 0.88;
}

.sm {
  padding: 4px 10px;
  font-size: 12px;
}

.btn-ghost:disabled,
.btn-primary:disabled,
.btn-danger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
