<!--
  OpenCodePresetDialog - OpenCode 预设弹窗（对照旧 ProviderCenter + §8.2 项16）。
  表单：预设 Key（新增时）、名称、描述、JSON 配置、Provider 绑定。
  保存 → SaveOpenCodePreset → 刷新 OpenCodePresets。
-->
<template>
  <Dialog :open="open" :title="title" @update:open="handleClose">
    <div class="opencode-preset-form">
      <!-- 预设 Key（仅新增时显示） -->
      <div v-if="!isEditing" class="form-group">
        <label class="form-label">预设 Key（唯一标识）</label>
        <input
          v-model="form.key"
          type="text"
          class="form-input mono"
          placeholder="例如: my-preset"
        />
      </div>

      <!-- 名称 -->
      <div class="form-group">
        <label class="form-label">名称</label>
        <input
          v-model="form.name"
          type="text"
          class="form-input"
          placeholder="预设显示名称"
        />
      </div>

      <!-- 描述 -->
      <div class="form-group">
        <label class="form-label">描述</label>
        <input
          v-model="form.description"
          type="text"
          class="form-input"
          placeholder="可选描述"
        />
      </div>

      <!-- opencode.json 配置（JSON） -->
      <div class="form-group">
        <label class="form-label">opencode.json 配置（JSON）</label>
        <textarea
          v-model="configJson"
          class="form-textarea mono"
          rows="10"
          spellcheck="false"
          placeholder='{ "model": "openai/gpt-4o" }'
        ></textarea>
        <p v-if="configError" class="form-error">{{ configError }}</p>
      </div>

      <!-- Provider 绑定 -->
      <div class="form-group">
        <label class="form-label">Provider 绑定</label>
        <p class="form-hint">
          将 opencode.json 中的 provider ID 映射到本地已配置的 Provider。
        </p>
        <div v-if="bindings.length === 0" class="bindings-empty">
          暂无绑定，点击下方按钮添加
        </div>
        <div v-for="(binding, idx) in bindings" :key="idx" class="binding-row">
          <div class="binding-kv-row">
            <input
              v-model="binding.providerId"
              type="text"
              class="form-input binding-key"
              placeholder="OpenCode provider ID"
            />
            <select v-model="binding.localProvider" class="form-input binding-value">
              <option value="">（无映射）</option>
              <option v-for="(_, pName) in store.providers" :key="pName" :value="pName">{{ pName }}</option>
            </select>
            <button class="remove-btn" @click="removeBinding(idx)" title="删除">×</button>
          </div>
          <div class="binding-options-row">
            <select v-model="binding.format" class="form-input" style="width: 120px;">
              <option value="">自动</option>
              <option value="openai">OpenAI</option>
              <option value="anthropic">Anthropic</option>
            </select>
            <label class="binding-checkbox">
              <input type="checkbox" v-model="binding.injectApiKey" />
              <span>apiKey</span>
            </label>
            <label class="binding-checkbox">
              <input type="checkbox" v-model="binding.injectBaseURL" />
              <span>baseURL</span>
            </label>
            <label class="binding-checkbox">
              <input type="checkbox" v-model="binding.injectOrganization" />
              <span>organization</span>
            </label>
          </div>
        </div>
        <AppButton variant="ghost" size="small" @click="addBinding">+ 添加绑定</AppButton>
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
import { SaveOpenCodePreset } from '../../../wailsjs/go/config/ConfigService';
import { useProviderStore } from '../../stores/provider';
import Dialog from '../ui/Dialog.vue';
import AppButton from '../ui/AppButton.vue';

interface Props {
  open?: boolean;
  preset?: config.OpenCodePreset | null;
}

const props = withDefaults(defineProps<Props>(), {
  open: false,
  preset: null,
});

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void;
  (e: 'saved'): void;
}>();

const store = useProviderStore();
const loading = ref(false);

// Provider 绑定接口
interface Binding {
  providerId: string;
  localProvider: string;
  format: string;
  injectApiKey: boolean;
  injectBaseURL: boolean;
  injectOrganization: boolean;
}

// 是否编辑模式
const isEditing = computed(() => !!props.preset);

// 标题
const title = computed(() => (isEditing.value ? '编辑' : '添加') + ' OpenCode 预设');

const form = reactive({
  key: '',
  name: '',
  description: '',
});

const configJson = ref('');
const configError = ref('');

const bindings = ref<Binding[]>([]);

const canSave = computed(() => {
  return (form.key || isEditing.value) && form.name && !configError.value;
});

// 解析 JSON 并验证
const parsedConfig = computed(() => {
  const raw = configJson.value.trim();
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
});

// 监听 JSON 变化，更新错误状态
watch(configJson, () => {
  const raw = configJson.value.trim();
  if (!raw) {
    configError.value = '';
    return;
  }
  try {
    JSON.parse(raw);
    configError.value = '';
  } catch (e) {
    configError.value = 'JSON 格式错误: ' + (e as Error).message;
  }
});

// 初始化表单（编辑时）
function initForm() {
  if (props.preset) {
    const p = props.preset;
    form.key = p.id || '';
    form.name = p.name || '';
    form.description = p.description || '';

    // 配置 JSON（从 config 数组反序列化）
    if (p.config && p.config.length > 0) {
      try {
        const str = new TextDecoder().decode(new Uint8Array(p.config));
        configJson.value = JSON.stringify(JSON.parse(str), null, 2);
      } catch {
        configJson.value = '';
      }
    }

    // 绑定
    if (p.bindings) {
      bindings.value = Object.entries(p.bindings).map(([providerId, binding]) => ({
        providerId,
        localProvider: binding.local_provider,
        format: binding.format || '',
        injectApiKey: binding.inject?.includes('apiKey') || false,
        injectBaseURL: binding.inject?.includes('baseURL') || false,
        injectOrganization: binding.inject?.includes('organization') || false,
      }));
    } else {
      bindings.value = [];
    }
  } else {
    resetForm();
  }
}

function resetForm() {
  form.key = '';
  form.name = '';
  form.description = '';
  configJson.value = '';
  configError.value = '';
  bindings.value = [];
}

// 监听 preset 变化
watch(() => props.preset, initForm, { immediate: true });

function addBinding() {
  bindings.value.push({
    providerId: '',
    localProvider: '',
    format: '',
    injectApiKey: true,
    injectBaseURL: true,
    injectOrganization: false,
  });
}

function removeBinding(idx: number) {
  bindings.value.splice(idx, 1);
}

function handleClose() {
  resetForm();
  emit('update:open', false);
}

async function handleSave() {
  if (!canSave.value) return;

  loading.value = true;
  try {
    const preset = new config.OpenCodePreset();

    // 基本信息
    const key = form.key || (isEditing.value && props.preset?.id ? props.preset.id : '');
    preset.id = key;
    preset.name = form.name;
    preset.description = form.description || undefined;

    // 配置 JSON（序列化为字节数组）
    if (parsedConfig.value) {
      const str = JSON.stringify(parsedConfig.value);
      const uint8Array = new TextEncoder().encode(str);
      preset.config = Array.from(uint8Array);
    }

    // 绑定
    if (bindings.value.length > 0) {
      const bindingMap: Record<string, config.OpenCodeBinding> = {};
      for (const b of bindings.value) {
        if (!b.providerId) continue;
        const inject: string[] = [];
        if (b.injectApiKey) inject.push('apiKey');
        if (b.injectBaseURL) inject.push('baseURL');
        if (b.injectOrganization) inject.push('organization');
        bindingMap[b.providerId] = {
          local_provider: b.localProvider,
          format: b.format || undefined,
          inject: inject.length > 0 ? inject : undefined,
        };
      }
      preset.bindings = bindingMap;
    }

    // 保存
    await SaveOpenCodePreset(key, preset);

    emit('saved');
    handleClose();
  } catch (err) {
    console.error('[OpenCodePresetDialog] 保存失败:', err);
  } finally {
    loading.value = false;
  }
}
</script>

<style scoped>
.opencode-preset-form {
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

.form-textarea {
  min-height: 160px;
  padding: 10px;
  font-size: 12px;
  color: var(--label);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  outline: none;
  transition: border-color 0.15s ease;
  font-family: var(--mono);
  resize: vertical;
}

.form-textarea:focus {
  border-color: var(--accent);
}

.form-error {
  margin: 4px 0 0 0;
  font-size: 12px;
  color: #FF3B30;
}

.form-hint {
  margin: 0;
  font-size: 12px;
  color: var(--tertiary);
}

.bindings-empty {
  padding: 16px;
  text-align: center;
  color: var(--tertiary);
  font-size: 13px;
  background: var(--sidebar);
  border-radius: 8px;
}

.binding-row {
  padding: 12px;
  background: var(--sidebar);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.binding-kv-row {
  display: flex;
  gap: 8px;
  align-items: center;
}

.binding-key {
  flex: 1;
}

.binding-value {
  flex: 1;
}

.remove-btn {
  width: 24px;
  height: 24px;
  border: none;
  background: var(--control);
  border-radius: 4px;
  color: var(--secondary);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  transition: all 0.15s ease;
}

.remove-btn:hover {
  background: var(--controlHover);
  color: var(--label);
}

.binding-options-row {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}

.binding-checkbox {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--secondary);
  cursor: pointer;
  user-select: none;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
