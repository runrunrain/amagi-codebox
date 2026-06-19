<!--
  JsonEditorDialog - JSON 编辑器弹窗（对照旧 ProviderCenter + §8.2 项18）。
  用于 Provider 详情页的 JSON 编辑模式。
  表单：JSON 文本编辑区 + 语法状态提示 + 取消/保存。
  保存 → SaveProviderFromJSON。
-->
<template>
  <Dialog :open="open" title="编辑 JSON 配置" @update:open="handleClose">
    <div class="json-editor-form">
      <!-- 当前 Provider 名称提示 -->
      <div v-if="providerName" class="provider-name-hint">
        正在编辑: <code class="mono">{{ providerName }}</code>
      </div>

      <!-- JSON 编辑区 -->
      <div class="json-editor-wrapper">
        <textarea
          v-model="jsonContent"
          class="json-editor mono"
          spellcheck="false"
          placeholder='{ "anthropic": { "enabled": true, "base_url": "https://api.anthropic.com" }, "default_model": "claude-3-7-sonnet-20250219" }'
        ></textarea>

        <!-- 语法状态提示 -->
        <div class="json-status">
          <span v-if="!jsonContent" class="json-status-empty">JSON 为空</span>
          <span v-else-if="parseError" class="json-status-error">语法错误: {{ parseError }}</span>
          <span v-else class="json-status-valid">语法有效</span>
        </div>
      </div>

      <!-- 格式化按钮 -->
      <div class="json-actions">
        <AppButton variant="ghost" size="small" @click="formatJson" :disabled="!canFormat">
          格式化
        </AppButton>
        <AppButton variant="ghost" size="small" @click="minifyJson" :disabled="!canFormat">
          压缩
        </AppButton>
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
import { ref, computed, watch } from 'vue';
import { SaveProviderFromJSON } from '../../../wailsjs/go/main/App';
import Dialog from '../ui/Dialog.vue';
import AppButton from '../ui/AppButton.vue';

interface Props {
  open?: boolean;
  providerName?: string;
  initialJson?: string;
}

const props = withDefaults(defineProps<Props>(), {
  open: false,
  providerName: '',
  initialJson: '',
});

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void;
  (e: 'saved'): void;
}>();

const loading = ref(false);
const jsonContent = ref('');

// 解析错误
const parseError = computed(() => {
  const raw = jsonContent.value.trim();
  if (!raw) return '';
  try {
    JSON.parse(raw);
    return '';
  } catch (e) {
    return (e as Error).message;
  }
});

// 是否可以格式化
const canFormat = computed(() => {
  return !parseError.value && jsonContent.value.trim();
});

// 是否可以保存
const canSave = computed(() => {
  return !parseError.value && jsonContent.value.trim() && props.providerName;
});

// 监听初始 JSON 变化
watch(() => props.initialJson, (val) => {
  if (val) {
    try {
      const parsed = JSON.parse(val);
      jsonContent.value = JSON.stringify(parsed, null, 2);
    } catch {
      jsonContent.value = val;
    }
  }
}, { immediate: true });

function handleClose() {
  jsonContent.value = props.initialJson || '';
  emit('update:open', false);
}

function formatJson() {
  if (!canFormat.value) return;
  try {
    const parsed = JSON.parse(jsonContent.value);
    jsonContent.value = JSON.stringify(parsed, null, 2);
  } catch {
    // 忽略错误（parseError 已处理）
  }
}

function minifyJson() {
  if (!canFormat.value) return;
  try {
    const parsed = JSON.parse(jsonContent.value);
    jsonContent.value = JSON.stringify(parsed);
  } catch {
    // 忽略错误（parseError 已处理）
  }
}

async function handleSave() {
  if (!canSave.value) return;

  loading.value = true;
  try {
    await SaveProviderFromJSON(props.providerName, jsonContent.value);
    emit('saved');
    handleClose();
  } catch (err) {
    console.error('[JsonEditorDialog] 保存失败:', err);
  } finally {
    loading.value = false;
  }
}
</script>

<style scoped>
.json-editor-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.provider-name-hint {
  font-size: 12px;
  color: var(--tertiary);
  padding: 8px 12px;
  background: var(--sidebar);
  border-radius: 6px;
}

.mono {
  font-family: var(--mono);
}

.json-editor-wrapper {
  position: relative;
}

.json-editor {
  min-height: 240px;
  max-height: 360px;
  width: 100%;
  padding: 12px;
  font-size: 12px;
  color: var(--label);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  outline: none;
  transition: border-color 0.15s ease;
  font-family: var(--mono);
  resize: vertical;
  line-height: 1.5;
}

.json-editor:focus {
  border-color: var(--accent);
}

.json-status {
  position: absolute;
  bottom: 8px;
  right: 12px;
  font-size: 11px;
  padding: 4px 8px;
  border-radius: 4px;
  pointer-events: none;
}

.json-status-empty {
  color: var(--tertiary);
  background: var(--sidebar);
}

.json-status-error {
  color: #FF3B30;
  background: rgba(255, 59, 48, 0.1);
}

.json-status-valid {
  color: #34C759;
  background: rgba(52, 199, 89, 0.1);
}

.json-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
