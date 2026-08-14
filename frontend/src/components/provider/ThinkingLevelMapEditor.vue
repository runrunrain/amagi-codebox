<!--
  ThinkingLevelMapEditor - thinkingLevelMap 可视化编辑器。
  结构 { 输入级别: 输出级别|null }，如 { off: null, high: high, max: max }。
  每行 = 输入级别下拉（标准级别，自定义值回退文本输入）→ 输出级别下拉
  （标准级别 + 不发送(null) 选项，自定义值回退文本输入）。
-->
<template>
  <div class="tlm-root">
    <div v-if="rows.length === 0" class="tlm-empty">未设置级别映射（模型将不支持 thinking 参数）</div>
    <div v-for="row in rows" :key="row.id" class="tlm-row">
      <Dropdown
        v-if="isStandard(row.key)"
        :model-value="row.key"
        :options="levelKeyOptions"
        class="tlm-dd"
        @update:model-value="updateKey(row.id, $event)"
      />
      <TextInput
        v-else
        :model-value="row.key"
        mono
        placeholder="自定义输入级别"
        class="tlm-text"
        @update:model-value="updateKey(row.id, $event)"
      />
      <span class="tlm-arrow">→</span>
      <template v-if="row.value === null">
        <div class="tlm-null">
          <span class="tlm-null-label">不发送（null）</span>
          <AppButton variant="ghost" size="small" @click="setValue(row.id, 'high')">设为级别</AppButton>
        </div>
      </template>
      <template v-else>
        <Dropdown
          v-if="isStandard(row.value)"
          :model-value="row.value"
          :options="levelValueOptions"
          class="tlm-dd"
          @update:model-value="setValue(row.id, $event)"
        />
        <TextInput
          v-else
          :model-value="row.value"
          mono
          placeholder="自定义输出值"
          class="tlm-text"
          @update:model-value="setValue(row.id, $event)"
        />
        <AppButton variant="ghost" size="small" @click="setValue(row.id, null)">置 null</AppButton>
      </template>
      <AppButton variant="icon" size="small" aria-label="删除映射" @click="removeRow(row.id)">
        <span class="tlm-remove">×</span>
      </AppButton>
    </div>
    <div class="tlm-actions">
      <AppButton variant="ghost" size="small" @click="addRow">+ 添加级别映射</AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import Dropdown from '../ui/Dropdown.vue';
import TextInput from '../ui/TextInput.vue';
import AppButton from '../ui/AppButton.vue';
import { DEFAULT_THINKING_LEVELS } from './useModelCatalog';

const props = defineProps<{
  /** { 输入级别: 输出级别 | null } */
  modelValue: Record<string, string | null>;
}>();

const emit = defineEmits<{ 'update:modelValue': [value: Record<string, string | null>] }>();

const rows = computed<{ id: string; key: string; value: string | null }[]>(() => {
  const obj = props.modelValue && typeof props.modelValue === 'object' ? props.modelValue : {};
  return Object.entries(obj).map(([key, value]) => ({
    id: key,
    key,
    value: typeof value === 'string' ? value : null,
  }));
});

const levelKeyOptions = computed(() =>
  DEFAULT_THINKING_LEVELS.map((l) => ({ value: l, label: l }))
);

const levelValueOptions = computed(() => [
  ...DEFAULT_THINKING_LEVELS.map((l) => ({ value: l, label: l })),
]);

function isStandard(v: string): boolean {
  return DEFAULT_THINKING_LEVELS.includes(v);
}

function emitPatch(patch: Record<string, string | null>, removed: string[] = []) {
  const out: Record<string, string | null> = { ...props.modelValue, ...patch };
  for (const r of removed) delete out[r];
  emit('update:modelValue', out);
}

function updateKey(id: string, newKey: string) {
  const trimmed = newKey.trim();
  if (!trimmed || trimmed === id) return;
  const current = props.modelValue[id];
  emitPatch({ [trimmed]: current ?? null }, [id]);
}

function setValue(id: string, value: string | null) {
  emitPatch({ [id]: value });
}

function removeRow(id: string) {
  emitPatch({}, [id]);
}

function addRow() {
  const existing = new Set(Object.keys(props.modelValue));
  const level = DEFAULT_THINKING_LEVELS.find((l) => !existing.has(l)) || 'custom';
  emitPatch({ [level]: level });
}
</script>

<style scoped>
.tlm-root {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.tlm-empty {
  font-size: 12px;
  color: var(--tertiary);
  padding: 2px 0;
}

.tlm-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.tlm-dd {
  min-width: 130px;
  max-width: 170px;
}

.tlm-text {
  width: 150px;
}

.tlm-arrow {
  color: var(--tertiary);
  font-size: 12px;
}

.tlm-null {
  display: flex;
  align-items: center;
  gap: 8px;
}

.tlm-null-label {
  font-size: 12px;
  color: var(--tertiary);
  font-family: var(--mono);
}

.tlm-remove {
  font-size: 16px;
  line-height: 1;
  color: var(--tertiary);
}

.tlm-actions {
  display: flex;
  gap: 6px;
}
</style>
