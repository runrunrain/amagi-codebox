<!--
  RawJsonEditor - 原始 JSON 编辑器（兜底）
  用于未识别顶层键（$schema 等）或复杂嵌套对象。
  parse 失败时不写回 modelValue，保留原值并红色提示。
-->
<template>
  <div class="raw-json-editor">
    <textarea
      v-model="text"
      class="rje-area"
      :class="{ invalid: !valid }"
      spellcheck="false"
      :placeholder="placeholder"
      @input="onInput"
    />
    <div v-if="!valid" class="rje-error">JSON 非法：{{ error }}</div>
    <div v-else class="rje-valid">JSON 合法</div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';

interface Props {
  modelValue: any;
  placeholder?: string;
}

const props = withDefaults(defineProps<Props>(), {
  modelValue: null,
  placeholder: '请输入合法 JSON',
});

const emit = defineEmits<{
  'update:modelValue': [value: any];
}>();

const text = ref<string>('');
const valid = ref<boolean>(true);
const error = ref<string>('');

function serialize(value: any): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return '';
  }
}

function onInput() {
  const trimmed = text.value.trim();
  if (!trimmed) {
    valid.value = true;
    error.value = '';
    emit('update:modelValue', null);
    return;
  }
  try {
    const parsed = JSON.parse(trimmed);
    valid.value = true;
    error.value = '';
    emit('update:modelValue', parsed);
  } catch (e) {
    // parse 失败：不写回 modelValue，保留原值，仅提示
    valid.value = false;
    error.value = (e as Error).message;
  }
}

// 外部 modelValue 变化时同步 text
watch(
  () => props.modelValue,
  (next) => {
    const serialized = serialize(next);
    // 仅在外部变化导致序列化文本不同时覆盖，避免自身 emit 回环
    const currentParsed = (() => {
      try {
        return JSON.parse(text.value.trim() || 'null');
      } catch {
        return undefined;
      }
    })();
    if (JSON.stringify(currentParsed) !== JSON.stringify(next === undefined ? null : next)) {
      text.value = serialized;
      valid.value = true;
      error.value = '';
    }
  },
  { immediate: true, deep: true },
);
</script>

<style scoped>
.raw-json-editor {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.rje-area {
  font-family: var(--mono);
  font-size: 12px;
  line-height: 1.55;
  color: var(--termText);
  background: var(--termBg);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 10px 12px;
  min-height: 80px;
  resize: vertical;
  outline: none;
  white-space: pre;
  overflow: auto;
}
.rje-area:focus {
  border-color: var(--accent);
}
.rje-area.invalid {
  border-color: var(--danger);
}
.rje-error {
  font-size: 11.5px;
  color: var(--danger);
}
.rje-valid {
  font-size: 11.5px;
  color: var(--success);
}
</style>
