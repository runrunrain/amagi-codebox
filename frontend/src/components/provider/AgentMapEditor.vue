<!--
  AgentMapEditor - agent object map 编辑器
  真实结构：{ "luban": { color, description, mode, model, prompt, variant, tools: {tool:bool} } }
  保持类型：object map（key=agent名），绝不变 array。
  统一 v-model：modelValue + update:modelValue。
-->
<template>
  <div class="agent-editor">
    <div v-if="!agents.length" class="ae-empty">暂无 agent，点击下方添加</div>

    <div v-for="entry in agents" :key="entry.id" :class="['ae-card', { collapsed: !isExpanded(entry.id) }]">
      <button
        type="button"
        class="ae-thumb-head"
        :aria-expanded="isExpanded(entry.id)"
        @click="toggleExpanded(entry.id)"
      >
        <span class="ae-thumb" :style="thumbStyle(entry.value.color)">
          <span class="ae-thumb-icon" v-html="AGENT_ICON" />
        </span>
        <span class="ae-thumb-meta">
          <span class="ae-thumb-name" :title="entry.key || '(未命名)'">{{ entry.key || '(未命名)' }}</span>
          <span v-if="entry.value.mode" class="ae-thumb-mode">{{ entry.value.mode }}</span>
          <span v-if="entry.value.model" class="ae-thumb-model" :title="entry.value.model">{{ entry.value.model }}</span>
        </span>
        <span class="ae-thumb-badge">{{ entry.toolEntries.length }} tools</span>
        <svg
          class="ae-thumb-chevron"
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

      <div v-if="isExpanded(entry.id)" class="ae-expanded">
        <div class="ae-card-head">
          <TextInput
            :model-value="entry.key"
            placeholder="agent 名（如 luban）"
            class="ae-name"
            mono
            @update:model-value="updateKey(entry.id, $event)"
          />
          <AppButton variant="icon" size="small" @click="removeAgent(entry.id)" aria-label="删除">
            <span class="ae-remove">×</span>
          </AppButton>
        </div>
        <div class="ae-fields">
        <div class="ae-field">
          <label class="ae-label">color</label>
          <div class="ae-color-row">
            <input
              type="color"
              class="ae-color-pick"
              :value="normalizeColor(entry.value.color)"
              @input="updateProp(entry.id, 'color', ($event.target as HTMLInputElement).value)"
            />
            <TextInput
              :model-value="entry.value.color || ''"
              placeholder="#RRGGBB"
              class="ae-color-text"
              @update:model-value="updateProp(entry.id, 'color', $event)"
            />
            <span
              class="ae-color-swatch"
              :style="{ background: entry.value.color || 'transparent' }"
            />
          </div>
        </div>

        <div class="ae-field">
          <label class="ae-label">description</label>
          <TextInput
            :model-value="entry.value.description || ''"
            placeholder="agent 描述"
            @update:model-value="updateProp(entry.id, 'description', $event)"
          />
        </div>

        <div class="ae-field-row">
          <div class="ae-field">
            <label class="ae-label">mode</label>
            <Dropdown
              :model-value="entry.value.mode || ''"
              :options="MODE_OPTIONS"
              placeholder="选择 mode"
              @update:model-value="updateProp(entry.id, 'mode', $event)"
            />
          </div>
          <div class="ae-field">
            <label class="ae-label">variant</label>
            <Dropdown
              :model-value="entry.value.variant || ''"
              :options="VARIANT_OPTIONS"
              placeholder="（可选）选择 variant"
              @update:model-value="updateProp(entry.id, 'variant', $event)"
            />
          </div>
        </div>

        <div class="ae-field">
          <label class="ae-label">model</label>
          <TextInput
            :model-value="entry.value.model || ''"
            placeholder="provider/model（如 openai/gpt-5.5）"
            mono
            @update:model-value="updateProp(entry.id, 'model', $event)"
          />
        </div>

        <div class="ae-field">
          <label class="ae-label">prompt</label>
          <textarea
            class="ae-prompt"
            :value="entry.value.prompt || ''"
            placeholder="system prompt（多行）"
            spellcheck="false"
            @input="updateProp(entry.id, 'prompt', ($event.target as HTMLTextAreaElement).value)"
          />
        </div>

        <div class="ae-field">
          <label class="ae-label">tools</label>
          <div class="ae-tools">
            <div v-for="t in entry.toolEntries" :key="t.id" class="ae-tool-row">
              <TextInput
                :model-value="t.key"
                placeholder="tool 名（如 bash）"
                class="ae-tool-name"
                mono
                @update:model-value="updateToolKey(entry.id, t.id, $event)"
              />
              <Switch
                :model-value="!!t.value"
                @update:model-value="updateToolValue(entry.id, t.id, $event)"
              />
              <AppButton variant="icon" size="small" @click="removeTool(entry.id, t.id)" aria-label="删除">
                <span class="ae-remove">×</span>
              </AppButton>
            </div>
            <AppButton variant="ghost" size="small" @click="addTool(entry.id)">+ 添加 tool</AppButton>
          </div>
        </div>
      </div>
      </div>
    </div>

    <div class="ae-actions">
      <AppButton variant="ghost" size="small" @click="addAgent">+ 添加 agent</AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue';
import TextInput from '../ui/TextInput.vue';
import Switch from '../ui/Switch.vue';
import Dropdown from '../ui/Dropdown.vue';
import AppButton from '../ui/AppButton.vue';
import { ICONS } from './icons';
import { useToast } from '../../composables/useToast';

const { showError } = useToast();

interface AgentConfig {
  color?: string;
  description?: string;
  mode?: string;
  model?: string;
  prompt?: string;
  variant?: string;
  tools?: Record<string, boolean>;
  [k: string]: any;
}

interface Props {
  modelValue: Record<string, AgentConfig>;
}

const props = withDefaults(defineProps<Props>(), {
  modelValue: () => ({}),
});

const emit = defineEmits<{
  'update:modelValue': [value: Record<string, AgentConfig>];
}>();

const MODE_OPTIONS = [
  { value: 'primary', label: 'primary' },
  { value: 'subagent', label: 'subagent' },
];
const VARIANT_OPTIONS = [
  { value: '', label: '（未设置）' },
  { value: 'low', label: 'low' },
  { value: 'medium', label: 'medium' },
  { value: 'high', label: 'high' },
  { value: 'xhigh', label: 'xhigh' },
  { value: 'max', label: 'max' },
];

function genId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return 'id-' + Math.random().toString(36).slice(2) + Date.now().toString(36);
}

interface ToolEntry {
  id: string;
  key: string;
  value: boolean;
}
interface AgentEntry {
  id: string;
  key: string;
  value: AgentConfig;
  toolEntries: ToolEntry[];
}

const agents = ref<AgentEntry[]>([]);

// 第二层折叠：每个 agent 项默认收起（略缩图），点击展开
const expandedKeys = ref<Record<string, boolean>>({});

const AGENT_ICON = ICONS.agent;

function isExpanded(id: string): boolean {
  return !!expandedKeys.value[id];
}
function toggleExpanded(id: string) {
  expandedKeys.value[id] = !expandedKeys.value[id];
}

function hexToRgb(hex: string): { r: number; g: number; b: number } | null {
  const m = /^#([0-9a-fA-F]{6})$/.exec(hex);
  if (!m) return null;
  return {
    r: parseInt(m[1].slice(0, 2), 16),
    g: parseInt(m[1].slice(2, 4), 16),
    b: parseInt(m[1].slice(4, 6), 16),
  };
}

// 略缩图色调：优先使用 agent 自定义 color（透明度 0.16），否则 systemGreen
function thumbStyle(color?: string): Record<string, string> {
  const fallback = '#34C759';
  const rgb = color ? hexToRgb(color) : null;
  if (rgb) {
    return {
      color: color as string,
      background: `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, 0.16)`,
    };
  }
  return {
    color: fallback,
    background: 'rgba(52, 199, 89, 0.12)',
  };
}

function normalizeColor(c: string | undefined): string {
  if (!c) return '#000000';
  return /^#[0-9a-fA-F]{6}$/.test(c) ? c : '#000000';
}

function toolEntriesFrom(tools: any): ToolEntry[] {
  if (!tools || typeof tools !== 'object') return [];
  return Object.entries(tools).map(([k, v]) => ({
    id: genId(),
    key: k,
    value: !!v,
  }));
}

function syncFromModel() {
  const obj =
    props.modelValue && typeof props.modelValue === 'object' ? props.modelValue : {};
  agents.value = Object.entries(obj).map(([k, v]) => {
    const exist = agents.value.find((a) => a.key === k);
    const valueCopy: AgentConfig = v && typeof v === 'object' ? { ...(v as object) } : {};
    return {
      id: exist ? exist.id : genId(),
      key: k,
      value: valueCopy,
      toolEntries: toolEntriesFrom((valueCopy as any).tools),
    };
  });
}

function emitAll() {
  const out: Record<string, AgentConfig> = {};
  for (const a of agents.value) {
    if (a.key === '') continue;
    const v: AgentConfig = { ...a.value };
    // 重建 tools
    const tools: Record<string, boolean> = {};
    for (const t of a.toolEntries) {
      if (t.key === '') continue;
      tools[t.key] = t.value;
    }
    // 仅在 toolEntries 非空时写回 tools，避免空对象污染（保留原 tools 若 toolEntries 为空但原值有）
    if (a.toolEntries.length > 0) {
      v.tools = tools;
    } else {
      delete v.tools;
    }
    out[a.key] = v;
  }
  emit('update:modelValue', out);
}

function updateKey(id: string, key: string) {
  const a = agents.value.find((x) => x.id === id);
  if (!a) return;
  // 重名校验：新 key 已被其他 agent 占用则阻止（避免 emitAll 覆盖）
  if (key !== '' && key !== a.key && agents.value.some((x) => x.id !== id && x.key === key)) {
    showError(`agent 名「${key}」已存在，请换一个`);
    // 受控组件 DOM 回滚（Min-2）：用户输入已显示重名，强制同步回旧值
    const oldKey = a.key;
    a.key = key;
    nextTick(() => {
      a.key = oldKey;
    });
    return;
  }
  a.key = key;
  emitAll();
}
function updateProp(id: string, prop: string, value: any) {
  const a = agents.value.find((x) => x.id === id);
  if (!a) return;
  if (prop === 'variant' && value === '') {
    delete a.value.variant;
  } else {
    a.value[prop] = value;
  }
  emitAll();
}
function updateToolKey(agentId: string, toolId: string, key: string) {
  const a = agents.value.find((x) => x.id === agentId);
  if (!a) return;
  const t = a.toolEntries.find((x) => x.id === toolId);
  if (!t) return;
  // 重名校验（M4 一致性补齐 Min-1）：同名 tool 会触发 emitAll 覆盖
  if (key !== '' && key !== t.key && a.toolEntries.some((x) => x.id !== toolId && x.key === key)) {
    showError(`tool 名「${key}」已存在，请换一个`);
    // 受控组件 DOM 回滚（Min-2）：先设新值触发 v-model patch，nextTick 恢复旧值
    const oldKey = t.key;
    t.key = key;
    nextTick(() => {
      t.key = oldKey;
    });
    return;
  }
  t.key = key;
  emitAll();
}
function updateToolValue(agentId: string, toolId: string, value: boolean) {
  const a = agents.value.find((x) => x.id === agentId);
  if (!a) return;
  const t = a.toolEntries.find((x) => x.id === toolId);
  if (t) {
    t.value = value;
    emitAll();
  }
}
function removeTool(agentId: string, toolId: string) {
  const a = agents.value.find((x) => x.id === agentId);
  if (!a) return;
  a.toolEntries = a.toolEntries.filter((x) => x.id !== toolId);
  emitAll();
}
function addTool(agentId: string) {
  const a = agents.value.find((x) => x.id === agentId);
  if (!a) return;
  let k = 'new_tool';
  let i = 1;
  while (a.toolEntries.some((t) => t.key === k)) k = 'new_tool_' + i++;
  a.toolEntries.push({ id: genId(), key: k, value: false });
  emitAll();
}
function removeAgent(id: string) {
  agents.value = agents.value.filter((x) => x.id !== id);
  // 清理 expandedKeys 孤儿键（M2），避免长期增删后小内存泄漏
  delete expandedKeys.value[id];
  emitAll();
}
function addAgent() {
  let k = 'new_agent';
  let i = 1;
  while (agents.value.some((a) => a.key === k)) k = 'new_agent_' + i++;
  agents.value.push({
    id: genId(),
    key: k,
    value: { mode: 'subagent' },
    toolEntries: [],
  });
  // 新增项默认展开，方便立即编辑
  expandedKeys.value[agents.value[agents.value.length - 1].id] = true;
  emitAll();
}

watch(
  () => props.modelValue,
  (next) => {
    const obj = next && typeof next === 'object' ? next : {};
    const cur: Record<string, any> = {};
    for (const a of agents.value) if (a.key !== '') cur[a.key] = a.value;
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
.agent-editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.ae-empty {
  color: var(--tertiary);
  font-size: 12px;
  padding: 6px 0;
}
.ae-card {
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 10px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.ae-card.collapsed {
  /* 收起态更扁平 */
  box-shadow: none;
}
.ae-card:hover {
  border-color: rgba(0, 122, 255, 0.25);
}

/* 第二层略缩图 header（与第一层 ConfigCategoryCard 风格统一） */
.ae-thumb-head {
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
.ae-thumb-head:hover {
  background: var(--card);
}
.ae-thumb-head:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}
.ae-thumb {
  flex: 0 0 28px;
  width: 28px;
  height: 28px;
  border-radius: 7px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: transform 0.18s ease;
}
.ae-thumb-head:hover .ae-thumb {
  transform: translateY(-1px);
}
.ae-thumb-icon {
  display: inline-flex;
  width: 18px;
  height: 18px;
}
.ae-thumb-icon :deep(svg) {
  width: 18px;
  height: 18px;
  display: block;
}
.ae-thumb-meta {
  flex: 1 1 auto;
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.ae-thumb-name {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--label);
  font-family: var(--mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ae-thumb-mode {
  flex-shrink: 0;
  font-size: 10.5px;
  font-weight: 500;
  color: var(--secondary);
  background: var(--bg);
  border: 1px solid var(--separator);
  border-radius: 4px;
  padding: 1px 6px;
}
.ae-thumb-model {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 11px;
  color: var(--tertiary);
  font-family: var(--mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ae-thumb-badge {
  flex: 0 0 auto;
  font-size: 10.5px;
  font-weight: 600;
  color: var(--secondary);
  background: var(--bg);
  border-radius: 999px;
  padding: 1px 8px;
  white-space: nowrap;
}
.ae-thumb-chevron {
  flex: 0 0 12px;
  width: 12px;
  height: 12px;
  color: var(--tertiary);
  transform: rotate(-90deg);
  transition: transform 0.18s ease, color 0.15s ease;
}
.ae-thumb-chevron.expanded {
  transform: rotate(0deg);
  color: var(--accent);
}
.ae-thumb-head:hover .ae-thumb-chevron {
  color: var(--secondary);
}
.ae-thumb-head:hover .ae-thumb-chevron.expanded {
  color: var(--accentHover);
}

.ae-expanded {
  padding: 10px 12px 12px;
  border-top: 1px solid var(--separator);
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.ae-card.collapsed .ae-thumb-head {
  padding: 7px 12px;
}
.ae-card-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.ae-name {
  flex: 1;
  font-weight: 600;
}
.ae-remove {
  font-size: 16px;
  color: var(--danger);
  line-height: 1;
}
.ae-fields {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.ae-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.ae-field-row {
  display: flex;
  gap: 10px;
}
.ae-field-row .ae-field {
  flex: 1;
}
.ae-label {
  font-size: 11.5px;
  font-weight: 500;
  color: var(--secondary);
}
.ae-color-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.ae-color-pick {
  width: 28px;
  height: 28px;
  padding: 0;
  border: 1px solid var(--separator);
  border-radius: 6px;
  background: transparent;
  cursor: pointer;
}
.ae-color-text {
  flex: 1;
}
.ae-color-swatch {
  width: 16px;
  height: 16px;
  border-radius: 4px;
  border: 1px solid var(--separator);
  flex-shrink: 0;
}
.ae-prompt {
  font-family: var(--mono);
  font-size: 12px;
  line-height: 1.5;
  color: var(--termText);
  background: var(--termBg);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 10px 12px;
  min-height: 90px;
  resize: vertical;
  outline: none;
  white-space: pre-wrap;
}
.ae-prompt:focus {
  border-color: var(--accent);
}
.ae-tools {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.ae-tool-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.ae-tool-name {
  flex: 1;
}
.ae-actions {
  display: flex;
  justify-content: flex-start;
  margin-top: 4px;
}
</style>
