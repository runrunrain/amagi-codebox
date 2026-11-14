<!--
  VisualValueEditor - 递归可视化值编辑器（JSON 零暴露）。
  按运行时类型分发控件：string→TextInput、number→数字输入、boolean→Switch、
  纯字符串数组→StringListEditor、其他数组→逐项递归编辑、object→逐键递归编辑。
  全部可视化，不出现任何 JSON 文本框；类型在写回时保持。
-->
<template>
  <div class="vve-root">
    <!-- string -->
    <TextInput
      v-if="type === 'string'"
      :model-value="stringValue"
      mono
      class="vve-string"
      @update:model-value="$emit('update:modelValue', $event)"
    />

    <!-- number -->
    <input
      v-else-if="type === 'number'"
      type="number"
      class="vve-number"
      :value="numberValue"
      @input="onNumberInput(($event.target as HTMLInputElement).value)"
    />

    <!-- boolean -->
    <Switch
      v-else-if="type === 'boolean'"
      :model-value="booleanValue"
      @update:model-value="$emit('update:modelValue', $event)"
    />

    <!-- null -->
    <div v-else-if="type === 'null'" class="vve-null">
      <span class="vve-null-label">空值（null）</span>
      <AppButton variant="ghost" size="small" @click="$emit('update:modelValue', '')">设为字符串</AppButton>
      <AppButton variant="ghost" size="small" @click="$emit('update:modelValue', false)">设为布尔</AppButton>
    </div>

    <!-- 纯字符串数组 -->
    <StringListEditor
      v-else-if="type === 'stringArray'"
      :model-value="stringArrayValue"
      item-placeholder="列表项"
      add-label="添加项"
      empty-text="空列表"
      mono
      @update:model-value="$emit('update:modelValue', $event)"
    />

    <!-- 其他数组：逐项递归编辑 -->
    <div v-else-if="type === 'array'" class="vve-array">
      <div v-for="(item, idx) in arrayValue" :key="idx" class="vve-item">
        <span class="vve-index">{{ idx + 1 }}</span>
        <VisualValueEditor
          :model-value="item"
          class="vve-item-value"
          @update:model-value="updateArrayItem(idx, $event)"
        />
        <AppButton variant="icon" size="small" aria-label="删除项" @click="removeArrayItem(idx)">
          <span class="vve-remove">×</span>
        </AppButton>
      </div>
      <div class="vve-add-row">
        <AppButton variant="ghost" size="small" @click="addArrayItem('')">+ 字符串</AppButton>
        <AppButton variant="ghost" size="small" @click="addArrayItem(0)">+ 数字</AppButton>
        <AppButton variant="ghost" size="small" @click="addArrayItem(false)">+ 布尔</AppButton>
        <AppButton variant="ghost" size="small" @click="addArrayItem({})">+ 对象</AppButton>
      </div>
    </div>

    <!-- object：逐键递归编辑 -->
    <div v-else-if="type === 'object'" class="vve-object">
      <div v-for="entryKey in objectKeys" :key="entryKey" class="vve-entry">
        <TextInput
          :model-value="entryKey"
          mono
          placeholder="键名"
          class="vve-key"
          @update:model-value="renameKey(entryKey, $event)"
        />
        <VisualValueEditor
          :model-value="objectValue[entryKey]"
          class="vve-entry-value"
          @update:model-value="setKey(entryKey, $event)"
        />
        <AppButton variant="icon" size="small" aria-label="删除键" @click="removeKey(entryKey)">
          <span class="vve-remove">×</span>
        </AppButton>
      </div>
      <div class="vve-add-row">
        <AppButton variant="ghost" size="small" @click="addKey('')">+ 字符串</AppButton>
        <AppButton variant="ghost" size="small" @click="addKey(false)">+ 布尔</AppButton>
        <AppButton variant="ghost" size="small" @click="addKey(0)">+ 数字</AppButton>
        <AppButton variant="ghost" size="small" @click="addKey({})">+ 对象</AppButton>
      </div>
    </div>

    <!-- 兜底（undefined 等）：不渲染输入，仅提示 -->
    <div v-else class="vve-null">
      <span class="vve-null-label">未设置</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import TextInput from '../ui/TextInput.vue';
import Switch from '../ui/Switch.vue';
import AppButton from '../ui/AppButton.vue';
import StringListEditor from './StringListEditor.vue';

const props = defineProps<{
  /** 任意 JSON 值（类型在写回时保持） */
  modelValue: unknown;
}>();

const emit = defineEmits<{ 'update:modelValue': [value: unknown] }>();

const stringValue = computed<string>(() => (typeof props.modelValue === 'string' ? props.modelValue : ''));
const numberValue = computed<number>(() => (typeof props.modelValue === 'number' ? props.modelValue : 0));
const booleanValue = computed<boolean>(() => Boolean(props.modelValue));
const stringArrayValue = computed<string[]>(() =>
  Array.isArray(props.modelValue) ? props.modelValue.map((x) => String(x)) : []
);
const arrayValue = computed<unknown[]>(() => (Array.isArray(props.modelValue) ? props.modelValue : []));
const objectValue = computed<Record<string, unknown>>(() =>
  props.modelValue !== null && typeof props.modelValue === 'object' && !Array.isArray(props.modelValue)
    ? (props.modelValue as Record<string, unknown>)
    : {}
);

const type = computed<string>(() => {
  const v = props.modelValue;
  if (v === null) return 'null';
  if (typeof v === 'string') return 'string';
  if (typeof v === 'number') return 'number';
  if (typeof v === 'boolean') return 'boolean';
  if (Array.isArray(v)) {
    return v.every((x) => typeof x === 'string') ? 'stringArray' : 'array';
  }
  if (v && typeof v === 'object') return 'object';
  return 'other';
});

const objectKeys = computed<string[]>(() =>
  type.value === 'object' ? Object.keys(objectValue.value) : []
);

function onNumberInput(raw: string) {
  const n = raw === '' ? NaN : Number(raw);
  if (!Number.isNaN(n)) emit('update:modelValue', n);
}

function objectPatch(fn: (obj: Record<string, unknown>) => void) {
  const out: Record<string, unknown> = { ...objectValue.value };
  fn(out);
  emit('update:modelValue', out);
}

function setKey(key: string, value: unknown) {
  objectPatch((obj) => {
    obj[key] = value;
  });
}

function renameKey(oldKey: string, newKey: string) {
  const trimmed = newKey.trim();
  if (!trimmed || trimmed === oldKey) return;
  objectPatch((obj) => {
    obj[trimmed] = obj[oldKey];
    delete obj[oldKey];
  });
}

function removeKey(key: string) {
  objectPatch((obj) => {
    delete obj[key];
  });
}

function addKey(defaultValue: unknown) {
  objectPatch((obj) => {
    let k = 'new_key';
    let i = 1;
    while (k in obj) k = 'new_key_' + i++;
    obj[k] = defaultValue;
  });
}

function updateArrayItem(idx: number, value: unknown) {
  const out = [...arrayValue.value];
  out[idx] = value;
  emit('update:modelValue', out);
}

function removeArrayItem(idx: number) {
  const out = [...arrayValue.value];
  out.splice(idx, 1);
  emit('update:modelValue', out);
}

function addArrayItem(defaultValue: unknown) {
  emit('update:modelValue', [...arrayValue.value, defaultValue]);
}
</script>

<style scoped>
.vve-root {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}

.vve-string {
  width: 100%;
}

.vve-number {
  font-family: var(--mono);
  font-size: 13px;
  padding: 6px 10px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--control);
  color: var(--label);
  outline: none;
  width: 100%;
  max-width: 220px;
}

.vve-number:focus {
  border-color: var(--accent);
}

.vve-null {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.vve-null-label {
  font-size: 12px;
  color: var(--tertiary);
  font-family: var(--mono);
}

.vve-array,
.vve-object {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.vve-item,
.vve-entry {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  flex-wrap: wrap;
}

.vve-index {
  font-size: 12px;
  color: var(--tertiary);
  min-width: 18px;
  padding-top: 8px;
}

.vve-item-value,
.vve-entry-value {
  flex: 1;
  min-width: 220px;
}

.vve-key {
  width: 150px;
  flex-shrink: 0;
}

.vve-remove {
  font-size: 16px;
  line-height: 1;
  color: var(--tertiary);
}

.vve-add-row {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
</style>
