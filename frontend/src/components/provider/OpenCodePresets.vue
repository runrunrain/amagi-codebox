<!--
  OpenCodePresets - OpenCode 配置文件管理（特殊性，对照 demo + 交接说明 §5.3.3）。
  与 Claude/Codex 不同：OpenCode 管理的是 ~/.config/opencode/config.json 配置文件，
  支持可视化/JSON 双模式。本批按钮 emit，编辑能力 P7 批次实现。

  - 可视化模式：从真实 config.json 解析出 provider/preset 结构渲染 oc-card；
    并固定渲染一个「本机默认配置」虚线卡片（与 demo 一致）。
  - JSON 模式：深色代码块展示真实 config.json 内容（轻量语法高亮）。
-->
<template>
  <div class="oc-panel">
    <!-- 工具栏：搜索 + 可视化/JSON 双模式 Segmented + 添加预设按钮 -->
    <div class="pc-toolbar">
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
        />
      </div>
      <div class="pc-actions">
        <Segmented
          v-model="modeModel"
          :options="MODE_OPTIONS"
          variant="pill"
          class="oc-mode-tabs"
        />
        <AppButton variant="primary" size="small" @click="handleAdd">+ 添加预设</AppButton>
      </div>
    </div>

    <!-- 可视化模式 -->
    <div v-if="mode === 'visual'" class="oc-view">
      <div v-if="visualItems.length > 0" class="preset-list">
        <!-- 本机默认配置（虚线卡片，固定首位） -->
        <div class="oc-card default">
          <div class="oc-head">
            <div class="oc-name">
              本机默认配置
              <span class="oc-default-tag">默认</span>
            </div>
          </div>
          <div class="oc-key">key: (本机)</div>
          <div class="oc-desc">
            读取本地 ~/.config/opencode 配置，未启用受管预设。启动时若未指定预设即使用此项。
          </div>
        </div>

        <!-- 用户预设卡片（来自真实 config.json 的 preset 段） -->
        <div v-for="item in visualItems" :key="item.key" class="oc-card">
          <div class="oc-head">
            <div class="oc-name">{{ item.name }}</div>
          </div>
          <div class="oc-key">key: {{ item.key }}</div>
          <div class="oc-desc" v-if="item.desc">{{ item.desc }}</div>
          <div class="oc-meta">
            <span v-if="item.bindCount > 0" class="oc-bind">绑定 {{ item.bindCount }} 个工作区</span>
            <span v-if="item.provider" class="oc-prov-tag">{{ item.provider }}</span>
            <span v-if="item.model" class="oc-model-tag">{{ item.model }}</span>
          </div>
        </div>
      </div>

      <EmptyState
        v-else
        icon="≡"
        :title="store.loadingPresets ? '正在加载 OpenCode 配置...' : '该配置下暂无 OpenCode 预设'"
        :description="store.loadingPresets ? '' : '在 config.json 的 preset 段中定义预设，或点击右上角「添加预设」'"
      />

      <!-- 配置文件路径提示 -->
      <div v-if="store.ocConfigPath" class="oc-path-row">
        <span class="oc-path-label">配置文件：</span>
        <code class="oc-path-value">{{ store.ocConfigPath }}</code>
      </div>
    </div>

    <!-- JSON 模式：深色代码块展示真实 config.json -->
    <div v-else class="oc-view">
      <pre v-if="store.ocConfigContent" class="oc-json" v-html="highlightedJson"></pre>
      <EmptyState
        v-else
        icon="≡"
        title="未读取到 OpenCode 配置"
        description="config.json 内容为空或加载失败"
      />
      <div v-if="store.ocConfigPath" class="oc-path-row">
        <span class="oc-path-label">配置文件：</span>
        <code class="oc-path-value">{{ store.ocConfigPath }}</code>
      </div>
    </div>
  </div>

  <!-- OpenCode 预设弹窗 -->
  <OpenCodePresetDialog
    v-model:open="showPresetDialog"
    :preset="editingPreset"
    @saved="handlePresetSaved"
  />
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { config } from '../../../wailsjs/go/models';
import { useProviderStore } from '../../stores/provider';
import Segmented from '../ui/Segmented.vue';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';
import OpenCodePresetDialog from './OpenCodePresetDialog.vue';

const emit = defineEmits<{ (e: 'add'): void }>();

const store = useProviderStore();
const showPresetDialog = ref(false);
const editingPreset = ref<config.OpenCodePreset | null>(null);

const MODE_OPTIONS = [
  { value: 'visual', label: '可视化' },
  { value: 'json', label: 'JSON' },
];

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

const parsedConfig = computed<any>(() => {
  const raw = store.ocConfigContent;
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
});

const allPresetItems = computed<VisualItem[]>(() => {
  const cfg = parsedConfig.value;
  if (!cfg || typeof cfg !== 'object') return [];
  const presetMap = cfg.preset;
  if (!presetMap || typeof presetMap !== 'object') return [];
  const items: VisualItem[] = [];
  for (const [key, val] of Object.entries(presetMap as Record<string, any>)) {
    if (!val || typeof val !== 'object') continue;
    items.push({
      key,
      name: val.name || val.title || key,
      desc: val.description || val.desc || '',
      bindCount: Number(val.bindingCount || val.bindCount || 0),
      provider: val.provider || null,
      model: val.model || null,
    });
  }
  return items;
});

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
const highlightedJson = computed<string>(() => {
  const raw = store.ocConfigContent;
  if (!raw) return '';
  // 先转义 HTML
  const escaped = raw
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
  // 行内注释 // ... （注意避免误伤 URL 中的 //）
  return escaped
    .replace(/("(?:\\.|[^"\\])*")(\s*:)?/g, (_m, str: string, colon: string | undefined) => {
      // colon 存在 => 是键名
      if (colon) return `<span class="k">${str}</span>${colon}`;
      // 否则是字符串值
      return `<span class="s">${str}</span>`;
    })
    .replace(/(^|[^:])\/\/(.*)$/gm, '<span class="c">//$2</span>');
});

function handleAdd() {
  editingPreset.value = null;
  showPresetDialog.value = true;
}

async function handlePresetSaved() {
  await store.loadPresets('opencode', true);
}
</script>

<style scoped>
.oc-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.pc-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
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

.pc-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.oc-mode-tabs {
  display: inline-flex;
}

.preset-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.oc-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 14px 16px;
}

.oc-card.default {
  border-style: dashed;
  border-color: #c5c5cc;
  background: var(--sidebar);
}

.oc-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 5px;
}

.oc-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
  display: flex;
  align-items: center;
  gap: 7px;
}

.oc-default-tag {
  font-size: 10px;
  font-weight: 600;
  color: var(--accent);
  background: rgba(0, 122, 255, 0.1);
  border-radius: 4px;
  padding: 1px 6px;
}

.oc-key {
  font-family: var(--mono);
  font-size: 11px;
  color: var(--tertiary);
  margin-bottom: 8px;
}

.oc-desc {
  font-size: 12px;
  color: var(--secondary);
  line-height: 1.5;
  margin-bottom: 10px;
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
  color: var(--success);
  background: rgba(52, 199, 89, 0.12);
  border-radius: 5px;
  padding: 2px 8px;
}

.oc-model-tag {
  font-size: 11px;
  color: var(--accent);
  background: rgba(0, 122, 255, 0.1);
  border-radius: 5px;
  padding: 2px 8px;
  font-family: var(--mono);
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

.oc-path-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 10px;
  font-size: 12px;
  color: var(--tertiary);
}

.oc-path-label {
  white-space: nowrap;
}

.oc-path-value {
  font-family: var(--mono);
  font-size: 11.5px;
  color: var(--secondary);
  word-break: break-all;
}
</style>
