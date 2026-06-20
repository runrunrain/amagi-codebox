<!--
  PermissionMapEditor - permission object map 编辑器
  真实结构：{ "bash": "allow", "edit": "allow", ... }，值是 enum "allow" | "deny" | "ask"。
  保持类型：object map，value 始终 string enum。
-->
<template>
  <div class="perm-editor">
    <div v-if="!entries.length" class="pe-empty">暂无权限条目</div>
    <div v-for="entry in entries" :key="entry.id" :class="['pe-card', { collapsed: !isExpanded(entry.id) }]">
      <button
        type="button"
        class="pe-thumb-head"
        :aria-expanded="isExpanded(entry.id)"
        @click="toggleExpanded(entry.id)"
      >
        <span class="pe-thumb" :class="`pe-thumb-${entry.value || 'unknown'}`">
          <span class="pe-thumb-icon" v-html="PERM_ICON" />
        </span>
        <span class="pe-thumb-meta">
          <span class="pe-thumb-name" :title="entry.key || '(未命名)'">{{ entry.key || '(未命名)' }}</span>
        </span>
        <span class="pe-thumb-pill" :class="`pe-pill-${entry.value || 'unknown'}`">{{ entry.value || '—' }}</span>
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
        <div class="pe-row">
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
      </div>
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
import { ICONS } from './icons';
import { useToast } from '../../composables/useToast';

const { showError } = useToast();

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

// 第二层折叠：每个 permission 项默认收起
const expandedKeys = ref<Record<string, boolean>>({});

const PERM_ICON = ICONS.permission;

function isExpanded(id: string): boolean {
  return !!expandedKeys.value[id];
}
function toggleExpanded(id: string) {
  expandedKeys.value[id] = !expandedKeys.value[id];
}

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
  if (!e) return;
  // 重名校验：新 key 已被其他 permission 条目占用则阻止（避免 emitAll 覆盖）
  if (key !== '' && key !== e.key && entries.value.some((x) => x.id !== id && x.key === key)) {
    showError(`工具名「${key}」已存在，请换一个`);
    return;
  }
  e.key = key;
  emitAll();
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
  // 清理 expandedKeys 孤儿键（M2）
  delete expandedKeys.value[id];
  emitAll();
}
function addEntry() {
  let k = 'new_tool';
  let i = 1;
  while (entries.value.some((e) => e.key === k)) k = 'new_tool_' + i++;
  entries.value.push({ id: genId(), key: k, value: 'allow' });
  // 新增项默认展开
  expandedKeys.value[entries.value[entries.value.length - 1].id] = true;
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

/* 第二层略缩图卡片 */
.pe-card {
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 9px;
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

.pe-thumb-head {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 10px;
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
  flex: 0 0 24px;
  width: 24px;
  height: 24px;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: transform 0.18s ease;
}
.pe-thumb-allow {
  color: #34C759;
  background: rgba(52, 199, 89, 0.12);
}
.pe-thumb-ask {
  color: #FF9500;
  background: rgba(255, 149, 0, 0.12);
}
.pe-thumb-deny {
  color: #FF3B30;
  background: rgba(255, 59, 48, 0.12);
}
.pe-thumb-unknown {
  color: #8E8E93;
  background: rgba(142, 142, 147, 0.12);
}
.pe-thumb-head:hover .pe-thumb {
  transform: translateY(-1px);
}
.pe-thumb-icon {
  display: inline-flex;
  width: 15px;
  height: 15px;
}
.pe-thumb-icon :deep(svg) {
  width: 15px;
  height: 15px;
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
.pe-thumb-pill {
  flex: 0 0 auto;
  font-size: 10.5px;
  font-weight: 600;
  border-radius: 4px;
  padding: 1px 7px;
  white-space: nowrap;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  border: 1px solid transparent;
}
.pe-pill-allow {
  color: #1d8a3f;
  background: rgba(52, 199, 89, 0.14);
  border-color: rgba(52, 199, 89, 0.28);
}
.pe-pill-ask {
  color: #b56400;
  background: rgba(255, 149, 0, 0.14);
  border-color: rgba(255, 149, 0, 0.28);
}
.pe-pill-deny {
  color: #c4281f;
  background: rgba(255, 59, 48, 0.14);
  border-color: rgba(255, 59, 48, 0.28);
}
.pe-pill-unknown {
  color: var(--secondary);
  background: var(--bg);
  border-color: var(--separator);
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
  padding: 8px 10px 10px;
  border-top: 1px solid var(--separator);
  display: flex;
  flex-direction: column;
  gap: 8px;
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
