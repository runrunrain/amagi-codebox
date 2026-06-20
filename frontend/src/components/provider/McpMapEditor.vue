<!--
  McpMapEditor - mcp object map 编辑器
  真实结构：{ "zhipu-web-reader": { type:"remote", url, headers:{Authorization:..} } | { type:"local", command, args:[] } }
  保持类型：object map，type 决定 url/headers 或 command/args 字段。
  args 用 StringListEditor，headers 用 TextInput（Authorization 自动 MaskedValue）。
-->
<template>
  <div class="mcp-editor">
    <div v-if="!servers.length" class="me-empty">暂无 MCP server</div>

    <div v-for="entry in servers" :key="entry.id" class="me-card">
      <div class="me-card-head">
        <TextInput
          :model-value="entry.key"
          placeholder="server 名（如 zhipu-web-reader）"
          class="me-name"
          mono
          @update:model-value="updateKey(entry.id, $event)"
        />
        <AppButton variant="icon" size="small" @click="removeServer(entry.id)" aria-label="删除">
          <span class="me-remove">×</span>
        </AppButton>
      </div>

      <div class="me-fields">
        <div class="me-field">
          <label class="me-label">type</label>
          <Dropdown
            :model-value="entry.value.type || ''"
            :options="TYPE_OPTIONS"
            placeholder="选择 type"
            @update:model-value="updateProp(entry.id, 'type', $event)"
          />
        </div>

        <!-- remote 字段 -->
        <template v-if="(entry.value.type || 'remote') === 'remote'">
          <div class="me-field">
            <label class="me-label">url</label>
            <TextInput
              :model-value="entry.value.url || ''"
              placeholder="https://..."
              mono
              @update:model-value="updateProp(entry.id, 'url', $event)"
            />
          </div>
          <div class="me-field">
            <label class="me-label">headers</label>
            <div class="me-headers">
              <div v-for="h in entry.headerEntries" :key="h.id" class="me-header-row">
                <TextInput
                  :model-value="h.key"
                  placeholder="header 名（如 Authorization）"
                  class="me-header-key"
                  mono
                  @update:model-value="updateHeaderKey(entry.id, h.id, $event)"
                />
                <template v-if="isSensitiveKey(h.key)">
                  <button
                    type="button"
                    class="me-reveal-btn"
                    @click="toggleReveal(entry.id, h.id)"
                  >
                    {{ isRevealed(entry.id, h.id) ? '收起' : '编辑' }}
                  </button>
                  <MaskedValue
                    v-if="!isRevealed(entry.id, h.id)"
                    :value="h.value"
                    class="me-header-val"
                  />
                  <TextInput
                    v-else
                    :model-value="h.value"
                    placeholder="敏感值"
                    class="me-header-val"
                    mono
                    @update:model-value="updateHeaderValue(entry.id, h.id, $event)"
                  />
                </template>
                <TextInput
                  v-else
                  :model-value="h.value"
                  placeholder="header 值"
                  class="me-header-val"
                  mono
                  @update:model-value="updateHeaderValue(entry.id, h.id, $event)"
                />
                <AppButton variant="icon" size="small" @click="removeHeader(entry.id, h.id)" aria-label="删除">
                  <span class="me-remove">×</span>
                </AppButton>
              </div>
              <AppButton variant="ghost" size="small" @click="addHeader(entry.id)">+ 添加 header</AppButton>
            </div>
          </div>
        </template>

        <!-- local 字段 -->
        <template v-else>
          <div class="me-field">
            <label class="me-label">command</label>
            <TextInput
              :model-value="entry.value.command || ''"
              placeholder="可执行命令（如 npx）"
              mono
              @update:model-value="updateProp(entry.id, 'command', $event)"
            />
          </div>
          <div class="me-field">
            <label class="me-label">args</label>
            <StringListEditor
              :model-value="entry.value.args || []"
              item-placeholder="参数（如 -y）"
              add-label="添加参数"
              empty-text="暂无参数"
              mono
              @update:model-value="updateProp(entry.id, 'args', $event)"
            />
          </div>
        </template>
      </div>
    </div>

    <div class="me-actions">
      <AppButton variant="ghost" size="small" @click="addServer('remote')">+ 添加 remote server</AppButton>
      <AppButton variant="ghost" size="small" @click="addServer('local')">+ 添加 local server</AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, watch } from 'vue';
import TextInput from '../ui/TextInput.vue';
import Dropdown from '../ui/Dropdown.vue';
import AppButton from '../ui/AppButton.vue';
import MaskedValue from '../ui/MaskedValue.vue';
import StringListEditor from './StringListEditor.vue';

interface McpServerConfig {
  type?: string;
  url?: string;
  headers?: Record<string, string>;
  command?: string;
  args?: string[];
  [k: string]: any;
}

interface Props {
  modelValue: Record<string, McpServerConfig>;
}

const props = withDefaults(defineProps<Props>(), {
  modelValue: () => ({}),
});

const emit = defineEmits<{
  'update:modelValue': [value: Record<string, McpServerConfig>];
}>();

const TYPE_OPTIONS = [
  { value: 'remote', label: 'remote（远程 URL）' },
  { value: 'local', label: 'local（本地命令）' },
];

function genId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return 'id-' + Math.random().toString(36).slice(2) + Date.now().toString(36);
}

function isSensitiveKey(key: string): boolean {
  return /apiKey|token|secret|authorization|password/i.test(key);
}

interface HeaderEntry {
  id: string;
  key: string;
  value: string;
}
interface ServerEntry {
  id: string;
  key: string;
  value: McpServerConfig;
  headerEntries: HeaderEntry[];
}

const servers = ref<ServerEntry[]>([]);
const revealed = reactive<Record<string, boolean>>({});

function headerEntriesFrom(headers: any): HeaderEntry[] {
  if (!headers || typeof headers !== 'object') return [];
  return Object.entries(headers).map(([k, v]) => ({
    id: genId(),
    key: k,
    value: String(v ?? ''),
  }));
}

function revealKey(entryId: string, hId: string) {
  return `${entryId}:${hId}`;
}
function isRevealed(entryId: string, hId: string) {
  return !!revealed[revealKey(entryId, hId)];
}
function toggleReveal(entryId: string, hId: string) {
  const k = revealKey(entryId, hId);
  revealed[k] = !revealed[k];
}

function syncFromModel() {
  const obj =
    props.modelValue && typeof props.modelValue === 'object' ? props.modelValue : {};
  servers.value = Object.entries(obj).map(([k, v]) => {
    const exist = servers.value.find((s) => s.key === k);
    const valueCopy: McpServerConfig = v && typeof v === 'object' ? { ...(v as object) } : {};
    return {
      id: exist ? exist.id : genId(),
      key: k,
      value: valueCopy,
      headerEntries: headerEntriesFrom((valueCopy as any).headers),
    };
  });
}

function emitAll() {
  const out: Record<string, McpServerConfig> = {};
  for (const s of servers.value) {
    if (s.key === '') continue;
    const v: McpServerConfig = { ...s.value };
    const headers: Record<string, string> = {};
    for (const h of s.headerEntries) {
      if (h.key === '') continue;
      headers[h.key] = h.value;
    }
    if (s.headerEntries.length > 0) {
      v.headers = headers;
    } else {
      delete v.headers;
    }
    out[s.key] = v;
  }
  emit('update:modelValue', out);
}

function updateKey(id: string, key: string) {
  const s = servers.value.find((x) => x.id === id);
  if (s) {
    s.key = key;
    emitAll();
  }
}
function updateProp(id: string, prop: string, value: any) {
  const s = servers.value.find((x) => x.id === id);
  if (!s) return;
  // type 切换时清理异类字段（remote/local 互斥）
  if (prop === 'type') {
    if (value === 'remote') {
      delete s.value.command;
      delete s.value.args;
    } else if (value === 'local') {
      delete s.value.url;
      delete s.value.headers;
      s.headerEntries = [];
    }
  }
  s.value[prop] = value;
  emitAll();
}
function updateHeaderKey(serverId: string, headerId: string, key: string) {
  const s = servers.value.find((x) => x.id === serverId);
  if (!s) return;
  const h = s.headerEntries.find((x) => x.id === headerId);
  if (h) {
    h.key = key;
    emitAll();
  }
}
function updateHeaderValue(serverId: string, headerId: string, value: string) {
  const s = servers.value.find((x) => x.id === serverId);
  if (!s) return;
  const h = s.headerEntries.find((x) => x.id === headerId);
  if (h) {
    h.value = value;
    emitAll();
  }
}
function removeHeader(serverId: string, headerId: string) {
  const s = servers.value.find((x) => x.id === serverId);
  if (!s) return;
  s.headerEntries = s.headerEntries.filter((x) => x.id !== headerId);
  emitAll();
}
function addHeader(serverId: string) {
  const s = servers.value.find((x) => x.id === serverId);
  if (!s) return;
  let k = 'X-Header';
  let i = 1;
  while (s.headerEntries.some((h) => h.key === k)) k = 'X-Header-' + i++;
  s.headerEntries.push({ id: genId(), key: k, value: '' });
  emitAll();
}
function removeServer(id: string) {
  servers.value = servers.value.filter((x) => x.id !== id);
  emitAll();
}
function addServer(type: 'remote' | 'local') {
  let k = type === 'remote' ? 'new-remote' : 'new-local';
  let i = 1;
  while (servers.value.some((s) => s.key === k)) k = `${type}-${i++}`;
  servers.value.push({
    id: genId(),
    key: k,
    value: { type },
    headerEntries: [],
  });
  emitAll();
}

watch(
  () => props.modelValue,
  (next) => {
    const obj = next && typeof next === 'object' ? next : {};
    const cur: Record<string, any> = {};
    for (const s of servers.value) if (s.key !== '') cur[s.key] = s.value;
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
.mcp-editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.me-empty {
  color: var(--tertiary);
  font-size: 12px;
  padding: 6px 0;
}
.me-card {
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.me-card-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.me-name {
  flex: 1;
  font-weight: 600;
}
.me-remove {
  font-size: 16px;
  color: var(--danger);
  line-height: 1;
}
.me-fields {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.me-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.me-label {
  font-size: 11.5px;
  font-weight: 500;
  color: var(--secondary);
}
.me-headers {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.me-header-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.me-header-key {
  width: 160px;
  flex-shrink: 0;
}
.me-header-val {
  flex: 1;
  min-width: 120px;
}
.me-reveal-btn {
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 6px;
  padding: 6px 10px;
  font-size: 12px;
  color: var(--accent);
  cursor: pointer;
  font-family: inherit;
}
.me-reveal-btn:hover {
  background: var(--controlHover);
}
.me-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 4px;
}
</style>
