<!--
  AddProviderDialog - 通用「新增 / 编辑」提供商弹窗。
  - 新增模式（editTarget 为空）：原 SaveProvider + SetAPIKey 逻辑，行为保持不变。
  - 编辑模式（editTarget 存在）：标题切换为「编辑提供商」、字段回填、名称可改；
    提交时构造 ExportProvider JSON（api_key 空 = 保持不变，presets 原样保留）
    → store.updateProvider(oldId, newName, json)。
  详见设计文档第六节 + 鲁班后端实现报告第六节（JSON 契约）。
-->
<template>
  <Dialog :open="open" :title="title" @update:open="handleClose">
    <div class="add-provider-form">
      <!-- 支持格式 -->
      <div class="form-group">
        <label class="form-label">支持格式</label>
        <div class="format-selector">
          <label class="format-checkbox" :class="{ active: form.supportsAnthropic }">
            <input type="checkbox" v-model="form.supportsAnthropic" />
            <span>Anthropic</span>
          </label>
          <label class="format-checkbox" :class="{ active: form.supportsOpenAI }">
            <input type="checkbox" v-model="form.supportsOpenAI" />
            <span>OpenAI</span>
          </label>
        </div>
        <p v-if="!form.supportsAnthropic && !form.supportsOpenAI" class="form-warning">
          至少启用一种 Provider 格式
        </p>
      </div>

      <!-- 名称 -->
      <div class="form-group">
        <label class="form-label">名称（唯一标识）</label>
        <input
          v-model="form.name"
          type="text"
          class="form-input"
          placeholder="例如: anthropic, openai"
        />
        <p v-if="nameError" class="form-warning">{{ nameError }}</p>
      </div>

      <!-- Anthropic Base URL -->
      <div v-if="form.supportsAnthropic" class="form-group">
        <label class="form-label">Anthropic Base URL</label>
        <input
          v-model="form.anthropicBaseUrl"
          type="text"
          class="form-input mono"
          placeholder="https://api.anthropic.com"
        />
      </div>

      <!-- OpenAI Base URL -->
      <div v-if="form.supportsOpenAI" class="form-group">
        <label class="form-label">OpenAI Base URL</label>
        <input
          v-model="form.openaiBaseUrl"
          type="text"
          class="form-input mono"
          placeholder="https://api.openai.com/v1"
        />
      </div>

      <!-- 默认模型 -->
      <div class="form-group">
        <label class="form-label">默认模型</label>
        <input
          v-model="form.defaultModel"
          type="text"
          class="form-input mono"
          :placeholder="form.supportsOpenAI && !form.supportsAnthropic ? 'o3' : 'claude-3-7-sonnet-20250219'"
        />
      </div>

      <!-- API Key -->
      <div class="form-group">
        <label class="form-label">API Key</label>
        <input
          v-model="form.apiKey"
          type="password"
          class="form-input mono"
          :placeholder="apiKeyPlaceholder"
        />
        <p v-if="isEditing" class="form-hint">{{ apiKeyHint }}</p>
      </div>
    </div>

    <template #footer>
      <div class="dialog-actions">
        <AppButton variant="ghost" @click="handleClose" :disabled="loading">取消</AppButton>
        <AppButton
          variant="primary"
          @click="handleSave"
          :disabled="!canSave || loading"
        >{{ loading ? '保存中...' : '保存' }}</AppButton>
      </div>
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue';
import { config } from '../../../wailsjs/go/models';
import { SaveProvider } from '../../../wailsjs/go/config/ConfigService';
import { SetAPIKey, Save as SaveSecrets } from '../../../wailsjs/go/secrets/SecretsService';
import { getProviderExportJSON } from '../../api/provider';
import { useProviderStore } from '../../stores/provider';
import Dialog from '../ui/Dialog.vue';
import AppButton from '../ui/AppButton.vue';

interface EditTarget {
  /** 当前 provider name（map key），即后端 UpdateProvider 的 oldName */
  id: string;
  /** 完整 Provider 对象，用于回填表单 */
  provider: config.Provider;
}

interface Props {
  open?: boolean;
  /** 编辑模式目标；新增模式不传或为 null */
  editTarget?: EditTarget | null;
}

const props = withDefaults(defineProps<Props>(), {
  open: false,
  editTarget: null,
});

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void;
  (e: 'saved'): void;
}>();

const store = useProviderStore();
const loading = ref(false);

const form = reactive({
  name: '',
  supportsAnthropic: true,
  supportsOpenAI: false,
  anthropicBaseUrl: '',
  openaiBaseUrl: '',
  defaultModel: '',
  apiKey: '',
});

/**
 * 编辑模式缓存：打开时通过 getProviderExportJSON 拉取的完整 ExportProvider 对象。
 * 用于提交时原样保留 presets（避免覆盖清空 legacy presets）+ organization 等非表单字段。
 * 新增模式下保持 null。
 */
const editExportCache = ref<any>(null);

// ---- 模式与动态文案 ----

const isEditing = computed(() => !!props.editTarget);

const title = computed(() => (isEditing.value ? '编辑提供商' : '添加提供商'));

/** API Key 输入框 placeholder：编辑模式根据密钥状态切换提示 */
const apiKeyPlaceholder = computed(() => {
  if (!isEditing.value) return 'sk-...';
  const oldId = props.editTarget?.id;
  if (oldId && store.keyStatus[oldId]) {
    return '留空保持当前密钥不变';
  }
  return '请输入 API Key';
});

/** API Key 下方提示文案（仅编辑模式） */
const apiKeyHint = computed(() => {
  const oldId = props.editTarget?.id;
  if (oldId && store.keyStatus[oldId]) {
    return '留空保持当前密钥不变；填写则更新为新密钥';
  }
  return '尚未配置密钥，填写后将被保存';
});

// ---- 校验 ----

/**
 * 名称校验（与后端 ConfigService.RenameProvider 对齐）：
 * - 非空
 * - 不含 `/`（破坏 stable key 结构）
 * - TrimSpace 后与原值一致（无前后空白）
 * - 改名时检查 newName 不被其他 provider 占用（前端提前拦截，避免后端拒绝）
 */
const nameError = computed<string>(() => {
  const raw = form.name;
  const trimmed = raw.trim();
  if (!trimmed) return '名称不能为空';
  if (raw !== trimmed) return '名称前后不能包含空白';
  if (trimmed.includes('/')) return "名称不能包含 '/'";
  // 编辑模式改名冲突检测（新增模式由后端 upsert 语义处理，不在此拦截）
  if (isEditing.value && props.editTarget && trimmed !== props.editTarget.id) {
    if (store.providers[trimmed]) {
      return '该名称已被其他提供商占用';
    }
  }
  return '';
});

const canSave = computed(() => {
  if (!(form.supportsAnthropic || form.supportsOpenAI)) return false;
  return !nameError.value;
});

// ---- 表单重置与回填 ----

function resetForm() {
  form.name = '';
  form.supportsAnthropic = true;
  form.supportsOpenAI = false;
  form.anthropicBaseUrl = '';
  form.openaiBaseUrl = '';
  form.defaultModel = '';
  form.apiKey = '';
}

/**
 * 编辑模式：从 editTarget 回填表单，并拉取 ExportProvider JSON 缓存。
 * 先用 provider 对象同步填充（避免 UI 闪烁），再异步拉取 export JSON（保留 presets）。
 * export JSON 拉取失败时，用 provider 对象构造兜底缓存（presets 从 provider.presets 取）。
 */
async function fillFromEditTarget() {
  if (!props.editTarget) {
    resetForm();
    editExportCache.value = null;
    return;
  }
  const { id, provider } = props.editTarget;
  const p = provider as any;

  // 同步初始回填（即时，无闪烁）
  form.name = id;
  form.supportsAnthropic = !!(p.anthropic && p.anthropic.enabled);
  form.supportsOpenAI = !!(p.openai && p.openai.enabled);
  form.anthropicBaseUrl = p.anthropic?.base_url || '';
  form.openaiBaseUrl = p.openai?.base_url || '';
  form.defaultModel = p.default_model || '';
  form.apiKey = ''; // 编辑模式始终清空，靠 placeholder 提示"留空保持不变"

  // 异步拉取 ExportProvider JSON，用于提交时原样保留 presets + organization
  try {
    const json = await getProviderExportJSON(id);
    editExportCache.value = JSON.parse(json);
  } catch (err) {
    console.error('[AddProviderDialog] getProviderExportJSON 失败，使用 provider 对象兜底', err);
    editExportCache.value = {
      anthropic: p.anthropic ? { ...p.anthropic } : undefined,
      openai: p.openai ? { ...p.openai } : undefined,
      default_model: p.default_model || '',
      presets: p.presets || {},
      api_key: '',
    };
  }
}

/**
 * 监听弹窗打开：进入时根据模式回填或重置。
 * 只在 open 由 false→true 时触发，避免重复请求。
 */
watch(
  () => props.open,
  async (isOpen, wasOpen) => {
    if (isOpen && !wasOpen) {
      if (props.editTarget) {
        await fillFromEditTarget();
      } else {
        resetForm();
        editExportCache.value = null;
      }
    }
  }
);

function handleClose() {
  resetForm();
  editExportCache.value = null;
  emit('update:open', false);
}

// ---- ExportProvider JSON 构造（编辑模式）----

/**
 * 将表单编辑结果合并到缓存的 ExportProvider 对象上：
 * - default_model：直接覆盖
 * - anthropic / openai：enabled + base_url 跟随表单；未勾选时保留原块并置 enabled=false
 *   （保留 base_url 便于重新勾选，不丢失数据）；organization 等非表单字段原样保留
 * - api_key：表单空 = 保持不变（后端走迁移旧密钥分支），非空 = 更新
 * - presets：原样保留（来自 editExportCache，已在 fillFromEditTarget 拉取）
 */
function applyFormToExport(exportObj: any): any {
  const next = { ...exportObj };

  next.default_model = form.defaultModel || '';

  // Anthropic 块
  if (form.supportsAnthropic) {
    const existing = next.anthropic || {};
    next.anthropic = {
      ...existing,
      enabled: true,
      base_url: form.anthropicBaseUrl || existing.base_url || '',
    };
  } else if (next.anthropic) {
    next.anthropic = { ...next.anthropic, enabled: false };
  }

  // OpenAI 块（保留 organization）
  if (form.supportsOpenAI) {
    const existing = next.openai || {};
    next.openai = {
      ...existing,
      enabled: true,
      base_url: form.openaiBaseUrl || existing.base_url || '',
    };
  } else if (next.openai) {
    next.openai = { ...next.openai, enabled: false };
  }

  // API Key：空 = 不变（契约），非空 = 更新
  next.api_key = form.apiKey.trim();

  return next;
}

// ---- 提交 ----

async function handleSave() {
  if (!canSave.value) return;

  loading.value = true;
  try {
    const newName = form.name.trim();

    if (!props.editTarget) {
      // —— 新增模式（保持原逻辑不变，避免回归）——
      // 构建 Provider 对象（照搬旧 ProviderCenter 逻辑）
      const provider = new config.Provider();
      provider.default_model = form.defaultModel || '';

      if (form.supportsAnthropic) {
        provider.anthropic = {
          enabled: true,
          base_url: form.anthropicBaseUrl || undefined,
        };
      }
      if (form.supportsOpenAI) {
        provider.openai = {
          enabled: true,
          base_url: form.openaiBaseUrl || undefined,
        };
      }

      // 保存 Provider
      await SaveProvider(form.name, provider);

      // 保存 API Key（如果提供）—— SetAPIKey 仅写入内存 cache，必须追加 Save() 持久化
      if (form.apiKey.trim()) {
        await SetAPIKey(form.name, form.apiKey.trim());
        await SaveSecrets();
      }
    } else {
      // —— 编辑模式：统一调 store.updateProvider ——
      const oldId = props.editTarget.id;
      // 编辑模式提交前再次校验名称冲突（防止缓存期间状态变化）
      if (newName !== oldId && store.providers[newName]) {
        throw new Error('该名称已被其他提供商占用');
      }
      // 构造 ExportProvider JSON：基于缓存（保留 presets + organization），应用表单编辑
      const exportObj = applyFormToExport(editExportCache.value || {});
      const providerJSON = JSON.stringify(exportObj);
      await store.updateProvider(oldId, newName, providerJSON);
    }

    // 刷新列表（编辑模式由 store.updateProvider 内部处理刷新 + activeProviderId 切换）
    emit('saved');
    handleClose();
  } catch (err) {
    console.error('[AddProviderDialog] 保存失败:', err);
  } finally {
    loading.value = false;
  }
}
</script>

<style scoped>
.add-provider-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

.form-input {
  height: 34px;
  padding: 0 10px;
  font-size: 13px;
  color: var(--label);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  outline: none;
  transition: border-color 0.15s ease;
  font-family: inherit;
}

.form-input:focus {
  border-color: var(--accent);
}

.form-input.mono {
  font-family: var(--mono);
}

.form-warning {
  margin: 0;
  font-size: 12px;
  color: #FF3B30;
}

.form-hint {
  margin: 0;
  font-size: 12px;
  color: var(--tertiary);
}

.format-selector {
  display: flex;
  gap: 8px;
}

.format-checkbox {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  font-size: 13px;
  color: var(--secondary);
  cursor: pointer;
  transition: all 0.15s ease;
  user-select: none;
}

.format-checkbox.active {
  background: rgba(0, 122, 255, 0.1);
  border-color: var(--accent);
  color: var(--accent);
}

.format-checkbox input {
  display: none;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
