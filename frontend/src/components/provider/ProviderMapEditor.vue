<!--
  ProviderMapEditor - provider object map 编辑器
  真实结构：{
    "openai": {
      "models": { "gpt-5.5": { name, variants:{high:{}}, options:{...} } },
      "options": { "apiKey": "sk-..", "baseURL": "http://.." },
      "name": "可选显示名", "npm": "@ai-sdk/anthropic"
    }
  }
  保持类型：object map，绝不变 array。
  P1：models 用 ModelSubEditor 专项编辑（替代 P0 的 RawJsonEditor 兜底），
       models 是 object map（key = model id），每项内含 name/variants/options。
-->
<template>
  <div class="provider-editor">
    <div v-if="!providers.length" class="pe-empty">暂无 provider</div>

    <div v-for="entry in providers" :key="entry.id" class="pe-card">
      <div class="pe-card-head">
        <TextInput
          :model-value="entry.key"
          placeholder="provider 名（如 openai）"
          class="pe-name"
          mono
          @update:model-value="updateKey(entry.id, $event)"
        />
        <AppButton variant="icon" size="small" @click="removeProvider(entry.id)" aria-label="删除">
          <span class="pe-remove">×</span>
        </AppButton>
      </div>

      <div class="pe-fields">
        <div class="pe-field">
          <label class="pe-label">name（可选显示名）</label>
          <TextInput
            :model-value="entry.value.name || ''"
            placeholder="如 Model Studio Coding Plan"
            @update:model-value="updateProp(entry.id, 'name', $event)"
          />
        </div>
        <div class="pe-field">
          <label class="pe-label">npm（可选包名）</label>
          <TextInput
            :model-value="entry.value.npm || ''"
            placeholder="如 @ai-sdk/anthropic"
            mono
            @update:model-value="updateProp(entry.id, 'npm', $event)"
          />
        </div>

        <div class="pe-field">
          <label class="pe-label">options</label>
          <div class="pe-options">
            <div class="pe-option-row">
              <span class="pe-option-key">apiKey</span>
              <button
                type="button"
                class="pe-reveal-btn"
                @click="toggleReveal(entry.id, 'apiKey')"
              >
                {{ isRevealed(entry.id, 'apiKey') ? '收起' : '编辑' }}
              </button>
              <MaskedValue
                v-if="!isRevealed(entry.id, 'apiKey')"
                :value="getApiKey(entry)"
                class="pe-option-val"
              />
              <TextInput
                v-else
                :model-value="getApiKey(entry)"
                placeholder="sk-..."
                class="pe-option-val"
                mono
                @update:model-value="updateOption(entry.id, 'apiKey', $event)"
              />
            </div>
            <div class="pe-option-row">
              <span class="pe-option-key">baseURL</span>
              <TextInput
                :model-value="getBaseUrl(entry)"
                placeholder="https://api.example.com/v1"
                class="pe-option-val"
                mono
                @update:model-value="updateOption(entry.id, 'baseURL', $event)"
              />
            </div>
            <!-- options 中其他未识别键的兜底编辑 -->
            <div class="pe-option-extra">
              <span class="pe-option-extra-label">options 其他键（兜底 JSON）：</span>
              <RawJsonEditor
                :model-value="getOptionsExtra(entry)"
                placeholder="{}"
                @update:model-value="updateOptionsExtra(entry.id, $event)"
              />
            </div>
          </div>
        </div>

        <div class="pe-field">
          <label class="pe-label">models（object map：key = model id）</label>
          <div v-if="!getModelEntries(entry).length" class="pe-models-empty">
            该 provider 暂无 model
          </div>
          <div v-for="m in getModelEntries(entry)" :key="m.key" class="pe-model-card">
            <div class="pe-model-head">
              <TextInput
                :model-value="m.key"
                placeholder="model id（如 gpt-5.5）"
                class="pe-model-id"
                mono
                @update:model-value="updateModelKey(entry.id, m.key, $event)"
              />
              <AppButton variant="icon" size="small" @click="removeModel(entry.id, m.key)" aria-label="删除">
                <span class="pe-remove">×</span>
              </AppButton>
            </div>
            <ModelSubEditor
              :model-value="m.value"
              @update:model-value="updateModelValue(entry.id, m.key, $event)"
            />
          </div>
          <div class="pe-model-actions">
            <AppButton variant="ghost" size="small" @click="addModel(entry.id)">+ 添加 model</AppButton>
          </div>
        </div>
      </div>
    </div>

    <div class="pe-actions">
      <AppButton variant="ghost" size="small" @click="addProvider">+ 添加 provider</AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, watch } from 'vue';
import TextInput from '../ui/TextInput.vue';
import AppButton from '../ui/AppButton.vue';
import MaskedValue from '../ui/MaskedValue.vue';
import RawJsonEditor from './RawJsonEditor.vue';
import ModelSubEditor from './ModelSubEditor.vue';

interface ProviderConfig {
  models?: Record<string, any>;
  options?: Record<string, any>;
  name?: string;
  npm?: string;
  [k: string]: any;
}

interface Props {
  modelValue: Record<string, ProviderConfig>;
}

const props = withDefaults(defineProps<Props>(), {
  modelValue: () => ({}),
});

const emit = defineEmits<{
  'update:modelValue': [value: Record<string, ProviderConfig>];
}>();

function genId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return 'id-' + Math.random().toString(36).slice(2) + Date.now().toString(36);
}

interface ProviderEntry {
  id: string;
  key: string;
  value: ProviderConfig;
}

const providers = ref<ProviderEntry[]>([]);
const revealed = reactive<Record<string, boolean>>({});

function revealKey(entryId: string, field: string) {
  return `${entryId}:${field}`;
}
function isRevealed(entryId: string, field: string) {
  return !!revealed[revealKey(entryId, field)];
}
function toggleReveal(entryId: string, field: string) {
  const k = revealKey(entryId, field);
  revealed[k] = !revealed[k];
}

function getApiKey(entry: ProviderEntry): string {
  return String(entry.value.options?.apiKey ?? '');
}
function getBaseUrl(entry: ProviderEntry): string {
  return String(entry.value.options?.baseURL ?? '');
}
// options 中除 apiKey/baseURL 外的其他键（兜底 JSON）
function getOptionsExtra(entry: ProviderEntry): Record<string, any> {
  const opts = entry.value.options || {};
  const extra: Record<string, any> = {};
  for (const [k, v] of Object.entries(opts)) {
    if (k !== 'apiKey' && k !== 'baseURL') extra[k] = v;
  }
  return extra;
}

function syncFromModel() {
  const obj =
    props.modelValue && typeof props.modelValue === 'object' ? props.modelValue : {};
  providers.value = Object.entries(obj).map(([k, v]) => {
    const exist = providers.value.find((p) => p.key === k);
    const valueCopy: ProviderConfig = v && typeof v === 'object' ? { ...(v as object) } : {};
    return { id: exist ? exist.id : genId(), key: k, value: valueCopy };
  });
}

function emitAll() {
  const out: Record<string, ProviderConfig> = {};
  for (const p of providers.value) {
    if (p.key === '') continue;
    out[p.key] = { ...p.value };
  }
  emit('update:modelValue', out);
}

function updateKey(id: string, key: string) {
  const p = providers.value.find((x) => x.id === id);
  if (p) {
    p.key = key;
    emitAll();
  }
}
function updateProp(id: string, prop: string, value: any) {
  const p = providers.value.find((x) => x.id === id);
  if (!p) return;
  if (value === '' || value === null) {
    delete (p.value as any)[prop];
  } else {
    p.value[prop] = value;
  }
  emitAll();
}
function updateOption(id: string, optionKey: string, value: any) {
  const p = providers.value.find((x) => x.id === id);
  if (!p) return;
  if (!p.value.options || typeof p.value.options !== 'object') {
    p.value.options = {};
  }
  p.value.options[optionKey] = value;
  emitAll();
}
function updateOptionsExtra(id: string, extra: any) {
  const p = providers.value.find((x) => x.id === id);
  if (!p) return;
  // 合并 apiKey/baseURL（保留）+ extra
  const opts: Record<string, any> = {};
  if (p.value.options?.apiKey !== undefined) opts.apiKey = p.value.options.apiKey;
  if (p.value.options?.baseURL !== undefined) opts.baseURL = p.value.options.baseURL;
  if (extra && typeof extra === 'object') {
    for (const [k, v] of Object.entries(extra)) opts[k] = v;
  }
  p.value.options = opts;
  emitAll();
}
function removeProvider(id: string) {
  providers.value = providers.value.filter((x) => x.id !== id);
  emitAll();
}

// === models object map 处理（P1：用 ModelSubEditor 替代 RawJsonEditor 兜底）===
interface ModelEntry {
  key: string;
  value: any;
}
function getModelEntries(entry: ProviderEntry): ModelEntry[] {
  const models = entry.value.models;
  if (!models || typeof models !== 'object' || Array.isArray(models)) return [];
  return Object.entries(models).map(([k, v]) => ({
    key: k,
    value: v && typeof v === 'object' ? v : { _raw: v },
  }));
}
function ensureModels(entry: ProviderEntry): Record<string, any> {
  if (!entry.value.models || typeof entry.value.models !== 'object' || Array.isArray(entry.value.models)) {
    entry.value.models = {};
  }
  return entry.value.models as Record<string, any>;
}
function updateModelKey(entryId: string, oldKey: string, newKey: string) {
  const entry = providers.value.find((x) => x.id === entryId);
  if (!entry) return;
  if (newKey === '' || newKey === oldKey) return;
  const models = ensureModels(entry);
  // 重命名：保持插入顺序（先构建新对象）
  const reordered: Record<string, any> = {};
  for (const [k, v] of Object.entries(models)) {
    if (k === oldKey) reordered[newKey] = v;
    else reordered[k] = v;
  }
  entry.value.models = reordered;
  emitAll();
}
function updateModelValue(entryId: string, key: string, value: any) {
  const entry = providers.value.find((x) => x.id === entryId);
  if (!entry) return;
  const models = ensureModels(entry);
  models[key] = value;
  entry.value.models = { ...models };
  emitAll();
}
function removeModel(entryId: string, key: string) {
  const entry = providers.value.find((x) => x.id === entryId);
  if (!entry || !entry.value.models) return;
  const models = { ...(entry.value.models as Record<string, any>) };
  delete models[key];
  entry.value.models = models;
  emitAll();
}
function addModel(entryId: string) {
  const entry = providers.value.find((x) => x.id === entryId);
  if (!entry) return;
  const models = ensureModels(entry);
  let k = 'new-model';
  let i = 1;
  while (Object.prototype.hasOwnProperty.call(models, k)) k = `model-${i++}`;
  models[k] = { name: k, variants: {} };
  entry.value.models = { ...models };
  emitAll();
}
function addProvider() {
  let k = 'new-provider';
  let i = 1;
  while (providers.value.some((p) => p.key === k)) k = `provider-${i++}`;
  providers.value.push({
    id: genId(),
    key: k,
    value: { options: {}, models: {} },
  });
  emitAll();
}

watch(
  () => props.modelValue,
  (next) => {
    const obj = next && typeof next === 'object' ? next : {};
    const cur: Record<string, any> = {};
    for (const p of providers.value) if (p.key !== '') cur[p.key] = p.value;
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
.provider-editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.pe-empty {
  color: var(--tertiary);
  font-size: 12px;
  padding: 6px 0;
}
.pe-card {
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.pe-card-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.pe-name {
  flex: 1;
  font-weight: 600;
}
.pe-remove {
  font-size: 16px;
  color: var(--danger);
  line-height: 1;
}
.pe-fields {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.pe-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.pe-label {
  font-size: 11.5px;
  font-weight: 500;
  color: var(--secondary);
}
.pe-options {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.pe-option-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.pe-option-key {
  width: 80px;
  font-size: 12px;
  font-family: var(--mono);
  color: var(--secondary);
  flex-shrink: 0;
}
.pe-option-val {
  flex: 1;
  min-width: 120px;
}
.pe-reveal-btn {
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 6px;
  padding: 6px 10px;
  font-size: 12px;
  color: var(--accent);
  cursor: pointer;
  font-family: inherit;
}
.pe-reveal-btn:hover {
  background: var(--controlHover);
}
.pe-option-extra {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 6px;
  padding-top: 8px;
  border-top: 1px dashed var(--separator);
}
.pe-option-extra-label {
  font-size: 11px;
  color: var(--tertiary);
}
.pe-actions {
  display: flex;
  justify-content: flex-start;
  margin-top: 4px;
}
.pe-models-empty {
  color: var(--tertiary);
  font-size: 12px;
  padding: 4px 0;
}
.pe-model-card {
  background: var(--bg);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.pe-model-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding-bottom: 6px;
  border-bottom: 1px dashed var(--separator);
}
.pe-model-id {
  flex: 1;
  font-weight: 600;
}
.pe-model-actions {
  display: flex;
  justify-content: flex-start;
  margin-top: 4px;
}
</style>
