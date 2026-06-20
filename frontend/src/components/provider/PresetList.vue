<!--
  PresetList - Claude Code / Codex 统一预设范式（对照 demo renderPresets + 旧 ProviderCenter）。
  props.engine: 'claude' | 'codex'
  数据来源：store.mergedPresets[engine]（GetMergedTerminalPresets，含内置默认 + 用户自定义，source 区分）
  卡片样式 .preset-card + .param.model 高亮模型参数。
  「添加预设」emit('add')，弹窗在 P7 批次实现。
-->
<template>
  <div class="preset-panel">
    <!-- Loading state -->
    <LoadingState v-if="loading" message="加载预设中..." />

    <!-- Error state -->
    <ErrorState
      v-else-if="error"
      :message="error"
      :on-retry="initialLoad"
    />

    <!-- Main content -->
    <template v-else>
    <!-- 工具栏：Provider 筛选 Chip + 添加预设按钮 -->
    <div class="pc-toolbar">
      <div class="pc-filter">
        <Chip
          :active="store.presetFilter === 'all'"
          @click="store.setPresetFilter('all')"
        >全部</Chip>
        <Chip
          v-for="name in providerNames"
          :key="name"
          :active="store.presetFilter === name"
          @click="store.setPresetFilter(name)"
        >{{ name }}</Chip>
      </div>
      <AppButton variant="primary" size="small" @click="handleAdd">+ 添加预设</AppButton>
    </div>

    <!-- 预设列表 -->
    <div v-if="filteredPresets.length > 0" class="preset-list">
      <div
        v-for="p in filteredPresets"
        :key="p.key"
        class="preset-card clickable"
        @click="handleEdit(p)"
      >
        <div class="preset-head">
          <div class="preset-name">{{ p.label || p.key }}</div>
          <span class="preset-prov" v-if="p.provider">{{ p.provider }}</span>
        </div>
        <div class="preset-badges">
          <span v-if="p.model" class="param model">{{ p.model }}</span>
          <!-- Claude Code：模型档位（非空才显示） -->
          <template v-if="engine === 'claude'">
            <span v-if="p.model_haiku" class="param tier">H: {{ p.model_haiku }}</span>
            <span v-if="p.model_sonnet" class="param tier">S: {{ p.model_sonnet }}</span>
            <span v-if="p.model_opus" class="param tier">O: {{ p.model_opus }}</span>
          </template>
          <!-- 关键 Parameters（最关键的 2-4 个） -->
          <span v-if="thinkingType(p)" class="param key">thinking·{{ thinkingType(p) }}</span>
          <span v-if="reasoningEffort(p)" class="param key">effort·{{ reasoningEffort(p) }}</span>
          <span v-if="tempOf(p) !== undefined" class="param">T={{ tempOf(p) }}</span>
          <span v-if="sourceLabel(p.source)" class="param">{{ sourceLabel(p.source) }}</span>
        </div>
      </div>
    </div>

    <!-- 空态 -->
    <EmptyState
      v-else
      icon="≡"
      :title="emptyTitle"
      :description="emptyDescription"
    />
    </template>
  </div>

  <!-- 预设弹窗 -->
  <PresetDialog
    v-model:open="showPresetDialog"
    :engine="engine"
    :preset="editingPreset"
    @saved="handlePresetSaved"
  />
</template>

<script setup lang="ts">
import { computed, ref, onMounted } from 'vue';
import { config } from '../../../wailsjs/go/models';
import { useProviderStore } from '../../stores/provider';
import Chip from '../ui/Chip.vue';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import PresetDialog from './PresetDialog.vue';

type MergedTerminalPreset = config.MergedTerminalPreset;

const props = defineProps<{ engine: 'claude' | 'codex' }>();

const store = useProviderStore();
const showPresetDialog = ref(false);
const editingPreset = ref<config.MergedTerminalPreset | null>(null);

// Loading and error states
const loading = ref(true);
const error = ref('');

const engineLabel = computed(() => (props.engine === 'claude' ? 'Claude Code' : 'Codex'));

const allPresets = computed<MergedTerminalPreset[]>(() => store.mergedPresets[props.engine] || []);

/** 按 Provider 维度聚合，供筛选 Chip 列出实际存在的 provider 名 */
const providerNames = computed<string[]>(() => {
  const set = new Set<string>();
  for (const p of allPresets.value) {
    if (p.provider) set.add(p.provider);
  }
  return Array.from(set).sort();
});

const filteredPresets = computed<MergedTerminalPreset[]>(() => {
  const f = store.presetFilter;
  if (!f || f === 'all') return allPresets.value;
  return allPresets.value.filter((p) => p.provider === f);
});

const emptyTitle = computed(() => {
  if (store.presetFilter && store.presetFilter !== 'all') {
    return `该提供商下暂无 ${engineLabel.value} 预设`;
  }
  return `暂无 ${engineLabel.value} 预设`;
});

const emptyDescription = computed(() =>
  store.loadingPresets
    ? '正在加载预设列表...'
    : '点击右上角「添加预设」创建第一个启动配置'
);

function sourceLabel(source?: string): string {
  if (!source) return '';
  if (source === 'user') return '';
  if (source === 'builtin' || source === 'default') return '内置默认';
  if (source === 'managed') return '受管';
  return source;
}

/** 提取关键参数（防御性：后端字段可能为空） */
function thinkingType(p: MergedTerminalPreset): string {
  const t = p.parameters?.thinking?.type;
  return t || '';
}

function reasoningEffort(p: MergedTerminalPreset): string {
  const r = p.parameters?.reasoning_effort;
  return r || '';
}

function tempOf(p: MergedTerminalPreset): number | undefined {
  return p.parameters?.temperature;
}

function handleAdd() {
  editingPreset.value = null;
  showPresetDialog.value = true;
}

function handleEdit(preset: MergedTerminalPreset) {
  editingPreset.value = preset;
  showPresetDialog.value = true;
}

async function handlePresetSaved() {
  loading.value = true;
  error.value = '';
  try {
    await store.loadPresets(props.engine, true);
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
    await store.loadPresets(props.engine, false);
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
.preset-panel {
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

.pc-filter {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.preset-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.preset-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  padding: 14px 16px;
  transition: border-color 0.15s ease, background 0.15s ease, transform 0.1s ease;
}

.preset-card.clickable {
  cursor: pointer;
}

.preset-card.clickable:hover {
  border-color: var(--accent, #007aff);
  background: var(--hover, rgba(0, 122, 255, 0.04));
}

.preset-card.clickable:active {
  transform: scale(0.997);
}

.preset-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 8px;
}

.preset-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
}

.preset-prov {
  font-size: 11px;
  color: var(--secondary);
  background: var(--control);
  border-radius: 5px;
  padding: 2px 8px;
  white-space: nowrap;
}

.preset-badges {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.param {
  font-size: 11px;
  color: var(--secondary);
  background: var(--control);
  border-radius: 5px;
  padding: 2px 8px;
  font-weight: 500;
}

.param.model {
  color: var(--accent);
  background: rgba(0, 122, 255, 0.1);
  font-family: var(--mono);
}

/* Claude 档位：中等权重 mono，与 model 同族但更弱 */
.param.tier {
  font-family: var(--mono);
  color: var(--secondary);
  background: var(--control);
}

/* 关键参数 thinking/effort：弱强调，便于一眼识别开关状态 */
.param.key {
  color: #AF52DE;
  background: rgba(175, 82, 222, 0.08);
}
</style>
