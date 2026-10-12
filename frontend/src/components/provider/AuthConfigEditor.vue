<!--
  AuthConfigEditor - pi auth.json 提供商凭据可视化编辑器。
  结构 { 提供商名: { type: "api_key", key } | { type: "oauth", access, ... } }。
  api_key 条目：密钥密文输入可直接编辑；oauth 条目：由 pi CLI 登录管理，
  仅展示状态（accountId / 过期时间），token 不展示、保存时原样保留。
  其余未知字段走 VisualValueEditor 递归可视化编辑（零 JSON）。
  源码模式：JSON 文本直接编辑。保存走后端校验 + 原子写入（0600）。

  布局（provider-center-layout-redesign §4.8）：导航即标题，不再有 h2 / subtitle；
  OAuth 行常显「由 pi CLI 管理」标签 + InfoTip（只读语义不再靠一段灰字）；
  空态把 OAuth 途径写进一句话（没有 OAuth 条目时这是唯一的承载位置）。
-->
<template>
  <div ref="editorRoot" class="ace-root" :class="{ 'editor-local-layout': navigation === 'local' }">
    <SectionNav v-if="navigation === 'local'" label="配置分区" :groups="localGroups" :current-id="mode === 'visual' ? activeSection : ''" controls-id="ace-editor-detail" :notice="mode !== 'visual' ? '源码模式：返回可视化后切换分区' : ''" @select="selectSection" />
    <div id="ace-editor-detail" class="editor-detail">
    <LoadingState v-if="loading" message="加载凭据中..." />

    <ErrorState v-else-if="error" title="加载失败" :message="error" :on-retry="initialLoad" />

    <template v-else>
      <!-- 工具栏：路径胶囊 + JSON 合法状态 ····· 模式切换 + 保存 -->
      <EditorToolbar
        :path="configPath"
        path-label="凭据文件"
        :valid-label="mode === 'source' && !parseError ? 'JSON 合法' : ''"
        @copy-path="copyPath"
      >
        <template #actions>
          <Segmented v-model="modeModel" :options="MODE_OPTIONS" variant="pill" />
          <AppButton variant="primary" size="small" :disabled="saving" @click="handleSave">
            {{ saving ? '保存中...' : '保存配置' }}
          </AppButton>
        </template>
      </EditorToolbar>

      <StatusBanner
        v-if="parseError"
        compact
        type="error"
        role="alert"
        class="ace-parse-error"
        :message="`${parseError}，可视化不可用，请切换到 JSON 模式修复`"
      />

      <!-- 可视化模式 -->
      <div v-if="mode === 'visual'" class="ace-visual">
        <template v-for="row in entries" :key="row.uiId">
        <EditorSection v-if="visited[row.uiId]" v-show="activeSection === row.uiId" :active="activeSection === row.uiId" :section-id="row.uiId" :title="row.name">
          <div class="ace-entry-head">
            <TextInput
              :model-value="row.name"
              mono
              placeholder="提供商名"
              class="ace-name"
              @update:model-value="renameEntry(row.name, $event)"
            />
            <!-- 类型徽章统一用 source 型：原 api_key 行用的 tag 型是「白字 + 透明底」，落在白卡片上完全不可见 -->
            <Badge type="source" :text="row.typeLabel" />
            <!-- OAuth 只读：常显「由 pi CLI 管理」标签 + 说明 InfoTip（没有 OAuth 条目时由空态文案承载） -->
            <template v-if="row.kind === 'oauth'">
              <span class="ace-managed">
                <svg
                  viewBox="0 0 16 16"
                  width="12"
                  height="12"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.4"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  aria-hidden="true"
                >
                  <rect x="3.5" y="7" width="9" height="6.5" rx="1.5" />
                  <path d="M5.5 7V5.5a2.5 2.5 0 0 1 5 0V7" />
                </svg>
                由 pi CLI 管理
              </span>
              <InfoTip label="OAuth 凭据" :text="OAUTH_TIP" />
            </template>
            <AppButton
              variant="icon"
              size="small"
              class="ace-remove-btn"
              aria-label="删除条目"
              @click="removeEntry(row.name)"
            >
              <span class="ace-remove">×</span>
            </AppButton>
          </div>

          <!-- api_key：密钥可编辑 -->
          <template v-if="row.kind === 'api_key'">
            <div class="ace-field">
              <label class="ace-label">API Key</label>
              <TextInput
                :model-value="row.key"
                type="password"
                mono
                placeholder="sk-...（留空将移除密钥字段）"
                @update:model-value="updateApiKey(row.name, $event)"
              />
            </div>
            <div v-if="row.extraKeys.length" class="ace-extra">
              <div class="ace-extra-title">其他字段</div>
              <div v-for="k in row.extraKeys" :key="k" class="ace-extra-item">
                <div class="ace-extra-key">{{ k }}</div>
                <VisualValueEditor
                  :model-value="data[row.name]?.[k]"
                  @update:model-value="setEntryField(row.name, k, $event)"
                />
              </div>
            </div>
          </template>

          <!-- oauth：只读状态（token 由 CLI 管理，不展示不修改） -->
          <template v-else-if="row.kind === 'oauth'">
            <div class="ace-oauth">
              <Badge type="ver" :text="`已登录${row.accountId ? ' · ' + row.accountId : ''}`" />
              <span v-if="row.expiresText" class="ace-oauth-expires">过期时间：{{ row.expiresText }}</span>
            </div>
          </template>

          <!-- 未知类型：整体走可视化编辑 -->
          <template v-else>
            <VisualValueEditor
              :model-value="data[row.name]"
              @update:model-value="setEntry(row.name, $event)"
            />
          </template>
        </EditorSection>
        </template>

        <EditorSection v-if="visited.add" v-show="activeSection === 'add'" :active="activeSection === 'add'" section-id="add" title="添加 API Key">
          <EmptyState v-if="entries.length === 0" title="暂无凭据" description="可在下方添加 API Key；OAuth 需通过 pi CLI 登录" />
          <div class="ace-add-row">
            <TextInput v-model="newName" mono placeholder="提供商名（与注册表一致）" class="ace-name" />
            <TextInput v-model="newKey" type="password" mono placeholder="API Key" class="ace-key-input" />
            <AppButton variant="ghost" size="small" :disabled="!newName.trim() || !newKey.trim()" @click="addApiKeyEntry">
              + 添加
            </AppButton>
          </div>
          <div v-if="unauthedProviders.length" class="ace-suggest">
            <span class="ace-suggest-label">未认证：</span>
            <AppButton
              v-for="p in unauthedProviders"
              :key="p"
              variant="ghost"
              size="small"
              @click="newName = p"
            >
              {{ p }}
            </AppButton>
          </div>
        </EditorSection>
      </div>

      <!-- 源码模式 -->
      <div v-else class="ace-source">
        <textarea v-model="sourceContent" class="ace-source-editor" spellcheck="false" @input="parseSource" />
      </div>
    </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch, nextTick } from 'vue';
import { getPiAuthConfig, savePiAuthConfig, getPiAuthConfigPath, getPiModelCatalog } from '../../api/piConfig';
import { useToast } from '../../composables/useToast';
import Segmented from '../ui/Segmented.vue';
import AppButton from '../ui/AppButton.vue';
import TextInput from '../ui/TextInput.vue';
import Badge from '../ui/Badge.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import EmptyState from '../ui/EmptyState.vue';
import StatusBanner from '../ui/StatusBanner.vue';
import EditorToolbar from '../ui/EditorToolbar.vue';
import InfoTip from '../ui/InfoTip.vue';
import VisualValueEditor from './VisualValueEditor.vue';
import EditorSection from '../ui/EditorSection.vue';
import SectionNav from '../ui/SectionNav.vue';
import { pageSectionId, type EditorNavigationProps, type EditorNavigationSnapshot } from './configNavigation';
const props = withDefaults(defineProps<EditorNavigationProps>(), { navigation: 'local', navOwner: '' });
const emit = defineEmits<{ 'navigation-change': [snapshot: EditorNavigationSnapshot] }>();
const editorRoot = ref<HTMLElement | null>(null);
const activeSection = ref(props.sectionId || '');
const visited = ref<Record<string, boolean>>({});
const entryIdByName = new Map<string, string>();
let entrySequence = 0;
function entryId(name: string) {
  if (!entryIdByName.has(name)) entryIdByName.set(name, `entry:${++entrySequence}`);
  return entryIdByName.get(name)!;
}

const { showSuccess, showError } = useToast();

const MODE_OPTIONS = [
  { value: 'visual', label: '可视化' },
  { value: 'source', label: 'JSON' },
];

/** OAuth 行 InfoTip（design §5.2 T53/T55）：只读语义 + 令牌保留语义 */
const OAUTH_TIP = 'OAuth 凭据由 pi CLI 登录产生，此处只读；access / refresh 令牌在保存时原样保留。';

const loading = ref(true);
const saving = ref(false);
const error = ref('');
const mode = ref<'visual' | 'source'>('visual');
const sourceContent = ref('');
const parseError = ref('');
const configPath = ref('');
const data = ref<Record<string, any>>({});
const catalogProviders = ref<{ name: string; hasAuth?: boolean }[]>([]);
const newName = ref('');
const newKey = ref('');

interface AuthRow {
  uiId: string;
  name: string;
  kind: 'api_key' | 'oauth' | 'other';
  typeLabel: string;
  key: string;
  extraKeys: string[];
  accountId: string;
  expiresText: string;
}

const entries = computed<AuthRow[]>(() =>
  Object.keys(data.value).map((name) => {
    const entry = data.value[name];
    const type = typeof entry?.type === 'string' ? entry.type : '';
    const kind: AuthRow['kind'] =
      type === 'api_key' ? 'api_key' : type === 'oauth' ? 'oauth' : 'other';
    const extraKeys =
      kind === 'api_key' && entry && typeof entry === 'object'
        ? Object.keys(entry).filter((k) => k !== 'type' && k !== 'key')
        : [];
    let expiresText = '';
    if (kind === 'oauth' && typeof entry?.expires === 'number') {
      const d = new Date(entry.expires);
      expiresText = Number.isNaN(d.getTime()) ? String(entry.expires) : d.toLocaleString();
    }
    return {
      uiId: entryId(name),
      name,
      kind,
      typeLabel: type || '未知类型',
      key: typeof entry?.key === 'string' ? entry.key : '',
      extraKeys,
      accountId: typeof entry?.accountId === 'string' ? entry.accountId : '',
      expiresText,
    };
  })
);

const navigationItems = computed(() => [...entries.value.map(row => ({ id: row.uiId, label: row.name, meta: row.typeLabel })), { id: 'add', label: '添加 API Key' }]);
function selectSection(id: string) {
  if (mode.value !== 'visual' || !navigationItems.value.some(s => s.id === id)) return;
  activeSection.value = id; visited.value[id] = true;
}
function focusSelection(inNav = false) {
  void nextTick(() => {
    if (inNav) {
      const host = editorRoot.value?.closest('.pc-workspace') || editorRoot.value;
      const id = props.navigation === 'local' ? activeSection.value : pageSectionId('pi:auth', activeSection.value);
      Array.from(host?.querySelectorAll<HTMLButtonElement>('.section-nav button') || []).find(b => b.dataset.navId === id)?.focus();
    } else {
      Array.from(editorRoot.value?.querySelectorAll<HTMLElement>('.editor-section') || []).find(s => s.dataset.section === activeSection.value)?.querySelector<HTMLElement>('h3')?.focus();
    }
  });
}
watch(() => props.sectionId, id => { if (id) selectSection(id); });
watch([navigationItems, loading, mode], () => {
  if (loading.value || error.value || mode.value !== 'visual') return;
  selectSection(navigationItems.value.some(s => s.id === activeSection.value) ? activeSection.value : navigationItems.value[0].id);
});

/** 注册表中存在但尚无凭据的提供商（快捷填入名称） */
const unauthedProviders = computed(() =>
  catalogProviders.value.filter((p) => !p.hasAuth && !(p.name in data.value)).map((p) => p.name).slice(0, 8)
);

async function initialLoad() {
  loading.value = true;
  error.value = '';
  try {
    const [content, path] = await Promise.all([getPiAuthConfig(), getPiAuthConfigPath()]);
    sourceContent.value = content || '';
    configPath.value = path || '';
    parseSource();
    if (parseError.value) mode.value = 'source';
    // 目录仅用于「未认证提供商」建议列表
    try {
      const raw = await getPiModelCatalog();
      const parsed = JSON.parse(raw || '{}');
      catalogProviders.value = Array.isArray(parsed.providers) ? parsed.providers : [];
    } catch {
      catalogProviders.value = [];
    }
  } catch (err) {
    error.value = String(err);
  } finally {
    loading.value = false;
  }
}

function parseSource() {
  const trimmed = (sourceContent.value || '').trim();
  if (!trimmed) {
    data.value = {};
    parseError.value = '';
    return;
  }
  try {
    const parsed = JSON.parse(trimmed);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw new Error('root must be an object');
    }
    data.value = parsed;
    parseError.value = '';
  } catch (e) {
    parseError.value = 'JSON 格式错误：' + (e as Error).message;
  }
}

function serialize() {
  try {
    sourceContent.value = JSON.stringify(data.value, null, 2) + '\n';
    parseError.value = '';
  } catch (e) {
    parseError.value = '序列化失败：' + (e as Error).message;
  }
}

function renameEntry(oldName: string, newNameValue: string) {
  const trimmed = newNameValue.trim();
  if (!trimmed || trimmed === oldName || trimmed in data.value) return;
  const entriesOrdered = Object.keys(data.value).map((k) => [k === oldName ? trimmed : k, data.value[k]] as const);
  const uiId = entryId(oldName);
  entryIdByName.delete(oldName);
  entryIdByName.set(trimmed, uiId);
  data.value = Object.fromEntries(entriesOrdered);
  serialize();
}

function setEntryField(name: string, key: string, value: any) {
  const entry = data.value[name];
  if (!entry || typeof entry !== 'object') return;
  entry[key] = value;
  data.value = { ...data.value };
  serialize();
}

function setEntry(name: string, value: any) {
  data.value[name] = value;
  data.value = { ...data.value };
  serialize();
}

function updateApiKey(name: string, key: string) {
  const entry = data.value[name];
  if (!entry || typeof entry !== 'object') return;
  if (key === '') {
    delete entry.key; // 留空 = 移除密钥字段
  } else {
    entry.key = key;
  }
  data.value = { ...data.value };
  serialize();
}

function removeEntry(name: string) {
  if (!confirm(`确认删除 ${name} 的认证条目？`)) return;
  const index = entries.value.findIndex(row => row.name === name);
  const successor = entries.value[index + 1] || entries.value[index - 1];
  const removedId = entryId(name);
  const restoreFocus = !!editorRoot.value?.contains(document.activeElement);
  delete data.value[name];
  entryIdByName.delete(name);
  data.value = { ...data.value };
  serialize();
  if (activeSection.value === removedId) selectSection(successor?.uiId || 'add');
  if (restoreFocus) focusSelection(true);
}

function addApiKeyEntry() {
  const name = newName.value.trim();
  const key = newKey.value.trim();
  if (!name || !key) return;
  if (name in data.value) {
    showError(`条目 ${name} 已存在，请直接编辑`);
    return;
  }
  data.value[name] = { type: 'api_key', key };
  data.value = { ...data.value };
  newName.value = '';
  newKey.value = '';
  serialize();
  selectSection(entryId(name));
  focusSelection();
}

async function handleSave() {
  if (parseError.value) {
    showError('JSON 格式错误，无法保存');
    return;
  }
  saving.value = true;
  try {
    await savePiAuthConfig(sourceContent.value);
    showSuccess('凭据已保存');
  } catch (err) {
    showError('保存失败：' + (err as Error).message);
  } finally {
    saving.value = false;
  }
}

async function copyPath() {
  if (!configPath.value) return;
  try {
    await navigator.clipboard.writeText(configPath.value);
    showSuccess('路径已复制到剪贴板');
  } catch {
    showError('复制失败');
  }
}

function handleModeChange(newMode: 'visual' | 'source') {
  if (newMode === 'visual') {
    parseSource();
  } else {
    serialize();
  }
}

const modeModel = computed({
  get: () => mode.value,
  set: (v: string) => {
    mode.value = v as 'visual' | 'source';
    handleModeChange(mode.value);
  },
});

const localGroups = computed(() => [{ id: 'auth', items: navigationItems.value.map(s => ({ ...s, disabled: loading.value || !!error.value || mode.value !== 'visual' })) }]);
const navigationSnapshot = computed<EditorNavigationSnapshot>(() => ({ owner: props.navOwner, phase: loading.value ? 'loading' : error.value ? 'error' : 'ready', mode: mode.value === 'visual' ? 'visual' : 'source', activeId: activeSection.value, items: loading.value ? [] : navigationItems.value }));
watch(navigationSnapshot, snapshot => emit('navigation-change', snapshot), { immediate: true });
onMounted(() => {
  void initialLoad();
});
</script>

<style scoped>
.ace-root {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* .ace-parse-error 仅作样式锚点：背景与内边距交给 StatusBanner compact */

.ace-visual {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ace-empty {
  background: var(--control);
  border: 1px dashed var(--separator);
  border-radius: 12px;
  padding: 8px 16px;
}

/* 虚线框内的空态紧凑化（EmptyState 默认上下 44px + 48px 图标，在框里显得过高） */
.ace-empty :deep(.empty-state) {
  padding: 22px 16px;
}

.ace-empty :deep(.empty-icon) {
  font-size: 34px;
  margin-bottom: 10px;
}

.ace-entry {
  border: 0;
  border-radius: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ace-entry-head {
  margin-bottom: 10px;
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.ace-name {
  width: 200px;
}

/* 删除按钮靠右，管理标签 / 说明贴在类型徽章后面 */
.ace-remove-btn {
  margin-left: auto;
}

.ace-remove {
  font-size: 16px;
  line-height: 1;
  color: var(--tertiary);
}

/* OAuth 行常显标签：只读、由 pi CLI 管理 */
.ace-managed {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  font-weight: 500;
  color: var(--secondary);
  background: var(--control);
  border-radius: 999px;
  padding: 1px 7px;
  white-space: nowrap;
}

.ace-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.ace-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--secondary);
}

.ace-extra {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-top: 8px;
  border-top: 1px dashed var(--separator);
}

.ace-extra-title {
  font-size: 11px;
  font-weight: 600;
  color: var(--tertiary);
}

.ace-extra-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.ace-extra-key {
  font-family: var(--mono);
  font-size: 11.5px;
  color: var(--secondary);
}

.ace-oauth {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.ace-oauth-expires {
  font-size: 12px;
  color: var(--secondary);
}

.ace-add {
  border: 0;
  border-radius: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ace-add-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--secondary);
}

.ace-add-row {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}

.ace-key-input {
  flex: 1;
  min-width: 220px;
}

.ace-suggest {
  margin-top: 10px;
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.ace-suggest-label {
  font-size: 12px;
  color: var(--tertiary);
}

.ace-source-editor {
  font-family: var(--mono);
  font-size: 11.5px;
  line-height: 1.6;
  background: var(--termBg);
  color: var(--termText);
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 14px 16px;
  min-height: 400px;
  max-height: 600px;
  resize: vertical;
  outline: none;
  white-space: pre;
  overflow: auto;
  width: 100%;
}

.ace-source-editor:focus {
  border-color: var(--accent);
}
.ace-root { min-width:0; }
.ace-root.editor-local-layout { --section-nav-width:184px; display:grid; grid-template-columns:var(--section-nav-width) minmax(0, 1fr); gap:16px; }
.editor-detail { min-width:0; display:flex; flex-direction:column; gap:12px; container:editor-detail / inline-size; }
.ace-extra-key { overflow-wrap:anywhere; }
@media (max-width:1199px) { .ace-root.editor-local-layout { --section-nav-width:144px; gap:12px; } }
@media (max-width:1023px) { .ace-root.editor-local-layout { --section-nav-width:112px; } }
@container editor-detail (max-width:479px) {
  .ace-name, .ace-key-input { width:100%; min-width:0; max-width:100%; }
  .ace-add-row { flex-direction:column; align-items:stretch; }
  :deep(.vve-entry-value), :deep(.vve-item-value) { min-width:0; flex-basis:100%; }
  :deep(.vve-key) { max-width:100%; }
  :deep(.tlm-actions) { flex-wrap:wrap; }
}
@supports not (container-type: inline-size) {
  .ace-name, .ace-key-input { width:100%; min-width:0; max-width:100%; }
  .ace-add-row { flex-direction:column; align-items:stretch; }
  :deep(.vve-entry-value), :deep(.vve-item-value) { min-width:0; flex-basis:100%; }
  :deep(.vve-key) { max-width:100%; }
  :deep(.tlm-actions) { flex-wrap:wrap; }
}
</style>
