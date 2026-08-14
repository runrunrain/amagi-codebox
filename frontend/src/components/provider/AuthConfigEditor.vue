<!--
  AuthConfigEditor - pi auth.json 提供商凭据可视化编辑器。
  结构 { 提供商名: { type: "api_key", key } | { type: "oauth", access, ... } }。
  api_key 条目：密钥密文输入可直接编辑；oauth 条目：由 pi CLI 登录管理，
  仅展示状态（accountId / 过期时间），token 不展示、保存时原样保留。
  其余未知字段走 VisualValueEditor 递归可视化编辑（零 JSON）。
  源码模式：JSON 文本直接编辑。保存走后端校验 + 原子写入（0600）。
-->
<template>
  <div class="ace-root">
    <div class="ace-header">
      <h2 class="ace-title">Pi 认证登录（auth.json）</h2>
      <p class="ace-subtitle">
        管理提供商凭据：API Key 可在此直接填写；OAuth 凭据由 pi CLI 登录产生，
        此处仅展示状态。Agent 配置下拉中以 ✓ 标注已认证的提供商。
      </p>
    </div>

    <LoadingState v-if="loading" message="加载凭据中..." />

    <ErrorState v-else-if="error" title="加载失败" :message="error" :on-retry="initialLoad" />

    <template v-else>
      <div class="ace-toolbar">
        <Segmented v-model="modeModel" :options="MODE_OPTIONS" variant="pill" class="ace-mode-tabs" />
        <AppButton variant="primary" size="small" :disabled="saving" @click="handleSave">
          {{ saving ? '保存中...' : '保存配置' }}
        </AppButton>
      </div>

      <div class="ace-path-row">
        <span class="ace-path-label">凭据文件：</span>
        <code class="ace-path-value">{{ configPath || '加载中...' }}</code>
        <AppButton v-if="configPath" variant="ghost" size="small" class="ace-copy-btn" @click="copyPath">
          复制路径
        </AppButton>
      </div>

      <div v-if="parseError" class="ace-parse-error">{{ parseError }}（可视化不可用，请切到源码模式修复）</div>
      <div v-else-if="mode === 'source'" class="ace-parse-valid">JSON 合法</div>

      <!-- 可视化模式 -->
      <div v-if="mode === 'visual'" class="ace-visual">
        <div v-if="entries.length === 0" class="ace-empty">
          <EmptyState title="暂无认证条目" description="为提供商添加 API Key，或通过 pi CLI 登录 OAuth 提供商。" />
        </div>

        <div v-for="row in entries" :key="row.name" class="ace-entry">
          <div class="ace-entry-head">
            <TextInput
              :model-value="row.name"
              mono
              placeholder="提供商名"
              class="ace-name"
              @update:model-value="renameEntry(row.name, $event)"
            />
            <Badge :type="row.kind === 'oauth' ? 'source' : 'tag'" :text="row.typeLabel" />
            <AppButton variant="icon" size="small" aria-label="删除条目" @click="removeEntry(row.name)">
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
              <span class="ace-oauth-note">OAuth 凭据由 pi CLI 管理，access/refresh 令牌保存时原样保留。</span>
            </div>
          </template>

          <!-- 未知类型：整体走可视化编辑 -->
          <template v-else>
            <VisualValueEditor
              :model-value="data[row.name]"
              @update:model-value="setEntry(row.name, $event)"
            />
          </template>
        </div>

        <!-- 添加 API Key 认证 -->
        <div class="ace-add">
          <div class="ace-add-title">添加 API Key 认证</div>
          <div class="ace-add-row">
            <TextInput v-model="newName" mono placeholder="提供商名（与 models.json 中一致）" class="ace-name" />
            <TextInput v-model="newKey" type="password" mono placeholder="API Key" class="ace-key-input" />
            <AppButton variant="ghost" size="small" :disabled="!newName.trim() || !newKey.trim()" @click="addApiKeyEntry">
              + 添加
            </AppButton>
          </div>
          <div v-if="unauthedProviders.length" class="ace-suggest">
            <span class="ace-suggest-label">注册表中未认证的提供商：</span>
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
        </div>
      </div>

      <!-- 源码模式 -->
      <div v-else class="ace-source">
        <textarea v-model="sourceContent" class="ace-source-editor" spellcheck="false" @input="parseSource" />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { getPiAuthConfig, savePiAuthConfig, getPiAuthConfigPath, getPiModelCatalog } from '../../api/piConfig';
import { useToast } from '../../composables/useToast';
import Segmented from '../ui/Segmented.vue';
import AppButton from '../ui/AppButton.vue';
import TextInput from '../ui/TextInput.vue';
import Badge from '../ui/Badge.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import EmptyState from '../ui/EmptyState.vue';
import VisualValueEditor from './VisualValueEditor.vue';

const { showSuccess, showError } = useToast();

const MODE_OPTIONS = [
  { value: 'visual', label: '可视化' },
  { value: 'source', label: 'JSON' },
];

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
  delete data.value[name];
  data.value = { ...data.value };
  serialize();
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

onMounted(() => {
  void initialLoad();
});
</script>

<style scoped>
.ace-root {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.ace-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.ace-title {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin: 0;
}

.ace-subtitle {
  font-size: 13px;
  color: var(--secondary);
  margin: 0;
  line-height: 1.6;
}

.ace-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.ace-mode-tabs {
  display: inline-flex;
}

.ace-path-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.ace-path-label {
  font-size: 12px;
  color: var(--tertiary);
}

.ace-path-value {
  font-family: var(--mono);
  font-size: 11.5px;
  color: var(--secondary);
  background: var(--control);
  padding: 4px 8px;
  border-radius: 6px;
}

.ace-copy-btn {
  font-size: 11px;
  padding: 4px 10px;
}

.ace-parse-error {
  font-size: 12px;
  color: var(--danger);
  background: rgba(255, 59, 48, 0.1);
  padding: 8px 12px;
  border-radius: 8px;
}

.ace-parse-valid {
  font-size: 12px;
  color: var(--success);
  padding: 4px 0;
}

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

.ace-entry {
  border: 1px solid var(--separator);
  border-radius: 10px;
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ace-entry-head {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.ace-name {
  width: 200px;
}

.ace-remove {
  font-size: 16px;
  line-height: 1;
  color: var(--tertiary);
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

.ace-oauth-note {
  font-size: 12px;
  color: var(--tertiary);
}

.ace-add {
  border: 1px dashed var(--separator);
  border-radius: 10px;
  padding: 10px 12px;
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
</style>
