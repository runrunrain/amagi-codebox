<!--
  VariantsMapEditor - provider model 的 variants 字段专项编辑器
  真实结构（实读 ~/.config/opencode/opencode.json 确认）：
    "variants": {
      "high": {},
      "medium": {},
      "xhigh": {}
    }
  规律：key = variant 名（high/medium/low/xhigh/max 等预设标记），value 通常为空对象 {}。
  极少数情况下 value 可能含 override 字段（如 temperature/tools 限制），仍允许展开 RawJsonEditor 兜底。

  设计：
  - 每个 variant 一行：variant 名（可编辑，下拉提示常见预设）+ 删除按钮
  - value 默认折叠（保持 {} 不污染），可点"展开覆盖项"用 RawJsonEditor 编辑（仅在用户主动展开时）
  - 保持类型：value 始终是 object，禁止变 array
  - 苹果HIG 克制：默认态极简，复杂覆盖项收起不打扰
-->
<template>
  <div class="variants-editor">
    <div v-if="!variants.length" class="vme-empty">暂无 variant</div>

    <div v-for="entry in variants" :key="entry.id" class="vme-row">
      <div class="vme-row-head">
        <input
          :value="entry.key"
          list="variant-presets"
          class="vme-key"
          placeholder="variant 名（如 high）"
          spellcheck="false"
          @input="onKeyInput(entry.id, ($event.target as HTMLInputElement).value)"
        />
        <datalist id="variant-presets">
          <option value="high" />
          <option value="medium" />
          <option value="low" />
          <option value="xhigh" />
          <option value="max" />
        </datalist>
        <span class="vme-value-badge" :class="{ nonempty: !isEmptyObject(entry.value) }">
          {{ isEmptyObject(entry.value) ? '空（预设标记）' : '含覆盖项' }}
        </span>
        <button
          type="button"
          class="vme-expand-btn"
          @click="toggleExpand(entry.id)"
        >
          {{ expanded[entry.id] ? '收起' : (isEmptyObject(entry.value) ? '添加覆盖项' : '编辑覆盖项') }}
        </button>
        <AppButton variant="icon" size="small" @click="removeVariant(entry.id)" aria-label="删除">
          <span class="vme-remove">×</span>
        </AppButton>
      </div>
      <div v-if="expanded[entry.id]" class="vme-row-body">
        <RawJsonEditor
          :model-value="entry.value"
          placeholder="{}（覆盖项，如 temperature、tools 等）"
          @update:model-value="updateValue(entry.id, $event)"
        />
      </div>
    </div>

    <div class="vme-actions">
      <AppButton variant="ghost" size="small" @click="addVariant">+ 添加 variant</AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, watch } from 'vue';
import AppButton from '../ui/AppButton.vue';
import RawJsonEditor from './RawJsonEditor.vue';

interface Props {
  modelValue: Record<string, any>;
}

const props = withDefaults(defineProps<Props>(), {
  modelValue: () => ({}),
});

const emit = defineEmits<{
  'update:modelValue': [value: Record<string, any>];
}>();

interface VariantEntry {
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

function isEmptyObject(v: any): boolean {
  return v === null || v === undefined || (typeof v === 'object' && !Array.isArray(v) && Object.keys(v).length === 0);
}

const variants = ref<VariantEntry[]>([]);
const expanded = reactive<Record<string, boolean>>({});

function toggleExpand(id: string) {
  expanded[id] = !expanded[id];
}

function syncFromModel() {
  const obj = props.modelValue && typeof props.modelValue === 'object' && !Array.isArray(props.modelValue)
    ? props.modelValue
    : {};
  const next = Object.entries(obj).map(([k, v]) => {
    const exist = variants.value.find((e) => e.key === k);
    return {
      id: exist ? exist.id : genId(),
      key: k,
      // value 必须是 object；若历史脏数据是其他类型，保留原值由 RawJsonEditor 兜底
      value: v === undefined ? {} : v,
    };
  });
  variants.value = next;
}

function emitAll() {
  const out: Record<string, any> = {};
  for (const e of variants.value) {
    if (e.key === '') continue;
    out[e.key] = e.value;
  }
  emit('update:modelValue', out);
}

function onKeyInput(id: string, newKey: string) {
  const e = variants.value.find((v) => v.id === id);
  if (e) {
    e.key = newKey;
    emitAll();
  }
}
function updateValue(id: string, newValue: any) {
  const e = variants.value.find((v) => v.id === id);
  if (e) {
    e.value = newValue === null ? {} : newValue;
    emitAll();
  }
}
function removeVariant(id: string) {
  variants.value = variants.value.filter((v) => v.id !== id);
  delete expanded[id];
  emitAll();
}
function addVariant() {
  // 默认新 variant 名 high（若已存在则递增）
  let k = 'high';
  let i = 1;
  while (variants.value.some((v) => v.key === k)) {
    k = 'variant_' + i++;
  }
  variants.value.push({ id: genId(), key: k, value: {} });
  emitAll();
}

watch(
  () => props.modelValue,
  (next) => {
    const obj = next && typeof next === 'object' && !Array.isArray(next) ? next : {};
    const cur: Record<string, any> = {};
    for (const e of variants.value) {
      if (e.key !== '') cur[e.key] = e.value;
    }
    const nk = Object.keys(obj);
    const ck = Object.keys(cur);
    const same =
      nk.length === ck.length &&
      nk.every((k) => ck.includes(k)) &&
      nk.every((k) => JSON.stringify(obj[k]) === JSON.stringify(cur[k]));
    if (!same) syncFromModel();
  },
  { immediate: true, deep: true },
);
</script>

<style scoped>
.variants-editor {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.vme-empty {
  color: var(--tertiary);
  font-size: 12px;
  padding: 4px 0;
}
.vme-row {
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 6px 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  background: var(--bg);
}
.vme-row-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.vme-key {
  font-family: var(--mono);
  font-size: 12px;
  padding: 5px 8px;
  border: 1px solid var(--separator);
  border-radius: 6px;
  background: var(--control);
  color: var(--label);
  outline: none;
  min-width: 120px;
  flex: 1;
}
.vme-key:focus {
  border-color: var(--accent);
}
.vme-value-badge {
  font-size: 11px;
  color: var(--tertiary);
  padding: 2px 6px;
  border-radius: 4px;
  background: var(--controlHover);
  flex-shrink: 0;
}
.vme-value-badge.nonempty {
  color: var(--accent);
}
.vme-expand-btn {
  background: transparent;
  border: 1px solid var(--separator);
  border-radius: 6px;
  padding: 4px 8px;
  font-size: 11.5px;
  color: var(--accent);
  cursor: pointer;
  font-family: inherit;
  flex-shrink: 0;
}
.vme-expand-btn:hover {
  background: var(--controlHover);
}
.vme-remove {
  font-size: 16px;
  color: var(--danger);
  line-height: 1;
}
.vme-row-body {
  padding-top: 4px;
  border-top: 1px dashed var(--separator);
}
.vme-actions {
  display: flex;
  justify-content: flex-start;
  margin-top: 2px;
}
</style>
