<!--
  TypedKeyValueEditor - 类型保持的 object 编辑器
  用于 opencode.json 中 plugin / experimental 等 object map 字段。
  关键：按运行时 typeof 选控件，写回保持原始类型（boolean→Switch、number→NumberInput、
  string→TextInput/MaskedValue、object/array→RawJsonEditor）。绝不用 String() 强转。
-->
<template>
  <div class="typed-kv-editor">
    <div v-if="!entries.length" class="tke-empty">
      <span class="tke-empty-text">{{ emptyText }}</span>
    </div>
    <div v-for="entry in entries" :key="entry.id" class="tke-row">
      <TextInput
        :model-value="entry.key"
        placeholder="键名"
        class="tke-key"
        @update:model-value="updateKey(entry.id, $event)"
      />
      <div class="tke-value">
        <!-- boolean -->
        <Switch
          v-if="typeof entry.value === 'boolean'"
          :model-value="entry.value"
          @update:model-value="updateValue(entry.id, $event)"
        />
        <!-- number -->
        <input
          v-else-if="typeof entry.value === 'number'"
          type="number"
          class="tke-number"
          :value="entry.value"
          @input="onNumberInput(entry.id, ($event.target as HTMLInputElement).value)"
        />
        <!-- string（敏感词遮蔽） -->
        <MaskedValue
          v-else-if="typeof entry.value === 'string' && isSensitiveKey(entry.key)"
          :value="entry.value"
        />
        <!-- string（普通） -->
        <TextInput
          v-else-if="typeof entry.value === 'string'"
          :model-value="entry.value"
          placeholder="字符串值"
          class="tke-text"
          @update:model-value="updateValue(entry.id, $event)"
        />
        <!-- object / array -->
        <RawJsonEditor
          v-else
          :model-value="entry.value"
          class="tke-json"
          @update:model-value="updateValue(entry.id, $event)"
        />
      </div>
      <AppButton variant="icon" size="small" @click="removeEntry(entry.id)" aria-label="删除">
        <span class="tke-remove">×</span>
      </AppButton>
    </div>
    <div class="tke-actions">
      <span class="tke-add-label">新增类型：</span>
      <AppButton variant="ghost" size="small" @click="addEntry('string')">字符串</AppButton>
      <AppButton variant="ghost" size="small" @click="addEntry('boolean')">布尔</AppButton>
      <AppButton variant="ghost" size="small" @click="addEntry('number')">数字</AppButton>
      <AppButton variant="ghost" size="small" @click="addEntry('object')">对象</AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import TextInput from '../ui/TextInput.vue';
import Switch from '../ui/Switch.vue';
import AppButton from '../ui/AppButton.vue';
import MaskedValue from '../ui/MaskedValue.vue';
import RawJsonEditor from './RawJsonEditor.vue';

interface Props {
  modelValue: Record<string, any>;
  emptyText?: string;
}

const props = withDefaults(defineProps<Props>(), {
  modelValue: () => ({}),
  emptyText: '暂无键值对',
});

const emit = defineEmits<{
  'update:modelValue': [value: Record<string, any>];
}>();

interface Entry {
  id: string;
  key: string;
  value: any;
}

function genId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return 'id-' + Math.random().toString(36).slice(2) + Date.now().toString(36);
}

const entries = ref<Entry[]>([]);

function isSensitiveKey(key: string): boolean {
  return /apiKey|token|secret|authorization|password/i.test(key);
}

function syncFromModel() {
  const obj = props.modelValue && typeof props.modelValue === 'object' ? props.modelValue : {};
  const next = Object.entries(obj).map(([k, v]) => {
    const exist = entries.value.find((e) => e.key === k);
    return { id: exist ? exist.id : genId(), key: k, value: v };
  });
  entries.value = next;
}

function emitAll() {
  const out: Record<string, any> = {};
  for (const e of entries.value) {
    if (e.key === '') continue; // 暂跳过空键
    out[e.key] = e.value;
  }
  emit('update:modelValue', out);
}

function updateKey(id: string, newKey: string) {
  const entry = entries.value.find((e) => e.id === id);
  if (entry) {
    entry.key = newKey;
    emitAll();
  }
}
function updateValue(id: string, newValue: any) {
  const entry = entries.value.find((e) => e.id === id);
  if (entry) {
    entry.value = newValue;
    emitAll();
  }
}
function onNumberInput(id: string, raw: string) {
  const n = raw === '' ? NaN : Number(raw);
  if (!Number.isNaN(n)) updateValue(id, n);
}
function removeEntry(id: string) {
  entries.value = entries.value.filter((e) => e.id !== id);
  emitAll();
}
function addEntry(type: 'string' | 'boolean' | 'number' | 'object') {
  const defaults: Record<string, any> = {
    string: '',
    boolean: false,
    number: 0,
    object: {},
  };
  // 新键名：默认 key_N 防止重名
  let k = 'new_key';
  let i = 1;
  while (entries.value.some((e) => e.key === k)) {
    k = 'new_key_' + i++;
  }
  entries.value.push({ id: genId(), key: k, value: defaults[type] });
  emitAll();
}

watch(
  () => props.modelValue,
  (next) => {
    const obj = next && typeof next === 'object' ? next : {};
    const cur: Record<string, any> = {};
    for (const e of entries.value) {
      if (e.key !== '') cur[e.key] = e.value;
    }
    // 简单相等检查；仅在长度或键集合不同时刷新
    const nextKeys = Object.keys(obj);
    const curKeys = Object.keys(cur);
    const sameKeys =
      nextKeys.length === curKeys.length && nextKeys.every((k) => curKeys.includes(k));
    const sameValues = sameKeys && nextKeys.every((k) => obj[k] === cur[k]);
    if (!sameValues) {
      syncFromModel();
    }
  },
  { immediate: true, deep: true },
);
</script>

<style scoped>
.typed-kv-editor {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.tke-empty {
  color: var(--tertiary);
  font-size: 12px;
  padding: 6px 0;
}
.tke-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}
.tke-key {
  width: 160px;
  flex-shrink: 0;
}
.tke-value {
  flex: 1;
  display: flex;
  align-items: center;
  min-width: 0;
}
.tke-text,
.tke-json {
  flex: 1;
}
.tke-number {
  font-family: var(--mono);
  font-size: 13px;
  padding: 6px 10px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--control);
  color: var(--label);
  outline: none;
  width: 100%;
  min-width: 0;
}
.tke-number:focus {
  border-color: var(--accent);
}
.tke-remove {
  font-size: 16px;
  color: var(--danger);
  line-height: 1;
}
.tke-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 4px;
}
.tke-add-label {
  font-size: 12px;
  color: var(--tertiary);
}
</style>
