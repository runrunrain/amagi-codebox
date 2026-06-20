<!--
  StringListEditor - array<string> 字符串列表编辑器
  用于 opencode.json 中 instructions / mcp.args 等 array<string> 字段。
  保持类型：永远是 string[]，绝不退化为 object。
-->
<template>
  <div class="string-list-editor">
    <div v-if="!items.length" class="sle-empty">
      <span class="sle-empty-text">{{ emptyText }}</span>
    </div>
    <div v-for="(item, idx) in items" :key="ids[idx]" class="sle-row">
      <span class="sle-index">{{ idx + 1 }}</span>
      <TextInput
        :model-value="item"
        :placeholder="itemPlaceholder"
        :mono="mono"
        class="sle-input"
        @update:model-value="updateItem(idx, $event)"
      />
      <AppButton variant="icon" size="small" @click="removeItem(idx)" aria-label="删除">
        <span class="sle-remove">×</span>
      </AppButton>
    </div>
    <div class="sle-actions">
      <AppButton variant="ghost" size="small" @click="addItem">+ {{ addLabel }}</AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import TextInput from '../ui/TextInput.vue';
import AppButton from '../ui/AppButton.vue';

interface Props {
  /** 真实 array<string>。空值按 [] 处理，绝不写空对象。 */
  modelValue: string[];
  itemPlaceholder?: string;
  addLabel?: string;
  emptyText?: string;
  mono?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  modelValue: () => [],
  itemPlaceholder: '请输入字符串',
  addLabel: '添加项',
  emptyText: '暂无条目',
  mono: false,
});

const emit = defineEmits<{
  'update:modelValue': [value: string[]];
}>();

// 稳定内部 id，避免 v-for index 抖动
const ids = ref<string[]>([]);
function ensureIds(len: number) {
  while (ids.value.length < len) ids.value.push(genId());
  if (ids.value.length > len) ids.value.length = len;
}
function genId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return 'id-' + Math.random().toString(36).slice(2) + Date.now().toString(36);
}

const items = ref<string[]>(Array.isArray(props.modelValue) ? [...props.modelValue] : []);

function emitAll() {
  emit('update:modelValue', [...items.value]);
}

function updateItem(idx: number, value: string) {
  items.value[idx] = value;
  emitAll();
}
function removeItem(idx: number) {
  items.value.splice(idx, 1);
  ids.value.splice(idx, 1);
  emitAll();
}
function addItem() {
  items.value.push('');
  ids.value.push(genId());
  emitAll();
}

// 父组件外部变更（如加载新配置）时同步
watch(
  () => props.modelValue,
  (next) => {
    const arr = Array.isArray(next) ? next : [];
    // 仅在外部数组长度或内容不同步时刷新，避免自身 emit 引起的回环
    if (
      arr.length !== items.value.length ||
      arr.some((v, i) => v !== items.value[i])
    ) {
      items.value = [...arr];
    }
    ensureIds(items.value.length);
  },
  { immediate: true, deep: true },
);
</script>

<style scoped>
.string-list-editor {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.sle-empty {
  padding: 8px 0;
  color: var(--tertiary);
  font-size: 12px;
}
.sle-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.sle-index {
  width: 22px;
  text-align: right;
  font-size: 11px;
  color: var(--tertiary);
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}
.sle-input {
  flex: 1;
}
.sle-remove {
  font-size: 16px;
  color: var(--danger);
  line-height: 1;
}
.sle-actions {
  display: flex;
  justify-content: flex-start;
  margin-top: 4px;
}
</style>
