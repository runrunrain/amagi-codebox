<!--
  AddProviderDialog - 添加提供商弹窗（对照旧 ProviderCenter + §8.2 项14）。
  表单：支持格式(Anthropic A / OpenAI O 多选)、名称、Base URL、默认模型、API Key。
  保存 → SaveProvider + SetAPIKey → 刷新列表。
-->
<template>
  <Dialog :open="open" title="添加提供商" @update:open="handleClose">
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
          placeholder="sk-..."
        />
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
import { ref, reactive, computed } from 'vue';
import { config } from '../../../wailsjs/go/models';
import { SaveProvider } from '../../../wailsjs/go/config/ConfigService';
import { SetAPIKey, Save as SaveSecrets } from '../../../wailsjs/go/secrets/SecretsService';
import Dialog from '../ui/Dialog.vue';
import AppButton from '../ui/AppButton.vue';

interface Props {
  open?: boolean;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void;
  (e: 'saved'): void;
}>();

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

const canSave = computed(() => {
  return (
    form.name.trim() &&
    (form.supportsAnthropic || form.supportsOpenAI)
  );
});

function resetForm() {
  form.name = '';
  form.supportsAnthropic = true;
  form.supportsOpenAI = false;
  form.anthropicBaseUrl = '';
  form.openaiBaseUrl = '';
  form.defaultModel = '';
  form.apiKey = '';
}

function handleClose() {
  resetForm();
  emit('update:open', false);
}

async function handleSave() {
  if (!canSave.value) return;

  loading.value = true;
  try {
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

    // 刷新列表
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
