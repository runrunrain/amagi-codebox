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

    <div v-for="entry in providers" :key="entry.id" :class="['pe-card', { collapsed: !isExpanded(entry.id) }]">
      <button
        type="button"
        class="pe-thumb-head"
        :aria-expanded="isExpanded(entry.id)"
        @click="toggleExpanded(entry.id)"
      >
        <span class="pe-thumb">
          <span class="pe-thumb-icon" v-html="PROVIDER_ICON" />
        </span>
        <span class="pe-thumb-meta">
          <span class="pe-thumb-name" :title="entry.key || '(未命名)'">{{ entry.key || '(未命名)' }}</span>
          <span v-if="entry.value.name" class="pe-thumb-display" :title="entry.value.name">{{ entry.value.name }}</span>
        </span>
        <span class="pe-thumb-badge">{{ getModelEntries(entry).length }} models</span>
        <svg
          class="pe-thumb-chevron"
          :class="{ expanded: isExpanded(entry.id) }"
          viewBox="0 0 12 12"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <polyline points="3 4.5 6 7.5 9 4.5" />
        </svg>
      </button>

      <div v-if="isExpanded(entry.id)" class="pe-expanded">
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
import { ICONS } from './icons';
import { useToast } from '../../composables/useToast';

const { showError } = useToast();

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

// 第二层折叠：每个 provider 项默认收起
const expandedKeys = ref<Record<string, boolean>>({});

const PROVIDER_ICON = ICONS.provider;

function isExpanded(id: string): boolean {
  return !!expandedKeys.value[id];
}
function toggleExpanded(id: string) {
  expandedKeys.value[id] = !expandedKeys.value[id];
}

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
  if (!p) return;
  // 重名校验：新 key 已被其他 provider 占用则阻止（避免 emitAll 覆盖）
  if (key !== '' && key !== p.key && providers.value.some((x) => x.id !== id && x.key === key)) {
    showError(`provider 名「${key}」已存在，请换一个`);
    return;
  }
  p.key = key;
  emitAll();
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
  // 清理 expandedKeys 孤儿键（M2）
  delete expandedKeys.value[id];
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
  // 重名校验：新 model id 已存在则阻止，避免覆盖
  if (Object.prototype.hasOwnProperty.call(models, newKey)) {
    showError(`model「${newKey}」已存在，请换一个`);
    return;
  }
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
  // 新增项默认展开
  expandedKeys.value[providers.value[providers.value.length - 1].id] = true;
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
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.pe-card:hover {
  border-color: rgba(0, 122, 255, 0.25);
}
.pe-card.collapsed {
  box-shadow: none;
}

/* 第二层略缩图 header */
.pe-thumb-head {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  background: transparent;
  border: none;
  cursor: pointer;
  text-align: left;
  font: inherit;
  transition: background 0.15s ease;
}
.pe-thumb-head:hover {
  background: var(--card);
}
.pe-thumb-head:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}
.pe-thumb {
  flex: 0 0 28px;
  width: 28px;
  height: 28px;
  border-radius: 7px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #007AFF;
  background: rgba(0, 122, 255, 0.12);
  transition: transform 0.18s ease;
}
.pe-thumb-head:hover .pe-thumb {
  transform: translateY(-1px);
}
.pe-thumb-icon {
  display: inline-flex;
  width: 18px;
  height: 18px;
}
.pe-thumb-icon :deep(svg) {
  width: 18px;
  height: 18px;
  display: block;
}
.pe-thumb-meta {
  flex: 1 1 auto;
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.pe-thumb-name {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--label);
  font-family: var(--mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.pe-thumb-display {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 11px;
  color: var(--tertiary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.pe-thumb-badge {
  flex: 0 0 auto;
  font-size: 10.5px;
  font-weight: 600;
  color: var(--secondary);
  background: var(--bg);
  border-radius: 999px;
  padding: 1px 8px;
  white-space: nowrap;
}
.pe-thumb-chevron {
  flex: 0 0 12px;
  width: 12px;
  height: 12px;
  color: var(--tertiary);
  transform: rotate(-90deg);
  transition: transform 0.18s ease, color 0.15s ease;
}
.pe-thumb-chevron.expanded {
  transform: rotate(0deg);
  color: var(--accent);
}
.pe-thumb-head:hover .pe-thumb-chevron {
  color: var(--secondary);
}
.pe-thumb-head:hover .pe-thumb-chevron.expanded {
  color: var(--accentHover);
}
.pe-expanded {
  padding: 10px 12px 12px;
  border-top: 1px solid var(--separator);
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.pe-card.collapsed .pe-thumb-head {
  padding: 7px 12px;
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
