<!--
  ModelSubEditor - provider 单个 model 的专项编辑器
  真实结构（实读 ~/.config/opencode/opencode.json 确认）：
    "gpt-5.5": {
      "name": "gpt-5.5",                           // string 显示名
      "variants": { "high": {}, "medium": {} },    // object map，专项 VariantsMapEditor
      "options": {                                 // object map，混合类型
        "enable_search": true,                     // boolean
        "enable_thinking": true,                   // boolean
        "thinking": { "budgetTokens": 1024, "type": "enabled" }  // 嵌套 object
      }
    }

  设计：
  - name：TextInput（字符串显示名）
  - variants：VariantsMapEditor（object map 专项）
  - options：TypedKeyValueEditor（类型保持：boolean→Switch、number→NumberInput、string→TextInput、object→RawJsonEditor）
  - 其他未识别字段（如未来 OpenCode 新增字段）：RawJsonEditor 兜底
  保持类型：name 始终 string、variants/options 始终 object，禁止退化。
-->
<template>
  <div class="model-sub-editor">
    <div class="mse-field">
      <label class="mse-label">name（显示名）</label>
      <TextInput
        :model-value="model.name || ''"
        placeholder="如 gpt-5.5"
        mono
        @update:model-value="updateField('name', $event)"
      />
    </div>

    <div class="mse-field">
      <div class="mse-field-head">
        <label class="mse-label">variants</label>
        <span class="mse-hint">预设标记（high/medium/xhigh 等）</span>
      </div>
      <VariantsMapEditor
        :model-value="model.variants || {}"
        @update:model-value="updateField('variants', $event)"
      />
    </div>

    <div class="mse-field">
      <div class="mse-field-head">
        <label class="mse-label">options</label>
        <span class="mse-hint">类型保持（boolean/number/string/object 自动适配）</span>
      </div>
      <TypedKeyValueEditor
        :model-value="model.options || {}"
        empty-text="该 model 无 options（如 enable_search/thinking 等）"
        @update:model-value="updateField('options', $event)"
      />
    </div>

    <div v-if="hasExtraFields" class="mse-field">
      <div class="mse-field-head">
        <label class="mse-label">其他字段（兜底 JSON）</label>
        <span class="mse-hint">未识别字段原样保留</span>
      </div>
      <RawJsonEditor
        :model-value="extraFields"
        @update:model-value="updateExtra($event)"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import TextInput from '../ui/TextInput.vue';
import VariantsMapEditor from './VariantsMapEditor.vue';
import TypedKeyValueEditor from './TypedKeyValueEditor.vue';
import RawJsonEditor from './RawJsonEditor.vue';

interface ModelConfig {
  name?: string;
  variants?: Record<string, any>;
  options?: Record<string, any>;
  [k: string]: any;
}

interface Props {
  modelValue: ModelConfig;
}

const props = withDefaults(defineProps<Props>(), {
  modelValue: () => ({}),
});

const emit = defineEmits<{
  'update:modelValue': [value: ModelConfig];
}>();

// 保留对 model 的稳定引用，避免每帧重渲染
const model = computed(() => props.modelValue || {});

const KNOWN_FIELDS = new Set(['name', 'variants', 'options']);

const extraFields = computed(() => {
  const out: Record<string, any> = {};
  for (const [k, v] of Object.entries(props.modelValue || {})) {
    if (!KNOWN_FIELDS.has(k)) out[k] = v;
  }
  return out;
});

const hasExtraFields = computed(() => Object.keys(extraFields.value).length > 0);

function emitUpdate(next: ModelConfig) {
  emit('update:modelValue', { ...next });
}

function updateField(field: string, value: any) {
  const next: ModelConfig = { ...props.modelValue };
  if (value === '' || value === null || (typeof value === 'object' && !Array.isArray(value) && Object.keys(value).length === 0)) {
    // 空 object / 空字符串：删除字段以保持 JSON 干净（不写空 {} 污染）
    // 但 variants/options 即使空也保留结构（用于用户继续编辑），仅在明确为空字符串时删除
    if (value === '' || value === null) {
      delete (next as any)[field];
    } else {
      // 空 object：保留（variants/options 可能正在编辑中）
      (next as any)[field] = value;
    }
  } else {
    (next as any)[field] = value;
  }
  emitUpdate(next);
}

function updateExtra(extra: any) {
  const next: ModelConfig = { ...props.modelValue };
  // 移除所有非已知字段，再写入 extra
  for (const k of Object.keys(next)) {
    if (!KNOWN_FIELDS.has(k)) delete (next as any)[k];
  }
  if (extra && typeof extra === 'object' && !Array.isArray(extra)) {
    for (const [k, v] of Object.entries(extra)) {
      (next as any)[k] = v;
    }
  }
  emitUpdate(next);
}
</script>

<style scoped>
.model-sub-editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.mse-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.mse-field-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.mse-label {
  font-size: 11.5px;
  font-weight: 500;
  color: var(--secondary);
}
.mse-hint {
  font-size: 10.5px;
  color: var(--tertiary);
}
</style>
