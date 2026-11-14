<!--
  OpenCodePresets - OpenCode 配置文件管理（特殊性，对照 demo + 交接说明 §5.3.3）。
  与 Claude/Codex 不同：OpenCode 管理的是 ~/.config/opencode/config.json 配置文件，
  支持可视化/JSON 双模式。本批按钮 emit，编辑能力 P7 批次实现。

  - 可视化模式：从真实 config.json 解析出 preset 段，渲染为分组列表（.oc-group）；
    「本机默认配置」行在未搜索时始终显示（不可点），用户预设行整行可点编辑。
  - JSON 模式：深色代码块展示真实 config.json 内容（轻量语法高亮）。
  - 布局（provider-center-layout-redesign §4.5）：搜索、路径胶囊（点击复制）、模式切换、添加预设
    收进一行 EditorToolbar；空态 = 标题 + 动作按钮。
-->
<template>
  <div class="oc-panel">
    <!-- Loading state -->
    <LoadingState v-if="loading" message="加载 OpenCode 配置中..." />

    <!-- Error state -->
    <ErrorState
      v-else-if="error"
      :message="error"
      :on-retry="initialLoad"
    />

    <!-- Main content -->
    <template v-else>
    <!-- 工具栏：搜索 + 路径胶囊 ····· 可视化/JSON 双模式 + 添加预设 -->
    <EditorToolbar :path="store.ocConfigPath" path-label="配置文件" @copy-path="copyPath">
      <template #leading>
        <div class="pc-search">
          <svg class="ic" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <circle cx="11" cy="11" r="7" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
          <input
            v-model="searchModel"
            type="text"
            class="oc-search-input"
            placeholder="搜索 OpenCode 预设"
            aria-label="搜索 OpenCode 预设"
          />
        </div>
      </template>
      <template #actions>
        <Segmented
          v-model="modeModel"
          :options="EDITOR_MODE_OPTIONS"
          variant="pill"
        />
        <AppButton variant="primary" size="small" @click="handleAdd">+ 添加预设</AppButton>
      </template>
    </EditorToolbar>

    <!-- 可视化模式 -->
    <div v-if="mode === 'visual'" class="oc-view">
      <!-- 分组列表（规格同 PresetList 的 .preset-group）：本机默认行 + 用户预设行 -->
      <ul v-if="!searchActive || visualItems.length > 0" class="oc-group">
        <!-- 本机默认配置：未搜索时始终显示（没有用户预设时也在，与「未指定预设即使用此项」的语义一致），不可点 -->
        <li v-if="!searchActive" class="oc-row is-default">
          <div class="oc-head">
            <span class="oc-name">本机默认配置</span>
            <span class="oc-default-tag">默认</span>
          </div>
          <div class="oc-desc">未选择预设时使用本机 ~/.config/opencode 配置</div>
        </li>

        <!-- 用户预设行（来自真实 config.json 的 preset 段）：名称是真 button，::after 撑满整行作点击热区 -->
        <li
          v-for="item in visualItems"
          :key="item.key"
          class="oc-row"
        >
          <div class="oc-head">
            <button
              type="button"
              class="oc-open"
              :aria-label="`编辑预设 ${item.name}`"
              @click="handleEdit(item)"
            >
              <span class="oc-name">{{ item.name }}</span>
            </button>
            <code class="oc-key">{{ item.key }}</code>
          </div>
          <div v-if="item.desc" class="oc-desc" :title="item.desc">{{ item.desc }}</div>
          <div v-if="item.bindCount || item.provider || item.model" class="oc-meta">
            <span v-if="item.bindCount > 0" class="oc-bind">绑定 {{ item.bindCount }} 个提供商</span>
            <span v-if="item.provider" class="oc-prov-tag">{{ item.provider }}</span>
            <span v-if="item.model" class="oc-model-tag">{{ item.model }}</span>
          </div>
        </li>
      </ul>

      <!-- 空态：标题 + 动作（P7） -->
      <EmptyState
        v-if="visualItems.length === 0"
        icon="≡"
        :title="store.loadingPresets ? '正在加载 OpenCode 配置...' : (searchActive ? '无匹配预设' : '暂无 OpenCode 预设')"
      >
        <template #action>
          <AppButton
            v-if="searchActive"
            variant="ghost"
            size="small"
            class="empty-action"
            @click="searchModel = ''"
          >清除搜索</AppButton>
          <AppButton
            v-else-if="!store.loadingPresets"
            variant="primary"
            size="small"
            class="empty-action"
            @click="handleAdd"
          >添加预设</AppButton>
        </template>
      </EmptyState>
    </div>

    <!-- JSON 模式：深色代码块展示真实 config.json -->
    <div v-else class="oc-view">
      <pre v-if="store.ocConfigContent" class="oc-json" v-html="highlightedJson"></pre>
      <EmptyState
        v-else
        icon="≡"
        title="未读取到 OpenCode 配置"
        description="文件为空或读取失败"
      />
    </div>
    </template>
  </div>

  <!-- OpenCode 预设弹窗 -->
  <OpenCodePresetDialog
    v-model:open="showPresetDialog"
    :preset="editingPreset"
    @saved="handlePresetSaved"
  />
</template>

<script setup lang="ts">
import { computed, ref, onMounted } from 'vue';
import { config } from '../../../wailsjs/go/models';
import { useProviderStore } from '../../stores/provider';
import { useToast } from '../../composables/useToast';
import Segmented from '../ui/Segmented.vue';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import EditorToolbar from '../ui/EditorToolbar.vue';
import OpenCodePresetDialog from './OpenCodePresetDialog.vue';
import { isJsonRecord, type JsonRecord } from './jsonTypes';
import { highlightJsonWithComments } from './openCodeHighlight';
import { EDITOR_MODE_OPTIONS, copyConfigPath } from './editorUtils';

defineEmits<{ (e: 'add'): void }>();

const store = useProviderStore();
const { showSuccess, showError } = useToast();
const showPresetDialog = ref(false);
const editingPreset = ref<config.OpenCodePreset | null>(null);

// Loading and error states
const loading = ref(true);
const error = ref('');

// 可视化/JSON 双模式本地状态
const mode = ref<'visual' | 'json'>('visual');
const modeModel = computed({
  get: () => mode.value,
  set: (v: string) => {
    mode.value = (v as 'visual' | 'json');
  },
});

const searchModel = computed({
  get: () => store.presetSearch,
  set: (v: string) => store.setPresetSearch(v),
});

/** 从真实 config.json 解析出 preset 段（可视化卡片数据源） */
interface VisualItem {
  key: string;
  name: string;
  desc: string;
  bindCount: number;
  provider: string | null;
  model: string | null;
}

const parsedConfig = computed<JsonRecord | null>(() => {
  const raw = store.ocConfigContent;
  if (!raw) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    return isJsonRecord(parsed) ? parsed : null;
  } catch {
    return null;
  }
});

const allPresetItems = computed<VisualItem[]>(() => {
  const cfg = parsedConfig.value;
  if (!cfg) return [];
  const presetMap = cfg.preset;
  if (!isJsonRecord(presetMap)) return [];
  const items: VisualItem[] = [];
  for (const [key, val] of Object.entries(presetMap)) {
    if (!isJsonRecord(val)) continue;
    const name = typeof val.name === 'string' && val.name ? val.name : typeof val.title === 'string' && val.title ? val.title : key;
    const desc = typeof val.description === 'string' ? val.description : typeof val.desc === 'string' ? val.desc : '';
    const provider = typeof val.provider === 'string' && val.provider ? val.provider : null;
    const model = typeof val.model === 'string' && val.model ? val.model : null;
    items.push({
      key,
      name,
      desc,
      bindCount: Number(val.bindingCount || val.bindCount || 0),
      provider,
      model,
    });
  }
  return items;
});

/** 是否处于搜索态（搜索态下不显示「本机默认配置」行，空态给「清除搜索」） */
const searchActive = computed(() => store.presetSearch.trim() !== '');

/** 应用搜索过滤后的可视化项 */
const visualItems = computed<VisualItem[]>(() => {
  const q = store.presetSearch.trim().toLowerCase();
  if (!q) return allPresetItems.value;
  return allPresetItems.value.filter(
    (p) =>
      p.name.toLowerCase().includes(q) ||
      p.key.toLowerCase().includes(q) ||
      (p.desc && p.desc.toLowerCase().includes(q))
  );
});

/** 轻量 JSON 语法高亮：键名蓝 / 字符串绿 / 注释灰 */
const highlightedJson = computed<string>(() => highlightJsonWithComments(store.ocConfigContent));

function handleAdd() {
  editingPreset.value = null;
  showPresetDialog.value = true;
}

/** 复制 config.json 路径（路径胶囊点击，写法同其他编辑器） */
function copyPath() {
  return copyConfigPath(store.ocConfigPath, { showSuccess, showError });
}

/**
 * 点击用户预设卡片进入编辑：把 VisualItem 还原回 config.OpenCodePreset。
 * 从 parsedConfig 取回该 key 的完整原始结构，整体作为可编辑 JSON 写入 preset.config；
 * bindings 字段尝试从 val.bindings 还原，保持弹窗内绑定列表与原配置一致。
 */
function handleEdit(item: VisualItem) {
  const cfg = parsedConfig.value;
  const presetMap = cfg && isJsonRecord(cfg.preset) ? cfg.preset : null;
  const rawCandidate = presetMap ? presetMap[item.key] : null;
  const raw = isJsonRecord(rawCandidate) ? rawCandidate : null;

  const preset = new config.OpenCodePreset();
  preset.id = item.key;
  preset.name = item.name;
  preset.description = item.desc || undefined;

  // 将原始 preset 对象（剥除 OpenCodePresets.vue 本地展示用的派生字段）整体作为可编辑 JSON
  if (raw) {
    const editable: JsonRecord = {};
    for (const [k, v] of Object.entries(raw)) {
      // 跳过本组件用于展示的派生字段，避免污染编辑器中的 JSON
      if (k === 'bindingCount' || k === 'bindCount') continue;
      editable[k] = v;
    }
    try {
      const str = JSON.stringify(editable, null, 2);
      preset.config = Array.from(new TextEncoder().encode(str));
    } catch {
      preset.config = [];
    }

    // 还原 bindings（OpenCodeBinding 结构）
    if (isJsonRecord(raw.bindings)) {
      const bindingMap: Record<string, config.OpenCodeBinding> = {};
      for (const [providerId, bRaw] of Object.entries(raw.bindings)) {
        if (!isJsonRecord(bRaw)) continue;
        const binding = new config.OpenCodeBinding();
        binding.local_provider = typeof bRaw.local_provider === 'string'
          ? bRaw.local_provider
          : typeof bRaw.localProvider === 'string'
            ? bRaw.localProvider
            : '';
        binding.format = typeof bRaw.format === 'string' ? bRaw.format : undefined;
        binding.inject = Array.isArray(bRaw.inject) ? (bRaw.inject as string[]) : undefined;
        bindingMap[providerId] = binding;
      }
      preset.bindings = Object.keys(bindingMap).length > 0 ? bindingMap : undefined;
    }
  }

  editingPreset.value = preset;
  showPresetDialog.value = true;
}

async function handlePresetSaved() {
  loading.value = true;
  error.value = '';
  try {
    await store.loadPresets('opencode', true);
    // store.loadPresets 内部捕获读取错误不 rethrow，需消费 presetLoadError 呈现错误态
    if (store.presetLoadError) error.value = store.presetLoadError;
  } catch (err) {
    error.value = String(err);
  } finally {
    loading.value = false;
  }
}

// Initial load
async function initialLoad() {
  loading.value = true;
  error.value = '';
  try {
    await store.loadPresets('opencode', false);
    // 同 handlePresetSaved：消费 store 内部捕获的错误，避免读取失败时误显「默认行＋暂无预设」
    if (store.presetLoadError) error.value = store.presetLoadError;
  } catch (err) {
    error.value = String(err);
  } finally {
    loading.value = false;
  }
}

// Load on mount
onMounted(() => {
  initialLoad();
});
</script>

<style scoped>
.oc-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.pc-search {
  position: relative;
  display: flex;
  align-items: center;
  flex: 1;
  min-width: 200px;
  max-width: 320px;
}

.pc-search .ic {
  position: absolute;
  left: 10px;
  width: 15px;
  height: 15px;
  color: var(--tertiary);
  pointer-events: none;
}

.oc-search-input {
  width: 100%;
  height: 32px;
  padding: 0 12px 0 32px;
  font-size: 13px;
  color: var(--label);
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  outline: none;
  transition: border-color 0.15s ease;
  font-family: inherit;
}

.oc-search-input:focus {
  border-color: var(--accent);
}

.oc-search-input::placeholder {
  color: var(--tertiary);
}

/* 分组列表：一个容器 + 行间分隔线（规格同 PresetList 的 .preset-group） */
.oc-group {
  list-style: none;
  margin: 0;
  padding: 0;
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  overflow: hidden;
}

.oc-row {
  position: relative;
  padding: 12px 16px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  transition: background 0.15s ease;
}

.oc-row + .oc-row {
  border-top: 1px solid var(--separator);
}

/* hover 底色同 PresetList：不用 --control，避免与 .oc-bind 等标签同色 */
.oc-row:not(.is-default):hover {
  background: var(--hover, rgba(0, 122, 255, 0.04));
}

/* 本机默认行：不可点，无 hover */
.oc-row.is-default {
  background: var(--sidebar);
}

.oc-head {
  flex-wrap: wrap;
  display: flex;
  align-items: center;
  gap: 10px;
}

/* 名称按钮：::after 撑满整行作点击热区（.oc-row 是定位祖先，按钮自身不能定位） */
.oc-open {
  flex: 1 1 auto;
  min-width: 0;
  text-align: left;
  background: none;
  border: none;
  padding: 0;
  font: inherit;
  cursor: pointer;
}

.oc-open::after {
  content: '';
  position: absolute;
  inset: 0;
}

.oc-open:focus-visible {
  outline: none;
}

.oc-open:focus-visible::after {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
  border-radius: inherit;
}

.oc-name {
  display: block;
  min-width: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.oc-default-tag {
  flex: 0 0 auto;
  font-size: 10px;
  font-weight: 600;
  color: var(--accent);
  background: rgba(0, 122, 255, 0.1);
  border-radius: 4px;
  padding: 1px 6px;
}

.oc-key {
  max-width: 100%;
  overflow-wrap: anywhere;
  white-space: normal;
  flex: 0 1 auto;
  font-family: var(--mono);
  font-size: 11px;
  color: var(--tertiary);
}

.oc-desc {
  font-size: 12px;
  color: var(--secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.oc-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.oc-bind {
  font-size: 11px;
  color: var(--secondary);
  background: var(--control);
  border-radius: 5px;
  padding: 2px 8px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.oc-prov-tag {
  font-size: 11px;
  color: var(--success-strong);
  background: rgba(52, 199, 89, 0.12);
  border-radius: 5px;
  padding: 2px 8px;
}

.oc-model-tag {
  min-width: 0;
  max-width: 100%;
  overflow-wrap: anywhere;
  font-size: 11px;
  color: var(--accent);
  background: rgba(0, 122, 255, 0.1);
  border-radius: 5px;
  padding: 2px 8px;
  font-family: var(--mono);
}

.empty-action {
  margin-top: 14px;
}

.oc-json {
  font-family: var(--mono);
  font-size: 11.5px;
  line-height: 1.7;
  color: #c7c7cc;
  background: #1b1b1f;
  border-radius: 10px;
  padding: 14px 16px;
  white-space: pre;
  overflow: auto;
  max-height: 440px;
  margin: 0;
}

.oc-json :deep(.k) {
  color: #5ea6ff;
}

.oc-json :deep(.s) {
  color: #3bc260;
}

.oc-json :deep(.c) {
  color: #6e6e73;
  font-style: italic;
}
@container editor-toolbar (max-width: 639px) {
  .pc-search { flex-basis:100%; min-width:0; max-width:none; }
}
@supports not (container-type: inline-size) {
  .pc-search { flex-basis:100%; min-width:0; max-width:none; }
}
</style>
