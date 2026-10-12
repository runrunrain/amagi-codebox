<!--
  PresetList - Anthropic / OpenAI 公共格式预设。
  Claude Code 消费 Anthropic；Codex / Pi / OMP 消费 OpenAI。
  布局（provider-center-layout-redesign §4.2）：分组列表——一个容器、行间 1px 分隔线，
  对标 macOS 设置的 inset grouped list；徽章 .param + .param.model 高亮模型参数。
  整行可点编辑且语义正确：行内的名称是真 <button>（Tab 聚焦、Enter 编辑），
  其 ::after 撑满整行作为点击热区；删除按钮 z-index 浮在热区之上，单独可聚焦。
  「添加预设」打开 PresetDialog。
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

    <!-- 预设分组列表 -->
    <ul v-if="filteredPresets.length > 0" class="preset-group">
      <li
        v-for="p in filteredPresets"
        :key="p.key"
        class="preset-row"
      >
        <div class="preset-head">
          <button
            type="button"
            class="preset-open"
            :aria-label="`编辑预设 ${p.label || p.key}`"
            @click="handleEdit(p)"
          >
            <span class="preset-name">{{ p.label || p.key }}</span>
          </button>
          <span class="preset-prov" v-if="p.provider">{{ p.provider }}</span>
          <!-- 删除入口：后端 GetMergedTerminalPresets 仅从 terminal_presets（用户配置）读取，
               所有返回的 merged 预设都可被 DeleteTerminalPreset 删除，无"内置不可删"项，
               因此对所有预设始终显示删除按钮（source 实际值为 'terminal_preset'）。 -->
          <button
            class="preset-delete"
            type="button"
            :aria-label="`删除预设 ${p.label || p.key}`"
            @click="handleDelete(p)"
          >删除</button>
        </div>
        <div class="preset-badges">
          <span v-if="p.model" class="param model">{{ p.model }}</span>
          <!-- Anthropic / Claude Code 模型档位（非空才显示） -->
          <template v-if="format === 'anthropic'">
            <span v-if="p.model_haiku" class="param tier">H: {{ p.model_haiku }}</span>
            <span v-if="p.model_sonnet" class="param tier">S: {{ p.model_sonnet }}</span>
            <span v-if="p.model_opus" class="param tier">O: {{ p.model_opus }}</span>
          </template>
          <!-- 视觉能力标记（amagi-media-understanding 导出，契约 §1） -->
          <span v-if="p.vision" class="param vision">识图</span>
          <span v-if="p.video" class="param vision">识视频</span>
          <!-- 同步到 CLI 独立配置标记（仅 Anthropic 格式）：语义直接写进徽章文字，不依赖 hover -->
          <span
            v-if="format === 'anthropic' && p.harness_sync"
            class="param vision"
          >同步 Pi/OMP</span>
          <!-- 关键 Parameters（最关键的 2-4 个） -->
          <span v-if="thinkingType(p)" class="param key">thinking·{{ thinkingType(p) }}</span>
          <span v-if="reasoningEffort(p)" class="param key">effort·{{ reasoningEffort(p) }}</span>
          <span v-if="tempOf(p) !== undefined" class="param">T={{ tempOf(p) }}</span>
          <span v-if="sourceLabel(p.source)" class="param">{{ sourceLabel(p.source) }}</span>
        </div>
      </li>
    </ul>

    <!-- 空态：标题 + 动作（P7），引导靠按钮完成 -->
    <EmptyState
      v-else
      icon="≡"
      :title="emptyTitle"
      :description="store.loadingPresets ? '正在加载预设列表…' : ''"
    >
      <template #action>
        <AppButton
          v-if="isFiltered"
          variant="ghost"
          size="small"
          class="empty-action"
          @click="store.setPresetFilter('all')"
        >显示全部</AppButton>
        <AppButton
          v-else-if="!store.loadingPresets"
          variant="primary"
          size="small"
          class="empty-action"
          @click="handleAdd"
        >添加预设</AppButton>
      </template>
    </EmptyState>
    </template>
  </div>

  <!-- 预设弹窗 -->
  <PresetDialog
    v-model:open="showPresetDialog"
    :format="format"
    :preset="editingPreset"
    @saved="handlePresetSaved"
  />

  <!-- 删除确认弹窗 -->
  <ConfirmDialog
    v-model:open="showDeleteDialog"
    :danger="true"
    title="删除预设"
    :message="deleteMessage"
    confirm-text="删除"
    cancel-text="取消"
    @confirm="confirmDelete"
  />
</template>

<script setup lang="ts">
import { computed, ref, onMounted } from 'vue';
import { config } from '../../../wailsjs/go/models';
import { useProviderStore } from '../../stores/provider';
import { useToast } from '../../composables/useToast';
import Chip from '../ui/Chip.vue';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';
import LoadingState from '../ui/LoadingState.vue';
import ErrorState from '../ui/ErrorState.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import PresetDialog from './PresetDialog.vue';

type MergedTerminalPreset = config.MergedTerminalPreset;

const props = defineProps<{ format: 'anthropic' | 'openai' }>();

const store = useProviderStore();
const { showSuccess, showError } = useToast();
const showPresetDialog = ref(false);
const editingPreset = ref<config.MergedTerminalPreset | null>(null);

// 删除流程状态：showDeleteDialog 控制确认弹窗，deletingPreset 记录待删预设，deleting 防重复点击
const showDeleteDialog = ref(false);
const deletingPreset = ref<config.MergedTerminalPreset | null>(null);
const deleting = ref(false);

const deleteMessage = computed(() => {
  const name = deletingPreset.value?.label || deletingPreset.value?.key || '';
  return `确定删除预设「${name}」吗？此操作不可恢复。`;
});

// Loading and error states
const loading = ref(true);
const error = ref('');

const engineLabel = computed(() => {
  return props.format === 'anthropic' ? 'Anthropic 格式' : 'OpenAI 格式';
});

const allPresets = computed<MergedTerminalPreset[]>(() => store.mergedPresets[props.format] || []);

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

/** 当前是否处于 provider 筛选态（空态据此给出「显示全部」而不是「添加预设」） */
const isFiltered = computed(() => !!store.presetFilter && store.presetFilter !== 'all');

const emptyTitle = computed(() => {
  if (isFiltered.value) {
    return `${store.presetFilter} 下暂无预设`;
  }
  return `暂无 ${engineLabel.value} 预设`;
});

function sourceLabel(source?: string): string {
  if (!source) return '';
  // 后端 MergedTerminalPreset.source 实际值为 'terminal_preset'（全部来自用户 terminal_presets 配置），
  // 该值对用户无意义且会造成困惑，统一不显示来源 badge。保留对其他未知值的兜底返回。
  if (source === 'terminal_preset') return '';
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
    await store.loadPresets(props.format, true);
    // store.loadPresets 内部捕获读取错误不 rethrow，需消费 presetLoadError 呈现错误态
    if (store.presetLoadError) error.value = store.presetLoadError;
  } catch (err) {
    error.value = String(err);
  } finally {
    loading.value = false;
  }
}

/**
 * 打开删除确认弹窗。
 * 后端所有 merged 预设都来自用户 terminal_presets 配置、均可被 DeleteTerminalPreset 删除，
 * 因此无需按 source 过滤（source 实际值为 'terminal_preset'，不再做 'user' 判断）。
 * store.deletePreset 内部已映射 terminalType 与刷新列表。
 */
function handleDelete(preset: MergedTerminalPreset) {
  deletingPreset.value = preset;
  deleting.value = false;
  showDeleteDialog.value = true;
}

async function confirmDelete() {
  const target = deletingPreset.value;
  if (!target || deleting.value) return;
  deleting.value = true;
  try {
    await store.deletePreset(props.format, target.key);
    showSuccess('已删除预设');
  } catch (err) {
    console.error('[PresetList] 删除失败:', err);
    showError('删除失败: ' + String(err));
  } finally {
    deleting.value = false;
    deletingPreset.value = null;
    showDeleteDialog.value = false;
  }
}

// Initial load
async function initialLoad() {
  loading.value = true;
  error.value = '';
  try {
    await store.loadPresets(props.format, false);
    // 同 handlePresetSaved：消费 store 内部捕获的错误，避免误显空态
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

/* 分组列表：一个容器 + 行间分隔线（inset grouped list） */
.preset-group {
  list-style: none;
  margin: 0;
  padding: 0;
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 12px;
  overflow: hidden;
}

.preset-row {
  position: relative;
  padding: 12px 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  transition: background 0.15s ease;
}

.preset-row + .preset-row {
  border-top: 1px solid var(--separator);
}

/* hover 底色沿用改造前的 --hover 回退值：若改用 --control，会与 .param / .preset-prov 的 --control 底同色，徽章在 hover 时「消失」 */
.preset-row:hover {
  background: var(--hover, rgba(0, 122, 255, 0.04));
}

.preset-head {
  flex-wrap: wrap;
  display: flex;
  align-items: center;
  gap: 10px;
}

/* 名称按钮：::after 撑满整行作为点击热区（.preset-row 是定位祖先，按钮自身不能定位） */
.preset-open {
  flex: 1 1 120px;
  min-width: 0;
  text-align: left;
  background: none;
  border: none;
  padding: 0;
  font: inherit;
  cursor: pointer;
}

.preset-open::after {
  content: '';
  position: absolute;
  inset: 0;
}

/* 焦点框画在撑满整行的伪元素上 */
.preset-open:focus-visible {
  outline: none;
}

.preset-open:focus-visible::after {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
  border-radius: inherit;
}

.preset-name {
  display: block;
  font-size: 14px;
  font-weight: 600;
  color: var(--label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.preset-prov {
  max-width: 100%;
  overflow-wrap: anywhere;
  flex: 0 1 auto;
  font-size: 11px;
  color: var(--secondary);
  background: var(--control);
  border-radius: 5px;
  padding: 2px 8px;
  white-space: normal;
}

/* 删除按钮：克制小巧，hover 才显危险色；浮在整行热区之上，单独可聚焦 */
.preset-delete {
  position: relative;
  z-index: 1;
  flex: 0 0 auto;
  border: none;
  background: transparent;
  color: var(--tertiary);
  font-size: 11px;
  padding: 3px 8px;
  border-radius: 5px;
  cursor: pointer;
  line-height: 1.4;
  transition: color 0.15s ease, background 0.15s ease;
  font-family: inherit;
}

.preset-delete:hover {
  color: var(--danger-strong);
  background: var(--danger-tint, rgba(255, 59, 48, 0.08));
}

.preset-delete:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}

.preset-badges {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.param {
  min-width: 0;
  max-width: 100%;
  overflow-wrap: anywhere;
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

/* 视觉能力 / 同步标记：绿色弱强调；文字用 success-strong 保证对比度 */
.param.vision {
  color: var(--success-strong);
  background: rgba(52, 199, 89, 0.1);
}

.empty-action {
  margin-top: 14px;
}
</style>
