<!--
  PermissionMapEditor - permission object map 编辑器
  真实结构：{ "bash": "allow", "edit": "allow", ... }，值是 enum "allow" | "deny" | "ask"。
  保持类型：object map，value 始终 string enum。
-->
<template>
  <div class="perm-editor">
    <div v-if="!entries.length" class="pe-empty">暂无权限条目</div>
    <div v-for="entry in entries" :key="entry.id" class="pe-row">
      <TextInput
        :model-value="entry.key"
        placeholder="工具名（如 bash/edit）"
        class="pe-key"
        @update:model-value="updateKey(entry.id, $event)"
      />
      <Dropdown
        :model-value="entry.value"
        :options="PERM_OPTIONS"
        placeholder="选择权限"
        class="pe-value"
        @update:model-value="updateValue(entry.id, $event)"
      />
      <AppButton variant="icon" size="small" @click="removeEntry(entry.id)" aria-label="删除">
        <span class="pe-remove">×</span>
      </AppButton>
    </div>
    <div class="pe-actions">
      <AppButton variant="ghost" size="small" @click="addEntry">+ 添加权限</AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import TextInput from '../ui/TextInput.vue';
import Dropdown from '../ui/Dropdown.vue';
import AppButton from '../ui/AppButton.vue';

interface Props {
  modelValue: Record<string, string>;
}

const props = withDefaults(defineProps<Props>(), {
  modelValue: () => ({}),
});

const emit = defineEmits<{
  'update:modelValue': [value: Record<string, string>];
}>();

const PERM_OPTIONS = [
  { value: 'allow', label: 'allow（自动允许）' },
  { value: 'ask', label: 'ask（每次询问）' },
  { value: 'deny', label: 'deny（拒绝）' },
];

interface Entry {
  id: string;
  key: string;
  value: string;
}

function genId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return 'id-' + Math.random().toString(36).slice(2) + Date.now().toString(36);
}

const entries = ref<Entry[]>([]);

function syncFromModel() {
  const obj = props.modelValue && typeof props.modelValue === 'object' ? props.modelValue : {};
  entries.value = Object.entries(obj).map(([k, v]) => {
    const exist = entries.value.find((e) => e.key === k);
    return { id: exist ? exist.id : genId(), key: k, value: String(v) };
  });
}

function emitAll() {
  const out: Record<string, string> = {};
  for (const e of entries.value) {
    if (e.key === '') continue;
    out[e.key] = e.value;
  }
  emit('update:modelValue', out);
}

function updateKey(id: string, key: string) {
  const e = entries.value.find((x) => x.id === id);
  if (e) {
    e.key = key;
    emitAll();
  }
}
function updateValue(id: string, value: string) {
  const e = entries.value.find((x) => x.id === id);
  if (e) {
    e.value = value;
    emitAll();
  }
}
function removeEntry(id: string) {
  entries.value = entries.value.filter((x) => x.id !== id);
  emitAll();
}
function addEntry() {
  let k = 'new_tool';
  let i = 1;
  while (entries.value.some((e) => e.key === k)) k = 'new_tool_' + i++;
  entries.value.push({ id: genId(), key: k, value: 'allow' });
  emitAll();
}

watch(
  () => props.modelValue,
  (next) => {
    const obj = next && typeof next === 'object' ? next : {};
    const cur: Record<string, string> = {};
    for (const e of entries.value) if (e.key !== '') cur[e.key] = e.value;
    const nk = Object.keys(obj);
    const ck = Object.keys(cur);
    const same =
      nk.length === ck.length &&
      nk.every((k) => ck.includes(k)) &&
      nk.every((k) => obj[k] === cur[k]);
    if (!same) syncFromModel();
  },
  { immediate: true, deep: true },
);
</script>

<style scoped>
.perm-editor {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.pe-empty {
  color: var(--tertiary);
  font-size: 12px;
  padding: 6px 0;
}
.pe-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.pe-key {
  flex: 1;
}
.pe-value {
  flex-shrink: 0;
}
.pe-remove {
  font-size: 16px;
  color: var(--danger);
  line-height: 1;
}
.pe-actions {
  display: flex;
  justify-content: flex-start;
  margin-top: 4px;
}
</style>
